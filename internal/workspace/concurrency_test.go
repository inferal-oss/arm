package workspace

import (
	"context"
	"database/sql"
	"sync"
	"testing"
)

func secondStore(t *testing.T, f fixture) *Store {
	t.Helper()
	var path string
	if e := f.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, e = db.Exec(`PRAGMA busy_timeout=5000`); e != nil {
		t.Fatal(e)
	}
	s, e := Open(f.ctx, db, f.authority, f.self, f.clock)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestConcurrentProcessesCannotDuplicateCreateOrOversubscribeMemberRoster(t *testing.T) {
	f := newStoreFixture(t)
	other := secondStore(t, f)
	q := CreateRequest{"same-key", "shared workspace", DefaultPolicy(), "concurrent exact request"}
	q.Policy.MaxMembers = 2
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, s := range []*Store{f.s, other} {
		wg.Add(1)
		go func(s *Store) { defer wg.Done(); _, e := s.PrepareCreate(f.ctx, q); errors <- e }(s)
	}
	wg.Wait()
	close(errors)
	success := 0
	for e := range errors {
		if e == nil {
			success++
		}
	}
	if success < 1 {
		t.Fatal("neither concurrent transaction committed")
	}
	v, e := f.s.Read(f.ctx)
	if e != nil || len(v.Instances) != 1 || len(v.Operations) != 1 {
		t.Fatal("concurrent exact key manufactured another workspace", v, e)
	}
	// The winning process's boot owns its pending review. A stale review from a
	// different process cannot approve; find which store prepared the receipt.
	op := v.Operations[0]
	owner := f.s
	if op.Private.Boot == other.boot {
		owner = other
	}
	if _, e = owner.Decide(f.ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true); e != nil {
		t.Fatal(e)
	}
	v, e = owner.Read(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	candidates := []Identity{identityFixture(t, f.clock.Wall()), identityFixture(t, f.clock.Wall())}
	start := make(chan struct{})
	errors = make(chan error, 2)
	for i, s := range []*Store{f.s, other} {
		wg.Add(1)
		go func(s *Store, i int) {
			defer wg.Done()
			<-start
			_, e := s.PrepareAdmission(f.ctx, rootPrincipal(root), AdmissionRequest{Key: candidates[i].Host, Candidate: candidates[i], ConsentHash: hash(i), HostInbox: true, PolicyRevision: 1, Reason: "competing last member slot"})
			errors <- e
		}(s, i)
	}
	close(start)
	wg.Wait()
	close(errors)
	success = 0
	for e := range errors {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent sibling candidates did not serialize exactly one last slot", success)
	}
	v, e = f.s.Read(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	count, _, _ := counts(&v, root.ID)
	if count != 2 {
		t.Fatal("pending capacity was not conservatively held", count)
	}
}
func TestFullOperationRosterDoesNotBlockKnownOutcomeOrReservedSettlement(t *testing.T) {
	v := emptyRecords()
	v.Initialized = true
	id := "11111111111111111111111111111111"
	v.Instances = []Instance{{ID: id, State: "active"}}
	for i := 0; i < MaxOperations; i++ {
		v.Operations = append(v.Operations, Operation{Workspace: id, Principal: "same", Key: hash(i), Phase: "completed", Kind: "deny"})
	}
	known := v.Operations[0]
	if e := room(&v, id, known); e != nil {
		t.Fatal("recorded outcome blocked by full operation roster", e)
	}
	fresh := known
	fresh.Key = "fresh"
	if code(room(&v, id, fresh)) != "history_full" {
		t.Fatal("new operation bypassed capacity")
	}
	v.Changes = make([]Change, MaxChanges-1)
	for i := range v.Changes {
		v.Changes[i].Workspace = id
	}
	retire := known
	retire.Kind = "retire"
	if e := room(&v, id, retire); e != nil {
		t.Fatal("permanent local-retirement audit slot lost", e)
	}
	if code(room(&v, id, known)) != "history_full" {
		t.Fatal("ordinary mutation spent permanent retirement slot")
	}
}
func TestOperationCapacityReservesNewLocalRetirementReceipt(t *testing.T) {
	v := emptyRecords()
	id := "11111111111111111111111111111111"
	for i := 0; i < MaxOperations-1; i++ {
		v.Operations = append(v.Operations, Operation{Workspace: id, Principal: "same", Key: hash(i), Phase: "completed", Kind: "deny"})
	}
	op := Operation{Workspace: id, Principal: "same", Key: "fresh", Phase: "completed", Kind: "deny"}
	if code(room(&v, id, op)) != "history_full" {
		t.Fatal("normal mutation consumed reserved retirement receipt")
	}
	op.Kind = "retire"
	if e := room(&v, id, op); e != nil {
		t.Fatal("new explicit retirement cannot be recorded", e)
	}
	v.Operations = append(v.Operations, op)
	op.Key = "another"
	if code(room(&v, id, op)) != "history_full" {
		t.Fatal("retirement bypassed hard operation bound")
	}
}
func TestBlockedProofSlotsRemainBoundedAndCurrentResultDoesNotReplay(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, _, admission := joinedCandidate(t, d, root)
	completed := completeCandidate(t, d, root, m, admission)
	service := NewService(d.ctx, d.s)
	defer service.Close()
	// Occupied transport slots must not hide an already committed exact result.
	service.slots <- struct{}{}
	service.slots <- struct{}{}
	_, e := service.Activate(context.Background(), rootPrincipal(root), completed.Key, proofFunc(func(context.Context, Challenge) (Proof, error) {
		t.Fatal("known result dispatched another proof")
		return Proof{}, nil
	}))
	if e != nil {
		t.Fatal("capacity hid known result", e)
	}
	<-service.slots
	<-service.slots
}
