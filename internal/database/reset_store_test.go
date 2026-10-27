package database

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/local"
	"golang.org/x/sys/unix"
)

func resetStoreFixture(t *testing.T) (*resetStore, ResetReceipt, File) {
	t.Helper()
	home, err := os.MkdirTemp("", "arm-reset-store-")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) }) // owned disposable test fixture only
	s, err := openResetStore(home, local.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.close() })
	now := time.Now().UTC()
	actor := local.CurrentIdentity()
	plan := ResetPlan{Schema: ResetSchema, ID: filepath.Base(s.operation.Path)[6:], Home: home, Actor: actor, Reason: "private reset test", Created: now, OldHost: local.ID(), NewHost: local.ID(), Proof: SettledProof{Complete: true, Settled: true, Owner: actor.UID, Blockers: []ProofBlocker{}}, Namespaces: []NamespaceEvidence{}, PriorSeed: File{Name: ResetSeedName}}
	plan.Proof.Host = plan.OldHost
	for i, name := range databaseNames {
		v := File{Name: name}
		if i == 0 {
			v.Present = true
			v.Owner = uint32(os.Geteuid())
			v.Mode = unix.S_IFREG | 0600
			v.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
		plan.Files = append(plan.Files, v)
	}
	plan.SHA256, err = planDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	r := ResetReceipt{Schema: ResetSchema, Plan: plan, Phase: "staged", Revision: 1, Updated: now, Moves: map[string]string{"state.db": "source", "state.db-wal": "absent", "state.db-shm": "absent"}, Archives: map[string]string{}}
	if err = s.saveReceipt(r, nil); err != nil {
		t.Fatal(err)
	}
	r, file, err := s.readReceipt()
	if err != nil {
		t.Fatal(err)
	}
	return s, r, file
}

func TestResetSeedIsStandaloneFreshAuthority(t *testing.T) {
	s, r, file := resetStoreFixture(t)
	sentinel := []byte("old ledger never touched")
	if err := os.WriteFile(filepath.Join(s.home.Path, "state.db"), sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.buildResetSeed(context.Background(), &r, &file); err != nil {
		t.Fatal(err)
	}
	if !r.SeedReady || !validDigest(r.Seed.SHA256) || r.Seed.Size == 0 {
		t.Fatalf("not durably ready: %+v", r)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.operation.Path, resetFreshName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var host, marker string
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key=?`, resetAppliedKey).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	var applied resetApplied
	if err = strictEvidence(marker, &applied); err != nil {
		t.Fatal(err)
	}
	if host != r.Plan.NewHost || applied.Authority != host || applied.Operation != r.Plan.ID || applied.Plan != r.Plan.SHA256 {
		t.Fatalf("wrong seed authority: %q %+v", host, applied)
	}
	var mode string
	if err = db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("standalone mode %q %v", mode, err)
	}
	got, err := os.ReadFile(filepath.Join(s.home.Path, "state.db"))
	if err != nil || string(got) != string(sentinel) {
		t.Fatal("old ledger changed")
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err = os.Lstat(filepath.Join(s.operation.Path, resetFreshName+suffix)); !os.IsNotExist(err) {
			t.Fatalf("unexpected companion %s %v", suffix, err)
		}
	}
}

func TestResetSeedPartialConstructionResumesOnlyExactOwnedInode(t *testing.T) {
	s, r, file := resetStoreFixture(t)
	f, v, err := s.operation.Open(resetFreshName, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("interrupted partial SQLite")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r.Seed = v
	if err = s.updateReceipt(&r, &file); err != nil {
		t.Fatal(err)
	}
	if err = s.buildResetSeed(context.Background(), &r, &file); err != nil {
		t.Fatal(err)
	}
	if !r.SeedReady || r.Seed.Inode != v.Inode {
		t.Fatal("did not preserve recorded construction inode")
	}
}

func TestResetSeedRefusesReplacedAndUnverifiedCompanions(t *testing.T) {
	for _, kind := range []string{"replace", "journal", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			s, r, file := resetStoreFixture(t)
			f, v, err := s.operation.Open(resetFreshName, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			r.Seed = v
			if err = s.updateReceipt(&r, &file); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.operation.Path, resetFreshName)
			switch kind {
			case "replace":
				os.Rename(path, path+".old")
				os.WriteFile(path, []byte("replacement"), 0600)
			case "journal":
				os.WriteFile(path+"-journal", []byte("unverified"), 0600)
			case "symlink":
				os.Rename(path, path+".old")
				os.Symlink(path+".old", path)
			}
			if err = s.buildResetSeed(context.Background(), &r, &file); err == nil {
				t.Fatal("unsafe construction accepted")
			}
			if kind == "replace" {
				got, _ := os.ReadFile(path)
				if string(got) != "replacement" {
					t.Fatal("replacement changed")
				}
			}
		})
	}
}

func TestResetReceiptStalePublicationDoesNotOverwriteReplacement(t *testing.T) {
	s, r, file := resetStoreFixture(t)
	path := filepath.Join(s.operation.Path, resetReceiptName)
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.updateReceipt(&r, &file); err == nil {
		t.Fatal("stale publication accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "replacement receipt" {
		t.Fatal("replacement overwritten")
	}
}

func TestResetSeedCancelledBeforeConstructionWritesNothing(t *testing.T) {
	s, r, file := resetStoreFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.buildResetSeed(ctx, &r, &file); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err := os.Lstat(filepath.Join(s.operation.Path, resetFreshName)); !os.IsNotExist(err) {
		t.Fatal("cancelled construction created seed")
	}
}

func TestResetReceiptStalePublicationDoesNotOverwriteInPlaceEdit(t *testing.T) {
	s, r, expected := resetStoreFixture(t)
	path := filepath.Join(s.operation.Path, resetReceiptName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Same length, same inode: identity-only CAS cannot detect this change.
	changed := bytes.Replace(before, []byte("private reset test"), []byte("changed reset text"), 1)
	if bytes.Equal(before, changed) || len(before) != len(changed) {
		t.Fatal("invalid in-place edit fixture")
	}
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	current, err := s.operation.Observe(resetReceiptName, false)
	if err != nil || !sameIdentity(expected, current) {
		t.Fatal("fixture replaced inode", err)
	}
	if err = s.updateReceipt(&r, &expected); err == nil {
		t.Fatal("stale bytes overwritten")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, changed) {
		t.Fatal("in-place changed bytes not retained", err)
	}
}
