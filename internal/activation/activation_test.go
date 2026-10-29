package activation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/agentsessions"
	"github.com/google/uuid"
)

func fixture(t *testing.T, code string) (Store, Plan, string) {
	t.Helper()
	base := t.TempDir()
	script := filepath.Join(base, "codex")
	log := filepath.Join(base, "sent")
	if e := os.WriteFile(script, []byte("#!/bin/sh\nprintf x >> "+Quote(log)+"\n"+code+"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(base, "state")
	s := agentsessions.Session{Adapter: "codex", ID: uuid.NewString(), CWD: base, Home: base, Generation: strings.Repeat("a", 64)}
	hash, e := BinaryHash(script)
	if e != nil {
		t.Fatal(e)
	}
	p := Seal(Plan{Project: "demo", PolicyRevision: 1, Session: s, Message: "Exact reviewed message", Delivery: "queue", Binary: script, BinarySHA: hash}, "policy")
	return Store{Home: home}, p, log
}
func TestActivationQueuedKeysNeverSendTwice(t *testing.T) {
	s, p, log := fixture(t, "exit 0")
	validate := func() (Plan, error) { return p, nil }
	for i := 0; i < 2; i++ {
		r, e := s.Send(t.Context(), p, "stable-key", validate)
		if e != nil || r.State != "queued" || r.Message != p.Message {
			t.Fatal(r, e)
		}
	}
	b, e := os.ReadFile(log)
	if e != nil || string(b) != "x" {
		t.Fatal("delivery duplicated", string(b), e)
	}
	r, e := s.Lookup("stable-key")
	if e != nil || r.State != "queued" {
		t.Fatal(r, e)
	}
	p.Message = "changed message"
	p = Seal(p, "policy")
	if _, e = s.Send(t.Context(), p, "stable-key", func() (Plan, error) { return p, nil }); e == nil {
		t.Fatal("changed request reused key")
	}
}
func TestActivationUnknownAndCanceledDeliveryNeverRetry(t *testing.T) {
	for _, code := range []string{"exit 1", "exec sleep 30"} {
		t.Run(code, func(t *testing.T) {
			s, p, log := fixture(t, code)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type outcome struct {
				r Record
				e error
			}
			done := make(chan outcome, 1)
			go func() {
				r, e := s.Send(ctx, p, "original-key", func() (Plan, error) { return p, nil })
				done <- outcome{r, e}
			}()
			if code == "exec sleep 30" {
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				deadline := time.NewTimer(10 * time.Second)
				defer deadline.Stop()
				waiting := true
				for waiting {
					select {
					case <-ticker.C:
						if b, e := os.ReadFile(log); e == nil && string(b) == "x" {
							waiting = false
						}
					case <-deadline.C:
						cancel()
						t.Fatal("controlled recipient never signaled entry")
					}
				}
				cancel()
			}
			var completed outcome
			select {
			case completed = <-done:
			case <-time.After(10 * time.Second):
				cancel()
				t.Fatal("controlled delivery did not join")
			}
			r, e := completed.r, completed.e
			if e == nil || r.State != "unknown" {
				t.Fatal(r, e)
			}
			r, e = s.Send(t.Context(), p, "original-key", func() (Plan, error) { t.Fatal("uncertain key revalidated for retry"); return p, nil })
			if e != nil || r.State != "unknown" {
				t.Fatal(r, e)
			}
			b, e := os.ReadFile(log)
			if e != nil || string(b) != "x" {
				t.Fatal("uncertain delivery repeated", string(b), e)
			}
		})
	}
}
func TestActivationRefusesChangedPreviewAndCanceledStart(t *testing.T) {
	s, p, log := fixture(t, "exit 0")
	changed := p
	changed.Message = "different"
	changed = Seal(changed, "policy")
	if _, e := s.Send(t.Context(), p, "key", func() (Plan, error) { return changed, nil }); e == nil {
		t.Fatal("stale review sent")
	}
	if _, e := os.Stat(log); !os.IsNotExist(e) {
		t.Fatal("stale review executed", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := s.Send(ctx, p, "other", func() (Plan, error) { return p, nil }); e == nil {
		t.Fatal("canceled start sent")
	}
	if _, e := os.Stat(log); !os.IsNotExist(e) {
		t.Fatal("canceled start executed", e)
	}
}
func TestActivationMessageAndReviewKeepOriginalIdentity(t *testing.T) {
	s, p, _ := fixture(t, "exit 0")
	_ = s
	p.Session.Started = time.Time{}
	p.Message = Message("'/arm' --home '/state'", "demo", 7, p.Session)
	for _, want := range []string{p.Session.ID, "policy revision: 7", "--project 'demo'", "--adapter 'codex'", "original session start is unknown", "Do not change rollout policy", "existing jobs, leases"} {
		if !strings.Contains(p.Message, want) {
			t.Fatal(want, p.Message)
		}
	}
	p = Seal(p, "policy")
	heartbeat := p
	heartbeat.Session.Updated = time.Now()
	heartbeat.Session.Activity = "idle"
	heartbeat = Seal(heartbeat, "policy")
	if p.Review != heartbeat.Review {
		t.Fatal("metadata refresh invalidated exact identity review")
	}
	heartbeat.Session.Generation = "replacement"
	heartbeat = Seal(heartbeat, "policy")
	if p.Review == heartbeat.Review {
		t.Fatal("replacement retained review")
	}
}

func TestActivationPendingRecordDoesNotRestartDelivery(t *testing.T) {
	s, p, log := fixture(t, "exit 0")
	if e := os.MkdirAll(s.Home, 0700); e != nil {
		t.Fatal(e)
	}
	r := Record{Key: "pending", Review: p.Review, Project: p.Project, Adapter: p.Session.Adapter, Session: p.Session.ID, State: "pending", At: time.Now().UTC(), Message: p.Message, CWD: p.Session.CWD, Generation: p.Session.Generation, PolicyRevision: p.PolicyRevision}
	if e := s.save(ledger{Version: 1, Records: []Record{r}}); e != nil {
		t.Fatal(e)
	}
	got, e := s.Send(t.Context(), p, r.Key, func() (Plan, error) { t.Fatal("pending operation revalidated to restart"); return p, nil })
	if e != nil || got.State != "pending" {
		t.Fatal(got, e)
	}
	if _, e = os.Stat(log); !os.IsNotExist(e) {
		t.Fatal("pending operation executed again")
	}
}

func TestActivationRefusesCorruptLedgerAndChangedExecutable(t *testing.T) {
	s, p, log := fixture(t, "exit 0")
	if e := os.MkdirAll(s.Home, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(s.Home, "rollout-activations.json")
	bad := []byte(`{"version":42,"records":[]}`)
	if e := os.WriteFile(file, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Send(t.Context(), p, "bad-ledger", func() (Plan, error) { return p, nil }); e == nil {
		t.Fatal("corrupt history permitted send")
	}
	after, e := os.ReadFile(file)
	if e != nil || string(after) != string(bad) {
		t.Fatal("corrupt ledger overwritten", e)
	}
	if e = os.Remove(file); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p.Binary, []byte("#!/bin/sh\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send(t.Context(), p, "changed-binary", func() (Plan, error) { return p, nil }); e == nil {
		t.Fatal("changed native executable accepted")
	}
	if _, e = os.Stat(log); !os.IsNotExist(e) {
		t.Fatal("unreviewed native executable ran")
	}
}
