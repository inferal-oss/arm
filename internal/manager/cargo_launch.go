package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yrashk/arm/internal/cargoprofile"
	"github.com/yrashk/arm/internal/childowner"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
)

type CargoHelperSource func() (string, cargoprofile.HelperRuntime, error)
type cargoWorker struct {
	leaseHost, leaseOwner string
	leaseRequest          model.Request
	cancel                context.CancelFunc
	done                  chan struct{}
	id                    string
}

// Cancel the original child before IPC EOF can make the helper's parent-loss
// path kill its own group. Preserve the signal result for sticky recording;
// closing these original IPC handles then unblocks the parent's protocol read.
func cancelCargoOriginal(owner *childowner.Owner, ipc ...io.Closer) error {
	var signalErr error
	if owner != nil {
		signalErr = owner.Signal(syscall.SIGKILL)
	} else {
		signalErr = model.Err("observation_unavailable", "original Cargo helper binding unavailable; no signal authority")
	}
	for _, closer := range ipc {
		if closer != nil {
			_ = closer.Close()
		}
	}
	return signalErr
}

func (m *Manager) ConfigureCargoHelper(source CargoHelperSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return model.Err("backend_closing", "Cargo helper configuration is closed")
	}
	m.cargoHelper = source
	return nil
}

// Called under Manager.mu: only record the charged intent and schedule work.
// All filesystem, image verification, process, pipe and tool work is below in
// the worker, after the global mutex has been released.
func (m *Manager) launchCargo(l model.Lease, run RunRequest) (model.Lease, error) {
	rec, e := m.getCargo(l.ID)
	if e != nil {
		return l, e
	}
	if rec.Phase != "admitted" || m.cargoWorkers[l.ID] != nil {
		return l, model.Err("cargo_state_corrupt", "Cargo cannot repeat a previously started spawn intent")
	}
	if err := childowner.Available(); err != nil {
		return m.cargoNoSpawnFailure(l, rec, err)
	}
	if m.cargoHelper == nil {
		return m.cargoNoSpawnFailure(l, rec, model.Err("cargo_helper_unavailable", "startup-verified Cargo helper is unavailable"))
	}
	if reason := m.startDeadlineProblem(l); reason != nil {
		return m.cargoRejectUnstarted(l, reason)
	}
	source := m.cargoHelper
	ctx, cancel := context.WithCancel(context.Background())
	worker := &cargoWorker{cancel: cancel, done: make(chan struct{}), id: local.ID(), leaseHost: l.Host, leaseOwner: l.Owner, leaseRequest: l.Request}
	rec.Phase, rec.Worker = "spawn_intent", worker.id
	rec.Revision++
	l.Revision++
	if e = m.putCargo(l, rec, "cargo.spawn_intent"); e != nil {
		cancel()
		return l, e
	}
	if m.cargoWorkers == nil {
		m.cargoWorkers = map[string]*cargoWorker{}
	}
	m.cargoWorkers[l.ID] = worker
	m.wg.Go(func() { m.runCargoWorker(ctx, worker, l.ID, run, source) })
	return m.get(l.ID)
}
func (m *Manager) cargoWorkerCurrent(id string, worker *cargoWorker) (model.Lease, cargoRecord, error) {
	l, e := m.get(id)
	if e != nil {
		return l, cargoRecord{}, e
	}
	rec, e := m.getCargo(id)
	if e != nil {
		return l, rec, e
	}
	if l.ID != id || m.cargoWorkers[id] != worker || rec.Worker != worker.id || l.Host != worker.leaseHost || l.Owner != worker.leaseOwner || !reflect.DeepEqual(l.Request, worker.leaseRequest) {
		return l, rec, model.Err("cargo_worker_changed", "Cargo worker identity no longer matches durable intent")
	}
	return l, rec, nil
}
func (m *Manager) cargoGate(l model.Lease, rec cargoRecord, worker *cargoWorker) error {
	if m.closing || m.draining || m.cargoWorkers[l.ID] != worker || rec.Worker != worker.id || l.State == "ending" || l.State == "needs_attention" || l.State == "terminal" || l.Expires == nil || !time.Now().Before(*l.Expires) {
		return model.Err("cargo_gate_closed", "Cargo lease is closing, expired or no longer owns its worker")
	}
	if reason := m.startDeadlineProblem(l); reason != nil {
		return reason
	}
	_, e := m.cargoStorageParent(l.Request, l.Expires)
	return e
}
func (m *Manager) runCargoWorker(ctx context.Context, worker *cargoWorker, id string, run RunRequest, source CargoHelperSource) {
	defer close(worker.done)
	defer worker.cancel()
	// A separate watcher is joined BEFORE Wait reaps the current child; no
	// signalling can subsequently race PID reuse. Pipes unblock on cancellation.
	var cmd *exec.Cmd
	var originalOwner *childowner.Owner
	var ownerCleanupErr error
	var j *job
	var childStarted, preflight bool
	var childWaited, childJoined bool
	var signalMu sync.Mutex
	var signalErr error
	recordSignal := func(err error) {
		if err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, childowner.ErrRetired) {
			signalMu.Lock()
			signalErr = errors.Join(signalErr, err)
			signalMu.Unlock()
		}
	}
	var finish *cargoprofile.HelperFinished
	var resultError error
	var parentWrite, childRead *os.File
	var inRead, outWrite *os.File
	var log *os.File
	var stopper chan struct{}
	var stopped chan struct{}
	var stopOnce sync.Once
	var native *model.ProcessIdentity
	stopWatch := func() {
		if stopper != nil {
			stopOnce.Do(func() { close(stopper) })
			<-stopped
		}
	}
	defer func() {
		stopWatch()
		if cmd != nil && childStarted && !childWaited {
			// Both IPC handles were created before this original child started.
			recordSignal(cancelCargoOriginal(originalOwner, parentWrite, childRead))
			if originalOwner != nil {
				if j != nil {
					j.retireControls()
				}
				cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				joined := originalOwner.Wait(cleanupCtx)
				cancel()
				childJoined = joined.Reaped
				ownerCleanupErr = joined.CleanupError
				resultError = errors.Join(resultError, ownerCleanupErr)
			} else {
				// Observer capability was checked before spawn. Losing the original
				// binding before init/preflight remains unknown and grants no group action.
				resultError = errors.Join(resultError, fmt.Errorf("original Cargo helper binding unavailable; no native Wait authority"))
			}
			if j != nil {
				j.cpuExited.Store(childJoined)
			}
			childWaited = true
		}
		for _, f := range []*os.File{parentWrite, childRead, inRead, outWrite, log} {
			if f != nil {
				f.Close()
			}
		}
		absent := !childStarted
		if childStarted && childJoined {
			deadline := time.Now().Add(2 * time.Second)
			for {
				absent = errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH)
				if absent || !time.Now().Before(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		signalMu.Lock()
		originalSignalErr := signalErr
		signalMu.Unlock()
		resultError = errors.Join(resultError, originalSignalErr)
		var controlErr error
		if j != nil {
			controlErr = j.controlFailure()
			resultError = errors.Join(resultError, controlErr)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		latest, rec, e := m.cargoWorkerCurrent(id, worker)
		if e != nil {
			return
		}
		rec.Worker = ""
		rec.Revision++
		latest.Revision++
		if j != nil {
			if childJoined {
				delete(m.jobs, id)
			} else {
				m.jobs[id] = j
				latest.PID = cmd.Process.Pid
				latest.Process = native
			}
			delete(m.usages, id)
			close(j.done)
		}
		delete(m.cargoWorkers, id)
		now := time.Now().UTC()
		rec.FinalAt = &now
		rec.GroupAbsent = absent
		rec.NoSpawn = !childStarted
		rec.NoPreflight = !preflight
		if finish != nil {
			rec.Finished = finish
		}
		if resultError != nil {
			rec.Error = resultError.Error()
			if latest.Failure == "" {
				latest.Failure = rec.Error
			}
		}
		// A lost post-grant receipt stays an explicit uncertain obligation, even
		// if a currently owned child was killed. Never restart the requested work.
		if !absent || ownerCleanupErr != nil || (childStarted && !childJoined) || originalSignalErr != nil || controlErr != nil || (childStarted && native == nil) || (preflight && finish == nil && latest.Outcome == "") {
			rec.Phase = "needs_attention"
			rec.Uncertain = true
			latest.State = "needs_attention"
			latest.Outcome = "cargo_outcome_unknown"
			latest.Ended = nil
		} else {
			rec.Phase = "finished"
			rec.Uncertain = false
			latest.State = "terminal"
			latest.Ended = &now
			code := 125
			if finish != nil {
				code = finish.Code
			}
			latest.ExitCode = &code
			if latest.Outcome == "" {
				latest.Outcome = "completed"
				if code != 0 {
					latest.Outcome = "failed"
				}
			}
		}
		if latest.Usage != nil {
			latest.Usage.Live = false
			latest.Usage.Stale = true
			latest.Usage.Members = nil
		}
		if e = m.putCargo(latest, rec, "cargo."+rec.Phase); e != nil {
			// Failure to persist finality is never permission to return capacity.
			// A best-effort distinct attention phase may succeed when only final
			// transition writes failed; otherwise the old consuming intent remains.
			rec.Phase = "needs_attention"
			rec.Uncertain = true
			rec.Error = "Cargo final persistence failed; explicit reconciliation required"
			latest.State = "needs_attention"
			latest.Outcome = "cargo_persistence_failed"
			latest.Ended = nil
			latest.Failure = rec.Error
			rec.Revision++
			latest.Revision++
			_ = m.putCargo(latest, rec, "cargo.persistence_failed")
		}
	}()
	fail := func(e error) { resultError = e }
	if e := ctx.Err(); e != nil {
		fail(e)
		return
	}
	path, runtime, e := source()
	if e != nil {
		fail(model.Err("cargo_helper_unavailable", "startup executable changed/unavailable; no Cargo tools ran"))
		return
	}
	command, e := cargoprofile.Parse(run.Command, run.Resources.CPU)
	if e != nil {
		fail(e)
		return
	}
	m.mu.Lock()
	l, rec, e := m.cargoWorkerCurrent(id, worker)
	var store model.Lease
	if e == nil {
		e = m.cargoGate(l, rec, worker)
	}
	if e == nil {
		store, e = m.cargoStorageParent(l.Request, l.Expires)
	}
	if e == nil {
		rec.Runtime = &runtime
		rec.Revision++
		l.Revision++
		e = m.putCargo(l, rec, "cargo.runtime_verified")
	}
	m.mu.Unlock()
	if e != nil {
		fail(e)
		return
	}
	canonicalAllocation, e := cargoprofile.CanonicalAllocation(store.ManagedDir, *store.Artifact.Identity)
	if e != nil {
		fail(e)
		return
	}
	store.ManagedDir = canonicalAllocation
	log, e = cargoOpenLog(m.home, id)
	if e != nil {
		fail(e)
		return
	}
	inRead, parentWrite, e = os.Pipe()
	if e != nil {
		fail(e)
		return
	}
	childRead, outWrite, e = os.Pipe()
	if e != nil {
		fail(e)
		return
	}
	cmd = exec.Command(path, "_cargo-helper")
	cmd.Dir = run.CWD
	cmd.Env = cargoChildEnvironment(l, store, m.home)
	cmd.ExtraFiles = []*os.File{inRead, outWrite}
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = ctx.Err(); e != nil {
		fail(e)
		return
	}
	if e = cmd.Start(); e != nil {
		fail(model.Err("cargo_helper_start_failed", "verified Cargo helper failed to start"))
		return
	}
	childStarted = true
	originalOwner, e = childowner.New(cmd)
	if e != nil {
		j = &job{cmd: cmd, done: make(chan struct{}), controlRetired: true}
		fail(e)
		return
	}
	inRead.Close()
	outWrite.Close()
	identity, e := usage.Capture(cmd.Process.Pid)
	if e != nil || identity.PID != cmd.Process.Pid || identity.PGID != cmd.Process.Pid || identity.UID != m.owner {
		fail(model.Err("observation_unavailable", "Cargo helper native identity unavailable; preflight not authorized"))
		return
	}
	native = &identity
	cpuAnchor, _ := usage.CaptureCPUAnchor(identity)
	stopper, stopped = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			recordSignal(cancelCargoOriginal(originalOwner, parentWrite, childRead))
		case <-stopper:
		}
	}()
	m.mu.Lock()
	l, rec, e = m.cargoWorkerCurrent(id, worker)
	if e == nil {
		rec.Process = native
		rec.Phase = "helper_bound"
		rec.Revision++
		l.PID = identity.PID
		l.Process = native
		l.Revision++
		j = &job{cmd: cmd, done: make(chan struct{}), identity: native, cpuAnchor: cpuAnchor, childOwner: originalOwner}
		m.jobs[id] = j
		e = m.putCargo(l, rec, "cargo.helper_bound")
	}
	if e == nil {
		e = m.cargoGate(l, rec, worker)
	}
	m.mu.Unlock()
	if e != nil {
		fail(e)
		return
	}
	channel := cargoprofile.HelperChannel{In: childRead, Out: parentWrite, Lease: id, Nonce: rec.Nonce}
	init := cargoprofile.HelperInit{Command: command, CWD: run.CWD, Allocation: store.ManagedDir, AllocationIdentity: *store.Artifact.Identity, Runtime: runtime, Expires: *l.Expires, StartDeadline: l.Request.StartDeadline}
	if e = channel.Send("init", init); e != nil {
		fail(e)
		return
	}
	var hello cargoprofile.HelperHello
	if e = channel.Receive("hello", &hello); e != nil || hello.Runtime != runtime || hello.Process != identity {
		fail(model.Err("cargo_helper_mismatch", "Cargo helper did not prove expected running image/native identity"))
		return
	}
	m.mu.Lock()
	l, rec, e = m.cargoWorkerCurrent(id, worker)
	if e == nil {
		e = m.cargoGate(l, rec, worker)
	}
	if e == nil {
		rec.Phase = "creating"
		now := time.Now().UTC()
		rec.PreflightAt = &now
		rec.Revision++
		l.Revision++
		e = m.putCargo(l, rec, "cargo.preflight_authorized")
	}
	m.mu.Unlock()
	if e != nil {
		fail(e)
		return
	}
	// From this point a dropped reply is not proof that tool/filesystem work did
	// not occur. Retain uncertainty if the helper's final receipt is unavailable.
	preflight = true
	if e = channel.Send("preflight", cargoprofile.HelperGrant{Approved: true, Revision: l.Revision, Expires: *l.Expires, StartDeadline: l.Request.StartDeadline}); e != nil {
		fail(e)
		return
	}
	for {
		packet, e := channel.Next()
		if e != nil {
			fail(e)
			return
		}
		switch packet.Kind {
		case "birth", "policy":
			m.mu.Lock()
			l, rec, e = m.cargoWorkerCurrent(id, worker)
			if e == nil {
				e = m.cargoGate(l, rec, worker)
			}
			var step int
			if e == nil && packet.Kind == "birth" {
				var birth cargoprofile.BirthStep
				e = cargoprofile.DecodeCanonical(packet.Data, &birth)
				if e == nil {
					step = birth.Step
					e = recordCargoBirth(&rec, init, birth)
				}
			}
			if e == nil && packet.Kind == "policy" {
				var policy cargoprofile.HelperPolicy
				e = cargoprofile.DecodeCanonical(packet.Data, &policy)
				if e == nil {
					step = 5
					if !rec.PolicyIntent {
						if policy.Evidence != nil {
							e = fmt.Errorf("Cargo policy evidence preceded intent")
						} else {
							rec.PolicyIntent = true
						}
					} else if rec.Policy != nil || policy.Evidence == nil {
						e = fmt.Errorf("Cargo policy birth acknowledgement repeated/missing")
					} else {
						copy := *policy.Evidence
						rec.Policy = &copy
						rec.Phase = "resolving"
					}
				}
			}
			if e == nil {
				rec.Revision++
				l.Revision++
				e = m.putCargo(l, rec, "cargo."+packet.Kind)
			}
			m.mu.Unlock()
			if e != nil {
				fail(e)
				return
			}
			if e = channel.Send("recorded", cargoprofile.HelperAck{Step: step, Revision: l.Revision}); e != nil {
				fail(e)
				return
			}
		case "prepared":
			var prepared cargoprofile.Prepared
			if e = cargoprofile.DecodeCanonical(packet.Data, &prepared); e != nil {
				fail(e)
				return
			}
			digest, e := cargoprofile.PreparedDigest(prepared)
			if e != nil {
				fail(e)
				return
			}
			m.mu.Lock()
			l, rec, e = m.cargoWorkerCurrent(id, worker)
			if e == nil {
				e = m.cargoGate(l, rec, worker)
			}
			if e == nil && (rec.Prepared != nil || rec.Policy == nil || !reflect.DeepEqual(*rec.Policy, prepared.Policy) || !reflect.DeepEqual(prepared.Command, command) || prepared.Owned.AllocationPath != init.Allocation || prepared.Owned.AllocationIdentity != init.AllocationIdentity || !cargoPreparedBirthsMatch(rec, prepared) || prepared.Plan.Jobs != command.Jobs || prepared.Plan.Roots != prepared.Owned.Roots()) {
				e = fmt.Errorf("Cargo prepared evidence differs from original command/owned policy")
			}
			if e == nil {
				now := time.Now().UTC()
				rec.Prepared = &prepared
				rec.PreparedSHA256 = digest
				rec.AuthorizedAt = &now
				rec.Phase = "compilation_authorized"
				rec.Revision++
				l.Revision++
				e = m.putCargo(l, rec, "cargo.compilation_authorized")
			}
			m.mu.Unlock()
			if e != nil {
				fail(e)
				return
			}
			if e = channel.Send("compile", cargoprofile.HelperGrant{Approved: true, Revision: l.Revision, Expires: *l.Expires, StartDeadline: l.Request.StartDeadline, EvidenceSHA256: digest}); e != nil {
				fail(e)
				return
			}
		case "started":
			var started cargoprofile.HelperStarted
			if e = cargoprofile.DecodeCanonical(packet.Data, &started); e != nil {
				fail(e)
				return
			}
			m.mu.Lock()
			l, rec, e = m.cargoWorkerCurrent(id, worker)
			if e == nil && (rec.AuthorizedAt == nil || rec.Started != nil || started.Process.PGID != identity.PGID || started.Process.UID != identity.UID || started.Process.PID == identity.PID) {
				e = fmt.Errorf("Cargo started evidence differs from supervised group/gate")
			}
			if e == nil {
				rec.Started = &started
				rec.Phase = "compiling"
				rec.Revision++
				if l.State != "ending" {
					l.State = "active"
				}
				l.Revision++
				delete(m.startClocks, id)
				e = m.putCargo(l, rec, "cargo.command_started")
			}
			m.mu.Unlock()
			if e != nil {
				fail(e)
				return
			}
		case "finished":
			var finished cargoprofile.HelperFinished
			if e = cargoprofile.DecodeCanonical(packet.Data, &finished); e != nil || finished.Code < 0 || finished.Code > 255 || finished.At.IsZero() {
				fail(fmt.Errorf("Cargo final receipt is malformed"))
				return
			}
			m.mu.Lock()
			current, record, lookupErr := m.cargoWorkerCurrent(id, worker)
			_ = current
			if lookupErr == nil {
				lookupErr = cargoFinishedConsistent(record, finished)
			}
			m.mu.Unlock()
			if lookupErr != nil {
				fail(lookupErr)
				return
			}
			finish = &finished
			if finished.Failure != "" {
				fail(model.Err("cargo_prepare_failed", finished.Failure))
			}
			return
		default:
			fail(fmt.Errorf("Cargo helper returned an unexpected phase"))
			return
		}
	}
}
func recordCargoBirth(rec *cargoRecord, init cargoprofile.HelperInit, birth cargoprofile.BirthStep) error {
	i := len(rec.Births)
	if i >= 8 || birth.Step != i/2+1 || birth.Step < 1 || birth.Step > 4 {
		return fmt.Errorf("Cargo directory birth is out of sequence")
	}
	root := filepath.Join(init.Allocation, "cargo-"+rec.Lease)
	path := root
	if birth.Step > 1 {
		path = filepath.Join(root, []string{"target", "build", "control"}[birth.Step-2])
	}
	if birth.Path != path || (i%2 == 0) != (birth.Born == nil) {
		return fmt.Errorf("Cargo born directory differs from recorded intention")
	}
	if birth.Born != nil && (birth.Born.Inode == 0 || !birth.Born.BirthKnown || birth.Born.Owner != init.AllocationIdentity.Owner || birth.Born.Device != init.AllocationIdentity.Device) {
		return fmt.Errorf("Cargo born identity is outside managed allocation")
	}
	rec.Births = append(rec.Births, birth)
	return nil
}

