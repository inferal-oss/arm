package sshservice

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/yrashk/arm/internal/historypack"
	"github.com/yrashk/arm/internal/local"
	"golang.org/x/sys/unix"
)

type historyFault func(string) error

func historyCheckpoint(f historyFault, phase string) error {
	if f != nil {
		return f(phase)
	}
	return nil
}
func withHistoryMutation(ctx context.Context, home string, b Binding, fn func(*State, *os.File, *historyCatalog) error) error {
	return withStateMutation(ctx, home, b, true, func(s *State, d *os.File) error {
		if s.historyMarker == nil {
			return fmt.Errorf("source catalog is not initialized")
		}
		c, e := openHistoryCatalog(ctx, home, b, *s.historyMarker, true, false, s.guard)
		if e != nil {
			return e
		}
		defer c.close()
		return fn(s, d, c)
	})
}

// InspectSourceHistoryOperation does not acquire an exclusive worker lock or
// revalidate a replacement plan. Exact receipts stay readable during another
// archive worker, and a missing/corrupt catalog never means a reusable key.
func InspectSourceHistoryOperation(ctx context.Context, home string, b Binding, key string) (SourceHistoryOperation, error) {
	if !stateText(key, 128) {
		return SourceHistoryOperation{}, fmt.Errorf("exact archive key required")
	}
	m, e := historyCanonicalMarker(ctx, home, b)
	if e != nil {
		return SourceHistoryOperation{}, e
	}
	if m == nil {
		return SourceHistoryOperation{}, sql.ErrNoRows
	}
	c, e := openHistoryCatalog(ctx, home, b, *m, false, false, nil)
	if e != nil {
		return SourceHistoryOperation{}, e
	}
	defer c.close()
	return c.lookupOp(ctx, key)
}

