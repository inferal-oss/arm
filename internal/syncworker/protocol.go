package syncworker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/inferal-oss/arm/internal/rsyncengine"
	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
)

const maxControlFrame = 32 << 20

type controlFrame struct {
	Version int                        `json:"version"`
	Kind    string                     `json:"kind"`
	Key     string                     `json:"key"`
	Plan    string                     `json:"plan"`
	Batch   string                     `json:"batch,omitempty"`
	Dirs    []syncfs.DirectoryMetadata `json:"dirs,omitempty"`
	Receipt *syncservice.SideReceipt   `json:"receipt,omitempty"`
}

func writeFrame(w io.Writer, f controlFrame, hold admittedHold) error {
	limit := min(int64(maxControlFrame), hold.MemoryBytes()/8)
	estimate := int64(2048)
	for _, dir := range f.Dirs {
		estimate += 128 + 6*int64(len(dir.Path))
	}
	if estimate > limit {
		return errors.New("sync control payload exceeds admitted memory")
	}
	f.Version = syncservice.Schema
	raw, err := json.Marshal(f)
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return errors.New("sync control frame exceeds bound")
	}
	var header [8]byte
	binary.BigEndian.PutUint64(header[:], uint64(len(raw)))
	if err = writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, raw)
}

func writeAll(w io.Writer, raw []byte) error {
	for len(raw) != 0 {
		n, e := w.Write(raw)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	return nil
}

func readFrame(ctx context.Context, r io.Reader, hold admittedHold, intent syncservice.Intent, kind, id string) (controlFrame, error) {
	var f controlFrame
	if err := hold.CheckActive(ctx); err != nil {
		return f, err
	}
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return f, err
	}
	n := binary.BigEndian.Uint64(header[:])
	memory := hold.MemoryBytes()
	if memory <= 0 || n == 0 || n > maxControlFrame || n > uint64(memory/8) {
		return f, errors.New("sync control frame exceeds admitted memory")
	}
	raw := make([]byte, int(n))
	if _, err := io.ReadFull(r, raw); err != nil {
		return f, err
	}
	if err := hold.CheckActive(ctx); err != nil {
		return f, err
	}
	// A small JSON array can expand to a much larger Go slice. Check its
	// structure before the typed decoder allocates Dirs, including spare
	// slice capacity and decoder/string overhead within the live hold.
	if err := preflightControlJSON(ctx, raw, memory, hold); err != nil {
		return f, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return f, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return f, errors.New("trailing sync control data")
	}
	if f.Version != syncservice.Schema || f.Kind != kind || f.Key != intent.Key || f.Plan != intent.PlanDigest || f.Batch != id || len(f.Dirs) > syncreconcile.MaxActions {
		return f, fmt.Errorf("sync control %q differs from reviewed operation", kind)
	}
	if kind != "ready" && len(f.Dirs) != 0 || kind != "receipt" && f.Receipt != nil {
		return f, errors.New("unexpected sync control payload")
	}
	return f, nil
}

