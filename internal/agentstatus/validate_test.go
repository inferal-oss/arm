package agentstatus

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/local"
	"github.com/google/uuid"
)

func validStatusState(t *testing.T) State {
	t.Helper()
	now := time.Now().UTC()
	s := NewState()
	identity := Identity{strings.Repeat("a", 32), agents.Contact{ID: "fixture", Adapter: "generic", Session: uuid.NewString(), Registration: local.ID(), CWD: t.TempDir(), Delivery: "poll"}}
	entry := Entry{identity.Key(), identity, now.Add(-48 * time.Hour), now}
	s.Entries = append(s.Entries, entry)
	a := Attempt{ID: local.ID(), Key: "fixture-key", EntryID: entry.ID, Identity: identity, Generation: 1, Provider: "openai", Model: CodexModel, State: "contributed", Created: now, Deadline: now.Add(2 * time.Minute), MessageID: local.ID(), ReplyID: local.ID(), ReplyAt: &now}
	a.Digest = RequestDigest(entry.ID, identity.Contact)
	a.Account = &Account{Version: 1, RequestID: a.ID, Current: []string{"Completion unknown"}}
	s.Attempts = append(s.Attempts, a)
	return s
}
func TestStateRejectsMalformedIdentityAndAccounting(t *testing.T) {
	for _, mutate := range []func(*State){func(s *State) { s.Entries = append(s.Entries, s.Entries[0]) }, func(s *State) { s.Attempts[0].Identity.Contact.Registration = "replacement" }, func(s *State) { s.Attempts[0].Digest = strings.Repeat("0", 64) }, func(s *State) { s.Attempts[0].State = "ready" }, func(s *State) { s.Attempts[0].Account.RequestID = local.ID() }, func(s *State) { s.Attempts[0].ReservedBytes = 40 << 10 }, func(s *State) { s.Attempts = append(s.Attempts, s.Attempts[0]) }} {
		s := validStatusState(t)
		mutate(&s)
		if s.Validate() == nil {
			t.Fatal("malformed state accepted")
		}
	}
}
func TestStateNearCapacityFinalizationReservation(t *testing.T) {
	s := validStatusState(t)
	final := s.Attempts[0]
	s.Attempts = nil
	now := final.Created
	old := now.Add(-24 * time.Hour)
	for len(s.Entries) < MaxIdentities {
		identity := s.Entries[0].Identity
		identity.Contact.ID = fmt.Sprintf("fixture-%d", len(s.Entries))
		identity.Contact.CWD = "/" + strings.Repeat("a", 4090)
		identity.Contact.ConfigDir = "/" + strings.Repeat("b", 4090)
		s.Entries = append(s.Entries, Entry{identity.Key(), identity, old.Add(-time.Hour), now})
	}
	for i := 0; i < MaxAttempts-1; i++ {
		a := final
		a.ID = local.ID()
		a.Key = fmt.Sprintf("old-%d", i)
		a.Generation = i + 1
		a.Created = old
		a.Deadline = old.Add(2 * time.Minute)
		a.ReplyAt = &old
		a.CallStarted = &old
		a.Generated = &old
		a.State = "ready"
		a.Account = &Account{Version: 1, RequestID: a.ID}
		s.Attempts = append(s.Attempts, a)
	}
	final.Generation = MaxAttempts
	s.Attempts = append(s.Attempts, final)
	low, high := 0, 6000
	for low < high {
		mid := (low + high + 1) / 2
		doc, err := Document(strings.Repeat("&", mid))
		if err != nil {
			high = mid - 1
			continue
		}
		for i := 0; i < len(s.Attempts)-1; i++ {
			s.Attempts[i].HTML = doc
		}
		raw, _ := json.Marshal(s)
		if len(raw) > MaxStateBytes-FinalizationReserve/2 {
			high = mid - 1
		} else {
			low = mid
		}
	}
	doc, err := Document(strings.Repeat("&", low))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(s.Attempts)-1; i++ {
		s.Attempts[i].HTML = doc
	}
	raw, _ := json.Marshal(s)
	if MaxStateBytes-len(raw) >= FinalizationReserve {
		t.Fatal("fixture did not reach reservation boundary")
	}
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(func(dst *State) error { *dst = s; return nil }); err != nil {
		t.Fatal(err)
	}
	reserve := func(st *State) error {
		a := st.Attempt(final.ID)
		a.CallStarted = &now
		a.State = "generating"
		a.ReservedBytes = FinalizationReserve
		st.Day = now.Format("2006-01-02")
		st.DailyCalls++
		return nil
	}
	if _, err = store.Update(reserve); err == nil {
		t.Fatal("unfunded finalization accepted")
	}
	unchanged, err := store.Update(nil)
	if err != nil || unchanged.Attempt(final.ID).CallStarted != nil {
		t.Fatal("failed preflight changed durable attempt", err)
	}
	small, _ := Document("<p>Old report</p>")
	if _, err = store.Update(func(st *State) error {
		for i := 0; i < 8; i++ {
			st.Attempts[i].HTML = small
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(reserve); err != nil {
		t.Fatal(err)
	}
	heavy, err := Document(strings.Repeat("&", 6000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(func(st *State) error {
		a := st.Attempt(final.ID)
		a.HTML = heavy
		a.Generated = &now
		a.State = "ready"
		a.ReservedBytes = 0
		a.Usage = &Usage{Billing: "synthetic tokens only"}
		return nil
	}); err != nil {
		t.Fatal("reserved finalization failed", err)
	}
}
