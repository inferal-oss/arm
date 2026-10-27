package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/storage"
)

type exportFiles []model.ExportSelection

func (files *exportFiles) String() string { return "" }
func (files *exportFiles) Set(text string) error {
	source, path, _ := strings.Cut(text, "=")
	*files = append(*files, model.ExportSelection{Source: source, Path: path})
	return nil
}

func (a *app) storageExport(args []string) (int, error) {
	f := a.flags("storage export")
	var files exportFiles
	f.Var(&files, "file", "repeatable managed SOURCE[=RELATIVE_OUTPUT] file; default output is its basename")
	reason := f.String("reason", "", "required purpose for preserving the selected outputs")
	key := f.String("key", "", "exact-retry idempotency key (generated when omitted)")
	revision := f.Int("revision", 0, "optional reviewed source lease revision")
	dry := f.Bool("dry-run", false, "preview files/identities and destination without creating output")
	wait := f.Bool("wait", false, "wait for published or needs-attention outcome")
	f.Bool("json", false, "force JSON output")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 2 || strings.TrimSpace(*reason) == "" || len(*reason) > 4096 || *revision < 0 || len(*key) > 128 || *dry && *wait {
		return 2, fmt.Errorf("export requires LEASE_ID DEST_DIR, --reason and selected --file paths; --dry-run and --wait are exclusive")
	}
	selected, err := storage.NormalizeExportSelections(files)
	if err != nil {
		return 2, err
	}
	destination, err := filepath.Abs(f.Arg(1))
	if err != nil {
		return 2, err
	}
	if *key == "" {
		*key = local.ID()
	}
	request := model.StorageExportRequest{Key: *key, Lease: f.Arg(0), Destination: destination, Files: selected, Reason: *reason, Revision: *revision}
	if *dry {
		var plan model.StorageExportPlan
		if err := a.call("storage_export_preview", request, &plan); err != nil {
			return 1, err
		}
		return 0, a.print(plan)
	}
	var operation model.StorageExport
	if err := a.call("storage_export", request, &operation); err != nil {
		return 1, storageExportError(err, *key, "")
	}
	if *wait {
		for operation.Running || operation.State == "planned" || operation.State == "copying" || operation.State == "ready" || operation.State == "publishing" {
			if !operation.Running {
				if err := a.print(operation); err != nil {
					return 1, err
				}
				return 1, model.Err("export_unresolved", fmt.Sprintf("operation %s has no live copy worker; inspect/reconcile its recorded outcome (export key %q)", operation.ID, *key))
			}
			select {
			case <-a.ctx.Done():
				return 1, fmt.Errorf("export wait interrupted; operation %s key %s may still be running", operation.ID, *key)
			case <-time.After(100 * time.Millisecond):
			}
			if err := a.call("storage_export_inspect", map[string]string{"id": operation.ID}, &operation); err != nil {
				return 1, storageExportError(err, *key, operation.ID)
			}
		}
	}
	if err := a.print(operation); err != nil {
		return 1, err
	}
	if operation.State == "needs_attention" {
		return 1, model.Err("export_unresolved", fmt.Sprintf("output requires explicit inspection/reconciliation or handoff; inspect operation %s (export key %q)", operation.ID, *key))
	}
	return 0, nil
}

func storageExportError(err error, key, id string) error {
	context := fmt.Sprintf("export operation %q key %q; inspect exports before choosing a new key", id, key)
	var api *model.Error
	if errors.As(err, &api) {
		return model.Err(api.Code, api.Message+" ("+context+")")
	}
	return fmt.Errorf("%s: %w", context, err)
}

func (a *app) storageExports(args []string) (int, error) {
	action := "list"
	if len(args) != 0 && (args[0] == "inspect" || args[0] == "reconcile" || args[0] == "handoff") {
		action, args = args[0], args[1:]
	}
	name := "storage exports"
	if action != "list" {
		name += " " + action
	}
	f := a.flags(name)
	lease, reason := new(string), new(string)
	if action == "list" {
		f.StringVar(lease, "lease", "", "filter exports by source storage lease")
	}
	if action == "handoff" {
		f.StringVar(reason, "reason", "", "required reason for assuming responsibility for partial/uncertain output")
	}
	f.Bool("json", false, "force JSON output")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if action == "list" {
		if f.NArg() != 0 {
			return 2, fmt.Errorf("use storage exports [--lease ID], or storage exports inspect|reconcile|handoff EXPORT_ID")
		}
		var operations []model.StorageExportSummary
		if err := a.call("storage_exports", map[string]string{"lease": *lease}, &operations); err != nil {
			return 1, err
		}
		return 0, a.print(operations)
	}
	if f.NArg() != 1 || action == "handoff" && (strings.TrimSpace(*reason) == "" || len(*reason) > 4096) {
		return 2, fmt.Errorf("storage exports %s requires one EXPORT_ID; handoff also requires --reason", action)
	}
	var operation model.StorageExport
	if err := a.call("storage_export_"+action, map[string]string{"id": f.Arg(0), "reason": *reason}, &operation); err != nil {
		return 1, err
	}
	return 0, a.print(operation)
}

func (a *app) storageRetain(args []string) (int, error) {
	f := a.flags("storage retain")
	ttl := f.Duration("ttl", 0, "shorter remaining lifetime, at most 24h")
	reason := f.String("reason", "", "required reason the intermediate artifacts remain useful")
	revision := f.Int("revision", 0, "reviewed source lease revision (inspect when omitted)")
	f.Bool("json", false, "force JSON output")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 1 || *ttl <= 0 || *ttl > 24*time.Hour || *revision < 0 || strings.TrimSpace(*reason) == "" || len(*reason) > 4096 {
		return 2, fmt.Errorf("retain requires LEASE_ID, a positive --ttl at most 24h and --reason; it only shortens the existing deadline")
	}
	if *revision == 0 {
		var lease model.Lease
		if err := a.call("inspect", map[string]string{"id": f.Arg(0)}, &lease); err != nil {
			return 1, err
		}
		*revision = lease.Revision
	}
	var lease model.Lease
	request := map[string]any{"id": f.Arg(0), "revision": *revision, "ttl_ns": *ttl, "reason": *reason}
	if err := a.call("storage_retain", request, &lease); err != nil {
		return 1, err
	}
	return 0, a.print(lease)
}
