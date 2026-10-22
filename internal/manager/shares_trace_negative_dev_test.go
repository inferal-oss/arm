//go:build arm_devtrace

package manager

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// This mutates actual private SQL/runtime observations. No Go transition model
// decides whether it is safe; the Python driver sends these observed traces to
// the imported TLA Init/Next relation, which must reject them.
func TestShareFormalObservationNegatives(t *testing.T) {
	output := os.Getenv("ARM_FORMAL_NEGATIVES")
	if output == "" {
		t.Fatal("formal negative output required")
	}
	result := map[string][]shareFormalState{}
	for _, kind := range []string{"actual-premature-release", "actual-duplicate-native-entry"} {
		t.Run(kind, func(t *testing.T) {
			f := newShareFixture(t)
			entered, _, release := blockShareNativeIO(t, f.m)
			o := &shareFormalObserver{key: "formal-negative", storage: f.storage.ID, events: make(chan shareFormalState, 256), native: "never", gates: map[string]chan struct{}{"settle": make(chan struct{})}}
			shareFormalObservers.Store(f.m.home, o)
			defer func() { shareFormalObservers.Delete(f.m.home); close(o.gates["settle"]); release() }()
			f.m.mu.Lock()
			f.m.traceShareLocked("init", "")
			f.m.mu.Unlock()
			p, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, o.key))
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			switch kind {
			case "actual-premature-release":
				f.m.mu.Lock()
				l, err := f.m.get(p.Lease)
				if err != nil {
					f.m.mu.Unlock()
					t.Fatal(err)
				}
				original := l
				l.State = "terminal"
				now := time.Now().UTC()
				l.Ended = &now
				l.Outcome = "released"
				raw, _ := json.Marshal(l)
				if _, err = f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID); err != nil {
					f.m.mu.Unlock()
					t.Fatal(err)
				}
				f.m.mu.Unlock()
				// Read-only lookup really runs against the faulty live Manager before
				// capture; observer must not hide false charge by validating business state.
				if _, found, err := f.m.LookupSharePreparation(o.key); err != nil || !found {
					t.Fatal(found, err)
				}
				f.m.mu.Lock()
				f.m.traceShareLocked("lookup", p.ID)
				raw, _ = json.Marshal(original)
				_, err = f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
				f.m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			case "actual-duplicate-native-entry":
				// A deliberately faulty SECOND actual native body entry for the same key.
				// Count occurs at the exact production checkpoint, not by editing JSON.
				read, write, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				go func() {
					defer close(done)
					f.m.traceShareNative(p.ID, "native_begin")
					var b [1]byte
					_, _ = read.Read(b[:])
				}()
				select {
				case <-o.events:
				case <-time.After(time.Second):
					t.Fatal("no actual observer")
				}
				_ = write.Close()
				<-done
				_ = read.Close()
			}
			o.mu.Lock()
			rows := append([]shareFormalState(nil), o.records...)
			err = o.err
			o.mu.Unlock()
			if err != nil || len(rows) != 5 {
				t.Fatal("actual mutation was hidden", len(rows), err)
			}
			result[kind] = rows
		})
	}
	// Wrong key/authority are outside the single-key abstraction. Prove the
	// normalization boundary refuses them, rather than silently relabeling them.
	for _, kind := range []string{"wrong-key", "wrong-authority"} {
		t.Run(kind, func(t *testing.T) {
			f := newShareFixture(t)
			p, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, "identity-key"))
			if err != nil {
				t.Fatal(err)
			}
			shareWait(t, func() bool { p, e := f.m.InspectShare(p.ID); return e == nil && p.State == "prepared" })
			o := &shareFormalObserver{key: "identity-key", events: make(chan shareFormalState, 256)}
			if kind == "wrong-key" {
				o.key = "another-key"
			}
			shareFormalObservers.Store(f.m.home, o)
			defer shareFormalObservers.Delete(f.m.home)
			f.m.mu.Lock()
			l, e := f.m.get(p.Lease)
			if e != nil {
				f.m.mu.Unlock()
				t.Fatal(e)
			}
			if kind == "wrong-authority" {
				bad := l
				bad.Host = "wrong-authority"
				raw, _ := json.Marshal(bad)
				_, err = f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
				if err != nil {
					f.m.mu.Unlock()
					t.Fatal(err)
				}
			}
			f.m.traceShareLocked("lookup", p.ID)
			if kind == "wrong-authority" {
				raw, _ := json.Marshal(l)
				_, err = f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
			}
			f.m.mu.Unlock()
			o.mu.Lock()
			observed, observationErr := len(o.records), o.err
			o.mu.Unlock()
			if err != nil || observed != 0 || observationErr == nil {
				t.Fatal("wrong immutable identity normalized", observed, observationErr, err)
			}
		})
	}
	if err := formalWriteJSON(output, result); err != nil {
		t.Fatal(err)
	}
}