// ArchiveSourceHistory requires an already reviewed exact request. Consent and
// machine/TTY presentation belong to its future native caller. Existing exact
// keys return their original state READ-ONLY; only explicit Reconcile may resume.
func ArchiveSourceHistory(ctx context.Context, home string, b Binding, q SourceHistoryRequest) (SourceHistoryOperation, error) {
	return archiveSourceHistory(ctx, home, b, q, nil)
}
func archiveSourceHistory(ctx context.Context, home string, b Binding, q SourceHistoryRequest, fault historyFault) (out SourceHistoryOperation, err error) {
	if q.Validate() != nil || q.Plan.Binding != b {
		return out, fmt.Errorf("exact valid source archive request required")
	}
	var worker *historyWorker
	defer func() {
		if worker != nil {
			worker.Close()
		}
	}()
	var records []historypack.Record
	fresh := false
	err = withStateMutation(ctx, home, b, true, func(s *State, d *os.File) error {
		var c *historyCatalog
		if s.historyMarker != nil {
			var e error
			c, e = openHistoryCatalog(ctx, home, b, *s.historyMarker, true, false, s.guard)
			if e != nil {
				return e
			}
			defer c.close()
			saved, e := c.lookupOp(ctx, q.Key)
			if e == nil {
				if !reflect.DeepEqual(saved.Request, q) {
					return fmt.Errorf("archive idempotency conflict; original request remains unchanged")
				}
				out = saved
				return nil
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		current, e := planSourceState(ctx, s, q.Plan.Selection)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(current, q.Plan) {
			return fmt.Errorf("source archive reviewed plan changed")
		}
		if c == nil {
			m, e := initializeHistory(ctx, s, d)
			if e != nil {
				return e
			}
			s.historyMarker = &m
			c, e = openHistoryCatalog(ctx, home, b, m, true, false, s.guard)
			if e != nil {
				return e
			}
			defer c.close()
		}
		if e = c.ensureCapacity(ctx, len(q.Plan.Members)); e != nil {
			return e
		}
		worker, e = acquireHistoryWorker(c)
		if e != nil {
			return e
		}
		records, e = selectedHistoryPayloads(s, q.Plan.Members, false)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		out = SourceHistoryOperation{Schema: 1, Request: q, RequestSHA256: historyRequestSHA(q), Actor: local.CurrentIdentity(), Created: now, Updated: now, Revision: 1, Phase: "prepared", PackID: local.ID(), Cleaned: []string{}}
		if e = c.saveOp(ctx, out, true); e != nil {
			return e
		}
		fresh = true
		return nil
	})
	if err != nil || !fresh {
		return out, err
	}
	return continueSourceArchive(ctx, home, b, out, records, worker, fault)
}
func selectedHistoryPayloads(s *State, members []SourceHistoryMember, allowMissing bool) ([]historypack.Record, error) {
	records := make([]historypack.Record, 0, len(members))
	for _, m := range members {
		raw, exists := s.contents[m.File]
		if !exists {
			if allowMissing {
				continue
			}
			return nil, fmt.Errorf("reviewed source receipt disappeared")
		}
		id := s.files[m.File]
		if historyID(id) != m.Identity || id.SHA != m.SHA256 || id.Size != m.Bytes {
			return nil, fmt.Errorf("reviewed source receipt identity/bytes changed")
		}
		intent, e := validateSourceHistoryPayload(raw, m)
		if e != nil {
			return nil, e
		}
		if _, e = os.Lstat(intent.Directory.Path); !os.IsNotExist(e) {
			return nil, fmt.Errorf("source invocation artifacts remain or cannot be proved absent")
		}
		if m.PendingFile != "" {
			if pending, ok := s.files[m.PendingFile]; ok {
				if historyID(pending) != *m.PendingIdentity || pending.SHA != m.PendingSHA256 || pending.SHA != m.IntentSHA256 {
					return nil, fmt.Errorf("pending duplicate changed")
				}
			}
		} else {
			if _, ok := s.files[intentName(intent)]; ok {
				return nil, fmt.Errorf("unreviewed pending source evidence appeared")
			}
		}
		records = append(records, historypack.Record{Key: sourceHistoryKey(m), Payload: bytes.Clone(raw)})
	}
	return records, nil
}
func validateSourceHistoryPayload(raw []byte, m SourceHistoryMember) (SourceIntent, error) {
	var intent SourceIntent
	if len(raw) > MaxIntentBytes || stateHash(raw) != m.SHA256 {
		return intent, fmt.Errorf("source payload bound/digest differs")
	}
	if m.Kind == "completion" {
		var v SourceCompletion
		if e := DecodeStateRecord(raw, &v); e != nil || v.Validate() != nil {
			return intent, fmt.Errorf("source completion lacks exact terminal receipt")
		}
		intent = v.Intent
	} else if m.Kind == "seal" {
		var v SourceSealCompletion
		if e := DecodeStateRecord(raw, &v); e != nil || v.Validate() != nil {
			return intent, fmt.Errorf("source seal lacks exact nonexecution commitment")
		}
		intent = v.Intent
	} else {
		return intent, fmt.Errorf("unknown source settlement kind")
	}
	original, _ := json.Marshal(intent)
	if intent.Source != m.Source || intent.Request.Pin.Target != m.Target || intent.Request.Key != m.Key || stateHash(original) != m.IntentSHA256 {
		return intent, fmt.Errorf("source payload differs from original reviewed tuple")
	}
	return intent, nil
}
func updateSourceArchive(ctx context.Context, home string, b Binding, expected SourceHistoryOperation, fn func(*SourceHistoryOperation) error) (out SourceHistoryOperation, err error) {
	err = withHistoryMutation(ctx, home, b, func(_ *State, _ *os.File, c *historyCatalog) error {
		current, e := c.lookupOp(ctx, expected.Request.Key)
		if e != nil {
			return e
		}
		if current.Revision != expected.Revision || current.RequestSHA256 != expected.RequestSHA256 {
			return fmt.Errorf("archive operation changed concurrently")
		}
		out = current
		if e = fn(&out); e != nil {
			return e
		}
		out.Revision++
		out.Updated = time.Now().UTC()
		return c.saveOp(ctx, out, false)
	})
	return
}
func continueSourceArchive(ctx context.Context, home string, b Binding, op SourceHistoryOperation, records []historypack.Record, worker *historyWorker, fault historyFault) (SourceHistoryOperation, error) {
	root := filepath.Join(home, SourceHistoryName, "packs")
	if op.Phase == "prepared" {
		p, e := historypack.Prepare(ctx, root, op.PackID, b.Host, records)
		phase := "pack_prepared"
		detail := ""
		if e != nil {
			phase = "needs_attention"
			detail = "Pack preparation failed; retain born stage and inspect exact evidence"
		}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		updated, saveErr := updateSourceArchive(saveCtx, home, b, op, func(v *SourceHistoryOperation) error {
			if p.Directory.Inode != 0 {
				v.Prepared = &p
			}
			v.Phase = phase
			v.Error = detail
			return nil
		})
		cancel()
		if saveErr != nil {
			return op, fmt.Errorf("archive prepared evidence acknowledgement unavailable; inspect exact key: %w", saveErr)
		}
		op = updated
		if e != nil {
			return op, e
		}
	}
	if op.Phase == "pack_prepared" {
		if op.Prepared == nil {
			return op, fmt.Errorf("archive lacks exact born Prepared")
		}
		r, e := historypack.Publish(ctx, root, *op.Prepared)
		if e != nil {
			return op, fmt.Errorf("archive publication outcome requires exact reconciliation: %w", e)
		}
		if e = historyCheckpoint(fault, "after_publish"); e != nil {
			return op, e
		}
		updated, e := updateSourceArchive(ctx, home, b, op, func(v *SourceHistoryOperation) error { v.Receipt = &r; v.Phase = "published"; return nil })
		if e != nil {
			return op, e
		}
		op = updated
	}
	if op.Phase == "published" {
		version, e := verifySourceArchivePackVersion(ctx, root, op)
		if e != nil {
			return op, e
		}
		updated, e := commitSourceArchive(ctx, home, b, op, version, fault)
		if e != nil {
			return op, e
		}
		op = updated
		if e = historyCheckpoint(fault, "after_catalog_commit"); e != nil {
			return op, e
		}
	}
	if op.Phase == "committed" {
		cleaned, e := cleanSourceArchive(ctx, home, b, op, worker, fault)
		if e != nil && cleaned.Request.Key == "" {
			return op, e
		}
		return cleaned, e
	}
	return op, nil
}
func verifySourceArchivePack(ctx context.Context, root string, op SourceHistoryOperation) error {
	if op.Receipt == nil {
		return fmt.Errorf("archive lacks published receipt")
	}
	if op.Receipt.RecordsBytes > SourceHistoryMaxPackBytes || op.Receipt.ManifestBytes > SourceHistoryMaxPackBytes-op.Receipt.RecordsBytes {
		return fmt.Errorf("source pack exceeds family bound")
	}
	pack, e := historypack.Open(ctx, root, *op.Receipt)
	if e != nil {
		return e
	}
	defer pack.Close()
	manifest, e := pack.Manifest()
	if e != nil || len(manifest.Entries) != len(op.Request.Plan.Members) {
		return fmt.Errorf("source pack exact member roster differs")
	}
	for _, m := range op.Request.Plan.Members {
		raw, e := pack.Read(ctx, sourceHistoryKey(m))
		if e != nil {
			return e
		}
		if _, e = validateSourceHistoryPayload(raw, m); e != nil {
			return e
		}
	}
	return pack.Verify(ctx)
}
func commitSourceArchive(ctx context.Context, home string, b Binding, expected SourceHistoryOperation, version sourceArchivePackVersion, fault historyFault) (out SourceHistoryOperation, err error) {
	err = withHistoryMutation(ctx, home, b, func(s *State, _ *os.File, c *historyCatalog) error {
		op, e := c.lookupOp(ctx, expected.Request.Key)
		if e != nil {
			return e
		}
		if op.Revision != expected.Revision || op.Phase != "published" || op.Receipt == nil {
			return fmt.Errorf("archive publication changed")
		}
		if _, e = selectedHistoryPayloads(s, op.Request.Plan.Members, false); e != nil {
			return e
		}
		if e = c.checkPackIdentity(*op.Receipt); e != nil {
			return e
		}
		if e = c.guard(); e != nil {
			return e
		}
		tx, e := c.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var packs, refs int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_history_packs`).Scan(&packs); e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_history_refs`).Scan(&refs); e != nil {
			return e
		}
		if packs >= SourceHistoryMaxPacks || len(op.Request.Plan.Members) > SourceHistoryMaxRefs-refs {
			return fmt.Errorf("archive permanent capacity exhausted; hot evidence retained")
		}
		row := SourceHistoryPack{Schema: 1, Operation: op.Request.Key, Members: len(op.Request.Plan.Members), Receipt: *op.Receipt}
		raw, _ := json.Marshal(row)
		if _, e = tx.ExecContext(ctx, `INSERT INTO source_history_packs(id,data) VALUES(?,?)`, op.PackID, string(raw)); e != nil {
			return e
		}
		for _, m := range op.Request.Plan.Members {
			record, _ := json.Marshal(sourceHistoryKey(m))
			if _, e = tx.ExecContext(ctx, `INSERT INTO source_history_refs(source,target,key,kind,pack,record,sha256,intent_sha256) VALUES(?,?,?,?,?,?,?,?)`, m.Source, m.Target, m.Key, m.Kind, op.PackID, string(record), m.SHA256, m.IntentSHA256); e != nil {
				return e
			}
		}
		out = op
		out.Phase = "committed"
		out.Revision++
		out.Updated = time.Now().UTC()
		raw, e = json.Marshal(out)
		if e != nil {
			return e
		}
		result, e := tx.ExecContext(ctx, `UPDATE source_history_operations SET phase=?,data=? WHERE key=? AND phase='published' AND request=?`, out.Phase, string(raw), op.Request.Key, mustHistoryJSON(op.Request))
		if e != nil {
			return e
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return fmt.Errorf("archive exact operation changed")
		}
		if e = historyCheckpoint(fault, "before_catalog_commit"); e != nil {
			return e
		}
		if version.Receipt != *op.Receipt {
			return fmt.Errorf("verified archive receipt changed")
		}
		if e = c.guard(); e != nil {
			return e
		}
		if e = version.check(filepath.Join(c.path, "packs")); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return fmt.Errorf("archive catalog commit outcome unknown; inspect exact key: %w", e)
		}
		return c.guard()
	})
	return
}
func mustHistoryJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

