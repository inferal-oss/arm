package meshops_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

// This uses actual manager leases and stored coordination. The injected peer
// transport is a scoped test adapter, not TLS/authentication or closure proof.
func TestStopLeaseInspectorBindsStoredReviewAndAllowlistsCurrentRead(t *testing.T) {
	f := stopFixtureNew(t)
	l, err := f.m.AcquirePeer(f.own.Host, model.Request{Resources: model.Resources{CPU: .25}, Reason: "complete lease purpose", TTL: time.Minute, Key: "detail"}, nil, model.Resources{CPU: 1}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	port, err := f.m.Acquire(model.Request{Kind: "ports", Key: "actual-bound", Reason: "actual auto-assigned held port", TTL: time.Minute, Ports: []model.PortClaim{{Name: "assigned", Network: "tcp4", Address: "127.0.0.1", Port: 0, Mode: "hold"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(port.Ports) != 1 || port.Ports[0].Port < 1 || port.Request.Ports[0].Port != 0 {
		t.Fatal("fixture did not obtain an actual assigned port0 binding", port)
	}
	queued, err := f.m.Acquire(model.Request{Kind: "ports", Key: "actual-unbound", Reason: "queued port awaiting original binding", TTL: time.Minute, Queue: true, Ports: []model.PortClaim{{Name: "waiting", Network: "tcp4", Address: "127.0.0.1", Port: port.Ports[0].Port, Mode: "hold"}}}, nil)
	if err != nil || queued.State != "queued" || len(queued.Ports) != 0 {
		t.Fatal("fixture did not preserve an actual unbound queued request", queued, err)
	}
	f.scope.Grant.Permissions = []string{"node-stop", "observe-own", "observe-all"}
	f.scope.NodeStop.Generation = strings.Repeat("3", 32)
	draft := meshops.StopDraft{Host: f.target.Host, Reason: "inspect before stopping", Policy: model.ShutdownRequest{Wait: true}}
	p, err := f.m.PlanNodeStop("read-only-stop", f.actor, draft.Policy, draft.Reason, f.impact, nodestop.LeaseReadScope{Source: f.actor.Host, Own: true, All: true})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	review := meshops.StopReview{Operation: "stop-arm-node", Key: p.Key, Plan: p, SHA256: digest, HistoryCapacity: 128, CompletionCheck: meshops.StopCompletionCheck{Available: true, Detail: "test adapter only"}}
	if err = review.Validate(); err != nil {
		t.Fatal(err)
	}
	requestDigest, _ := mesh.ReviewDigest(draft)
	guard, err := mesh.ClaimCoordination(f.source, p.Key, "stop", requestDigest, "host:"+f.target.Host)
	if err != nil {
		t.Fatal(err)
	}
	reviewRaw, _ := json.Marshal(review)
	reviewHash, _ := mesh.ReviewDigest(review)
	requestRaw, _ := json.Marshal(meshops.StartRequest{Key: p.Key, Kind: "stop", Stop: &draft})
	if err = guard.Change(func(op *mesh.Coordination) error {
		op.Review = reviewRaw
		op.ReviewHash = reviewHash
		op.ReviewRevision = op.Revision + 1
		op.ReviewExpires = p.Expires
		op.Request = requestRaw
		op.Phase = "awaiting_review"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stored, _, err := mesh.ReadCoordination(f.source, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(stored)
	q := meshops.StopLeaseRequest{Key: p.Key, ReviewRevision: stored.ReviewRevision, ReviewHash: reviewHash, Obligation: "lease:" + l.ID}
	var inspections atomic.Int32
	registered, released := 0, 0
	revokeOnInspect := false
	service := &meshops.Service{Home: f.source, Context: t.Context(), Register: func() (func(), error) { registered++; return func() { released++ }, nil }}
	service.New = func(ctx context.Context) *meshops.Coordinator {
		c := f.coordinator()
		c.Context = ctx
		base := c.Call
		c.Call = func(ctx context.Context, operation string, params, result any) error {
			raw, _ := json.Marshal(params)
			var envelope struct {
				Host    string
				Request mesh.Request
			}
			if err := mesh.Decode(raw, &envelope); err != nil {
				return err
			}
			if envelope.Request.Operation != "inspect" {
				return base(ctx, operation, params, result)
			}
			inspections.Add(1)
			if revokeOnInspect {
				f.scope.Revision++
			}
			expected := envelope.Request.Expected
			if expected == nil || expected.Pair != f.scope.Pair || expected.Revision != f.scope.Revision {
				return model.Err("node_context_changed", "test destination current enrollment changed")
			}
			var read struct {
				ID         string `json:"id"`
				Generation string `json:"backend_generation"`
			}
			if err := nodestop.Decode(envelope.Request.Params, &read); err != nil {
				return err
			}
			if read.Generation != f.scope.NodeStop.Generation {
				return model.Err("node_generation_changed", "test actual generation changed")
			}
			var lease model.Lease
			var err error
			if slices.Contains(f.scope.Grant.Permissions, "observe-all") {
				lease, err = f.m.Inspect(read.ID)
			} else {
				lease, err = f.m.PeerLease(f.actor.Host, read.ID)
			}
			if err != nil {
				return err
			}
			if lease.ID == l.ID {
				lease.ManagedDir = "private-managed-path-sentinel"
				lease.Request.Profile = "private-profile-sentinel"
				lease.Request.PeerCertificate = "private-certificate-sentinel"
				lease.Request.Pools = []model.PoolClaim{{Pool: "pool-id", Quantity: 2, Authority: "private-pool-authority-sentinel"}}
				lease.Request.Ports = []model.PortClaim{{Name: "preview", Network: "tcp4", Address: "127.0.0.1", Port: 8080, Mode: "cooperative", Namespace: "private-namespace-sentinel"}}
			}

			raw, _ = json.Marshal(lease)
			return json.Unmarshal(raw, result)
		}
		return c
	}
	detail, err := service.InspectStopLease(q)
	if err != nil || detail.ID != l.ID || detail.Host != f.target.Host || detail.Reason != l.Request.Reason || detail.Username == "" || detail.ReviewedRevision != int64(l.Revision) || detail.Changed {
		t.Fatal("actual stored lease read failed", detail, err)
	}
	if len(detail.Pools) != 1 || detail.Pools[0].Quantity != 2 || len(detail.Ports) != 1 || detail.Ports[0].Port != 8080 || detail.Ports[0].State != "requested" {
		t.Fatal("resource claim quantities omitted", detail)
	}
	raw, _ := json.Marshal(detail)
	for _, secret := range []string{"private-managed-path-sentinel", "private-profile-sentinel", "private-certificate-sentinel", "managed_dir", `"request":`, "budget_policy", "private-pool-authority-sentinel", "private-namespace-sentinel"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("detail DTO forwarded private destination fields", secret)
		}
	}
	for _, original := range []model.Lease{port, queued} {
		portQuery := q
		portQuery.Obligation = "lease:" + original.ID
		observed, err := service.InspectStopLease(portQuery)
		if err != nil || len(observed.Ports) != 1 {
			t.Fatal("actual port lease inspection failed", observed, err)
		}
		expectedPort, expectedState := original.Request.Ports[0].Port, "requested"
		if len(original.Ports) > 0 {
			expectedPort = original.Ports[0].Port
			expectedState = original.Ports[0].State
		}
		if observed.Ports[0].Port != expectedPort || observed.Ports[0].State != expectedState {
			t.Fatal("assigned binding or unbound request misreported", observed.Ports, expectedPort, expectedState)
		}
	}
	after, _, err := mesh.ReadCoordination(f.source, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	afterRaw, _ := json.Marshal(after)
	if !bytes.Equal(before, afterRaw) || f.apply != 0 {
		t.Fatal("inspection changed review/worker or applied Stop")
	}
	if _, err = f.m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	detail, err = service.InspectStopLease(q)
	if err != nil || !detail.Changed || detail.Revision == l.Revision || detail.State == l.State {
		t.Fatal("current revision change not explained", detail, err)
	}
	for _, tc := range []string{"hash", "revision", "obligation", "key", "read-revoked", "grant-changed", "generation-changed", "pin-changed", "wire-revoked"} {
		t.Run(tc, func(t *testing.T) {
			altered := q
			oldScope := f.scope
			oldTarget := f.target
			defer func() { f.scope = oldScope; f.target = oldTarget; revokeOnInspect = false }()
			switch tc {
			case "hash":
				altered.ReviewHash = strings.Repeat("0", 64)
			case "revision":
				altered.ReviewRevision++
			case "obligation":
				altered.Obligation = "lease:unreviewed"
			case "key":
				altered.Key = "other-key"
			case "read-revoked":
				f.scope.Grant.Permissions = []string{"node-stop"}
			case "grant-changed":
				f.scope.Revision++
			case "generation-changed":
				copy := *f.scope.NodeStop
				copy.Generation = strings.Repeat("8", 32)
				f.scope.NodeStop = &copy
			case "pin-changed":
				f.target.SPKI = strings.Repeat("8", 64)
			case "wire-revoked":
				revokeOnInspect = true
			}
			beforeCalls := inspections.Load()
			_, err := service.InspectStopLease(altered)
			want := map[string]string{"hash": "review_changed", "revision": "review_changed", "obligation": "not_found", "key": "review_changed", "read-revoked": "forbidden", "grant-changed": "node_context_changed", "generation-changed": "node_context_changed", "pin-changed": "node_context_changed", "wire-revoked": "node_context_changed"}[tc]
			var api *model.Error
			if !errors.As(err, &api) || api.Code != want {
				t.Fatal("changed scope did not reach intended refusal", tc, want, err)
			}
			if tc != "wire-revoked" && inspections.Load() != beforeCalls {
				t.Fatal("invalid stored/current scope reached lease inspect", tc)
			}
		})
	}
	if registered != released {
		t.Fatal("inspection interval leaked", registered, released)
	}

	// Two concurrent reads remain owned until their original calls return. Close
	// neither turns cancellation into completion nor admits a third read.
	entered := make(chan struct{}, 2)
	finish := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(finish) }) }
	defer unblock()
	baseNew := service.New
	var transportMu sync.Mutex
	concurrent := &meshops.Service{Home: f.source, Context: t.Context(), New: func(ctx context.Context) *meshops.Coordinator {
		c := baseNew(ctx)
		originalCall := c.Call
		// This fixture's original adapter counts calls with ordinary integers. Its
		// independent transport bookkeeping is serialized, not the owned reads.
		c.Call = func(ctx context.Context, operation string, params, result any) error {
			transportMu.Lock()
			defer transportMu.Unlock()
			return originalCall(ctx, operation, params, result)
		}
		original := c.Inspect
		c.Inspect = func() (mesh.LifecycleInspection, error) {
			entered <- struct{}{}
			select {
			case <-finish:
				return original()
			case <-ctx.Done():
				return mesh.LifecycleInspection{}, ctx.Err()
			}
		}
		return c
	}}
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := concurrent.InspectStopLease(q); results <- err }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("original read did not reach owned inspection interval")
		}
	}
	_, busyErr := concurrent.InspectStopLease(q)
	var api *model.Error
	if !errors.As(busyErr, &api) || api.Code != "busy" {
		t.Fatal("third concurrent read was not bounded", busyErr)
	}
	closed := make(chan struct{})
	go func() { concurrent.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close declared completion while original reads were still blocked")
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal("original retained read failed", err)
			}
		case <-time.After(time.Second):
			t.Fatal("original read did not join")
		}
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not join original reads")
	}
	service.Close()
	beforeCalls := inspections.Load()
	if _, err = service.InspectStopLease(q); err == nil || inspections.Load() != beforeCalls {
		t.Fatal("closed coordinator started another inspection")
	}
	after, _, _ = mesh.ReadCoordination(f.source, p.Key)
	afterRaw, _ = json.Marshal(after)
	if !bytes.Equal(before, afterRaw) || f.apply != 0 {
		t.Fatal("detail failures mutated immutable Stop review")
	}
	// Even a newly available read grant cannot turn a previously redacted stored
	// review into a private summary. A new review is a separate user action.
	redacted := review
	for n := range redacted.Plan.Impact.Obligations {
		if redacted.Plan.Impact.Obligations[n].ID == q.Obligation {
			redacted.Plan.Impact.Obligations[n].Lease = nil
			redacted.Plan.Impact.Obligations[n].LeaseDetails = "not-permitted"
		}
	}
	redacted.SHA256, _ = redacted.Plan.Digest()
	redactedRaw, _ := json.Marshal(redacted)
	redactedHash, _ := mesh.ReviewDigest(redacted)
	if err = guard.Change(func(op *mesh.Coordination) error {
		op.Review = redactedRaw
		op.ReviewHash = redactedHash
		op.ReviewRevision = op.Revision + 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed, _, _ := mesh.ReadCoordination(f.source, p.Key)
	q.ReviewRevision = changed.ReviewRevision
	q.ReviewHash = changed.ReviewHash
	reader := &meshops.Service{Home: f.source, Context: t.Context(), New: service.New}
	beforeCalls = inspections.Load()
	_, err = reader.InspectStopLease(q)
	api = nil
	if !errors.As(err, &api) || api.Code != "forbidden" || inspections.Load() != beforeCalls {
		t.Fatal("redacted stored review did not reach intended read refusal", err)
	}
	reader.Close()

}
