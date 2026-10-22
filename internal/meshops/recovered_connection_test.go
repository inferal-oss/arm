package meshops

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
)

func TestIncompleteConnectedRecoveryRequiresReviewAndCurrentMTLS(t *testing.T) {
	for _, scenario := range []string{"verified", "wrong-proof", "changed-revision"} {
		t.Run(scenario, func(t *testing.T) {
			service, q, applied, _ := serviceFixture(t)
			defer service.Close()
			if _, e := service.Start(q); e != nil {
				t.Fatal(e)
			}
			review := waitPhase(t, service, q.Key, "awaiting_review")
			if _, e := service.Decide(Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); e != nil {
				t.Fatal(e)
			}
			waitPhase(t, service, q.Key, "completed")
			// A private crash fixture models native lifecycle commit followed by lost
			// coordination outcome persistence. Keep the immutable approved root review.
			original, _, e := mesh.ReadCoordination(service.Home, q.Key)
			if e != nil {
				t.Fatal(e)
			}
			original.Phase = "incomplete"
			original.Result = nil
			path, e := mesh.LifecycleOperationPath(service.Home, q.Key)
			if e != nil {
				t.Fatal(e)
			}
			if e = local.AtomicJSON(filepath.Join(filepath.Dir(path), "coordinator-"+filepath.Base(path)), original); e != nil {
				t.Fatal(e)
			}
			immutableReview := string(original.Review)
			immutableRequest := string(original.Request)
			before := applied.Load()
			var proofs atomic.Int32
			base := service.New
			service.New = func(ctx context.Context) *Coordinator {
				c := base(ctx)
				if scenario == "changed-revision" {
					inspect := c.Inspect
					reads := 0
					c.Inspect = func() (mesh.LifecycleInspection, error) {
						value, e := inspect()
						reads++
						if reads > 1 {
							var config mesh.Config
							_ = assign(&config, value.Config)
							config.Peers[0].Revision++
							value.Config = &config
						}
						return value, e
					}
				}
				call := c.Call
				c.Call = func(ctx context.Context, op string, q, out any) error {
					if op == "mesh_dispatch" {
						proofs.Add(1)
						if scenario == "wrong-proof" {
							return assign(out, mesh.Identity{Host: "wrong-proof"})
						}
					}
					return call(ctx, op, q, out)
				}
				return c
			}
			child, e := service.Resume(q.Key, original.Revision)
			if e != nil {
				t.Fatal(e)
			}
			recovery := waitPhase(t, service, child.Key, "awaiting_review")
			if recovery.Approved || proofs.Load() != 0 || applied.Load() != before {
				t.Fatal("recovery skipped review or mutated trust")
			}
			var plan struct {
				Operation string `json:"operation"`
			}
			if json.Unmarshal(recovery.Review, &plan) != nil || plan.Operation != "verify-existing-connection" {
				t.Fatal("missing exact read-only recovery review", string(recovery.Review))
			}
			if _, e = service.Decide(Decision{Key: child.Key, ReviewRevision: recovery.ReviewRevision, ReviewHash: recovery.ReviewHash, Approve: true}); e != nil {
				t.Fatal(e)
			}
			phase := "completed"
			if scenario != "verified" {
				phase = "incomplete"
			}
			result := waitPhase(t, service, child.Key, phase)
			expectedProofs := int32(1)
			if scenario == "changed-revision" {
				expectedProofs = 0
			}
			if !result.Approved || len(result.Review) == 0 || proofs.Load() != expectedProofs || applied.Load() != before {
				t.Fatal("recovery proof or durable approval missing", result)
			}
			after, _, e := mesh.ReadCoordination(service.Home, q.Key)
			if e != nil || string(after.Review) != immutableReview || string(after.Request) != immutableRequest {
				t.Fatal("original immutable receipt changed", e)
			}
		})
	}
}