// preflightControlJSON retains only a bounded delimiter stack. The typed
// schema below remains authoritative; this pass limits allocation even for
// malformed, sparse, repeated or unexpected fields before decoding them.
func preflightControlJSON(ctx context.Context, raw []byte, memory int64, hold admittedHold) error {
	const reserve = 128 << 10
	remaining := memory - reserve - 6*int64(len(raw))
	charge := func(n int64) error {
		if n < 0 || n > remaining {
			return errors.New("sync control exceeds admitted structural decode memory")
		}
		remaining -= n
		return nil
	}
	if err := charge(0); err != nil {
		return err
	}
	type container struct {
		kind      json.Delim
		count     int
		expectKey bool
	}
	stack := make([]container, 0, 4)
	d := json.NewDecoder(bytes.NewReader(raw))
	rootSeen := false
	beginValue := func() error {
		if len(stack) == 0 {
			if rootSeen {
				return errors.New("multiple sync control values")
			}
			rootSeen = true
			return nil
		}
		parent := &stack[len(stack)-1]
		if parent.kind == '{' {
			if parent.expectKey {
				return errors.New("sync control object lacks key")
			}
			parent.expectKey = true
			return nil
		}
		parent.count++
		if parent.count > syncreconcile.MaxActions {
			return errors.New("sync control array exceeds count limit")
		}
		return charge(256)
	}
	for tokens := 0; ; tokens++ {
		if tokens%1024 == 0 {
			if err := hold.CheckActive(ctx); err != nil {
				return err
			}
		}
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch v := token.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				if err := beginValue(); err != nil {
					return err
				}
				if len(stack) >= 4 {
					return errors.New("sync control nesting exceeds limit")
				}
				if err := charge(128); err != nil {
					return err
				}
				stack = append(stack, container{kind: v, expectKey: v == '{'})
			case '}', ']':
				if len(stack) == 0 || stack[len(stack)-1].kind == '{' && v != '}' || stack[len(stack)-1].kind == '[' && v != ']' {
					return errors.New("sync control delimiter mismatch")
				}
				stack = stack[:len(stack)-1]
			}
		case string:
			if len(stack) > 0 && stack[len(stack)-1].kind == '{' && stack[len(stack)-1].expectKey {
				parent := &stack[len(stack)-1]
				parent.count++
				if parent.count > 16 {
					return errors.New("sync control object exceeds member limit")
				}
				parent.expectKey = false
				if err := charge(512 + 4*int64(len(v))); err != nil {
					return err
				}
			} else {
				if err := beginValue(); err != nil {
					return err
				}
				if err := charge(32 + 4*int64(len(v))); err != nil {
					return err
				}
			}
		default:
			if err := beginValue(); err != nil {
				return err
			}
		}
	}
	if !rootSeen || len(stack) != 0 {
		return errors.New("incomplete sync control JSON")
	}
	return nil
}

func frame(intent syncservice.Intent, kind, id string) controlFrame {
	return controlFrame{Kind: kind, Key: intent.Key, Plan: intent.PlanDigest, Batch: id}
}

func expectedSnapshot(rows []syncservice.ManifestRow, side syncreconcile.Side) syncreconcile.Snapshot {
	out := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{}}
	for _, r := range rows {
		if r.Side == side {
			out.NameKeys[r.Path] = r.NameKey
			if r.Entry != nil {
				out.Entries[r.Path] = *r.Entry
			}
		}
	}
	return out
}

func (e *endpoint) verifyManifest(ctx context.Context, intent syncservice.Intent, rows []syncservice.ManifestRow) error {
	var candidates []string
	for _, r := range rows {
		if r.Side == e.Side {
			candidates = append(candidates, r.Path)
		}
	}
	native, err := syncfs.DetectNamePolicy(e.Root, e.WorkParent)
	if err != nil {
		return err
	}
	if native != e.Names {
		return syncfs.ErrChanged
	}
	entryLimit := min(int64(syncreconcile.MaxEntries), e.Hold.MemoryBytes()/4096)
	if entryLimit < 1 {
		return errors.New("admitted memory cannot cover selected manifest")
	}
	actual, err := e.Root.Scan(ctx, candidates, intent.Binding.Exclusions, e.Names, e.Metadata, syncfs.ScanLimits{Entries: int(entryLimit), Bytes: 1 << 40})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expectedSnapshot(rows, e.Side)) {
		return syncfs.ErrChanged
	}
	return e.Hold.CheckActive(ctx)
}

func (e *endpoint) boundedLookup(ctx context.Context) (syncservice.Lookup, error) {
	if e.Ledger.DB == nil {
		return syncservice.Lookup{}, errors.New("missing durable sync ledger")
	}
	if err := e.Hold.CheckActive(ctx); err != nil {
		return syncservice.Lookup{}, err
	}
	var record, manifest int64
	err := e.Ledger.DB.QueryRowContext(ctx, `SELECT length(record),length(manifest) FROM sync_operations WHERE key=? AND side=?`, e.Ledger.Key, e.Side).Scan(&record, &manifest)
	if err != nil {
		return syncservice.Lookup{}, err
	}
	if record < 1 || record > syncservice.MaxRecordBytes || manifest < 1 || manifest > syncservice.MaxManifestBytes || record+manifest > e.Hold.MemoryBytes()/8 {
		return syncservice.Lookup{}, errors.New("stored sync evidence exceeds admitted memory")
	}
	return e.Ledger.Lookup(ctx)
}

