package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

type ownedStopServe struct {
	home   string
	done   chan struct{}
	result error
}

func startOwnedStopServe(t *testing.T, home string) (*ownedStopServe, local.Endpoint, model.Status) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	own := &ownedStopServe{home: home, done: make(chan struct{})}
	go func() { own.result = Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 4}); close(own.done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-own.done:
			if own.result != nil {
				t.Error("original private Serve close failed", own.result)
			}
		case <-time.After(10 * time.Second):
			t.Error("original private Serve remains unjoined")
		}
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-own.done:
			t.Fatal("private Serve ended before readiness", own.result)
		case <-deadline.C:
			t.Fatal("private Serve did not publish readiness")
		case <-tick.C:
			ep, err := local.ReadEndpoint(home)
			if err != nil {
				continue
			}
			var status model.Status
			if err = client.Call(t.Context(), home, "status", nil, &status); err == nil {
				return own, ep, status
			}
		}
	}
}
func (o *ownedStopServe) joined(t *testing.T) {
	t.Helper()
	select {
	case <-o.done:
		if o.result != nil {
			t.Fatal(o.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary shutdown did not join its exact Serve owner")
	}
	if _, err := local.ReadEndpoint(o.home); !os.IsNotExist(err) {
		t.Fatal("joined Serve still publishes an endpoint", err)
	}
}
func retainedStopRows(t *testing.T, home string) []string {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: filepath.Join(home, "state.db"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT data FROM node_stop_state UNION ALL SELECT data FROM node_stop_operations ORDER BY data`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		out = append(out, text)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestActualStopReviewedRestartThenOrdinaryShutdownJoinsWithoutHistoryRewrite(t *testing.T) {
	sourceHome, _, sourceStatus := stopPrivateBackend(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	first, oldEndpoint, destinationStatus := startOwnedStopServe(t, home)
	left := stopEnablePrivateMesh(t, sourceHome, sourceStatus.Host, "restart-source")
	right := stopEnablePrivateMesh(t, home, destinationStatus.Host, "restart-destination")
	peer := stopPairPrivate(t, sourceHome, home, left, right, strings.Repeat("d", 64))
	plan, err := stopPlanPrivate(t, sourceHome, right, peer, "stop-before-real-restart")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	params, err := json.Marshal(nodestop.Apply{Key: plan.Key, Plan: plan, ReviewSHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	request := mesh.Request{Operation: "node_stop_apply", Key: plan.Key, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: params}
	// One original apply only. A closed/lost transport reply is not retried;
	// the owned Serve join plus fixed original-key inspector decides completion.
	if err = client.Call(t.Context(), sourceHome, "mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{right.Host, request}, nil); err != nil {
		t.Log("original stop acknowledgement unconfirmed", err)
	}
	first.joined(t)
	observation, err := InspectNodeStop(t.Context(), home, plan.Target, plan.Key)
	if err != nil || !observation.ServerJoined || !observation.OriginalGenerationAbsent {
		t.Fatal(observation, err)
	}
	if !reflect.DeepEqual(observation.Record.Plan, plan) {
		t.Fatal("inspector did not retain the original exact plan")
	}
	marker := filepath.Join(home, "node-stop-closed-"+plan.Target.Generation+".json")
	oldMarker, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	oldMarkerInfo, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	review, needed, err := PrepareNodeRestart(t.Context(), home, "real reviewed restart before ordinary shutdown")
	if err != nil || !needed {
		t.Fatal(needed, err)
	}
	if err = review.Apply(t.Context()); err != nil {
		_ = review.Close()
		t.Fatal(err)
	}
	if err = review.Close(); err != nil {
		t.Fatal(err)
	}
	second, newEndpoint, _ := startOwnedStopServe(t, home)
	if newEndpoint.Generation == oldEndpoint.Generation {
		t.Fatal("reviewed restart reused original generation")
	}
	before := retainedStopRows(t, home)
	var ordinary model.ShutdownReport
	if err = client.Call(t.Context(), home, "shutdown", model.ShutdownRequest{Force: true}, &ordinary); err != nil {
		t.Fatal(err)
	}
	if ordinary.ID == "" || ordinary.ID == observation.Record.Shutdown {
		t.Fatal("ordinary shutdown borrowed historical remote-stop identity")
	}
	second.joined(t)
	after := retainedStopRows(t, home)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("ordinary shutdown rewrote historical stop state/record")
	}
	retained, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(retained, oldMarker) {
		t.Fatal("ordinary shutdown changed original closure marker", err)
	}
	info, err := os.Stat(marker)
	if err != nil || !os.SameFile(oldMarkerInfo, info) || !info.ModTime().Equal(oldMarkerInfo.ModTime()) {
		t.Fatal("original closure marker was replaced", err)
	}
	if _, err = os.Stat(filepath.Join(home, "node-stop-closed-"+newEndpoint.Generation+".json")); !os.IsNotExist(err) {
		t.Fatal("ordinary shutdown fabricated a new remote Stop marker", err)
	}
}