// Each unchanged five-second mutation window processes at most four reviewed
// members (up to eight exact hot files, including pending duplicates). Keeping
// this bound independent of the 64-member selection prevents one whole archive
// from consuming a single short owner-lock deadline.
const sourceHistoryCleanupBatchMembers = 4

func cleanSourceArchive(ctx context.Context, home string, b Binding, expected SourceHistoryOperation, worker *historyWorker, fault historyFault) (SourceHistoryOperation, error) {
	// Full frame/settlement verification is outside source locks. The exact
	// namespace/file identities are rechecked under them before any unlink.
	version, err := verifySourceArchivePackVersion(ctx, filepath.Join(home, SourceHistoryName, "packs"), expected)
	if err != nil {
		return expected, err
	}
	if worker == nil {
		return expected, fmt.Errorf("original archive worker is required for cleanup")
	}
	for {
		if err = ctx.Err(); err != nil {
			return expected, err
		}
		out, err := cleanSourceArchiveBatch(ctx, home, b, expected, version, worker, fault)
		if err != nil || out.Phase == "completed" {
			return out, err
		}
		if len(out.Cleaned) <= len(expected.Cleaned) {
			return out, fmt.Errorf("archive cleanup checkpoint made no progress")
		}
		// The next batch is allowed only after this exact checkpoint was
		// durably acknowledged. Unknown effects/acks always return above;
		// ordinary same-key Archive stays read-only, explicit Reconcile owns
		// any later uncertain continuation.
		if err = historyCheckpoint(fault, "after_cleanup_checkpoint"); err != nil {
			return out, err
		}
		expected = out
		historyCleanupObserve("ack", ctx, home, expected, worker, len(out.Cleaned))
	}
}

