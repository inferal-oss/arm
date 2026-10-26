package sshservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/inferal-oss/arm/internal/historypack"
	"github.com/inferal-oss/arm/internal/local"
	"golang.org/x/sys/unix"
)

func hashHistoryFile(ctx context.Context, dir *os.File, name string, limit int64) (historypack.Identity, int64, string, time.Time, error) {
	id, size, e := sourceFileIdentity(dir, name, limit)
	if e != nil {
		return id, size, "", time.Time{}, e
	}
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return id, size, "", time.Time{}, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	v, e := statState(f)
	if e != nil || historyID(v) != id || v.Size != size {
		return id, size, "", time.Time{}, fmt.Errorf("history file changed before hashing")
	}
	info, e := f.Stat()
	if e != nil {
		return id, size, "", time.Time{}, e
	}
	modified := info.ModTime()
	h := sha256.New()
	buffer := make([]byte, 64<<10)
	var count int64
	for {
		if e = ctx.Err(); e != nil {
			return id, size, "", modified, e
		}
		n, readErr := f.Read(buffer)
		if n > 0 {
			count += int64(n)
			if count > size {
				return id, size, "", modified, fmt.Errorf("history file grew during hash")
			}
			h.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return id, size, "", modified, readErr
		}
		if n == 0 {
			return id, size, "", modified, io.ErrNoProgress
		}
	}
	current, currentSize, e := sourceFileIdentity(dir, name, limit)
	if e != nil || current != id || currentSize != size || count != size {
		return id, size, "", modified, fmt.Errorf("history file identity/size changed during hash")
	}
	after, e := f.Stat()
	if e != nil || after.ModTime() != modified {
		return id, size, "", modified, fmt.Errorf("history file version changed during hash")
	}
	return id, size, hex.EncodeToString(h.Sum(nil)), modified, nil
}

// PlanSourceHistoryRecovery only observes an exact private journal. Existing
// keyed attempts resolve before current-journal checks, including when SQLite
// already removed that journal but a pending durable fence survived a crash.
func PlanSourceHistoryRecovery(ctx context.Context, home string, b Binding, key, reason string) (SourceHistoryRecoveryRequest, error) {
	if !stateText(key, 128) || !stateText(reason, 4096) {
		return SourceHistoryRecoveryRequest{}, fmt.Errorf("exact recovery key/reason required")
	}
	s, e := readSourceState(ctx, home, b, false)
	if e != nil {
		return SourceHistoryRecoveryRequest{}, e
	}
	if s.historyMarker == nil {
		return SourceHistoryRecoveryRequest{}, fmt.Errorf("existing source catalog required")
	}
	m := *s.historyMarker
	previous, _, e := readSourceRecovery(ctx, home, m)
	if e != nil {
		return SourceHistoryRecoveryRequest{}, e
	}
	if previous != nil {
		for _, a := range previous.Attempts {
			if a.Key == key {
				if a.Reason != reason {
					return SourceHistoryRecoveryRequest{}, fmt.Errorf("recovery idempotency conflict")
				}
				return recoveryRequest(m, a), nil
			}
		}
		if previous.Pending() {
			return SourceHistoryRecoveryRequest{}, ErrHistoryBusy
		}
	}
	dir, e := privateDirectory(filepath.Join(home, SourceHistoryName))
	if e != nil {
		return SourceHistoryRecoveryRequest{}, e
	}
	defer dir.Close()
	if e = checkHistorySidecars(dir, true); e != nil {
		return SourceHistoryRecoveryRequest{}, e
	}
	id, size, hash, _, e := hashHistoryFile(ctx, dir, SourceHistoryCatalogName+"-journal", SourceHistoryMaxCatalogBytes+4096)
	if e != nil {
		return SourceHistoryRecoveryRequest{}, e
	}
	q := SourceHistoryRecoveryRequest{1, key, reason, m, id, size, hash}
	return q, q.Validate()
}
func nativeRecoveryCatalog(ctx context.Context, home string, m SourceHistoryMarker, policy func() error) (*historyCatalog, error) {
	dir, e := privateDirectory(filepath.Join(home, SourceHistoryName))
	if e != nil {
		return nil, e
	}
	c := &historyCatalog{ctx: ctx, home: home, path: filepath.Join(home, SourceHistoryName), marker: m, directory: dir, write: true, guardPolicy: policy, allowRecovery: true}
	if e = c.guard(); e != nil {
		dir.Close()
		return nil, e
	}
	return c, nil
}

