package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/database"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/storage"
)

// existingStartup verifies the selected home and actual nonsecret generation;
// a status from a copied/wrong-home endpoint cannot satisfy idempotent Start.
func (a *app) existingStartup() (model.Status, bool, error) {
	return a.existingStartupContext(a.ctx)
}

func (a *app) existingStartupContext(parent context.Context) (model.Status, bool, error) {
	var status model.Status
	if err := parent.Err(); err != nil {
		return status, false, err
	}
	before, err := local.ReadEndpoint(a.home)
	if os.IsNotExist(err) {
		// A missing symlink target is a failed inspection of an existing
		// endpoint name, not authority to start a replacement backend.
		if _, namedErr := os.Lstat(filepath.Join(a.home, "endpoint.json")); os.IsNotExist(namedErr) {
			return status, false, nil
		}
	}
	if err != nil {
		return status, false, a.startupOutcomeUnknown()
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if err = client.Call(ctx, a.home, "status", nil, &status); err != nil {
		return status, false, a.startupInspectionError(parent, err)
	}
	var actual model.BackendAuthority
	if err = client.Call(ctx, a.home, "startup_inspect", struct{}{}, &actual); err != nil {
		return status, false, a.startupInspectionError(parent, err)
	}
	dir, err := database.OpenDirectory(a.home)
	if err != nil {
		return status, false, err
	}
	defer dir.Close()
	id, err := storage.DirectoryIdentity(int(dir.FD.Fd()))
	if err != nil {
		return status, false, err
	}
	home, err := filepath.EvalSymlinks(a.home)
	if err != nil {
		return status, false, err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return status, false, err
	}
	after, err := local.ReadEndpoint(a.home)
	if err != nil || after != before || len(actual.Generation) != 32 || actual.Generation != before.Generation || actual.Home != home || actual.Directory != id || actual.Host != status.Host || actual.Owner != status.Owner || actual.Owner != strconv.Itoa(os.Geteuid()) {
		return status, false, model.Err("node_generation_changed", "running endpoint differs from selected native authority/home/generation")
	}
	if status.Draining {
		return status, false, model.Err("backend_draining", "existing authority is draining; Start cannot override its intent")
	}
	return status, true, nil
}

func (a *app) startupOutcomeUnknown() error {
	return model.Err("startup_outcome_unknown", fmt.Sprintf("Backend startup for %q is unconfirmed; inspect %q before retrying. The original backend may still be running.", a.home, filepath.Join(a.home, "daemon.log")))
}

func (a *app) startupInspectionError(parent context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	// A definite backend refusal keeps its original code; only transport/read
	// uncertainty is polled. In particular, stop intent is never overridden.
	var detail *model.Error
	if errors.As(err, &detail) {
		return err
	}
	return a.startupOutcomeUnknown()
}

// waitForStartup is used only after Start succeeded. Cancellation stops this
// client's wait; it does not establish that the original backend stopped.
func (a *app) waitForStartup() (model.Status, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	for {
		status, running, err := a.existingStartupContext(ctx)
		if ctx.Err() != nil {
			return status, a.startupOutcomeUnknown()
		}
		if err != nil {
			var detail *model.Error
			if !errors.As(err, &detail) || detail.Code != "startup_outcome_unknown" {
				return status, err
			}
		}
		if running {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, a.startupOutcomeUnknown()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
