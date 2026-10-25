package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/nodestop"
	"golang.org/x/sys/unix"
)

func TestUninstallMaintenanceRetainsActualStopLockAndOriginalHistory(t *testing.T) {
	source, _, sourceStatus := stopPrivateBackend(t)
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(home, 0700); e != nil {
		t.Fatal(e)
	}
	destination, _, status := startOwnedStopServe(t, home)
	left := stopEnablePrivateMesh(t, source, sourceStatus.Host, "uninstall-source")
	right := stopEnablePrivateMesh(t, home, status.Host, "uninstall-destination")
	peer := stopPairPrivate(t, source, home, left, right, strings.Repeat("d", 64))
	plan, e := stopPlanPrivate(t, source, right, peer, "stop-for-maintenance-lock-test")
	if e != nil {
		t.Fatal(e)
	}
	hash, e := plan.Digest()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(nodestop.Apply{Key: plan.Key, Plan: plan, ReviewSHA256: hash})
	if e != nil {
		t.Fatal(e)
	}
	if e = client.Call(t.Context(), source, "mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{right.Host, mesh.Request{Operation: "node_stop_apply", Key: plan.Key, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: raw}}, nil); e != nil {
		t.Log("sole original Stop acknowledgement unavailable", e)
	}
	destination.joined(t)
	before := retainedStopRows(t, home)
	called := false
	sentinel := errors.New("maintenance refused without effects")
	observation, e := WithInspectedNodeStop(t.Context(), home, plan.Target, plan.Key, func(o NodeStopObservation) error {
		called = true
		if !o.ServerJoined || !o.OriginalGenerationAbsent || o.Desired.Running || o.Desired.Key != plan.Key {
			t.Fatal("callback lacked actual current Stop", o)
		}
		f, e := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e == nil {
			unix.Flock(int(f.Fd()), unix.LOCK_UN)
			t.Fatal("original lock released before maintenance")
		}
		return sentinel
	})
	if !called || !errors.Is(e, sentinel) {
		t.Fatal("maintenance error not retained", observation, e)
	}
	after := retainedStopRows(t, home)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("maintenance refusal rewrote original Stop history")
	}
	if _, e = InspectNodeStop(t.Context(), home, plan.Target, plan.Key); e != nil {
		t.Fatal("original read-only inspector changed", e)
	}
}