// RecoverSourceHistoryCatalog is an explicit reviewed operation. It fences
// ordinary admission BEFORE any RW SQLite open, then holds the exact worker
// lock while recovery/full semantic verification run OUTSIDE source locks.
func RecoverSourceHistoryCatalog(ctx context.Context, home string, b Binding, q SourceHistoryRecoveryRequest) (SourceHistoryRecoveryAttempt, error) {
	return recoverSourceHistoryCatalog(ctx, home, b, q, nil)
}
func recoverSourceHistoryCatalog(ctx context.Context, home string, b Binding, q SourceHistoryRecoveryRequest, fault historyFault) (out SourceHistoryRecoveryAttempt, err error) {
	if q.Validate() != nil || q.Marker.Binding != b {
		return out, fmt.Errorf("valid exact reviewed source recovery required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// Revalidate a NEW reviewed journal outside source locks before publishing
	// any fence. Inside the short lock we compare this exact native version;
	// an existing durable pending attempt instead preserves its original bytes
	// even after SQLite removed the journal before a lost completion ACK.
	var newJournalVersion *sourceHistoryVersion
	if _, e := InspectSourceHistoryRecovery(ctx, home, b, q.Key); errors.Is(e, os.ErrNotExist) {
		path := filepath.Join(home, SourceHistoryName)
		name := SourceHistoryCatalogName + "-journal"
		version, e := historyVersionBound(path, name, SourceHistoryMaxCatalogBytes+4096)
		if e != nil {
			return out, e
		}
		dir, e := privateDirectory(path)
		if e != nil {
			return out, e
		}
		id, size, sha, _, e := hashHistoryFile(ctx, dir, name, SourceHistoryMaxCatalogBytes+4096)
		dir.Close()
		if e != nil {
			return out, e
		}
		after, e := historyVersionBound(path, name, SourceHistoryMaxCatalogBytes+4096)
		if e != nil || version != after || id != q.Journal || size != q.JournalBytes || sha != q.JournalSHA256 {
			return out, fmt.Errorf("reviewed journal changed before recovery intent")
		}
		newJournalVersion = &version
	} else if e != nil {
		return out, e
	}
	var worker *historyWorker
	defer func() {
		if worker != nil {
			worker.Close()
		}
	}()
	already := false
	hadPending := false
	err = withSourceMutation(ctx, home, b, true, false, func(s *State, d *os.File) error {
		if s.historyMarker == nil || *s.historyMarker != q.Marker {
			return fmt.Errorf("source recovery catalog binding changed")
		}
		old, id, e := readSourceRecovery(ctx, home, q.Marker)
		if e != nil {
			return e
		}
		if old == nil {
			old = &SourceHistoryRecovery{Schema: 1, Marker: q.Marker, Attempts: []SourceHistoryRecoveryAttempt{}}
		}
		for _, a := range old.Attempts {
			if a.Key == q.Key {
				if !reflect.DeepEqual(recoveryRequest(q.Marker, a), q) {
					return fmt.Errorf("recovery exact request changed")
				}
				out = a
				if a.Completed != nil {
					already = true
					return nil
				}
				hadPending = true
			}
		}
		if !hadPending && old.Pending() {
			return ErrHistoryBusy
		}
		c, e := nativeRecoveryCatalog(ctx, home, q.Marker, s.guard)
		if e != nil {
			return e
		}
		defer c.directory.Close()
		worker, e = acquireHistoryWorker(c)
		if e != nil {
			return e
		}
		if hadPending {
			return nil
		}
		if newJournalVersion == nil {
			return fmt.Errorf("original recovery attempt disappeared; no fresh intent")
		}
		if len(old.Attempts) >= SourceHistoryRecoveryMaxAttempts {
			return fmt.Errorf("bounded recovery audit full; no record removed")
		}
		journal, size, e := sourceFileIdentity(c.directory, SourceHistoryCatalogName+"-journal", SourceHistoryMaxCatalogBytes+4096)
		if e != nil || journal != q.Journal || size != q.JournalBytes {
			return fmt.Errorf("reviewed journal identity/size changed before recovery")
		}
		currentVersion, e := historyVersionBound(c.path, SourceHistoryCatalogName+"-journal", SourceHistoryMaxCatalogBytes+4096)
		if e != nil || currentVersion != *newJournalVersion {
			return fmt.Errorf("reviewed journal version changed before intent")
		}
		out = SourceHistoryRecoveryAttempt{Key: q.Key, Reason: q.Reason, Actor: local.CurrentIdentity(), Journal: q.Journal, JournalBytes: q.JournalBytes, JournalSHA256: q.JournalSHA256, Started: time.Now().UTC()}
		old.Attempts = append(old.Attempts, out)
		if e = old.Validate(); e != nil {
			return e
		}
		raw, e := json.Marshal(old)
		if e != nil {
			return e
		}
		return atomicStateRecord(ctx, s, d, SourceHistoryRecoveryName, raw, id, SourceHistoryRecoveryMaxBytes)
	})
	if err != nil || already {
		return out, err
	}
	if err = historyCheckpoint(fault, "after_recovery_fence"); err != nil {
		return out, err
	}
	// Native-only policy is valid beyond the original short source-lock callback.
	// It never infers process absence; the exact held worker FD excludes writers.
	policy := func() error {
		m, e := historyCanonicalMarker(ctx, home, b)
		if e != nil || m == nil || *m != q.Marker {
			return fmt.Errorf("recovery authority/marker changed")
		}
		v, _, e := readSourceRecovery(ctx, home, q.Marker)
		if e != nil || v == nil || !v.Pending() || v.Attempts[len(v.Attempts)-1].Key != q.Key {
			return fmt.Errorf("exact pending recovery fence changed")
		}
		return nil
	}
	native, e := nativeRecoveryCatalog(ctx, home, q.Marker, policy)
	if e != nil {
		return out, e
	}
	defer native.directory.Close()
	if e = worker.check(native); e != nil {
		return out, e
	}
	journal, size, hash, _, e := hashHistoryFile(ctx, native.directory, SourceHistoryCatalogName+"-journal", SourceHistoryMaxCatalogBytes+4096)
	if errors.Is(e, unix.ENOENT) && hadPending {
		e = nil
	} else if e == nil && (journal != q.Journal || size != q.JournalBytes || hash != q.JournalSHA256) {
		e = fmt.Errorf("journal differs from original recovery request")
	}
	if e != nil {
		return out, e
	}
	guard := func() error {
		if e := policy(); e != nil {
			return e
		}
		return worker.check(native)
	}
	catalog, e := openHistoryCatalog(ctx, home, b, q.Marker, true, true, guard)
	if e != nil {
		return out, e
	}
	if e = historyCheckpoint(fault, "after_sqlite_recovery"); e != nil {
		catalog.close()
		return out, e
	}
	version, e := captureSourceCatalogVersion(ctx, catalog)
	if e != nil {
		catalog.close()
		return out, e
	}
	if e = catalog.validateFull(ctx); e != nil {
		catalog.close()
		return out, fmt.Errorf("recovered SQLite requires semantic inspection; fence remains pending: %w", e)
	}
	summary, e := catalog.summary(ctx)
	if e != nil {
		catalog.close()
		return out, e
	}
	if e = version.check(catalog.path); e != nil {
		catalog.close()
		return out, e
	}
	if e = catalog.close(); e != nil {
		return out, e
	}
	if e = checkHistorySidecars(native.directory, false); e != nil {
		return out, e
	}
	_, bytes, sha, _, e := hashHistoryFile(ctx, native.directory, SourceHistoryCatalogName, SourceHistoryMaxCatalogBytes)
	if e != nil {
		return out, e
	}
	if e = version.check(native.path); e != nil {
		return out, e
	}
	result := SourceHistoryRecoveryResult{sha, bytes, summary.Packs, summary.Archived}
	if e = historyCheckpoint(fault, "after_recovery_validation"); e != nil {
		return out, e
	}
	err = withSourceMutation(ctx, home, b, true, false, func(s *State, d *os.File) error {
		if s.historyMarker == nil || *s.historyMarker != q.Marker {
			return fmt.Errorf("recovery marker changed before completion")
		}
		if e = worker.check(native); e != nil {
			return e
		}
		if e = version.check(native.path); e != nil {
			return e
		}
		record, id, e := readSourceRecovery(ctx, home, q.Marker)
		if e != nil || record == nil || !record.Pending() {
			return fmt.Errorf("exact pending recovery receipt missing")
		}
		last := &record.Attempts[len(record.Attempts)-1]
		if !reflect.DeepEqual(recoveryRequest(q.Marker, *last), q) {
			return fmt.Errorf("original recovery request changed")
		}
		finished := time.Now().UTC()
		last.Completed = &finished
		last.Result = &result
		if e = record.Validate(); e != nil {
			return e
		}
		raw, e := json.Marshal(record)
		if e != nil {
			return e
		}
		if e = historyCheckpoint(fault, "before_recovery_completed"); e != nil {
			return e
		}
		if e = version.check(native.path); e != nil {
			return e
		}
		if e = worker.check(native); e != nil {
			return e
		}
		// Retain complete verified native evidence through the atomic writer's
		// post-fsync guard too; the earlier check alone cannot authorize a
		// completion after pack/catalog/worker evidence changes during staging.
		previousGuard := s.guard
		s.guard = func() error {
			if e := previousGuard(); e != nil {
				return e
			}
			if e := native.guard(); e != nil {
				return e
			}
			if e := version.check(native.path); e != nil {
				return e
			}
			return worker.check(native)
		}
		if fault != nil {
			s.syncFile = func(f *os.File) error {
				if e := f.Sync(); e != nil {
					return e
				}
				return historyCheckpoint(fault, "after_recovery_stage_fsync")
			}
		}
		if e = atomicStateRecord(ctx, s, d, SourceHistoryRecoveryName, raw, id, SourceHistoryRecoveryMaxBytes); e != nil {
			return e
		}
		out = *last
		return historyCheckpoint(fault, "after_recovery_completed")
	})
	return out, err
}
