package sshservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func rewriteArchivedReference(t *testing.T, home string, b Binding, op SourceHistoryOperation, mode string) {
	t.Helper()
	db, e := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if mode == "erased-ref" {
		if _, e = db.Exec(`DELETE FROM source_history_refs`); e != nil {
			t.Fatal(e)
		}
		return
	}
	m := op.Request.Plan.Members[0]
	if mode == "matched-op-and-ref" {
		m.Key = "replacement-ref-key"
		tuple := m.Source + "\x00" + m.Target + "\x00" + m.Key
		m.File = "ssh-complete-" + stateHash([]byte(tuple)) + ".json"
		if m.PendingFile != "" {
			m.PendingFile = "ssh-client-" + stateHash([]byte(tuple)) + ".json"
		}
		op.Request.Plan.Members[0] = m
		op.Request.Plan.SHA256 = historyPlanDigest(op.Request.Plan)
		op.RequestSHA256 = historyRequestSHA(op.Request)
		op.Cleaned = []string{m.File}
		if e = op.validate(b); e != nil {
			t.Fatal("retargeted operation fixture is not individually valid", e)
		}
		raw, e := json.Marshal(op)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`UPDATE source_history_operations SET request=?,data=? WHERE key=?`, mustHistoryJSON(op.Request), string(raw), op.Request.Key); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`UPDATE source_history_refs SET key='replacement-ref-key',record=?`, mustHistoryJSON(sourceHistoryKey(m))); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
}
func TestSourceHistoryDamagedAbsentRefCannotReopenArchivedKey(t *testing.T) {
	for _, mode := range []string{"retargeted-ref", "erased-ref", "matched-op-and-ref"} {
		t.Run(mode, func(t *testing.T) {
			home, b, items := settledSourceHistoryFixture(t, 1)
			op, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "absent-ref"))
			if e != nil {
				t.Fatal(e)
			}
			next := sourceFixture(t, home, b, items[0].Request.Pin, items[0].Request.Key)
			defer os.Remove(next.Directory.Path)
			rewriteArchivedReference(t, home, b, op, mode)
			if mode != "erased-ref" {
				if _, e = ReadState(t.Context(), home, b); e != nil {
					t.Fatal("balanced catalog fixture lost its intended ordinary-read precondition", e)
				}
			}
			got, e := LookupSourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key)
			if e == nil && !got.Found {
				t.Fatal("damaged reference returned definitive absence for an immutable archived key")
			}
			if e = ReserveSource(t.Context(), home, b, next); e == nil {
				t.Fatal("damaged catalog permitted fresh dispatch of old key")
			}
			if _, e = os.Lstat(filepath.Join(home, "mesh", intentName(next))); !os.IsNotExist(e) {
				t.Fatal("refusal wrote new source intent", e)
			}
			if _, e = preverifySourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key); e == nil {
				t.Fatal("settlement absent path accepted damaged closure")
			}
		})
	}
}
func TestSourceHistoryAbsentClosureOffLockAndVersionBound(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	op, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "absence-locks"))
	if e != nil {
		t.Fatal(e)
	}
	ctx := &absenceValidationBarrierContext{Context: t.Context(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, e := preverifySourceAbsence(ctx, home, b, items[0].Request.Pin.Target, "unused-key")
		done <- e
	}()
	select {
	case <-ctx.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("complete absence validation pipeline not reached")
	}
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- withStateMutation(t.Context(), home, b, true, func(*State, *os.File) error { return nil })
	}()
	select {
	case e := <-lockDone:
		if e != nil {
			close(ctx.release)
			<-done
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		close(ctx.release)
		<-done
		t.Fatal("absence payload hashing held source locks")
	}
	close(ctx.release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	verified, e := preverifySourceAbsence(t.Context(), home, b, items[0].Request.Pin.Target, "unused-key")
	if e != nil {
		t.Fatal(e)
	}
	rewriteArchivedReference(t, home, b, op, "retargeted-ref")
	if e = withStateMutation(t.Context(), home, b, true, func(s *State, _ *os.File) error { return verified.check(s) }); e == nil {
		t.Fatal("changed catalog reused prior absence witness")
	}
}
func TestSourceHistoryAbsentLegacyWitnessRejectsNewArchive(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	verified, e := preverifySourceAbsence(t.Context(), home, b, items[0].Request.Pin.Target, "new-key")
	if e != nil {
		t.Fatal(e)
	}
	if verified.Marker != nil {
		t.Fatal("fixture already had history")
	}
	if _, e = ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "appeared")); e != nil {
		t.Fatal(e)
	}
	if e = withStateMutation(t.Context(), home, b, true, func(s *State, _ *os.File) error { return verified.check(s) }); e == nil {
		t.Fatal("legacy absence witness accepted new archive")
	}
}

type absenceValidationBarrierContext struct {
	context.Context
	once             sync.Once
	entered, release chan struct{}
}

