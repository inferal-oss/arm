package meshops_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
	"github.com/yrashk/arm/internal/storage"
)

type stopFixture struct {
	t      *testing.T
	source string
	m      *manager.Manager
	target mesh.Identity
	own    mesh.Identity
	actor  nodestop.Actor
	impact nodestop.Impact
	scope  model.NodeAccessScope
	apply  int
	calls  int
	last   any
}

func stopFixtureNew(t *testing.T) *stopFixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manager.Open(home, model.Resources{CPU: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
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
	target := mesh.Identity{Host: status.Host, Owner: strconv.Itoa(os.Geteuid()), Fingerprint: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64)}
	boundary, err := nodestop.NewBoundary(nodestop.Target{Host: target.Host, Owner: target.Owner, Home: home, Directory: id, Generation: strings.Repeat("3", 32), Certificate: target.Fingerprint, SPKI: target.SPKI, Startup: nodestop.StartupContract})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ConfigureNodeStop(boundary); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	own := mesh.Identity{Host: strings.Repeat("1", 32), Owner: target.Owner, Fingerprint: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64)}
	actor := nodestop.Actor{Host: own.Host, Certificate: own.Fingerprint, SPKI: own.SPKI, Pair: strings.Repeat("4", 64), GrantRevision: 1}
	scope := model.NodeAccessScope{Source: own.Host, Target: target.Host, Pair: actor.Pair, Revision: 1, Observed: time.Now(), Grant: model.NodeGrant{Permissions: []string{"node-stop"}}, NodeStop: &model.NodeStopAvailability{Supported: true, HistoryCapacity: 128}}
	return &stopFixture{t: t, source: source, m: m, target: target, own: own, actor: actor, impact: nodestop.Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{own.Host}}, scope: scope}
}
func (f *stopFixture) coordinator() *meshops.Coordinator {
	c := &meshops.Coordinator{Context: f.t.Context(), Home: f.source, Inspect: func() (mesh.LifecycleInspection, error) {
		own := f.own
		return mesh.LifecycleInspection{Running: true, Host: own.Host, Identity: &own, Config: &mesh.Config{Identity: own, Peers: []mesh.Peer{{Identity: f.target, Pair: f.actor.Pair, Revision: 1, State: "active", Proven: true}}}}, nil
	}, Print: func(v any) error { f.last = v; return nil }, Confirm: func(any, bool, bool) error { return nil }}
	c.Call = func(ctx context.Context, op string, params, result any) error {
		f.calls++
		if op != "mesh_dispatch" {
			f.t.Fatal(op)
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		var q struct {
			Host    string
			Request mesh.Request
		}
		if err = mesh.Decode(raw, &q); err != nil {
			return err
		}
		var value any
		switch q.Request.Operation {
		case "access_scope":
			value = f.scope
		case "node_stop_plan":
			var plan struct {
				Key    string                `json:"key"`
				Reason string                `json:"reason"`
				Policy model.ShutdownRequest `json:"policy"`
			}
			if err = nodestop.Decode(q.Request.Params, &plan); err != nil {
				return err
			}
			p, e := f.m.PlanNodeStop(plan.Key, f.actor, plan.Policy, plan.Reason, f.impact)
			if e != nil {
				return e
			}
			hash, e := p.Digest()
			if e != nil {
				return e
			}
			value = nodestop.Prepared{Plan: p, SHA256: hash, HistoryCapacity: 128}
		case "node_stop_apply":
			f.apply++
			if q.Request.Expected == nil || q.Request.Expected.Pair != f.scope.Pair || q.Request.Expected.Revision != f.scope.Revision {
				return model.Err("node_context_changed", "current exact grant changed")
			}
			var a nodestop.Apply
			if err = nodestop.Decode(q.Request.Params, &a); err != nil {
				return err
			}
			value, err = f.m.BeginNodeStop(a, f.actor, true, f.impact)
			if err != nil {
				return err
			}
		default:
			f.t.Fatal(q.Request.Operation)
		}
		raw, err = json.Marshal(value)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, result)
	}
	return c
}
func TestStopCoordinatorUsesRealLedgerAndKnownKeyNeverReapplies(t *testing.T) {
	f := stopFixtureNew(t)
	q := meshops.StopDraft{Host: f.target.Host, Reason: "private actual ledger stop"}
	if _, err := f.coordinator().Stop("same-stop", q, true, false); err == nil {
		t.Fatal("transport acceptance falsely reported native completion")
	}
	if f.apply != 1 {
		t.Fatal(f.apply)
	}
	state, present, err := f.m.NodeStopDesiredState()
	if err != nil || !present || state.Running || state.Revision != 2 {
		t.Fatal(state, present, err)
	}
	op, exists, err := mesh.ReadCoordination(f.source, "same-stop")
	if err != nil || !exists || !op.Approved || op.Phase != "incomplete" || op.Worker != "" {
		t.Fatal(op, exists, err)
	}
	forged, _ := json.Marshal(map[string]any{"operation": "stop-arm-node", "key": op.Key, "status": "stopped", "detail": "transport closed"})
	if _, err = mesh.RecordStopLookup(f.source, op.Key, op.Revision, forged, nil); err == nil {
		t.Fatal("producer-only stopped flag recorded")
	}
	afterRefusal, exists, err := mesh.ReadCoordination(f.source, op.Key)
	if err != nil || !exists || afterRefusal.Revision != op.Revision {
		t.Fatal("refused observation changed original receipt", afterRefusal, err)
	}
	service := meshops.Service{Home: f.source, Context: t.Context(), New: func(context.Context) *meshops.Coordinator { t.Fatal("Stop resume launched a worker"); return nil }}
	if _, err = service.Resume(op.Key, op.Revision); err == nil {
		t.Fatal("Stop resumed as a fresh apply")
	}
	before := f.calls
	if code, err := f.coordinator().Stop("same-stop", q, true, false); err != nil || code != 0 || f.calls != before || f.apply != 1 {
		t.Fatal("known key performed new transport/effect", code, err, f.apply, f.calls)
	}
	if _, err = mesh.HandoffCoordination(f.source, op.Key, op.Revision, "bad", "unsafe recovery"); err == nil {
		t.Fatal("Stop handoff became replay authority")
	}
	if err = database.WithLifecycleStopScope(t.Context(), f.source, strconv.Itoa(os.Geteuid()), f.own.Host, func(scope database.LifecycleStopScope) error {
		if len(scope.Blockers) == 0 {
			t.Fatal("unknown remote duty vanished from source census")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestStopCoordinatorGrantChangeDoesNotCreateTargetIntent(t *testing.T) {
	f := stopFixtureNew(t)
	c := f.coordinator()
	c.Confirm = func(any, bool, bool) error { f.scope.Revision++; return nil }
	if _, err := c.Stop("grant-change", meshops.StopDraft{Host: f.target.Host, Reason: "grant fence control"}, true, false); err == nil {
		t.Fatal("changed grant accepted")
	}
	if _, present, err := f.m.NodeStopDesiredState(); err != nil || present {
		t.Fatal("target intent appeared after grant refusal", present, err)
	}
	op, exists, err := mesh.ReadCoordination(f.source, "grant-change")
	if err != nil || !exists || !meshops.Project(op).LookupOnly {
		t.Fatal("unknown attempt became fresh/replayable", op, err)
	}
}
