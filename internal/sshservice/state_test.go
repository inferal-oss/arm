package sshservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
	"golang.org/x/sys/unix"
)

func stateFixture(t *testing.T) (string, Binding) {
	t.Helper()
	home := privateFixture(t)
	native := local.CurrentIdentity()
	if native.Username == "" {
		t.Fatal("native account unavailable")
	}
	b := Binding{strings.Repeat("a", 32), native.UID, native.Username}
	s, e := mesh.Init(home, b.Host, "ssh-source", "127.0.0.1:19317", "127.0.0.1:19317")
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	return home, b
}
func setupState(t *testing.T, home string, b Binding) *State {
	t.Helper()
	r, e := ReviewService(context.Background(), home, b, true, "test explicit SSH service")
	if e != nil {
		t.Fatal(e)
	}
	if e = ApplyService(context.Background(), home, b, r); e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func statePin(t *testing.T, home string, b Binding) Pin {
	t.Helper()
	s := setupState(t, home, b)
	p := Pin{1, b.Host, strings.Repeat("b", 32), strings.Repeat("c", 64), 3, strings.Repeat("d", 64), strings.Repeat("e", 64), "destination", s.PublicKey, time.Now().UTC()}
	review, e := ReviewPin(filepath.Join(home, "mesh"), p)
	if e != nil {
		t.Fatal(e)
	}
	if e = CommitStatePin(context.Background(), home, b, review); e != nil {
		t.Fatal(e)
	}
	return p
}
func sourceFixture(t *testing.T, home string, b Binding, p Pin, key string) SourceIntent {
	t.Helper()
	dir, e := CreateInvocationDirectory(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Remove(dir.Path) })
	worker, e := usage.Capture(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	return SourceIntent{Schema: 1, Source: b.Host, Owner: b.Owner, Request: SourceRequest{Key: key, Pin: p, TargetOwner: "1000", SourceCertificate: s.config.Identity.Fingerprint, PublicKey: s.PublicKey, Reason: "synthetic exact intent", TTL: time.Minute, Resources: model.Resources{CPU: 1}, CWD: "/tmp", Kind: "exec", CommandSHA256: stateHash([]byte("private command never persisted"))}, Worker: worker, Directory: dir, Created: time.Now().UTC()}
}
func terminalReceipt(r SourceIntent) model.SSHLookup {
	now := time.Now().UTC()
	q := r.Request
	request := model.SSHExecutionRequest{Source: r.Source, Target: q.Pin.Target, Pair: q.Pin.Pair, Revision: q.Pin.Revision, Certificate: q.SourceCertificate, Account: q.Pin.Account, Key: q.Key, PublicKeySHA256: publicKeyHash(q.PublicKey), Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, CWD: q.CWD, Kind: q.Kind, CommandSHA256: q.CommandSHA256}
	if q.PTY {
		request.TerminalSHA256 = strings.Repeat("f", 64)
	}
	lease := model.Lease{ID: strings.Repeat("9", 32), Host: q.Pin.Target, Owner: q.TargetOwner, OwnerUsername: q.Pin.Account, State: "terminal", Outcome: "completed", Created: r.Created, Ended: &now, Request: model.Request{Host: request.Target, Peer: r.Source, OriginKey: q.Key, Key: mesh.Key(r.Source, "ssh-exec\x00"+q.Key), Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, PeerCertificate: q.SourceCertificate, PeerGrantRevision: q.Pin.Revision}}
	return model.SSHLookup{Found: true, Operation: &model.SSHOperation{Schema: 1, Request: request, Lease: lease.ID, AdmittedAt: r.Created}, Lease: &lease}
}
func TestStateServiceReviewReadOnlyCASAndBinding(t *testing.T) {
	home, b := stateFixture(t)
	before, _ := os.ReadDir(filepath.Join(home, "mesh"))
	review, e := ReviewService(context.Background(), home, b, true, "reviewed service creation")
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadDir(filepath.Join(home, "mesh"))
	if len(before) != len(after) {
		t.Fatal("review initialized state")
	}
	if _, e = LoadHostKey(filepath.Join(home, "mesh"), b.Host); !os.IsNotExist(e) {
		t.Fatal("review created key", e)
	}
	changed := review
	changed.After.Enabled = false
	if ApplyService(context.Background(), home, b, changed) == nil {
		t.Fatal("mutable review accepted")
	}
	if e = ApplyService(context.Background(), home, b, review); e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(context.Background(), home, b)
	if e != nil || s.Service == nil || !s.Service.Enabled || s.PublicKey != review.After.PublicKey {
		t.Fatal(s, e)
	}
	if ApplyService(context.Background(), home, b, review) == nil {
		t.Fatal("stale service replay accepted")
	}
	disable, e := ReviewService(context.Background(), home, b, false, "stop SSH access")
	if e != nil {
		t.Fatal(e)
	}
	if e = ApplyService(context.Background(), home, b, disable); e != nil {
		t.Fatal(e)
	}
	s, e = ReadState(context.Background(), home, b)
	if e != nil || s.Service.Enabled || s.PublicKey != review.After.PublicKey {
		t.Fatal("disable replaced key", e)
	}
	other := b
	other.Host = strings.Repeat("f", 32)
	if _, e = ReadState(context.Background(), home, other); e == nil {
		t.Fatal("different authority adopted")
	}
}
func TestSourceIntentOnlyExactTerminalReceiptSettles(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "one")
	if e := ReserveSource(context.Background(), home, b, r); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "mesh", intentName(r)))
	if bytes.Contains(raw, []byte("private command never persisted")) {
		t.Fatal("raw command persisted")
	}
	if e := ReserveSource(context.Background(), home, b, r); !errors.Is(e, ErrIntentExists) {
		t.Fatal("duplicate authorizes execution", e)
	}
	if SettleSource(context.Background(), home, b, r, model.SSHLookup{}) == nil {
		t.Fatal("found:false settled")
	}
	receipt := terminalReceipt(r)
	receipt.Lease.State = "active"
	if SettleSource(context.Background(), home, b, r, receipt) == nil {
		t.Fatal("active lease settled")
	}
	receipt = terminalReceipt(r)
	receipt.Operation.Request.CommandSHA256 = strings.Repeat("f", 64)
	if SettleSource(context.Background(), home, b, r, receipt) == nil {
		t.Fatal("different command receipt settled")
	}
	receipt = terminalReceipt(r)
	for _, alter := range []func(*model.SSHLookup){
		func(v *model.SSHLookup) {
			v.Lease.Cargo = &model.CargoBinding{Schema: 1, Adapter: "cargo", State: "retained"}
		},
		func(v *model.SSHLookup) { v.Lease.Host = r.Source },
		func(v *model.SSHLookup) { v.Lease.Request.StorageLease = "unreviewed" },
		func(v *model.SSHLookup) { v.Lease.Request.Queue = true },
		func(v *model.SSHLookup) { v.Operation.AdmittedAt = v.Lease.Created.Add(-time.Second) },
	} {
		v := terminalReceipt(r)
		alter(&v)
		if MatchTerminalReceipt(r, v) == nil {
			t.Fatal("inconsistent receipt or attached obligation settled")
		}
	}
	if SettleSource(context.Background(), home, b, r, receipt) == nil {
		t.Fatal("existing local artifacts discarded")
	}
	if e := os.Remove(r.Directory.Path); e != nil {
		t.Fatal(e)
	}
	if e := SettleSource(context.Background(), home, b, r, receipt); e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(context.Background(), home, b)
	if e != nil || len(s.Intents) != 0 || len(s.Completions) != 1 {
		t.Fatal("receipt not retained", s, e)
	}
	if e = SettleSource(context.Background(), home, b, r, receipt); e != nil {
		t.Fatal("exact settlement continuation failed", e)
	}
	if e = ReserveSource(context.Background(), home, b, r); !errors.Is(e, ErrIntentExists) {
		t.Fatal("completed key reused", e)
	}
}
func TestSourceIntentDeadWorkerPreventsSameTargetReplacement(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "unknown")
	if e := ReserveSource(context.Background(), home, b, r); e != nil {
		t.Fatal(e)
	}
	r.Worker.Start += "-changed"
	raw, _ := json.Marshal(r)
	path := filepath.Join(home, "mesh", intentName(r))
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	next := sourceFixture(t, home, b, pin, "replacement")
	if e := ReserveSource(context.Background(), home, b, next); !errors.Is(e, ErrTargetUncertain) {
		t.Fatal("unknown bypassed", e)
	}
	now, _ := os.ReadFile(path)
	if !bytes.Equal(now, raw) {
		t.Fatal("unknown overwritten")
	}
}
func TestStateProducerRejectsNestedAliasesNullAndExactIntegers(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "closed")
	r.Request.Resources.Memory = math.MaxInt64
	invalid := r
	for _, uid := range []string{"not-a-uid", "01", "4294967296", "-1", "+1"} {
		invalid.Request.TargetOwner = uid
		if invalid.Validate() == nil {
			t.Fatal("noncanonical/non32bit owner accepted", uid)
		}
	}
	invalid = r
	invalid.Worker.PID = math.MaxInt32 + 1
	if invalid.Validate() == nil {
		t.Fatal("out-of-range native PID accepted")
	}
	raw, _ := json.Marshal(r)
	var got SourceIntent
	if e := DecodeStateRecord(raw, &got); e != nil || got.Request.Resources.Memory != math.MaxInt64 {
		t.Fatal("integer lost", e)
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"pty":false`), []byte(`"PTY":false`), 1), bytes.Replace(raw, []byte(`"pty":false`), []byte(`"pty":null`), 1), bytes.Replace(raw, []byte(`"pty":false`), []byte(`"pty":false,"pty":false`), 1), bytes.Replace(raw, []byte(`"target_owner":"1000"`), []byte(`"target_owner":null`), 1), append(append([]byte{}, raw...), []byte(" ")...)} {
		if bytes.Equal(bad, raw) {
			t.Fatal("bad fixture unchanged")
		}
		if DecodeStateRecord(bad, &got) == nil {
			t.Fatal("permissive record accepted")
		}
	}
}
func TestStatePrivatePathsFenceAndCancellation(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public", "executable", "special", "lock-executable", "lock-special", "directory-special", "changed-account", "unknown", "fence"} {
		t.Run(kind, func(t *testing.T) {
			home, b := stateFixture(t)
			s := setupState(t, home, b)
			path := filepath.Join(home, "mesh", ServiceFile)
			checkMutation := func(operation string, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("fixture %s failed: %v", operation, err)
				}
			}
			setMode := func(path string, mode os.FileMode) {
				t.Helper()
				checkMutation("chmod", os.Chmod(path, mode))
				info, err := os.Lstat(path)
				checkMutation("stat mode", err)
				const fixtureModeBits = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
				if got := info.Mode() & fixtureModeBits; got != mode {
					t.Fatalf("fixture %s mode mismatch: got %v, want %v", kind, got, mode)
				}
			}
			switch kind {
			case "symlink":
				checkMutation("rename", os.Rename(path, path+".old"))
				checkMutation("symlink", os.Symlink(path+".old", path))
			case "hardlink":
				checkMutation("hardlink", os.Link(path, path+".copy"))
			case "public":
				setMode(path, 0644)
			case "executable":
				setMode(path, 0700)
			case "special":
				setMode(path, 0600|os.ModeSetuid)
			case "lock-executable":
				setMode(filepath.Join(home, "mesh", StateLockName), 0700)
			case "lock-special":
				lockPath := filepath.Join(home, "mesh", StateLockName)
				// A private temp directory may inherit a group other than our effective group.
				// Set our own group so the kernel can retain the intended SGID fixture bit.
				checkMutation("chown effective group", os.Chown(lockPath, -1, os.Getegid()))
				var lockStat unix.Stat_t
				checkMutation("stat effective group", unix.Lstat(lockPath, &lockStat))
				if lockStat.Gid != uint32(os.Getegid()) {
					t.Fatal("fixture lock effective group mismatch")
				}
				setMode(lockPath, 0600|os.ModeSetgid)
			case "directory-special":
				setMode(filepath.Join(home, "mesh"), 0700|os.ModeSticky)
			case "changed-account":
				s.Service.Binding.Account = "changed-account"
				raw, err := json.Marshal(s.Service)
				checkMutation("marshal changed account", err)
				checkMutation("write changed account", os.WriteFile(path, raw, 0600))
			case "unknown":
				checkMutation("write interrupted marker", os.WriteFile(filepath.Join(home, "mesh", ".ssh-write-interrupted"), []byte("evidence"), 0600))
			case "fence":
				checkMutation("write reset fence", os.WriteFile(filepath.Join(home, local.ResetFenceName), []byte("{}"), 0600))
			}
			if _, e := ReadState(context.Background(), home, b); e == nil {
				t.Fatalf("unsafe state accepted (%s)", kind)
			}
			if kind == "changed-account" {
				if _, e := ReviewService(context.Background(), home, b, true, "new service review"); e == nil {
					t.Fatal("changed account silently reviewed as current")
				}
				if e := ApplyService(context.Background(), home, b, ServiceReview{}); e == nil {
					t.Fatal("changed account allowed mutation")
				}
			}
		})
	}
	home, b := stateFixture(t)
	review, e := ReviewService(context.Background(), home, b, true, "cancelled setup")
	if e != nil {
		t.Fatal(e)
	}
	lock, e := os.OpenFile(filepath.Join(home, "mesh-lifecycle.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil {
			t.Fatalf("fixture cancellation lock close failed: %v", err)
		}
	})
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	e = ApplyService(ctx, home, b, review)
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatal("held lock ignored cancellation", e)
	}
	if _, e = os.Stat(filepath.Join(home, "mesh", hostKeyFile)); !os.IsNotExist(e) {
		t.Fatal("cancelled setup wrote key")
	}
}
func TestSSHStateProcessWorker(t *testing.T) {
	if os.Getenv("ARM_SSH_STATE_WORKER") != "1" {
		return
	}
	home := os.Getenv("ARM_SSH_STATE_HOME")
	var b Binding
	var q SourceRequest
	if json.Unmarshal([]byte(os.Getenv("ARM_SSH_STATE_BINDING")), &b) != nil || json.Unmarshal([]byte(os.Getenv("ARM_SSH_STATE_REQUEST")), &q) != nil {
		t.Fatal("worker input")
	}
	dir, e := CreateInvocationDirectory(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	worker, e := usage.Capture(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	r := SourceIntent{1, b.Host, b.Owner, q, worker, dir, time.Now().UTC()}
	if e = ReserveSource(context.Background(), home, b, r); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(r)
	os.Stdout.Write(append(raw, '\n'))
	var signal [1]byte
	_, _ = os.Stdin.Read(signal[:])
}
func TestSourceIntentConcurrentProcessesAndRestartRecovery(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	base := sourceFixture(t, home, b, pin, "process")
	binding, _ := json.Marshal(b)
	type worker struct {
		cmd    *exec.Cmd
		input  ioWriteCloser
		record SourceIntent
	}
	workers := []worker{}
	defer func() {
		for _, w := range workers {
			w.input.Close()
			if w.cmd.ProcessState == nil {
				w.cmd.Process.Kill()
				w.cmd.Wait()
			}
			os.Remove(w.record.Directory.Path)
		}
	}()
	for i := 0; i < 2; i++ {
		q := base.Request
		q.Key = "process-" + strconv.Itoa(i)
		raw, _ := json.Marshal(q)
		cmd := exec.Command(os.Args[0], "-test.run=^TestSSHStateProcessWorker$")
		cmd.Env = append(os.Environ(), "ARM_SSH_STATE_WORKER=1", "ARM_SSH_STATE_HOME="+home, "ARM_SSH_STATE_BINDING="+string(binding), "ARM_SSH_STATE_REQUEST="+string(raw))
		input, e := cmd.StdinPipe()
		if e != nil {
			t.Fatal(e)
		}
		output, e := cmd.StdoutPipe()
		if e != nil {
			t.Fatal(e)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		scanner := bufio.NewScanner(output)
		if !scanner.Scan() {
			t.Fatal("worker did not reserve", stderr.String())
		}
		var r SourceIntent
		if e = json.Unmarshal(scanner.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		workers = append(workers, worker{cmd, input, r})
	}
	s, e := ReadState(context.Background(), home, b)
	if e != nil || len(s.Intents) != 2 {
		t.Fatal("independent intents lost", e)
	}
	for i := range workers {
		workers[i].input.Close()
		if e = workers[i].cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	reopened, e := ReadState(context.Background(), home, b)
	if e != nil || !reflect.DeepEqual(s.Intents, reopened.Intents) {
		t.Fatal("restart rewrote/replayed intentions", e)
	}
	next := sourceFixture(t, home, b, pin, "after-crash")
	if e = ReserveSource(context.Background(), home, b, next); !errors.Is(e, ErrTargetUncertain) {
		t.Fatal("dead source replaced without lookup", e)
	}
}

type ioWriteCloser interface {
	Write([]byte) (int, error)
	Close() error
}

func TestSourcePublicationFailuresPreserveHonestOutcomeAndInPlaceCAS(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "durability")
	raw, _ := json.Marshal(r)
	s, e := ReadState(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	d, e := privateDirectory(filepath.Join(home, "mesh"))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	fault := errors.New("injected durable I/O failure")
	s.syncFile = func(*os.File) error { return fault }
	if e = atomicStateFile(context.Background(), s, d, intentName(r), raw, nil); !errors.Is(e, fault) {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(home, "mesh", intentName(r))); !os.IsNotExist(e) {
		t.Fatal("prepublication failure left a dispatched-looking record", e)
	}
	s.syncFile = nil
	s.syncDirectory = func(*os.File) error { return fault }
	if e = atomicStateFile(context.Background(), s, d, intentName(r), raw, nil); !errors.Is(e, fault) {
		t.Fatal(e)
	}
	saved, e := os.ReadFile(filepath.Join(home, "mesh", intentName(r)))
	if e != nil || !bytes.Equal(saved, raw) {
		t.Fatal("published uncertain intention lost", e)
	}
	s.syncDirectory = nil
	s, e = ReadState(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	expected := s.files[intentName(r)]
	changed := r
	changed.Request.Reason = "changed in place"
	different, _ := json.Marshal(changed)
	if e = os.WriteFile(filepath.Join(home, "mesh", intentName(r)), different, 0600); e != nil {
		t.Fatal(e)
	}
	if atomicStateFile(context.Background(), s, d, intentName(r), raw, &expected) == nil {
		t.Fatal("in-place changed bytes overwritten")
	}
	now, _ := os.ReadFile(filepath.Join(home, "mesh", intentName(r)))
	if !bytes.Equal(now, different) {
		t.Fatal("stale CAS damaged changed bytes")
	}
}
func TestSourceSettlementContinuationRetainsExactReceipt(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "settlement-crash")
	if e := ReserveSource(context.Background(), home, b, r); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(r.Directory.Path); e != nil {
		t.Fatal(e)
	}
	receipt := terminalReceipt(r)
	completion := SourceCompletion{1, r, receipt, time.Now().UTC()}
	raw, _ := json.Marshal(completion)
	if e := os.WriteFile(filepath.Join(home, "mesh", completionName(r)), raw, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(context.Background(), home, b)
	if e != nil || len(s.Intents) != 1 || len(s.Completions) != 1 {
		t.Fatal("paired recovery evidence rejected", e)
	}
	if e = SettleSource(context.Background(), home, b, r, receipt); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(home, "mesh", completionName(r)))
	if !bytes.Equal(after, raw) {
		t.Fatal("settlement rewrote original receipt")
	}
	s, e = ReadState(context.Background(), home, b)
	if e != nil || len(s.Intents) != 0 || len(s.Completions) != 1 {
		t.Fatal("exact continuation failed", e)
	}
}
func TestStateNamedLockReplacementDoesNotPublish(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "lock-race")
	e := withStateMutation(context.Background(), home, b, true, func(s *State, d *os.File) error {
		path := filepath.Join(home, "mesh", StateLockName)
		if e := os.Rename(path, path+".old"); e != nil {
			return e
		}
		if e := os.WriteFile(path, nil, 0600); e != nil {
			return e
		}
		raw, _ := json.Marshal(r)
		return atomicStateFile(context.Background(), s, d, intentName(r), raw, nil)
	})
	if e == nil {
		t.Fatal("replaced lock authorized publication")
	}
	if _, e = os.Lstat(filepath.Join(home, "mesh", intentName(r))); !os.IsNotExist(e) {
		t.Fatal("intent published under changed lock", e)
	}
}

func TestSourceIntentCompletedRosterRefusesBeforeNewReservation(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "new-after-full-history")
	// Completed history is retained. Filling its declared bound must stop a new
	// invocation before dispatch instead of admitting work that cannot settle.
	for i := 0; i < MaxCompletions; i++ {
		old := r
		old.Request.Key = "completed-" + strconv.Itoa(i)
		receipt := terminalReceipt(old)
		c := SourceCompletion{1, old, receipt, time.Now().UTC()}
		raw, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(home, "mesh", completionName(old)), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := ReserveSource(context.Background(), home, b, r); e == nil {
		t.Fatal("full completed history admitted a new intention")
	}
	s, e := ReadState(context.Background(), home, b)
	if e != nil || len(s.Intents) != 0 || len(s.Completions) != MaxCompletions {
		t.Fatal("full history changed or intent published", e)
	}
}

func TestSourceSettlementSeparatesAuthorityClocks(t *testing.T) {
	for _, shift := range []time.Duration{-5 * time.Minute, 5 * time.Minute} {
		t.Run(shift.String(), func(t *testing.T) {
			home, b := stateFixture(t)
			pin := statePin(t, home, b)
			intent := sourceFixture(t, home, b, pin, "skewed-destination")
			if e := ReserveSource(context.Background(), home, b, intent); e != nil {
				t.Fatal(e)
			}
			receipt := terminalReceipt(intent)
			receipt.Lease.Created = receipt.Lease.Created.Add(shift)
			receipt.Operation.AdmittedAt = receipt.Operation.AdmittedAt.Add(shift)
			end := receipt.Lease.Ended.Add(shift)
			receipt.Lease.Ended = &end
			if e := MatchTerminalReceipt(intent, receipt); e != nil {
				t.Fatal("valid internally ordered remote clock rejected", e)
			}
			wrong := terminalReceipt(intent)
			wrong.Operation.Request.Reason = "different request"
			if e := MatchTerminalReceipt(intent, wrong); e == nil {
				t.Fatal("request mismatch accepted")
			}
			if e := os.Remove(intent.Directory.Path); e != nil {
				t.Fatal(e)
			}
			if e := SettleSource(context.Background(), home, b, intent, receipt); e != nil {
				t.Fatal("clock skew prevented exact settlement", e)
			}
			state, e := ReadState(context.Background(), home, b)
			if e != nil || len(state.Intents) != 0 || len(state.Completions) != 1 {
				t.Fatal("skewed completion not durably inspectable", e)
			}
			bad := state.Completions[0]
			bad.At = intent.Created.Add(-time.Second)
			if e := bad.Validate(); e == nil {
				t.Fatal("source-local reversed chronology accepted")
			}
			bad = state.Completions[0]
			bad.Receipt.Operation.AdmittedAt = receipt.Lease.Created.Add(-time.Second)
			if e := bad.Validate(); e == nil {
				t.Fatal("destination-local reversed chronology accepted")
			}
		})
	}
}

func TestSourceReservationReservesFutureCompletionSlots(t *testing.T) {
	for _, paired := range []bool{false, true} {
		t.Run(strconv.FormatBool(paired), func(t *testing.T) {
			home, b := stateFixture(t)
			pin := statePin(t, home, b)
			next := sourceFixture(t, home, b, pin, "next-live")
			for i := 0; i < MaxCompletions-1; i++ {
				old := next
				old.Request.Key = "completed-" + strconv.Itoa(i)
				c := SourceCompletion{1, old, terminalReceipt(old), time.Now().UTC()}
				raw, e := json.Marshal(c)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(home, "mesh", completionName(old)), raw, 0600); e != nil {
					t.Fatal(e)
				}
				if paired && i == 0 {
					raw, e = json.Marshal(old)
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(filepath.Join(home, "mesh", intentName(old)), raw, 0600); e != nil {
						t.Fatal(e)
					}
				}
			}
			if e := ReserveSource(context.Background(), home, b, next); e != nil {
				t.Fatal("63 unique completed keys must leave one slot, interrupted pair counts once", e)
			}
			another := sourceFixture(t, home, b, pin, "must-not-dispatch")
			if e := ReserveSource(context.Background(), home, b, another); e == nil {
				t.Fatal("overbooked future completion slot")
			}
			if _, e := os.Lstat(filepath.Join(home, "mesh", intentName(another))); !os.IsNotExist(e) {
				t.Fatal("refused invocation still persisted", e)
			}
			state, e := ReadState(context.Background(), home, b)
			wantPending := 1
			if paired {
				wantPending++
			}
			if e != nil || len(state.Completions) != MaxCompletions-1 || len(state.Intents) != wantPending {
				t.Fatal("slot refusal lost or rewrote retained evidence", e)
			}
		})
	}
}
