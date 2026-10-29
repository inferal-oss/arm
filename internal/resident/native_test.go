package resident

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/childowner"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

const fixtureVersion = "resident-test-v1"

// Exec'd helper only: no CLI entry point or default home can be reached here.
func TestResidentExecWorker(t *testing.T) {
	if os.Getenv("ARM_RESIDENT_WORKER_FIXTURE") != "1" {
		t.Skip("owned exec helper only")
	}
	privateFile := os.NewFile(3, "private-fixture-channel")
	private, err := net.FileConn(privateFile)
	_ = privateFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	listenerFile := os.NewFile(4, "private-fixture-listener")
	listener, err := net.FileListener(listenerFile)
	_ = listenerFile.Close()
	if err != nil {
		private.Close()
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err = ServeWorker(ctx, private, listener, fixtureVersion); err != nil {
		t.Fatal(err)
	}
}

type nativeWorker struct {
	t         *testing.T
	a         *Authority
	b         *Binding
	cmd       *exec.Cmd
	owner     *childowner.Owner
	cancel    context.CancelFunc
	result    <-chan childowner.Result
	private   net.Conn
	serveDone <-chan error
	url       string
	output    *bytes.Buffer
	lossCut   *lostResponse
	once      sync.Once
	stopErr   error
}

// Drop one committed response on the original channel, not a fake dispatch.
type lostResponse struct {
	net.Conn
	key     string
	dropped bool
}

func (c *lostResponse) Write(frame []byte) (int, error) {
	if !c.dropped && len(frame) > 4 && int(binary.BigEndian.Uint32(frame[:4])) == len(frame)-4 {
		var reply Reply
		if json.Unmarshal(frame[4:], &reply) == nil && reply.Key == c.key && reply.Outcome == "complete" {
			c.dropped = true
			_ = c.Conn.Close()
			return 0, io.ErrClosedPipe
		}
	}
	return c.Conn.Write(frame)
}

func launchNativeWorker(t *testing.T, a *Authority, n, dropKey string) *nativeWorker {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ExecutableSHA256(exe)
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.Stage(WorkerIdentity{Generation: strings.Repeat(n, 32), SHA256: digest, Version: fixtureVersion}, Principal{Kind: "local-fixture", Owner: a.identity.Owner})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	unix.CloseOnExec(pair[0])
	unix.CloseOnExec(pair[1])
	parentFile := os.NewFile(uintptr(pair[0]), "resident-side")
	childFile := os.NewFile(uintptr(pair[1]), "worker-side")
	private, err := net.FileConn(parentFile)
	_ = parentFile.Close()
	if err != nil {
		childFile.Close()
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		private.Close()
		childFile.Close()
		t.Fatal(err)
	}
	listenerFile, err := listener.(*net.TCPListener).File()
	if err != nil {
		listener.Close()
		private.Close()
		childFile.Close()
		t.Fatal(err)
	}
	url := "http://" + listener.Addr().String() + "/call"
	output := new(bytes.Buffer)
	cmd := exec.Command(exe, "-test.run=^TestResidentExecWorker$", "-test.v", "-test.timeout=25s")
	cmd.Env = append(os.Environ(), "ARM_RESIDENT_WORKER_FIXTURE=1")
	cmd.ExtraFiles = []*os.File{childFile, listenerFile}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	err = cmd.Start()
	_ = childFile.Close()
	_ = listenerFile.Close()
	_ = listener.Close()
	if err != nil {
		private.Close()
		t.Fatal(err)
	}
	owner, err := childowner.New(cmd)
	if err != nil {
		// No bootstrap has been sent; only the fixed trusted helper can exist.
		_ = private.Close()
		waitErr := cmd.Wait()
		t.Fatalf("original worker binding unavailable: %v; wait=%v", err, waitErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan childowner.Result, 1)
	go func() { result <- owner.Wait(ctx) }()
	served := make(chan error, 1)
	serveConn := private
	var lossCut *lostResponse
	if dropKey != "" {
		lossCut = &lostResponse{Conn: private, key: dropKey}
		serveConn = lossCut
	}
	go func() { served <- a.Serve(b, serveConn) }()
	w := &nativeWorker{t: t, a: a, b: b, cmd: cmd, owner: owner, cancel: cancel, result: result, private: private, serveDone: served, url: url, output: output, lossCut: lossCut}
	t.Cleanup(func() {
		if err := w.stop(); err != nil {
			t.Error(err)
		}
	})
	after, err := os.Stat(exe)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		t.Fatal("worker executable changed across Start")
	}
	select {
	case <-b.Ready():
	case <-time.After(6 * time.Second):
		t.Fatalf("worker handshake missing: %s", output.String())
	}
	return w
}

func (w *nativeWorker) stop() error {
	w.once.Do(func() {
		nativeJoined, groupAbsent, transportJoined := false, false, false
		waitStatus := -1
		idle, err := w.a.Seal(w.b)
		w.stopErr = errors.Join(w.stopErr, err)
		if idle != nil {
			select {
			case <-idle:
			case <-time.After(7 * time.Second):
				w.stopErr = errors.Join(w.stopErr, errors.New("resident dispatch join unknown"))
			}
		}
		w.cancel() // Only this original worker; no resident or job cancellation.
		select {
		case result := <-w.result:
			nativeJoined = result.Reaped && result.CleanupError == nil
			if state := w.cmd.ProcessState; state != nil {
				waitStatus = int(state.Sys().(syscall.WaitStatus))
			}
			var exitError *exec.ExitError
			if result.WaitError != nil && !errors.As(result.WaitError, &exitError) {
				w.stopErr = errors.Join(w.stopErr, fmt.Errorf("original worker Wait/I/O failed: %w", result.WaitError))
			}
			if !result.Reaped || result.CleanupError != nil {
				w.stopErr = errors.Join(w.stopErr, fmt.Errorf("original worker cleanup unknown: %v", result.CleanupError))
			}
			groupAbsent = errors.Is(syscall.Kill(-w.cmd.Process.Pid, 0), syscall.ESRCH)
			if !groupAbsent {
				w.stopErr = errors.Join(w.stopErr, errors.New("original worker group not absent after Wait"))
			}
		case <-time.After(6 * time.Second):
			w.stopErr = errors.Join(w.stopErr, errors.New("original worker Wait not joined"))
		}
		_ = w.private.Close()
		select {
		case <-w.serveDone:
			transportJoined = true
		case <-time.After(3 * time.Second):
			w.stopErr = errors.Join(w.stopErr, errors.New("private transport join unknown"))
		}
		// Raw native receipts belong only in the private proof output. Public
		// normalized evidence omits PIDs, lease IDs and native wait statuses.
		w.t.Logf("private_worker_receipt=%s", mustJSON(map[string]any{"pid": w.cmd.Process.Pid, "wait_status": waitStatus, "native_joined": nativeJoined, "group_absent": groupAbsent, "private_transport_joined": transportJoined, "complete": w.stopErr == nil}))
	})
	return w.stopErr
}

func nativeCall(w *nativeWorker, op, key string, params any) (Reply, int, error) {
	raw, err := json.Marshal(ClientRequest{Operation: op, Key: key, Params: mustJSON(params)})
	if err != nil {
		return Reply{}, 0, err
	}
	r, err := http.NewRequest("POST", w.url, bytes.NewReader(raw))
	if err != nil {
		return Reply{}, 0, err
	}
	r.Header.Set("Authorization", "Bearer "+w.b.ClientToken())
	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	defer client.Transport.(*http.Transport).CloseIdleConnections()
	response, err := client.Do(r)
	if err != nil {
		return Reply{}, 0, err
	}
	defer response.Body.Close()
	var reply Reply
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return reply, response.StatusCode, nil
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, MaxResponse+1)).Decode(&reply); err != nil {
		return reply, response.StatusCode, err
	}
	return reply, response.StatusCode, nil
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func activeNativeWorker(t *testing.T, a *Authority, w *nativeWorker) {
	t.Helper()
	if err := a.Activate(w.b); err != nil {
		t.Fatal(err)
	}
	eventuallyNative(t, 3*time.Second, func() bool {
		reply, status, err := nativeCall(w, "status", "", struct{}{})
		return err == nil && status == http.StatusOK && reply.Error == nil
	})
}