func cargoPreparedBirthsMatch(rec cargoRecord, p cargoprofile.Prepared) bool {
	if len(rec.Births) != 8 {
		return false
	}
	for i, d := range []cargoprofile.OwnedDirectory{p.Owned.Root, p.Owned.Target, p.Owned.Build, p.Owned.Control} {
		birth := rec.Births[i*2+1]
		if birth.Born == nil || birth.Path != d.Path || *birth.Born != d.Identity {
			return false
		}
	}
	return p.Policy.Path == filepath.Join(p.Owned.Control.Path, "policy.toml") && p.Policy.SHA256 == p.Plan.PolicySHA256
}

func cargoChildEnvironment(l, store model.Lease, home string) []string {
	fixed := map[string]string{"ARM_HOME": home, "ARM_STORAGE_DIR": store.ManagedDir, "ARM_STORAGE_LEASE": store.ID}
	claims, _ := json.Marshal(l.Request.Pools)
	if len(l.Request.Pools) > 0 {
		fixed["ARM_POOLS"] = string(claims)
	} else {
		fixed["ARM_POOLS"] = ""
	}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := fixed[key]; !ok {
			env = append(env, entry)
		}
	}
	for key, value := range fixed {
		env = append(env, key+"="+value)
	}
	return env
}

func cargoFinishedConsistent(rec cargoRecord, finished cargoprofile.HelperFinished) error {
	if rec.Started != nil && !finished.CommandStarted {
		return fmt.Errorf("Cargo final receipt denied its previously recorded command start")
	}
	if finished.CommandStarted && rec.AuthorizedAt == nil {
		return fmt.Errorf("Cargo final receipt claims execution without a compilation grant")
	}
	if finished.Code == 0 && (!finished.CommandStarted || rec.Started == nil) {
		return fmt.Errorf("Cargo success lacks its actual command-start evidence")
	}
	return nil
}
