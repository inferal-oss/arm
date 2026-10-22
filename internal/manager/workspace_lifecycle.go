package manager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
)

type workspaceBoundary struct {
	manager         *Manager
	mu              sync.Mutex
	initialize      sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	operations      sync.WaitGroup
	closed          bool
	invalid         error
	view            model.WorkspaceAvailability
	operator        *workspace.Operator
	store           *workspace.Store
	service         *workspace.Service
	runtime         *workspace.Runtime
	identity        workspace.Identity
	identityBlocked bool
}

func (m *Manager) initWorkspaces() error {
	ctx, cancel := context.WithCancel(context.Background())
	w := &workspaceBoundary{manager: m, ctx: ctx, cancel: cancel, view: model.WorkspaceAvailability{Ledger: "unconfigured", Identity: "absent"}}
	m.workspaces = w
	read, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	op, e := workspace.OpenOperator(read, m.db, workspace.Authority{Host: m.host, UID: m.owner}, nil)
	if e != nil {
		if !workspace.IsInvalidState(e) {
			if errors.Is(e, context.DeadlineExceeded) {
				w.recordUnavailable()
				return nil
			}
			cancel()
			return e
		}
		w.recordError(e)
		return nil
	}
	w.operator = op
	v, e := op.Read(read)
	if e != nil {
		if !workspace.IsInvalidState(e) {
			if errors.Is(e, context.DeadlineExceeded) {
				w.recordUnavailable()
				return nil
			}
			cancel()
			return e
		}
		w.recordError(e)
		return nil
	}
	w.recordRead(v)
	// Only existing public identity is read. No key, listener or schema is made.
	_, e = w.current(read)
	if e != nil && !workspace.IsInvalidState(e) {
		var api *model.Error
		if !errors.As(e, &api) || api.Code != "workspace_identity_unavailable" {
			if errors.Is(e, context.DeadlineExceeded) {
				w.recordUnavailable()
			} else {
				cancel()
				return e
			}
		}
	}
	return nil
}
func (w *workspaceBoundary) projection() *model.WorkspaceAvailability {
	w.mu.Lock()
	defer w.mu.Unlock()
	v := w.view
	if v.Error != nil {
		copy := *v.Error
		v.Error = &copy
	}
	return &v
}
func (w *workspaceBoundary) recordRead(v workspace.Records) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.view.Validated = time.Now().UTC()
	if v.Initialized {
		w.view.Ledger = "available"
	} else {
		w.view.Ledger = "unconfigured"
	}
}
func (w *workspaceBoundary) recordError(e error) {
	if !workspace.IsInvalidState(e) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalid = e
	w.view.Ledger = "invalid"
	w.view.Error = &model.Error{Code: "workspace_state_invalid", Message: "Workspace evidence is invalid; workspace changes are paused. Ordinary resource cleanup remains available; no records were repaired."}
}
func (w *workspaceBoundary) current(ctx context.Context) (*workspace.Store, error) {
	w.manager.mu.Lock()
	stopping := w.manager.closing || w.manager.draining
	w.manager.mu.Unlock()
	if stopping {
		return nil, model.Err("backend_stopping", "workspace authority is draining or closing")
	}
	w.initialize.Lock()
	defer w.initialize.Unlock()
	w.mu.Lock()
	closed, invalid, blocked := w.closed, w.invalid, w.identityBlocked
	w.mu.Unlock()
	if closed {
		return nil, model.Err("backend_stopping", "workspace lifecycle closing")
	}
	if invalid != nil {
		return nil, w.failure()
	}
	if blocked {
		return nil, model.Err("workspace_identity_unavailable", "Current workspace identity was invalidated; restart and explicit identity review may be required, never silent rotation")
	}
	config, found, e := mesh.ReadRegistry(w.manager.home)
	state := "current"
	if e != nil {
		state = "unavailable"
	} else if !found {
		state = "absent"
	} else if config.Identity.Host != w.manager.host || config.Identity.Owner != w.manager.owner {
		state = "changed"
	} else if _, e = mesh.ValidateIdentity(config.Identity); e != nil {
		state = "expired"
	}
	self := workspace.Identity{Host: config.Identity.Host, Name: config.Identity.Name, Certificate: config.Identity.Certificate, Fingerprint: config.Identity.Fingerprint, SPKI: config.Identity.SPKI, Expires: config.Identity.Expires}
	w.mu.Lock()
	if state == "current" && w.store != nil && w.identity != self {
		state = "changed"
	}
	w.view.Identity = state
	if state != "current" {
		runtime, service := w.runtime, w.service
		if w.store != nil {
			w.identityBlocked = true
		}
		w.mu.Unlock()
		if service != nil {
			service.Close()
		}
		if runtime != nil {
			runtime.Close()
		}
		return nil, model.Err("workspace_identity_unavailable", "Existing current node identity is unavailable or changed; historical inspection/local settlement remain available. No identity was initialized.")
	}
	store := w.store
	w.mu.Unlock()
	if store != nil {
		return store, nil
	}
	store, e = workspace.Open(ctx, w.manager.db, workspace.Authority{Host: w.manager.host, UID: w.manager.owner}, self, nil)
	if e != nil {
		w.recordError(e)
		return nil, e
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, model.Err("backend_stopping", "workspace lifecycle closing")
	}
	w.store = store
	w.identity = self
	w.runtime = workspace.NewRuntime(store)
	w.service = workspace.NewServiceWithGate(w.ctx, store, func(ctx context.Context) error { _, e := w.current(ctx); return e })
	return store, nil
}
func (w *workspaceBoundary) close() {
	w.mu.Lock()
	w.closed = true
	w.view.Closing = true
	w.cancel()
	service, runtime := w.service, w.runtime
	w.mu.Unlock()
	if service != nil {
		service.Close()
	}
	if runtime != nil {
		runtime.Close()
	}
	w.operations.Wait()
}
func workspaceCall[T any](ctx context.Context, m *Manager, authority bool, fn func(context.Context, *workspaceBoundary, *workspace.Store) (T, error)) (result T, err error) {
	m.mu.Lock()
	stopping := m.closing || authority && m.draining
	m.mu.Unlock()
	if stopping {
		return result, model.Err("backend_stopping", "workspace authority is draining or closing")
	}
	w := m.workspaces
	if w == nil {
		return result, model.Err("workspace_unavailable", "workspace lifecycle unavailable")
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return result, model.Err("backend_stopping", "workspace lifecycle closing")
	}
	if w.invalid != nil {
		w.mu.Unlock()
		return result, w.failure()
	}
	w.operations.Add(1)
	w.mu.Unlock()
	defer w.operations.Done()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	if w.ctx.Err() != nil {
		return result, model.Err("backend_stopping", "workspace lifecycle closing")
	}
	var store *workspace.Store
	if authority {
		store, err = w.current(ctx)
		if err != nil {
			return result, err
		}
	}
	result, err = fn(ctx, w, store)
	if workspace.IsInvalidState(err) {
		w.recordError(err)
	}
	if err == nil {
		if records, ok := any(result).(workspace.Records); ok {
			w.recordRead(records)
		} else {
			w.mu.Lock()
			w.view.Validated = time.Now().UTC()
			if op, ok := any(result).(workspace.Operation); ok && op.Workspace != "" {
				w.view.Ledger = "available"
			}
			w.mu.Unlock()
		}
	}
	if errors.Is(err, context.Canceled) && w.ctx.Err() != nil {
		return result, model.Err("backend_stopping", "workspace call interrupted by shutdown; inspect exact key before recovery")
	}
	return
}

func (w *workspaceBoundary) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.view.Error != nil {
		copy := *w.view.Error
		return &copy
	}
	return model.Err("workspace_unavailable", "Workspace ledger unavailable; ordinary resource cleanup remains available")
}
func (w *workspaceBoundary) recordUnavailable() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalid = context.DeadlineExceeded
	w.view.Ledger = "unavailable"
	w.view.Error = &model.Error{Code: "workspace_unavailable", Message: "Workspace validation timed out; workspace authority is unavailable, ordinary resource cleanup remains available"}
}
