package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"getarm.org/internal/local"
)

// This fixture exercises filesystem protocol mechanics independently of the
// settled-policy proof. The external integration test uses real manager data.
func resetFileEngineFixture(t *testing.T) (*resetStore, ResetReceipt, File) {
	t.Helper()
	s, r, file := resetStoreFixture(t)
	if err := os.WriteFile(filepath.Join(s.home.Path, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	r.Plan.Files = nil
	for _, name := range databaseNames {
		if err := os.WriteFile(filepath.Join(s.home.Path, name), []byte("reviewed-private-fixture-"+name), 0600); err != nil {
			t.Fatal(err)
		}
		f, v, err := s.home.openSource(name)
		if err != nil {
			t.Fatal(err)
		}
		v.SHA256, err = hashFile(context.Background(), f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		r.Plan.Files = append(r.Plan.Files, v)
		r.Moves[name] = "source"
	}
	r.Plan.PriorSeed = File{Name: ResetSeedName}
	review := &SettledReview{source: &Source{Directory: s.home}}
	if err := review.createLock(s.home, "mesh-lifecycle.lock"); err != nil {
		t.Fatal(err)
	}
	pending, err := resetChild(s.home, "dashboard-pending", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pending.Close() })
	if err = review.createLock(pending, ".lock"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = review.readNamespace(context.Background(), pending, 64<<10, 65); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, lock := range review.locks {
			unlockReset(lock)
		}
	})
	review.Namespaces = append(review.Namespaces, NamespaceEvidence{Name: "storage", Files: []File{}}, NamespaceEvidence{Name: "mesh", Files: []File{}}, NamespaceEvidence{Name: "mesh-lifecycle", Files: []File{}})
	r.Protected = review.Namespaces
	r.Plan.Namespaces = append([]NamespaceEvidence{}, review.Namespaces...)
	r.Plan.SHA256, err = planDigest(r.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.updateReceipt(&r, &file); err != nil {
		t.Fatal(err)
	}
	if err = s.buildResetSeed(context.Background(), &r, &file); err != nil {
		t.Fatal(err)
	}
	// Simulate the first process exiting before recovery opens its own locks.
	for _, lock := range review.locks {
		unlockReset(lock)
	}
	review.locks = nil
	return s, r, file
}
func TestResetFileEngineInterruptionAndExplicitContinuation(t *testing.T) {
	stages := []string{"fence-intent", "fence-published", "seed-pointer-published", "completed-receipt", "fence-removed"}
	for _, name := range databaseNames {
		stages = append(stages, "before-move:"+name, "after-move:"+name, "before-erase:"+name, "after-erase:"+name)
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			s, r, file := resetFileEngineFixture(t)
			interrupted := errors.New("simulated process interruption")
			reached := false
			_, err := s.finishReset(context.Background(), &r, &file, func(at string) error {
				if at == stage {
					reached = true
					return interrupted
				}
				return nil
			})
			if !reached || !errors.Is(err, interrupted) {
				t.Fatalf("fault not reached %s: %v", stage, err)
			}
			// A new continuation reconstructs only retained intent; opening it must
			// not perform recovery or erase anything.
			before, err := os.ReadFile(resetReceiptPath(s.home.Path, r.Plan.ID))
			if err != nil {
				t.Fatal(err)
			}
			c, err := ReviewResetContinuation(context.Background(), s.home.Path, r.Plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			after, err := os.ReadFile(resetReceiptPath(s.home.Path, r.Plan.ID))
			if err != nil || string(before) != string(after) {
				t.Fatal("read-only continuation changed receipt")
			}
			result, err := c.Continue(context.Background())
			if err != nil || !result.Completed {
				t.Fatalf("continuation %+v %v", result, err)
			}
			if err = local.CheckStateFence(s.home.Path); err != nil {
				t.Fatal(err)
			}
			for _, name := range databaseNames {
				if _, err = os.Lstat(filepath.Join(s.home.Path, name)); !os.IsNotExist(err) {
					t.Fatalf("source left %s %v", name, err)
				}
				if _, err = os.Lstat(filepath.Join(s.operation.Path, name)); !os.IsNotExist(err) {
					t.Fatalf("quarantine left %s %v", name, err)
				}
			}
		})
	}
}
func TestResetFileEngineNeverErasesReplacement(t *testing.T) {
	for _, stage := range []string{"before-move:state.db", "before-erase:state.db"} {
		t.Run(stage, func(t *testing.T) {
			s, r, file := resetFileEngineFixture(t)
			_, err := s.finishReset(context.Background(), &r, &file, func(at string) error {
				if at != stage {
					return nil
				}
				parent := s.home
				if at == "before-erase:state.db" {
					parent = s.operation
				}
				path := filepath.Join(parent.Path, "state.db")
				if err := os.Rename(path, path+".retained"); err != nil {
					return err
				}
				return os.WriteFile(path, []byte("not-reviewed-replacement"), 0600)
			})
			if err == nil {
				t.Fatal("replacement accepted")
			}
			parent := s.home
			if stage == "before-erase:state.db" {
				parent = s.operation
			}
			bytes, readErr := os.ReadFile(filepath.Join(parent.Path, "state.db"))
			if readErr != nil || string(bytes) != "not-reviewed-replacement" {
				t.Fatalf("replacement altered: %q %v", bytes, readErr)
			}
		})
	}
}
func TestResetFileEngineRefusesPendingRosterChange(t *testing.T) {
	s, r, file := resetFileEngineFixture(t)
	path := filepath.Join(s.home.Path, "dashboard-pending", "unknown-intent.json")
	if err := os.WriteFile(path, []byte("retained unknown operation"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishReset(context.Background(), &r, &file, nil); err == nil {
		t.Fatal("unknown intent ignored")
	}
	for _, v := range r.Plan.Files {
		if _, err := exactResetFile(context.Background(), s.home, v, true); err != nil {
			t.Fatal("source changed despite failed proof", err)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("intent removed")
	}
}
func TestResetFileEngineRequiresPrivateUnchangedFence(t *testing.T) {
	s, r, file := resetFileEngineFixture(t)
	if err := os.WriteFile(filepath.Join(s.home.Path, local.ResetFenceName), []byte(`{"schema":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishReset(context.Background(), &r, &file, nil); err == nil {
		t.Fatal("malformed fence accepted")
	}
	for _, v := range r.Plan.Files {
		if _, err := exactResetFile(context.Background(), s.home, v, true); err != nil {
			t.Fatal("source changed", err)
		}
	}
}

func TestResetReceiptCannotLoseProtectedOrMoveScope(t *testing.T) {
	for _, kind := range []string{"pending", "lock", "move", "seed", "archive"} {
		t.Run(kind, func(t *testing.T) {
			s, r, file := resetFileEngineFixture(t)
			switch kind {
			case "pending", "lock":
				name := "dashboard-pending"
				if kind == "lock" {
					name = filepath.Join(s.home.Path, "mesh-lifecycle.lock")
				}
				filtered := []NamespaceEvidence{}
				for _, n := range r.Protected {
					if n.Name != name {
						filtered = append(filtered, n)
					}
				}
				r.Protected = filtered
			case "move":
				r.Moves["state.db-wal"] = "absent"
			case "seed":
				r.Phase = "fenced"
				r.SeedReady = false
			case "archive":
				r.Archives["mesh"] = "source" // no such namespace was reviewed
			}
			if err := atomicResetJSON(s.operation, resetReceiptName, r, &file); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.readReceipt(); err == nil {
				t.Fatal("incomplete/corrupt reset scope accepted")
			}
			for _, reviewed := range r.Plan.Files {
				if _, err := exactResetFile(context.Background(), s.home, reviewed, true); err != nil {
					t.Fatal("source changed during read-only refusal", err)
				}
			}
		})
	}
}
