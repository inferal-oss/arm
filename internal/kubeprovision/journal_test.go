package kubeprovision

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestJournalLockRespectsCancellation(t *testing.T) {
	j := FileJournal{Root: filepath.Join(t.TempDir(), "journal")}
	if err := j.withLock(t.Context(), func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(filepath.Join(j.Root, ".lock"), unix.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := j.withLock(ctx, func(int) error { t.Fatal("entered held lock"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held lock ignored context: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("held lock outlived cancellation")
	}
}

func TestJournalReadRejectsSymlinkAndOversizedRecord(t *testing.T) {
	p, plan, _ := fixture(t)
	j := p.Journal.(FileJournal)
	if err := j.withLock(t.Context(), func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	path, err := j.path(plan.Review.Key)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.Read(t.Context(), plan.Review.Key); err == nil {
		t.Fatal("journal followed a symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 65537)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.Read(t.Context(), plan.Review.Key); err == nil {
		t.Fatal("journal accepted oversized record")
	}
}

func TestBoundedOutputRejectsOversize(t *testing.T) {
	w := newBoundedOutput(4)
	if n, err := w.Write([]byte("safe")); n != 4 || err != nil {
		t.Fatalf("first write: %d %v", n, err)
	}
	if n, err := w.Write([]byte("extra")); n != 0 || err == nil || !w.Overflow() || w.String() != "safe" {
		t.Fatalf("unbounded output: %d %v %q", n, err, w.String())
	}
}

func TestOversizedExecOutputCancelsStream(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bounded := newBoundedOutput(4)
	guard := &streamOutput{writer: bounded, cancel: cancel}
	if _, err := guard.Write([]byte("oversized")); err == nil {
		t.Fatal("oversized remote output accepted")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("oversized remote output did not cancel exec stream")
	}
	if !bounded.Overflow() || guard.seal() == nil {
		t.Fatal("overflow was not retained")
	}
}

func TestJournalWritesStayOnVerifiedDirectory(t *testing.T) {
	p, plan, _ := fixture(t)
	j := p.Journal.(FileJournal)
	record := Record{Plan: plan, Phase: PhasePreflight, Revision: 1}
	moved := j.Root + "-original"
	if err := j.withLock(t.Context(), func(dirfd int) error {
		if err := os.Rename(j.Root, moved); err != nil {
			return err
		}
		if err := os.Mkdir(j.Root, 0700); err != nil {
			return err
		}
		return j.writeLocked(dirfd, record)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, plan.Review.Key+".json")); err != nil {
		t.Fatal("verified directory lost write", err)
	}
	if _, err := os.Stat(filepath.Join(j.Root, plan.Review.Key+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement directory received write: %v", err)
	}
}

func TestJournalRejectsInvalidStateCombinations(t *testing.T) {
	p, plan, _ := fixture(t)
	j := p.Journal.(FileJournal)
	if err := j.withLock(t.Context(), func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	invalid := Record{Plan: plan, Phase: PhaseCreated, Revision: 3, BinaryPhase: "binary_verified"}
	raw, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	path, err := j.path(plan.Review.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.Read(t.Context(), plan.Review.Key); err == nil {
		t.Fatal("invalid created-without-Pod record accepted")
	}
}