func cleanSourceArchiveBatch(ctx context.Context, home string, b Binding, expected SourceHistoryOperation, version sourceArchivePackVersion, worker *historyWorker, fault historyFault) (out SourceHistoryOperation, err error) {
	out = expected
	err = withHistoryMutation(ctx, home, b, func(s *State, d *os.File, c *historyCatalog) error {
		// Use this window's actual bounded context for SQLite and file I/O,
		// in addition to the existing native owner guards before effects.
		ctx := s.mutationContext
		historyCleanupObserve("enter", ctx, home, expected, worker, len(expected.Cleaned))
		if e := worker.check(c); e != nil {
			return e
		}
		op, e := c.lookupOp(ctx, expected.Request.Key)
		if e != nil {
			return e
		}
		// Bind the entire previously acknowledged row before either a fast
		// path or an effect. A same-revision change to progress/timestamps, or
		// an unexpected completed row, is not this worker's checkpoint.
		if !reflect.DeepEqual(op, expected) {
			return fmt.Errorf("archive cleanup expected row changed; outcome requires exact-key inspection")
		}
		if op.Phase == "completed" {
			out = op
			return nil
		}
		if op.Phase != "committed" {
			return fmt.Errorf("archive cleanup has no exact committed operation")
		}
		if op.Revision == 1<<63-1 {
			return fmt.Errorf("archive cleanup revision exhausted; preserve hot evidence")
		}
		out = op
		out.Cleaned = append([]string{}, op.Cleaned...)
		if op.Receipt == nil {
			return fmt.Errorf("committed archive lacks receipt")
		}
		if e = c.checkPackIdentity(*op.Receipt); e != nil {
			return e
		}
		cleaned := make(map[string]bool, len(op.Cleaned))
		for _, name := range op.Cleaned {
			cleaned[name] = true
		}
		for _, m := range op.Request.Plan.Members {
			if cleaned[m.File] {
				if e = sourceCleanupMemberAbsent(ctx, d, m); e != nil {
					return e
				}
			}
		}
		historyCleanupObserve("bound", ctx, home, expected, worker, len(out.Cleaned))
		processed := 0
		for _, m := range op.Request.Plan.Members {
			if cleaned[m.File] {
				continue
			}
			if processed == sourceHistoryCleanupBatchMembers {
				break
			}
			ref, e := c.ref(ctx, m.Target, m.Key)
			if e != nil || ref.Pack != op.PackID || ref.Source != m.Source || ref.Kind != m.Kind || ref.SHA256 != m.SHA256 || ref.IntentSHA256 != m.IntentSHA256 || ref.Record != sourceHistoryKey(m) {
				return fmt.Errorf("committed cold evidence cannot authorize exact hot unlink: %v", e)
			}
			for _, item := range []struct {
				name     string
				identity *historypack.Identity
				sha      string
			}{{m.File, &m.Identity, m.SHA256}, {m.PendingFile, m.PendingIdentity, m.PendingSHA256}} {
				if item.name == "" {
					continue
				}
				hotVersion, e := historyVersionBound(filepath.Join(home, "mesh"), item.name, MaxIntentBytes)
				if errors.Is(e, unix.ENOENT) {
					continue
				}
				if e != nil {
					return e
				}
				existing, id, e := readStateFile(ctx, d, item.name, MaxIntentBytes)
				if errors.Is(e, unix.ENOENT) {
					continue
				}
				if e != nil {
					return e
				}
				if item.identity == nil || historyID(id) != *item.identity || hotVersion.Identity != historyID(id) || hotVersion.Size != int64(len(existing)) || stateHash(existing) != item.sha {
					return fmt.Errorf("hot cleanup identity or bytes changed; nothing overwritten")
				}
				if e = historyCheckpoint(fault, "before_unlink"); e != nil {
					return e
				}
				if version.Receipt != *op.Receipt {
					return fmt.Errorf("verified archive receipt changed")
				}
				if e = c.guard(); e != nil {
					return e
				}
				if e = version.check(filepath.Join(c.path, "packs")); e != nil {
					return e
				}
				currentHot, e := historyVersionBound(filepath.Join(home, "mesh"), item.name, MaxIntentBytes)
				if e != nil {
					return e
				}
				if currentHot != hotVersion {
					return fmt.Errorf("verified hot receipt native version changed; evidence retained")
				}
				historyCleanupObserve("guard", ctx, home, expected, worker, len(out.Cleaned))
				if e = unix.Unlinkat(int(d.Fd()), item.name, 0); e != nil {
					return e
				}
				historyCleanupObserve("unlink", ctx, home, expected, worker, len(out.Cleaned))
				if e = historyCheckpoint(fault, "after_unlink"); e != nil {
					return e
				}
			}
			if e = d.Sync(); e != nil {
				return fmt.Errorf("hot unlink durability unknown; cold fence preserved: %w", e)
			}
			if e = historyCheckpoint(fault, "after_unlink_fsync"); e != nil {
				return e
			}
			out.Cleaned = append(out.Cleaned, m.File)
			historyCleanupObserve("sync", ctx, home, expected, worker, len(out.Cleaned))
			processed++
		}
		if e = historyCheckpoint(fault, "before_cleanup_checkpoint"); e != nil {
			return e
		}
		if e = version.check(filepath.Join(c.path, "packs")); e != nil {
			return e
		}
		if len(out.Cleaned) == len(op.Request.Plan.Members) {
			for _, m := range op.Request.Plan.Members {
				if e = sourceCleanupMemberAbsent(ctx, d, m); e != nil {
					return e
				}
			}
			out.Phase = "completed"
		}
		out.Revision++
		out.Updated = time.Now().UTC()
		if e = c.guard(); e != nil {
			return e
		}
		if e = worker.check(c); e != nil {
			return e
		}
		// Compare the complete original row, not merely its request key: a
		// changed revision/phase/receipt cannot be overwritten by a stale batch.
		raw := mustHistoryJSON(out)
		if out.validate(b) != nil || len(raw) > SourceHistoryMaxRecordBytes {
			return fmt.Errorf("invalid bounded archive cleanup checkpoint")
		}
		result, e := c.db.ExecContext(ctx, `UPDATE source_history_operations SET phase=?,data=? WHERE key=? AND request=? AND phase='committed' AND data=?`, out.Phase, raw, op.Request.Key, mustHistoryJSON(op.Request), mustHistoryJSON(op))
		if e != nil {
			return fmt.Errorf("archive cleanup checkpoint outcome unknown; inspect exact key: %w", e)
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return fmt.Errorf("archive cleanup checkpoint changed; inspect exact key")
		}
		historyCleanupObserve("checkpoint", ctx, home, expected, worker, len(out.Cleaned))
		if e = c.guard(); e != nil {
			return e
		}
		return nil
	})
	return
}