func (c *absenceValidationBarrierContext) Err() error {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "historyCatalog).validateFull") {
			c.once.Do(func() { close(c.entered); <-c.release })
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestSourceHistoryAbsenceGuardRechecksAfterIntentStageSync(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	op, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "late-absence"))
	if e != nil {
		t.Fatal(e)
	}
	verified, e := preverifySourceAbsence(t.Context(), home, b, items[0].Request.Pin.Target, "unused-key")
	if e != nil {
		t.Fatal(e)
	}
	next := sourceFixture(t, home, b, items[0].Request.Pin, "unused-key")
	defer os.Remove(next.Directory.Path)
	fired := false
	e = withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error {
		bindSourceAbsenceGuard(s, verified)
		s.syncFile = func(f *os.File) error {
			if e := f.Sync(); e != nil {
				return e
			}
			fired = true
			rewriteArchivedReference(t, home, b, op, "retargeted-ref")
			return nil
		}
		raw, e := json.Marshal(next)
		if e != nil {
			return e
		}
		return atomicStateFile(t.Context(), s, d, intentName(next), raw, nil)
	})
	if !fired || e == nil {
		t.Fatal("late catalog mutation did not refuse intent publication")
	}
	if _, e = os.Lstat(filepath.Join(home, "mesh", intentName(next))); !os.IsNotExist(e) {
		t.Fatal("new intent published after catalog change", e)
	}
}

func TestSourceHistoryLegacyAbsenceGuardRechecksPhysicalNamespace(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	verified, e := preverifySourceAbsence(t.Context(), home, b, items[0].Request.Pin.Target, "unused-key")
	if e != nil {
		t.Fatal(e)
	}
	next := sourceFixture(t, home, b, items[0].Request.Pin, "unused-key")
	defer os.Remove(next.Directory.Path)
	fired := false
	e = withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error {
		bindSourceAbsenceGuard(s, verified)
		s.syncFile = func(f *os.File) error {
			if e := f.Sync(); e != nil {
				return e
			}
			fired = true
			return os.Mkdir(filepath.Join(home, SourceHistoryName), 0700)
		}
		raw, e := json.Marshal(next)
		if e != nil {
			return e
		}
		return atomicStateFile(t.Context(), s, d, intentName(next), raw, nil)
	})
	if !fired || e == nil {
		t.Fatal("new physical archive namespace reused legacy absence witness")
	}
	if _, e = os.Lstat(filepath.Join(home, "mesh", intentName(next))); !os.IsNotExist(e) {
		t.Fatal("intent published after new namespace appeared", e)
	}
	if _, e = os.Stat(filepath.Join(home, SourceHistoryName)); e != nil {
		t.Fatal("partial unknown namespace was removed", e)
	}
}

func TestSourceHistoryAbsenceGuardRechecksPackAfterIntentStageSync(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	op, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "late-pack-absence"))
	if e != nil {
		t.Fatal(e)
	}
	verified, e := preverifySourceAbsence(t.Context(), home, b, items[0].Request.Pin.Target, "unused-key")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, SourceHistoryName, "packs", "pack-"+op.Receipt.ID, "records.bin")
	before, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) == 0 {
		t.Fatal("pack fixture lacks records", e)
	}
	raw[len(raw)-1] ^= 1
	next := sourceFixture(t, home, b, items[0].Request.Pin, "unused-key")
	defer os.Remove(next.Directory.Path)
	fired := false
	e = withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error {
		bindSourceAbsenceGuard(s, verified)
		s.syncFile = func(f *os.File) error {
			if e := f.Sync(); e != nil {
				return e
			}
			fired = true
			if e := os.WriteFile(path, raw, 0600); e != nil {
				return e
			}
			// Equal size and restored mtime must not make changed verified
			// bytes reusable. The native ctime is part of the witness.
			return os.Chtimes(path, before.ModTime(), before.ModTime())
		}
		raw, e := json.Marshal(next)
		if e != nil {
			return e
		}
		return atomicStateFile(t.Context(), s, d, intentName(next), raw, nil)
	})
	if !fired || e == nil {
		t.Fatal("late same-size pack mutation did not refuse intent publication")
	}
	if _, e = os.Lstat(filepath.Join(home, "mesh", intentName(next))); !os.IsNotExist(e) {
		t.Fatal("new intent published after immutable pack changed", e)
	}
}

func TestSourceHistoryPositiveFenceDoesNotStreamCompleteAbsenceProof(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	if _, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "positive-fast")); e != nil {
		t.Fatal(e)
	}
	next := sourceFixture(t, home, b, items[0].Request.Pin, items[0].Request.Key)
	defer os.Remove(next.Directory.Path)
	ctx := &absenceValidationBarrierContext{Context: t.Context(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- ReserveSource(ctx, home, b, next) }()
	select {
	case <-ctx.entered:
		close(ctx.release)
		<-done
		t.Fatal("a retained positive fence streamed a complete absence proof")
	case e := <-done:
		close(ctx.release)
		if e != ErrIntentExists {
			t.Fatal("positive fence did not conservatively refuse execution", e)
		}
	case <-time.After(5 * time.Second):
		close(ctx.release)
		<-done
		t.Fatal("positive fence did not finish its bounded metadata path")
	}
}
