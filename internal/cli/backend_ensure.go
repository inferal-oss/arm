package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func (a *app) inspectMeshLifecycle() (mesh.LifecycleInspection, error) {
	inspection, err := mesh.InspectLifecycle(a.home, Version)
	if err != nil {
		return inspection, err
	}
	resolveStopped := func() (mesh.LifecycleInspection, error) {
		if inspection.StartupSource == "current defaults; no prior launch policy exists" {
			resolved, err := daemonconfig.Resolve(a.home, daemonDefaults(), daemonconfig.Overrides{})
			if err != nil {
				return inspection, err
			}
			inspection.Startup.Resources = resolved.Settings.Resources
			inspection.Startup.Listen = resolved.Settings.Listen
			inspection.StartupSource = "validated desired configuration; no prior launch policy exists"
		}
		return inspection, nil
	}
	if _, err := os.Lstat(filepath.Join(a.home, "endpoint.json")); os.IsNotExist(err) {
		return resolveStopped()
	} else if err != nil {
		return inspection, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
	defer cancel()
	var status model.Status
	if err := client.Call(ctx, a.home, "status", nil, &status); err != nil {
		return resolveStopped()
	}
	inspection.Running = true
	inspection.Host = status.Host
	if status.Draining {
		return inspection, model.Err("backend_draining", "mesh connection cannot resume admission or override an operator drain")
	}
	if inspection.StartupSource != "persisted successful backend launch" {
		inspection.Startup.Resources = status.Capacity
		inspection.Startup.Resources.Memory = 0
		if status.Memory != nil && status.Memory.Ceiling != nil {
			inspection.Startup.Resources.Memory = *status.Memory.Ceiling
		}
		if status.Disk != nil {
			inspection.Startup.Resources.Disk = 0
			if status.Disk.Ceiling != nil {
				inspection.Startup.Resources.Disk = *status.Disk.Ceiling
			}
		}
		inspection.StartupSource = "actual existing backend settings; launch provenance not yet persisted"
	}
	return inspection, nil
}

// ensureMeshBackend has no output side effects, so callers preserve the single
// JSON result contract. It never signals a stale endpoint PID or changes a
// running authority's budgets.
func (a *app) ensureMeshBackend(expected mesh.StartupPolicy) (mesh.LifecycleInspection, error) {
	return a.ensureMeshBackendWithLauncher(expected, nil)
}

// The optional launcher lets disposable tests retain their original child and
// Wait. Production uses only the current executable and existing detached start.
func (a *app) ensureMeshBackendWithLauncher(expected mesh.StartupPolicy, launch func(*exec.Cmd) error) (mesh.LifecycleInspection, error) {
	if err := expected.Validate(); err != nil {
		return mesh.LifecycleInspection{}, err
	}
	snapshot, err := daemonconfig.SnapshotSHA256(daemonconfig.Settings{Resources: expected.Resources, Listen: expected.Listen})
	if err != nil {
		return mesh.LifecycleInspection{}, err
	}
	inspection, err := a.inspectMeshLifecycle()
	if err != nil {
		return inspection, err
	}
	if inspection.Running {
		return inspection, nil
	}
	if inspection.Startup != expected {
		return inspection, model.Err("setup_changed", "backend startup policy changed since inspection")
	}
	if err := local.Prepare(a.home); err != nil {
		return inspection, err
	}
	executable, err := os.Executable()
	if err != nil {
		return inspection, err
	}
	log, err := mesh.OpenLifecycleLog(a.home)
	if err != nil {
		return inspection, err
	}
	defer log.Close()
	memory := "auto"
	if expected.Resources.Memory > 0 {
		memory = strconv.FormatInt(expected.Resources.Memory, 10)
	}
	disk := "auto"
	if expected.Resources.Disk > 0 {
		disk = strconv.FormatInt(expected.Resources.Disk, 10)
	}
	arguments := []string{"--home", a.home, "serve", "--expect-config", snapshot}
	if inspection.StartupSource == "persisted successful backend launch" {
		arguments = append(arguments, "--cpu", strconv.FormatFloat(expected.Resources.CPU, 'f', -1, 64), "--memory", memory, "--disk", disk, "--listen", expected.Listen)
	}
	command := exec.Command(executable, arguments...)
	command.Stdout, command.Stderr = log, log
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if launch == nil {
		launch = func(cmd *exec.Cmd) error {
			if err := cmd.Start(); err != nil {
				return err
			}
			_ = cmd.Process.Release()
			return nil
		}
	}
	if err := launch(command); err != nil {
		return inspection, err
	}
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		current, err := a.inspectMeshLifecycle()
		if err != nil {
			return current, err
		}
		if current.Running {
			if current.Startup.Resources != expected.Resources || current.Startup.Listen != expected.Listen {
				return current, model.Err("setup_changed", "started backend does not match the inspected startup policy; inspect its retained startup decision before proceeding")
			}
			return current, nil
		}
		select {
		case <-a.ctx.Done():
			return inspection, a.ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return inspection, fmt.Errorf("backend did not become available; private startup log retained under selected ARM home")
}
