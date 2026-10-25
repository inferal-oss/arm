package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/storage"
)

// existingStartup verifies the selected home and actual nonsecret generation;
// a status from a copied/wrong-home endpoint cannot satisfy idempotent Start.
func (a *app) existingStartup() (model.Status, bool, error) {
	var status model.Status
	before, err := local.ReadEndpoint(a.home)
	if err != nil {
		return status, false, nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Second)
	defer cancel()
	if err = client.Call(ctx, a.home, "status", nil, &status); err != nil {
		return status, false, nil
	}
	var actual model.BackendAuthority
	if err = client.Call(ctx, a.home, "startup_inspect", struct{}{}, &actual); err != nil {
		return status, false, err
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