// runEndpoint is intentionally private. The Manager adapter must create its
// hold and session from the same exact admitted SSH execution before calling.
func runEndpoint(ctx context.Context, e *endpoint) (retErr error) {
	if ctx == nil || e == nil || e.Session == nil || e.Hold == nil || e.Ledger == nil {
		return errors.New("admitted endpoint required")
	}
	var closeOnce sync.Once
	var closeErr error
	closeSession := func() error { closeOnce.Do(func() { closeErr = e.Session.Close() }); return closeErr }
	cancelDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(cancelDone)
		_ = e.Session.Abort()
	})
	defer func() {
		if !stopCancel() {
			<-cancelDone
		}
	}()
	joined := false
	defer func() {
		if joined {
			return
		}
		closeErr := e.Session.Abort()
		joinErr := e.Session.Join()
		retErr = errors.Join(retErr, closeErr, joinErr)
	}()
	lookup, err := e.boundedLookup(ctx)
	if err != nil {
		return err
	}
	intent := lookup.Intent
	if lookup.State != "reserved" || intent.Side != e.Side {
		return syncservice.ErrUnknown
	}
	if intent.WorkerCommandSHA256 != FixedCommandSHA256() {
		return errors.New("unreviewed SSH worker command")
	}
	if err = e.Hold.CheckActive(ctx); err != nil {
		return err
	}
	if err = e.Hold.CheckIntent(ctx, intent); err != nil {
		return err
	}
	var prep syncservice.Preparation
	control := e.Session.Control()
	if control == nil {
		return errors.New("missing admitted SSH control")
	}
	if e.Side == syncreconcile.Local {
		if !e.PreparedSent {
			if err = syncservice.WritePreparation(control, syncservice.Preparation{Version: syncservice.Schema, Intent: intent, Manifest: lookup.Manifest}); err != nil {
				return err
			}
		}
	} else {
		if e.Prepared != nil {
			prep = *e.Prepared
			if prep.Version != syncservice.Schema {
				return errors.New("charged preparation version changed")
			}
		} else {
			prep, err = syncservice.ReadPreparation(ctx, control, e.Hold)
			if err != nil {
				return err
			}
		}
		prep.Intent.Side = syncreconcile.Remote
		if !reflect.DeepEqual(prep.Intent, intent) || !reflect.DeepEqual(prep.Manifest, lookup.Manifest) {
			return errors.New("SSH preparation differs from destination admission")
		}
	}
	var prior *syncreconcile.ConfirmedBaseline
	if intent.Generation > 1 {
		prior, err = syncservice.LoadBaseline(ctx, e.Ledger.DB, intent.Binding, e.Side)
		if err != nil {
			return err
		}
	}
	plan, err := syncservice.ReconcileIntent(intent, lookup.Manifest, prior)
	if err != nil {
		return err
	}
	if err = e.check(ctx, intent, plan); err != nil {
		return err
	}
	need, err := forecast(intent, e.Side)
	if err != nil {
		return err
	}
	reviewedDisk := intent.LocalResources.Disk
	if e.Side == syncreconcile.Remote {
		reviewedDisk = intent.RemoteResources.Disk
	}
	if reviewedDisk < need {
		return errors.New("reviewed disk promise below physical stage forecast")
	}
	rootID, err := e.Root.NativeID()
	if err != nil {
		return err
	}
	if e.Hold.DiskBytes() < need {
		return errors.New("admitted disk hold below physical stage forecast")
	}
	if err = e.Hold.ChargeVolume(ctx, rootID, need); err != nil {
		return err
	}
	if err = e.verifyManifest(ctx, intent, lookup.Manifest); err != nil {
		return err
	}
	if e.Side == syncreconcile.Remote {
		if err = writeFrame(control, frame(intent, "admitted", ""), e.Hold); err != nil {
			return err
		}
	} else if _, err = readFrame(ctx, control, e.Hold, intent, "admitted", ""); err != nil {
		return err
	}
	work, err := e.workspace(plan, need)
	if err != nil {
		if work != nil {
			_ = work.Close()
		}
		return err
	}
	defer work.Close()
	if _, err = syncfs.SeedReceiverBasis(ctx, e.Root, work.Stage, plan, e.Side, e.Names, e.Metadata, need); err != nil {
		return err
	}
	var dirs []syncfs.DirectoryMetadata
	for _, b := range batches(plan) {
		id := batchID(b)
		if e.Side == b.Source {
			sourceWork, ownDirs, matErr := e.materialize(ctx, plan, b, need)
			if sourceWork != nil {
				defer sourceWork.Close()
			}
			if matErr != nil {
				return matErr
			}
			ready := frame(intent, "ready", id)
			ready.Dirs = ownDirs
			if err = writeFrame(control, ready, e.Hold); err != nil {
				return err
			}
			if b.Receiver == e.Side {
				dirs = append(dirs, ownDirs...)
				err = runLocalBatch(ctx, sourceWork.StagePath(), work.StagePath(), e.Metadata)
			} else {
				var handle transferHandle
				handle, err = e.Session.OpenTransfer(ctx, b)
				if err == nil {
					err = runTransfer(ctx, handle, rsyncengine.ClientSender, sourceWork.StagePath(), e.Metadata)
				}
			}
			if err != nil {
				return err
			}
			if err = writeFrame(control, frame(intent, "done", id), e.Hold); err != nil {
				return err
			}
			if _, err = readFrame(ctx, control, e.Hold, intent, "ack", id); err != nil {
				return err
			}
		} else {
			ready, readErr := readFrame(ctx, control, e.Hold, intent, "ready", id)
			if readErr != nil {
				return readErr
			}
			if e.Side == b.Receiver {
				dirs = append(dirs, ready.Dirs...)
				var handle transferHandle
				handle, err = e.Session.OpenTransfer(ctx, b)
				if err == nil {
					err = runTransfer(ctx, handle, rsyncengine.ServerReceiver, work.StagePath(), e.Metadata)
				}
				if err != nil {
					return err
				}
			}
			if _, err = readFrame(ctx, control, e.Hold, intent, "done", id); err != nil {
				return err
			}
			if err = writeFrame(control, frame(intent, "ack", id), e.Hold); err != nil {
				return err
			}
		}
	}
	if err = e.publish(ctx, plan, work, sortedDirs(dirs)); err != nil {
		return err
	}
	if err = e.Hold.CheckActive(ctx); err != nil {
		return err
	}
	if err = e.Root.Check(); err != nil {
		return err
	}
	e.Receipt, err = ptrReceipt(e.Ledger.CompleteOwnSide(ctx))
	if err != nil {
		return err
	}
	if e.Side == syncreconcile.Local {
		own := frame(intent, "receipt", "")
		own.Receipt = e.Receipt
		if err = writeFrame(control, own, e.Hold); err != nil {
			return err
		}
		peer, readErr := readFrame(ctx, control, e.Hold, intent, "receipt", "")
		if readErr != nil {
			return readErr
		}
		if peer.Receipt == nil {
			return errors.New("missing peer receipt")
		}
		if err = e.Ledger.RecordPeerReceipt(ctx, *peer.Receipt); err != nil {
			return err
		}
		if err = writeFrame(control, frame(intent, "receipt-ack", ""), e.Hold); err != nil {
			return err
		}
	} else {
		peer, readErr := readFrame(ctx, control, e.Hold, intent, "receipt", "")
		if readErr != nil {
			return readErr
		}
		if peer.Receipt == nil {
			return errors.New("missing peer receipt")
		}
		if err = e.Ledger.RecordPeerReceipt(ctx, *peer.Receipt); err != nil {
			return err
		}
		own := frame(intent, "receipt", "")
		own.Receipt = e.Receipt
		if err = writeFrame(control, own, e.Hold); err != nil {
			return err
		}
		if _, err = readFrame(ctx, control, e.Hold, intent, "receipt-ack", ""); err != nil {
			return err
		}
	}
	// Baseline confirmation is outside the defer: actual channel workers must
	// have joined successfully before a receipt becomes jointly confirmed.
	closeErr = closeSession()
	joinErr := e.Session.Join()
	joined = true
	if err = errors.Join(closeErr, joinErr); err != nil {
		return err
	}
	if err = e.Hold.CheckActive(ctx); err != nil {
		return err
	}
	if _, err = e.Ledger.ConfirmBaseline(ctx); err != nil {
		return err
	}
	return nil
}

func ptrReceipt(r syncservice.SideReceipt, err error) (*syncservice.SideReceipt, error) {
	if err != nil {
		return nil, err
	}
	return &r, nil
}
