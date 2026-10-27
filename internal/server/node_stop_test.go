package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
	"getarm.org/internal/storage"
	"golang.org/x/sys/unix"
)

func openStoppedLedger(t *testing.T) (string, *manager.Manager, nodestop.Record, nodestop.State) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	m, err := manager.Open(home, model.Resources{CPU: 4})
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	id, err := storage.DirectoryIdentity(int(f.Fd()))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	target := nodestop.Target{Host: status.Host, Owner: strconv.Itoa(os.Geteuid()), Home: home, Directory: id, Generation: strings.Repeat("3", 32), Certificate: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64), Startup: nodestop.StartupContract}
	b, err := nodestop.NewBoundary(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ConfigureNodeStop(b); err != nil {
		t.Fatal(err)
	}
	a := nodestop.Actor{Host: strings.Repeat("2", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Pair: strings.Repeat("4", 64), GrantRevision: 1}
	impact := nodestop.Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{a.Host}}
	p, err := m.PlanNodeStop("private-stop", a, model.ShutdownRequest{}, "stop fixture without any native job", impact)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.BeginNodeStop(nodestop.Apply{Key: p.Key, Plan: p, ReviewSHA256: hash}, a, true, impact)
	if err != nil {
		t.Fatal(err)
	}
	// Create no backend or endpoint; retain the ordinary real private authority
	// lock so checkNodeStopStartup uses exactly the normal existing-lock path.
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	s := nodestop.State{Schema: nodestop.StateSchema, Revision: r.DesiredRevision, Running: false, Target: r.Plan.Target, Key: r.Plan.Key, Digest: r.Digest}
	return home, m, r, s
}

func stoppedLedger(t *testing.T) (string, nodestop.Record, nodestop.State) {
	t.Helper()
	home, m, r, s := openStoppedLedger(t)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	return home, r, s
}

func TestDurableStopRejectsAutomaticServeBeforeRecoveryOrEndpoint(t *testing.T) {
	home, r, _ := stoppedLedger(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 4}); err == nil || !strings.Contains(err.Error(), "intentionally stopped") {
		t.Fatal("automatic Serve resurrected stopped authority", err)
	}
	if _, err := local.ReadEndpoint(home); !os.IsNotExist(err) {
		t.Fatal("endpoint created after startup refusal", err)
	}
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	source, err := database.OfflineHeld(t.Context(), home, lock)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	after, err := nodestop.Lookup(source.DB, r.Plan.Key)
	if err != nil || after.Phase != "accepted" {
		t.Fatal("Manager recovery changed old stop despite refused startup", after.Phase, err)
	}
	var raw string
	if err = source.DB.QueryRow(`SELECT value FROM metadata WHERE key='shutdown'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var ordinary model.ShutdownReport
	if err = json.Unmarshal([]byte(raw), &ordinary); err != nil || ordinary.Phase != "draining" {
		t.Fatal("ordinary recovery ran before latch", ordinary.Phase, err)
	}
}

func TestExplicitRestartUsesExactStoppedRevisionCASAndRetainsOldDuty(t *testing.T) {
	home, r, state := stoppedLedger(t)
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	q, err := nodestop.ReviewRestart(state, r, "explicitly restart this private authority", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wrong := q
	wrong.Expected.Revision++
	if err = checkNodeStopStartup(t.Context(), home, lock, &wrong); err == nil {
		t.Fatal("changed restart revision accepted")
	}
	if err = checkNodeStopStartup(t.Context(), home, lock, &q); err != nil {
		t.Fatal(err)
	}
	if err = checkNodeStopStartup(t.Context(), home, lock, &q); err == nil {
		t.Fatal("same restart permit cleared intent twice")
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	current, present, err := nodestop.ReadState(db)
	if err != nil || !present || !current.Running || current.Revision != state.Revision+1 {
		t.Fatal(current, present, err)
	}
	old, err := nodestop.Lookup(db, r.Plan.Key)
	if err != nil || old.Phase != "interrupted" || old.Digest != r.Digest {
		t.Fatal("restart erased/replayed old duty", old, err)
	}
}

func TestStopRegistrationFencesUnrepresentedWorkButAllowsSettlementAndJoins(t *testing.T) {
	s := &Server{}
	leave, err := s.beginStopWork("operator")
	if err != nil {
		t.Fatal(err)
	}
	s.nodeStopMu.Lock()
	if s.nodeStopCalls["operator"] != 1 {
		t.Fatal("source worker not represented before launch")
	}
	s.nodeStopClosed = true
	s.nodeStopMu.Unlock()
	if _, err = s.beginStopWork("workspace"); err == nil {
		t.Fatal("post-commit native workspace launch admitted")
	}
	if _, err = s.beginStopWork("rpc"); err == nil {
		t.Fatal("post-commit mutation admitted")
	}
	settlement, err := s.registerStopOperation("release")
	if err != nil {
		t.Fatal("existing settlement blocked", err)
	}
	leave()
	leave()
	settlement()
	s.nodeStopMu.Lock()
	s.nodeStopClosing = true
	s.nodeStopMu.Unlock()
	if _, err = s.beginStopWork("inspection"); err == nil {
		t.Fatal("new inspection admitted after join fence")
	}
	s.nodeStopCallsWG.Wait()
}

func TestStopLookupKeepsInspectionAfterMutationGrantRevocation(t *testing.T) {
	_, ledger, record, _ := openStoppedLedger(t)
	defer ledger.Close()
	s := &Server{Manager: ledger} // lookup needs no mutation/native boundary
	m := &meshService{owner: s}
	actor := record.Plan.Actor
	local := mesh.Identity{Host: record.Plan.Target.Host, Owner: record.Plan.Target.Owner, SPKI: record.Plan.Target.SPKI, Fingerprint: record.Plan.Target.Certificate}
	peer := mesh.Peer{Identity: mesh.Identity{Host: actor.Host, SPKI: actor.SPKI, Fingerprint: actor.Certificate}, Pair: actor.Pair, Revision: actor.GrantRevision + 1, State: "active", Proven: true}
	raw, _ := json.Marshal(stopLookupRequest{Key: record.Plan.Key})
	request := mesh.Request{Operation: "node_stop_lookup", Host: local.Host, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: raw}
	for _, permission := range []string{"observe-own", "observe-all", "node-stop"} {
		peer.Grant = mesh.Grant{Permissions: []string{permission}}
		value, err := m.dispatchStop(t.Context(), peer, request, local, nil)
		if err != nil {
			t.Fatal(permission, err)
		}
		got, ok := value.(nodestop.Record)
		if !ok || got.Digest != record.Digest || got.Phase != "accepted" {
			t.Fatal("lookup changed/replayed original duty", value)
		}
	}
	peer.Grant = mesh.Grant{}
	if _, err := m.dispatchStop(t.Context(), peer, request, local, nil); err == nil {
		t.Fatal("no-inspection principal read receipt")
	}
	peer.Grant = mesh.Grant{Permissions: []string{"observe-all"}}
	peer.Identity.Host = strings.Repeat("9", 32)
	if _, err := m.dispatchStop(t.Context(), peer, request, local, nil); err == nil {
		t.Fatal("foreign principal borrowed original receipt")
	}
	got, err := ledger.LookupNodeStop(record.Plan.Key, actor)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(record)
	after, _ := json.Marshal(got)
	if string(before) != string(after) {
		t.Fatal("inspection mutated original stop record")
	}
	report, err := ledger.ShutdownStatus()
	if err != nil || report.ForceApplied {
		t.Fatal("inspection dispatched force continuation", report, err)
	}
}
