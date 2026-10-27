package cli

import (
	"fmt"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
	"path/filepath"
)

type meshLifecycleHelper = meshops.Helper
type meshDisconnectRequest = meshops.DisconnectRequest

func (a *app) meshLifecycleBootstrap(raw []byte) (int, error) {
	var q meshLifecycleHelper
	if err := mesh.Decode(raw, &q); err != nil {
		return 2, err
	}
	if q.Home != "" {
		if !filepath.IsAbs(q.Home) {
			return 2, fmt.Errorf("remote ARM home must be absolute")
		}
		a.home = q.Home
	}
	if q.Action == "lifecycle_inspect" {
		result, err := a.inspectMeshLifecycle()
		if err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	if q.Action == "lifecycle_ensure" {
		current, err := a.inspectMeshLifecycle()
		if err != nil {
			return 1, err
		}
		if q.ExpectedOwner != current.Owner.UID || (q.ExpectedHost != "" && q.ExpectedHost != current.Host) {
			return 1, model.Err("setup_changed", "destination owner/host changed since inspection")
		}
		result, err := a.ensureMeshBackend(q.Startup)
		if err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	var result any
	var err error
	switch q.Action {
	case "lifecycle_prepare":
		err = a.call("mesh_setup_prepare", q.Prepare, &result)
	case "lifecycle_apply":
		err = a.call("mesh_setup_apply", q.Apply, &result)
	case "lifecycle_abort":
		err = a.call("mesh_setup_abort", map[string]string{"Key": q.Apply.Key, "Token": q.Apply.Token}, &result)
	case "lifecycle_revoke":
		current, e := a.inspectMeshLifecycle()
		if e != nil {
			return 1, e
		}
		if !current.Running {
			if _, e = a.ensureMeshBackend(current.Startup); e != nil {
				return 1, e
			}
		}
		err = a.call("mesh_setup_revoke", q.Revoke, &result)
	default:
		return 2, fmt.Errorf("unknown mesh lifecycle helper action")
	}
	if err != nil {
		return 1, err
	}
	return 0, a.print(result)
}