func eventuallyNative(t *testing.T, limit time.Duration, condition func() bool) {
	t.Helper()
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("bounded native condition did not complete")
}

func nativeAuthority(t *testing.T) (*Authority, *manager.Manager, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	lock, err := local.Lock(home, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	})
	m, err := manager.OpenConfiguredWithUnixRecovery(home, daemonconfig.Settings{Resources: model.Resources{CPU: .25, Memory: 32 << 20}, Listen: "127.0.0.1:0"}, lock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error("fixture Manager cleanup:", err)
		}
	})
	m.Start()
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{Host: status.Host, Owner: status.Owner, Generation: strings.Repeat("e", 32)}
	dir := filepath.Join(t.TempDir(), "receipts")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := func(_ context.Context, c Call) (json.RawMessage, error) {
		if c.Principal.Owner != status.Owner || c.Principal.Kind != "local-fixture" {
			return nil, failure("principal_changed")
		}
		var result any
		var err error
		switch c.Operation {
		case "status":
			result, err = m.Status()
		case "run":
			var q manager.RunRequest
			if err = decode(c.Params, &q); err != nil {
				return nil, err
			}
			if q.Key != c.Key || q.Profile != "" || q.SSH != nil || q.Queue {
				return nil, failure("fixture_scope")
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				result, err = m.Acquire(q.Request, &q)
				var api *model.Error
				if !errors.As(err, &api) || api.Code != "insufficient_capacity" || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		case "renew":
			var q struct {
				ID  string        `json:"id"`
				TTL time.Duration `json:"ttl"`
			}
			if err = decode(c.Params, &q); err == nil {
				result, err = m.Renew(q.ID, q.TTL)
			}
		case "end":
			var q struct {
				ID string `json:"id"`
			}
			if err = decode(c.Params, &q); err == nil {
				result, err = m.End(q.ID, "cancelled")
			}
		case "run_inspect":
			var q struct {
				ID string `json:"id"`
			}
			if err = decode(c.Params, &q); err == nil {
				result, err = m.InspectRun(q.ID)
			}
		case "run_output":
			var q struct {
				ID     string `json:"id"`
				Offset int64  `json:"offset"`
			}
			if err = decode(c.Params, &q); err == nil {
				result, err = m.RunOutput(q.ID, q.Offset)
			}
		default:
			err = failure("operation_forbidden")
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	a, err := Open(dir, id, dispatch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a, m, home
}

func TestResidentNativeHealthyReplacement(t *testing.T) {
	if os.Getenv("ARM_RESIDENT_NATIVE") != "1" {
		t.Skip("explicit owned native proof required")
	}
	proveNativeContinuity(t, "healthy")
}

func TestResidentNativeLostResponseReplacement(t *testing.T) {
	if os.Getenv("ARM_RESIDENT_NATIVE") != "1" {
		t.Skip("explicit owned native proof required")
	}
	proveNativeContinuity(t, "lost_response")
}

func TestResidentNativeAbruptCrashReplacement(t *testing.T) {
	if os.Getenv("ARM_RESIDENT_NATIVE") != "1" {
		t.Skip("explicit owned native proof required")
	}
	proveNativeContinuity(t, "abrupt_crash")
}

func proveNativeContinuity(t *testing.T, mode string) {
	t.Helper()
	dropResponse := mode == "lost_response"
	a, m, home := nativeAuthority(t)
	residentPID, residentID, originalManager := os.Getpid(), a.identity, m
	dropKey := ""
	if dropResponse {
		dropKey = "native-run"
	}
	worker1 := launchNativeWorker(t, a, "3", dropKey)
	activeNativeWorker(t, a, worker1)
	cwd := t.TempDir()
	finish := filepath.Join(cwd, "finish")
	q := manager.RunRequest{Request: model.Request{Key: "native-run", Reason: "resident worker continuity fixture", Resources: model.Resources{CPU: .01, Memory: 1 << 20}, TTL: 20 * time.Second}, CWD: cwd, Command: []string{"/bin/sh", "-c", `i=0; while [ ! -f "$1" ] && [ "$i" -lt 400 ]; do printf 'tick:%s\n' "$i"; i=$((i+1)); sleep 0.05; done; printf 'done\n'; exit 7`, "fixture", finish}}
	reply, status, err := nativeCall(worker1, "run", q.Key, q)
	if dropResponse && err == nil && status != http.StatusBadGateway {
		t.Fatal("lost-response cut not observed", status, reply)
	}
	if !dropResponse && (err != nil || status != http.StatusOK || reply.Error != nil) {
		t.Fatal("healthy worker run failed", status, err, reply.Error)
	}
	eventuallyNative(t, 2*time.Second, func() bool { s, e := m.Status(); return e == nil && len(s.Leases) == 1 && s.Leases[0].PID > 0 })
	s, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	original := s.Leases[0]
	if original.Process == nil || original.State != "active" {
		t.Fatal("original native owner missing")
	}
	page, err := m.RunOutput(original.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !dropResponse {
		if healthy, code, e := nativeCall(worker1, "status", "", struct{}{}); e != nil || code != http.StatusOK || healthy.Error != nil {
			t.Fatal("old worker not healthy before retirement", e, code)
		}
	}
	if mode == "abrupt_crash" {
		// Signal only this original still-unreaped fixture worker handle.
		// Its childowner waiter, not a stored PID, supplies the native join.
		if err := worker1.owner.Signal(syscall.SIGKILL); err != nil {
			t.Fatal("original worker abrupt-exit control failed", err)
		}
	}
	if err = worker1.stop(); err != nil {
		t.Fatal(err)
	}
	requireResidentLockHeld(t, home)
	if mode == "abrupt_crash" {
		state := worker1.cmd.ProcessState
		if state == nil || !state.Sys().(syscall.WaitStatus).Signaled() || state.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatal("original worker did not witness abrupt SIGKILL exit")
		}
	}
	if dropResponse && (worker1.lossCut == nil || !worker1.lossCut.dropped) {
		t.Fatal("committed lost-response control did not fire")
	}
	// There is now no active worker. The resident producer must still progress.
	startBytes := page.Run.LogBytes
	eventuallyNative(t, time.Second, func() bool { p, e := m.RunOutput(original.ID, 0); return e == nil && p.Run.LogBytes > startBytes })
	current, err := m.Inspect(original.ID)
	if err != nil || current.PID != original.PID || !reflect.DeepEqual(current.Process, original.Process) || current.State != "active" {
		t.Fatal("job custody changed across worker loss", err)
	}
	worker2 := launchNativeWorker(t, a, "4", "")
	activeNativeWorker(t, a, worker2)
	requireResidentLockHeld(t, home)
	if worker1.b.worker.SHA256 != worker2.b.worker.SHA256 {
		t.Fatal("same-binary fixture unexpectedly changed executable")
	}
	if os.Getpid() != residentPID || a.identity != residentID || m != originalManager {
		t.Fatal("resident lifetime replaced")
	}
	reply, status, err = nativeCall(worker2, "run", q.Key, q)
	var replay model.Lease
	if err != nil || status != http.StatusOK || reply.Error != nil || json.Unmarshal(reply.Result, &replay) != nil || replay.ID != original.ID {
		t.Fatal("committed run receipt not recovered", status, err, reply.Error)
	}
	if s, err = m.Status(); err != nil || len(s.Leases) != 1 {
		t.Fatal("duplicate native run", err, len(s.Leases))
	}
	changed := q
	changed.Reason = "changed body"
	conflict, _, err := nativeCall(worker2, "run", q.Key, changed)
	if err != nil || conflict.Error == nil || conflict.Error.Code != "idempotency_conflict" {
		t.Fatal("changed key accepted", conflict.Error, err)
	}
	stale := a.call(worker1.b, unitRequest(a, worker1.b, "run", "stale-effect", string(mustJSON(q))))
	if stale.Error == nil || stale.Error.Code != "stale_epoch" {
		t.Fatal("stale worker caused effect", stale.Error)
	}
	reply, status, err = nativeCall(worker2, "renew", "native-renew", map[string]any{"id": original.ID, "ttl": 60 * time.Second})
	var renewed model.Lease
	if err != nil || status != http.StatusOK || reply.Error != nil || json.Unmarshal(reply.Result, &renewed) != nil || renewed.Expires == nil || original.Expires == nil || !renewed.Expires.After(*original.Expires) {
		t.Fatal("renewal failed", reply.Error, err)
	}
	current, err = m.Inspect(original.ID)
	if err != nil || !reflect.DeepEqual(current.Clock, renewed.Clock) || current.Revision != renewed.Revision {
		t.Fatal("ACKed renewal not durable", err)
	}
	duplicate, _, err := nativeCall(worker2, "renew", "native-renew", map[string]any{"id": original.ID, "ttl": 60 * time.Second})
	var duplicateRenewal model.Lease
	if err != nil || duplicate.Error != nil || json.Unmarshal(duplicate.Result, &duplicateRenewal) != nil || !reflect.DeepEqual(duplicateRenewal.Clock, renewed.Clock) || duplicateRenewal.Revision != renewed.Revision {
		t.Fatal("renewal replay changed original entitlement", err, duplicate.Error)
	}
	current, err = m.Inspect(original.ID)
	if err != nil || current.Revision != renewed.Revision || !reflect.DeepEqual(current.Clock, renewed.Clock) {
		t.Fatal("renewal applied twice", err)
	}
	// Inspect reads the saved lease through the original Manager's database;
	// no second Manager/writer or startup recovery is introduced.
	if err = os.WriteFile(finish, []byte("finish"), 0600); err != nil {
		t.Fatal(err)
	}
	eventuallyNative(t, 4*time.Second, func() bool { l, e := m.Inspect(original.ID); return e == nil && l.State == "terminal" })
	final, err := m.Inspect(original.ID)
	if err != nil || final.ExitCode == nil || *final.ExitCode != 7 || final.Outcome != "failed" || !errors.Is(syscall.Kill(-original.PID, 0), syscall.ESRCH) {
		t.Fatal("original Wait/cleanup not settled", err, final.Outcome)
	}
	firstPage, err := m.RunOutput(original.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(firstPage.Data, page.Data) || !bytes.HasSuffix(firstPage.Data, []byte("done\n")) || firstPage.NextOffset != int64(len(firstPage.Data)) {
		t.Fatal("output prefix/cursor changed")
	}
	tail, err := m.RunOutput(original.ID, page.NextOffset)
	if err != nil || !bytes.Equal(append(append([]byte{}, page.Data...), tail.Data...), firstPage.Data) {
		t.Fatal("output cursor lost/duplicated bytes", err)
	}
	if s, err = m.Status(); err != nil || s.Reserved.CPU != 0 || s.Reserved.Memory != 0 {
		t.Fatal("original charges not settled", err)
	}
	if err = worker2.stop(); err != nil {
		t.Fatal(err)
	}
	t.Logf("private_job_receipt=%s", mustJSON(map[string]any{"resident_pid": residentPID, "lease_id": original.ID, "original_pid": original.PID, "original_pgid": original.PID, "exit_code": *final.ExitCode, "outcome": final.Outcome, "group_absent": true, "terminal_charges_zero": true}))
	t.Logf("same-binary fixture: version=%s sha256=%s generations=2 mode=%s; resident unchanged; replay without duplicate; original job exit=7, group absent, output contiguous, workers positively joined", fixtureVersion, worker1.b.worker.SHA256, mode)
}

func requireResidentLockHeld(t *testing.T, home string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		t.Fatal("original daemon authority lock was released")
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		t.Fatal("authority lock observation unavailable", err)
	}
}

func TestResidentNativeExpiryWhileWorkersUnavailable(t *testing.T) {
	if os.Getenv("ARM_RESIDENT_NATIVE") != "1" {
		t.Skip("explicit owned native proof required")
	}
	a, m, _ := nativeAuthority(t)
	w := launchNativeWorker(t, a, "3", "")
	activeNativeWorker(t, a, w)
	q := manager.RunRequest{Request: model.Request{Key: "expiring-run", Reason: "resident expiry fixture", Resources: model.Resources{CPU: .01, Memory: 1 << 20}, TTL: 1200 * time.Millisecond}, CWD: t.TempDir(), Command: []string{"/bin/sh", "-c", "printf ready; exec sleep 10"}}
	reply, status, err := nativeCall(w, "run", q.Key, q)
	var original model.Lease
	if err != nil || status != http.StatusOK || reply.Error != nil || json.Unmarshal(reply.Result, &original) != nil || original.PID <= 0 {
		t.Fatal("native admission failed", reply.Error, err)
	}
	if err = w.stop(); err != nil {
		t.Fatal(err)
	}
	eventuallyNative(t, 5*time.Second, func() bool { l, e := m.Inspect(original.ID); return e == nil && l.State == "terminal" })
	final, err := m.Inspect(original.ID)
	if err != nil || final.Outcome != "expired" || final.ExitCode == nil || !errors.Is(syscall.Kill(-original.PID, 0), syscall.ESRCH) {
		t.Fatal("resident expiry did not settle original job", err, final.Outcome)
	}
	if s, e := m.Status(); e != nil || s.Reserved.CPU != 0 || s.Reserved.Memory != 0 {
		t.Fatal("expiry charges not settled", e)
	}
	t.Logf("private_job_receipt=%s", mustJSON(map[string]any{"resident_pid": os.Getpid(), "lease_id": original.ID, "original_pid": original.PID, "original_pgid": original.PID, "exit_code": *final.ExitCode, "outcome": final.Outcome, "group_absent": true, "terminal_charges_zero": true}))
	t.Log("worker unavailable: original elapsed expiry, cancellation, native Wait and charge settlement completed under resident Manager")
}
