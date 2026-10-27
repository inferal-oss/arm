package manager

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
)

func (m *Manager) captureGradleLaunch(l *model.Lease, identity *model.ProcessIdentity) error {
	if !isGradleProfile(l.Request.Profile) {
		return nil
	}
	if identity == nil {
		return model.Err("observation_unavailable", "Gradle profiles require a captured native process identity before execution")
	}
	r, err := m.gradleRecord(l.ID)
	if err != nil {
		return err
	}
	if err := m.checkGradleDirectories(r); err != nil {
		return err
	}
	if r.Binding.Role == "service" {
		r.ExpectedPID = identity.PID
		copy := *identity
		r.Binding.ServiceProcess = &copy
	} else {
		if _, err := m.gradleParent(l.Request, l.ID); err != nil {
			return err
		}
		if _, err := gradleProjectRoot(r.Binding.CWD); err != nil {
			return err
		}
	}
	return m.saveGradleRecord(l, r, "workload.process_captured")
}

func (m *Manager) gradleServerAnnounced(l model.Lease) (bool, error) {
	f, err := os.Open(filepath.Join(m.home, "logs", l.ID+".log"))
	if err != nil {
		return false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), "Daemon server started."), nil
}

// This probe is ARM-authored code only. It joins the already captured owned
// service group, so its bounded startup work is within that group's claim and
// usage observations; application clients later get their own leases/groups.
func (m *Manager) startGradleProbe(l model.Lease, r gradleRecord, j *job) error {
	if j == nil || j.identity == nil || l.State != "active" || r.Binding.State != "starting" {
		return model.Err("service_unavailable", "Gradle service is not awaiting its owned readiness probe")
	}
	current, err := usage.Capture(j.identity.PID)
	if err != nil || current != *j.identity {
		return m.blockGradleService(&l, r, "Service native identity changed before its readiness probe")
	}
	root, _, err := m.openGradleDirectory(r.Storage, r.Name, r.Identity, false)
	if err != nil {
		return m.blockGradleService(&l, r, err.Error())
	}
	store, err := m.get(r.Storage)
	if err != nil {
		root.Close()
		return err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(store.ManagedDir, r.Name))
	if err != nil {
		root.Close()
		return m.blockGradleService(&l, r, err.Error())
	}
	program := gradleInitProgram(l.ID, r.Nonce, filepath.Join(path, "receipts"), r.Binding.CWD, r.Binding, r.ExpectedPID, 0, r.ServiceHeap, r.ServiceMetaspace, true)
	err = gradleWriteFile(root, "probe-init.gradle", []byte(program))
	root.Close()
	if err != nil {
		return m.blockGradleService(&l, r, err.Error())
	}
	remaining, clockErr := m.leaseRemaining(l)
	if clockErr != nil || remaining <= 0 {
		return model.Err("lease_inactive", "elapsed entitlement unavailable")
	}
	timeout := min(30*time.Second, remaining)
	if timeout <= 0 {
		return model.Err("lease_inactive", "Gradle service expired before its readiness probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	reader, writer, err := os.Pipe()
	if err != nil {
		cancel()
		return err
	}
	defer reader.Close()
	defer writer.Close()
	args := []string{r.Binding.Executable, "--offline", "--daemon", "--no-parallel", "--no-configuration-cache", "--no-watch-fs", fmt.Sprintf("--max-workers=%d", r.Binding.WorkerLimit), "--gradle-user-home", r.Binding.Home, "--project-cache-dir", r.Binding.ProjectCache, "--init-script", filepath.Join(path, "probe-init.gradle"), "-Dorg.gradle.jvmargs=" + strings.Join(r.Binding.JVMArgs, " "), "-Dorg.gradle.java.home=" + r.Binding.JavaHome, "--console=plain", "--quiet", "armServiceProbe"}
	gate := append([]string{"-c", `IFS= read -r arm_gate <&3 || exit 125; [ "$arm_gate" = go ] || exit 125; exec 3<&-; exec "$@"`, "arm-gradle-probe"}, args...)
	cmd := exec.CommandContext(ctx, "/bin/sh", gate...)
	cmd.Dir, cmd.Env = r.Binding.CWD, os.Environ()
	probeLease := l
	probeBinding := r.Binding
	probeBinding.JVMArgs, _, _, _ = gradleJVMArgs(gradleProbeMemory)
	probeLease.Workload = &probeBinding
	applyGradleEnvironment(probeLease, cmd)
	cmd.ExtraFiles = []*os.File{reader}
	cmd.Stdout, cmd.Stderr = j.cmd.Stdout, j.cmd.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: j.identity.PGID}
	if err := cmd.Start(); err != nil {
		cancel()
		return m.blockGradleService(&l, r, err.Error())
	}
	id, err := usage.Capture(cmd.Process.Pid)
	if err != nil || id.PGID != j.identity.PGID || id.UID != m.owner {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		return m.blockGradleService(&l, r, "Readiness probe native identity is unavailable; probe execution was not authorized")
	}
	r.Probe, r.Phase, r.Binding.State = &id, "probing", "probing"
	if err := m.saveGradleRecord(&l, r, "workload.probe_started"); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		return err
	}
	current, err = usage.Capture(j.identity.PID)
	if err != nil || current != *j.identity || m.jobs[l.ID] != j || !m.leaseLive(l) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		return m.blockGradleService(&l, r, "Service changed before probe authorization; probe was not permitted to execute")
	}
	_, gateErr := writer.Write([]byte("go\n"))
	if gateErr != nil {
		_ = cmd.Process.Kill()
	}
	m.wg.Go(func() {
		waitErr := cmd.Wait()
		cancel()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.finishGradleProbe(l.ID, j, waitErr, gateErr)
	})
	return nil
}

func (m *Manager) finishGradleProbe(id string, j *job, waitErr, gateErr error) {
	latest, err := m.get(id)
	if err != nil {
		return
	}
	record, err := m.gradleRecord(id)
	if err != nil {
		return
	}
	// A cancellation/expiry waiter may already have durably settled this
	// operation. A delayed probe callback must not resurrect blocked state.
	if latest.State == "terminal" || record.Phase == "finished" {
		return
	}
	evidence, evidenceErr := m.gradleFinishedEvidence(record)
	if waitErr != nil || gateErr != nil || evidenceErr != nil || evidence.Failure {
		record.Uncertain = true
		_ = m.blockGradleService(&latest, record, fmt.Sprintf("Owned Gradle readiness probe failed (process %v, gate %v, evidence %v); no application consumer authorized", waitErr, gateErr, evidenceErr))
		return
	}
	current, err := usage.Capture(j.identity.PID)
	if err != nil || current != *j.identity || m.jobs[id] != j || latest.State != "active" || latest.Expires == nil || !m.leaseLive(latest) {
		return
	}
	record.Binding.ServiceStartMillis = evidence.StartMillis
	record.Binding.GradleVersion = evidence.Version
	record.Binding.State, record.Phase = "ready", "ready"
	_ = m.saveGradleRecord(&latest, record, "workload.service_ready")
}

func (m *Manager) blockGradleService(l *model.Lease, r gradleRecord, detail string) error {
	r.Binding.State, r.Binding.Detail = "blocked", detail
	r.Phase = "blocked"
	l.Failure = detail
	return m.saveGradleRecord(l, r, "workload.service_blocked")
}
