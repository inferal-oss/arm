//go:build arm_devtrace

package manager

import (
	"encoding/json"
	"github.com/yrashk/arm/internal/sharelease"
	"os"
	"testing"
	"time"
)

// Faults mutate actual private SQL or enter a second actual pipe-blocked body.
// The observer emits the faulty state; only imported TLC Next decides safety.
func TestSharePublicationFormalNegatives(t *testing.T) {
	output := os.Getenv("ARM_PUBLICATION_NEGATIVES")
	if output == "" {
		t.Fatal("negative publication output required")
	}
	result := map[string][]publicationFormalState{}
	for _, kind := range []string{"actual-premature-release", "actual-premature-publication", "actual-original-change", "actual-intent-change", "actual-duplicate-verifier"} {
		t.Run(kind, func(t *testing.T) {
			f := newShareFixture(t)
			o := &publicationFormalObserver{key: "formal-publication-negative", storage: f.storage.ID, events: make(chan publicationFormalState, 256), native: map[string]publicationNative{}, gates: map[string]chan struct{}{"verification:settle": make(chan struct{})}}
			publicationFormalObservers.Store(f.m.home, o)
			defer publicationFormalObservers.Delete(f.m.home)
			f.m.mu.Lock()
			f.m.tracePublicationLocked("init", "")
			f.m.mu.Unlock()
			p, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, o.key))
			if err != nil {
				t.Fatal(err)
			}
			shareWait(t, func() bool {
				f.m.mu.Lock()
				defer f.m.mu.Unlock()
				return f.m.shares.state.Publications[p.ID].State == "prepared" && f.m.shares.workers[p.ID] == nil
			})
			p, err = f.m.InspectShare(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			entered, _, release := blockShareNativeIO(t, f.m)
			defer func() { close(o.gates["verification:settle"]); release() }()
			if _, err = f.m.PublishShare(t.Context(), publicationRequest(p)); err != nil {
				t.Fatal(err)
			}
			<-entered
			if kind == "actual-duplicate-verifier" {
				read, write, e := os.Pipe()
				if e != nil {
					t.Fatal(e)
				}
				done := make(chan struct{})
				begun := make(chan struct{})
				go func() {
					defer close(done)
					f.m.traceShareNative(p.ID, "native_begin")
					close(begun)
					var b [1]byte
					_, _ = read.Read(b[:])
				}()
				<-begun
				_ = write.Close()
				<-done
				_ = read.Close()
			} else {
				f.m.mu.Lock()
				original := formalPublicationUnlocked(t, f.m, o.key)
				rawOriginal, _ := json.Marshal(original)
				l, e := f.m.get(p.Lease)
				if e != nil {
					f.m.mu.Unlock()
					t.Fatal(e)
				}
				leaseOriginal, _ := json.Marshal(l)
				switch kind {
				case "actual-premature-release":
					l.State = "terminal"
					now := time.Now().UTC()
					l.Ended = &now
					l.Outcome = "released"
					raw, _ := json.Marshal(l)
					_, err = f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
				case "actual-premature-publication":
					fault := original
					now := time.Now().UTC()
					fault.State = "published"
					fault.WorkerState = "joined"
					fault.Published = &now
					raw, _ := json.Marshal(fault)
					_, err = f.m.db.Exec(`UPDATE share_publications SET data=? WHERE id=?`, string(raw), p.ID)
				case "actual-original-change":
					fault := clonePublication(original)
					fault.Summary.Entries++
					raw, _ := json.Marshal(fault)
					_, err = f.m.db.Exec(`UPDATE share_publications SET data=? WHERE id=?`, string(raw), p.ID)
				case "actual-intent-change":
					fault := clonePublication(original)
					fault.Publish.Reason = "substituted control"
					raw, _ := json.Marshal(fault)
					_, err = f.m.db.Exec(`UPDATE share_publications SET data=? WHERE id=?`, string(raw), p.ID)
				}
				if err == nil {
					f.m.tracePublicationLocked("lookup", p.ID)
				}
				_, restore := f.m.db.Exec(`UPDATE share_publications SET data=? WHERE id=?`, string(rawOriginal), p.ID)
				if restore != nil {
					t.Error(restore)
				}
				_, restore = f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(leaseOriginal), p.Lease)
				if restore != nil {
					t.Error(restore)
				}
				f.m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			o.mu.Lock()
			rows := append([]publicationFormalState(nil), o.records...)
			e := o.err
			o.mu.Unlock()
			if e != nil || len(rows) != 11 {
				t.Fatal("faulty actual observation hidden", len(rows), e)
			}
			result[kind] = rows
		})
	}
	if err := formalWriteJSON(output, result); err != nil {
		t.Fatal(err)
	}
}

// Caller holds mu; do not recursively lock the production manager.
func formalPublicationUnlocked(t *testing.T, m *Manager, key string) sharelease.Publication {
	t.Helper()
	var raw []byte
	if err := m.db.QueryRow(`SELECT data FROM share_publications WHERE json_extract(data,'$.request.key')=?`, key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p sharelease.Publication
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}
