package meshops

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
	"time"
)

func (a *Coordinator) begin(key, kind, digest, target string) error {
	if a.guard != nil {
		op, e := a.guard.Read()
		if e != nil {
			return e
		}
		if op.Kind != kind || op.Digest != digest {
			return model.Err("idempotency_conflict", "worker intent differs from immutable admission")
		}
		return nil
	}
	var guard *mesh.CoordinationGuard
	var e error
	if a.attemptParent != "" {
		guard, e = mesh.ClaimCoordinationAttempt(a.Home, a.attemptParent, target)
	} else {
		guard, e = mesh.ClaimCoordination(a.Home, key, kind, digest, target)
	}
	if e != nil {
		return e
	}
	a.guard = guard
	ctx, cancel := context.WithCancel(a.Context)
	a.heartbeatStop = cancel
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if _, e := guard.Read(); e != nil {
					return
				}
				if e := guard.Heartbeat(); e != nil {
					return
				}
			}
		}
	}()
	return nil
}
func (a *Coordinator) markApproved() error {
	if e := a.Context.Err(); e != nil {
		return e
	}
	if _, e := a.guard.Read(); e != nil {
		return e
	}
	return a.guard.Change(func(op *mesh.Coordination) error { op.Approved = true; op.Phase = "applying"; return nil })
}
func (a *Coordinator) saveResult(result any, err error) error {
	if a.guard == nil {
		return nil
	}
	if a.heartbeatStop != nil {
		a.heartbeatStop()
	}
	// Adopt a separately persisted review decision before final settlement.
	if _, e := a.guard.Read(); e != nil {
		return e
	}
	e := a.guard.Change(func(op *mesh.Coordination) error {
		op.Phase = "completed"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		if err != nil {
			op.Phase = "rejected"
			if op.Approved {
				op.Phase = "incomplete"
			}
			var api *model.Error
			if !errors.As(err, &api) {
				api = &model.Error{Code: "operation_failed", Message: "mesh operation failed; inspect the exact operation and backend diagnostics"}
			}
			op.Error = api
		}
		if value, ok := result.(map[string]any); ok && value["remote_pending"] == true {
			op.Phase = "incomplete"
		}
		if result != nil {
			raw, e := json.Marshal(result)
			if e != nil {
				return e
			}
			op.Result = raw
		}
		return nil
	})
	if e != nil {
		return e
	}
	return mesh.ResolveCoordinationParent(a.Home, a.guard.Key())
}

// run defers machine output until the exact durable outcome is recorded.
func (a *Coordinator) run(fn func() (int, error)) (int, error) {
	originalConfirm := a.Confirm
	a.Confirm = func(plan any, yes, dry bool) error {
		if a.guard != nil && !dry {
			raw, e := json.Marshal(plan)
			if e != nil {
				return e
			}
			hash, e := mesh.ReviewDigest(plan)
			if e != nil {
				return e
			}
			if e = a.guard.Change(func(op *mesh.Coordination) error {
				if len(op.Review) == 0 {
					op.Review = raw
					op.ReviewHash = hash
					op.ReviewRevision = op.Revision + 1
				}
				return nil
			}); e != nil {
				return e
			}
		}
		return originalConfirm(plan, yes, dry)
	}
	defer func() { a.Confirm = originalConfirm }()
	original := a.Print
	var result any
	a.Print = func(v any) error { result = v; return nil }
	defer func() { a.Print = original }()
	code, e := fn()
	if save := a.saveResult(result, e); save != nil {
		return 1, model.Err("mesh_operation_incomplete", "operation outcome could not be durably settled; inspect its exact key before recovery")
	}
	if result != nil {
		if printErr := original(result); printErr != nil {
			return 1, printErr
		}
	}
	return code, e
}
func (a *Coordinator) Connect(key string, request ConnectRequest, yes, dry bool) (int, error) {
	return a.run(func() (int, error) { return a.connect(key, request, yes, dry) })
}
func (a *Coordinator) Disconnect(key, host, reason string, yes, dry bool) (int, error) {
	return a.run(func() (int, error) { return a.disconnect(key, host, reason, yes, dry) })
}

func (a *Coordinator) check() error {
	if e := a.Context.Err(); e != nil {
		return e
	}
	if a.guard != nil {
		op, e := a.guard.Read()
		if e != nil {
			return e
		}
		if op.CancelRequested {
			return context.Canceled
		}
	}
	return nil
}
func (a *Coordinator) call(op string, q, result any) error {
	if e := a.check(); e != nil {
		return e
	}
	return a.Call(a.Context, op, q, result)
}
func (a *Coordinator) ssh(destination, binary string, q any, expected meshssh.Identity, result any) (meshssh.Identity, error) {
	if e := a.check(); e != nil {
		return expected, e
	}
	return a.SSH(a.Context, destination, binary, q, expected, result)
}
func (a *Coordinator) cleanupCall(op string, q, result any) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.Context), 3*time.Second)
	defer cancel()
	return a.Call(ctx, op, q, result)
}
func (a *Coordinator) cleanupSSH(destination, binary string, q any, expected meshssh.Identity, result any) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.Context), 3*time.Second)
	defer cancel()
	_, e := a.SSH(ctx, destination, binary, q, expected, result)
	return e
}

func (a *Coordinator) recordRequest(request StartRequest) error {
	raw, e := json.Marshal(request)
	if e != nil {
		return e
	}
	return a.guard.Change(func(op *mesh.Coordination) error {
		if len(op.Request) == 0 {
			op.Request = raw
		}
		return nil
	})
}