func sourceCleanupMemberAbsent(ctx context.Context, d *os.File, member SourceHistoryMember) error {
	for _, name := range []string{member.File, member.PendingFile} {
		if name == "" {
			continue
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		var st unix.Stat_t
		e := unix.Fstatat(int(d.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(e, unix.ENOENT) {
			continue
		}
		if e != nil {
			return e
		}
		return fmt.Errorf("checkpointed hot receipt reappeared; preserve it without replaying cleanup")
	}
	return nil
}

// ReconcileSourceHistory resumes only the recorded archival copy/publication/
// fence/unlink steps. It never sends SSH execution, seals a destination key or
// substitutes a fresh selection. A partial unrecorded stage remains attention.
func ReconcileSourceHistory(ctx context.Context, home string, b Binding, key string) (SourceHistoryOperation, error) {
	return reconcileSourceHistory(ctx, home, b, key, nil)
}
func reconcileSourceHistory(ctx context.Context, home string, b Binding, key string, fault historyFault) (out SourceHistoryOperation, err error) {
	if !stateText(key, 128) {
		return out, fmt.Errorf("exact archive key required")
	}
	var worker *historyWorker
	defer func() {
		if worker != nil {
			worker.Close()
		}
	}()
	var records []historypack.Record
	err = withHistoryMutation(ctx, home, b, func(s *State, _ *os.File, c *historyCatalog) error {
		op, e := c.lookupOp(ctx, key)
		if e != nil {
			return e
		}
		out = op
		if op.Phase == "completed" {
			return nil
		}
		worker, e = acquireHistoryWorker(c)
		if e != nil {
			return e
		}
		if op.Phase == "needs_attention" {
			return fmt.Errorf("partial archival stage requires evidence-preserving operator inspection; no implicit rebuild")
		}
		if op.Phase == "prepared" {
			for _, name := range []string{".stage-" + op.PackID, "pack-" + op.PackID} {
				if _, e = os.Lstat(filepath.Join(c.path, "packs", name)); !os.IsNotExist(e) {
					return fmt.Errorf("unrecorded pack path exists or is uncertain; no rebuild")
				}
			}
			records, e = selectedHistoryPayloads(s, op.Request.Plan.Members, false)
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil || out.Phase == "completed" {
		return out, err
	}
	return continueSourceArchive(ctx, home, b, out, records, worker, fault)
}
