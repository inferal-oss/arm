package database

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// File-engine fixture only. Independent external tests separately create and
// prove actual SQL/source pack settlement through public producers.
func attachResetHistoryEngineFixture(t *testing.T, s *resetStore, r *ResetReceipt, file *File) {
	t.Helper()
	mesh, e := resetChild(s.home, "mesh", true)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(mesh.Path, "ssh-pins.lock"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	lock, e := historyProofFile(t.Context(), mesh, "ssh-pins.lock", 0)
	if e != nil {
		t.Fatal(e)
	}
	meshEvidence := NamespaceEvidence{Name: "mesh", Present: true, Identity: mesh.identity, Files: []File{lock}}
	mesh.Close()
	history, e := resetChild(s.home, sourceHistoryProofName, true)
	if e != nil {
		t.Fatal(e)
	}
	defer history.Close()
	for _, name := range []string{"catalog.db", ".worker.lock"} {
		payload := []byte(nil)
		if name == "catalog.db" {
			payload = []byte("private engine bytes; not settlement proof")
		}
		if e = os.WriteFile(filepath.Join(history.Path, name), payload, 0600); e != nil {
			t.Fatal(e)
		}
	}
	packs, e := resetChild(history, "packs", true)
	if e != nil {
		t.Fatal(e)
	}
	packsID := packs.identity
	packs.Close()
	catalog, e := historyProofFile(t.Context(), history, "catalog.db", 64<<20)
	if e != nil {
		t.Fatal(e)
	}
	worker, e := historyProofFile(t.Context(), history, ".worker.lock", 0)
	if e != nil {
		t.Fatal(e)
	}
	m := proofHistoryMarker{Directory: historyProofID(history.identity), Catalog: historyProofID(catalog), WorkerLock: historyProofID(worker), Packs: historyProofID(packsID)}
	evidence, files, e := collectSSHHistoryEvidence(t.Context(), history, m)
	if e != nil {
		t.Fatal(e)
	}
	n := NamespaceEvidence{Name: sourceHistoryProofName, Present: true, Identity: history.identity, Files: files, SSHHistory: evidence}
	for i, v := range r.Plan.Namespaces {
		if v.Name == "mesh" {
			r.Plan.Namespaces[i] = meshEvidence
		}
	}
	r.Plan.Namespaces = append(r.Plan.Namespaces, n)
	r.Protected = append([]NamespaceEvidence{}, r.Plan.Namespaces...)
	r.Plan.RetireMesh = true
	r.Archives["mesh"] = "source"
	r.Archives[sourceHistoryProofName] = "source"
	r.Plan.SHA256, e = planDigest(r.Plan)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.updateReceipt(r, file); e != nil {
		t.Fatal(e)
	}
}

func TestResetSourceHistoryInterruptedWholeMoveAndWorkerExclusion(t *testing.T) {
	for _, stage := range []string{"before-archive:ssh-source-history", "after-archive:ssh-source-history"} {
		t.Run(stage, func(t *testing.T) {
			s, r, file := resetFileEngineFixture(t)
			attachResetHistoryEngineFixture(t, s, &r, &file)
			lost := errors.New("test exact whole-source-history move acknowledgement lost")
			_, e := s.finishReset(t.Context(), &r, &file, func(point string) error {
				if point == stage {
					return lost
				}
				return nil
			})
			if !errors.Is(e, lost) {
				t.Fatal("wrong interruption", e)
			}
			home, id := s.home.Path, r.Plan.ID
			parent := home
			if stage == "after-archive:ssh-source-history" {
				parent = s.operation.Path
			}
			lock, e := os.Open(filepath.Join(parent, sourceHistoryProofName, ".worker.lock"))
			if e != nil {
				t.Fatal(e)
			}
			if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
				t.Fatal(e)
			}
			if c, e := ReviewResetContinuation(t.Context(), home, id); e == nil {
				c.Close()
				t.Fatal("continuation ignored exact source worker lock")
			}
			if e = unlockReset(lock); e != nil {
				t.Fatal(e)
			}
			c, e := ReviewResetContinuation(t.Context(), home, id)
			if e != nil {
				t.Fatal(e)
			}
			result, e := c.Continue(t.Context())
			c.Close()
			if e != nil || !result.Completed {
				t.Fatal(result, e)
			}
			if _, e = os.Stat(filepath.Join(s.operation.Path, sourceHistoryProofName, "catalog.db")); e != nil {
				t.Fatal("retained history lost", e)
			}
			if _, e = os.Stat(filepath.Join(home, sourceHistoryProofName)); !os.IsNotExist(e) {
				t.Fatal("source namespace was recreated", e)
			}
		})
	}
}
