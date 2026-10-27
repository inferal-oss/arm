//go:build linux

package manager

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/childowner"
	"getarm.org/internal/model"
	"getarm.org/internal/usage"
)

const recoveryCrashRoot = "TestLeaseClockNativeManagerOwnerCrashRetainsRelayJoin"
const recoveryCrashPrefix = "ARM_NATIVE_RECOVERY_CRASH_"

type recoveryCrashGuard struct {
	Nonce        string `json:"nonce"`
	Home         string `json:"home"`
	Parent       int    `json:"parent"`
	ParentDevice uint64 `json:"parent_device"`
	ParentInode  uint64 `json:"parent_inode"`
}

type recoveryCrashReceipt struct {
	Nonce        string          `json:"nonce"`
	Lease        model.Lease     `json:"lease"`
	Reserved     model.Resources `json:"reserved"`
	CopyEntered  bool            `json:"copy_entered"`
	OriginalOpen bool            `json:"original_open"`
}

// Refuse every preexisting home, including a symlink, before opening any
// backend. The parent is an owned real 0700 directory whose identity is bound
// into the private guard; recheck it immediately before marker and Open.
func recoveryCrashPrivateHome(home string) (*syscall.Stat_t, error) {
	parent, err := os.Lstat(filepath.Dir(home))
	if err != nil {
		return nil, err
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !parent.IsDir() || parent.Mode().Perm() != 0700 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("crash fixture parent must be an owned real 0700 directory")
	}
	if _, err = os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("crash fixture refuses every existing home")
	}
	return stat, nil
}

func recoveryCrashRequest(nonce string) model.Request {
	return model.Request{Key: "native-relay-crash-" + nonce, Reason: "genuine Manager-owner crash with lost original pipe join", Resources: model.Resources{CPU: .01, Memory: 64}, TTL: time.Second}
}

// This dispatch is part of the selected root, not a second passing helper test.
// The helper must be killed through the parent's retained original child owner;
// it never closes its Manager and never turns cancellation into a join witness.
func recoveryCrashChild(t *testing.T) {
	t.Helper()
	nonce := os.Getenv(recoveryCrashPrefix + "NONCE")
	home := os.Getenv(recoveryCrashPrefix + "HOME")
	parent, err := strconv.Atoi(os.Getenv(recoveryCrashPrefix + "PARENT"))
	decoded, hexErr := hex.DecodeString(nonce)
	if err != nil || parent <= 1 || os.Getppid() != parent || hexErr != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != nonce || !filepath.IsAbs(home) || filepath.Clean(home) != home || filepath.Base(home) != "arm" {
		t.Fatal("invalid private crash-helper dispatch")
	}
	parentStat, err := recoveryCrashPrivateHome(home)
	if err != nil {
		t.Fatal("unsafe private crash-helper home", err)
	}
	guardPath := filepath.Join(filepath.Dir(home), "crash-helper.json")
	info, err := os.Lstat(guardPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("private crash-helper guard unavailable", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || info.Size() > 4096 {
		t.Fatal("private crash-helper guard binding differs")
	}
	data, err := os.ReadFile(guardPath)
	if err != nil {
		t.Fatal(err)
	}
	var guard recoveryCrashGuard
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&guard); err != nil || guard != (recoveryCrashGuard{Nonce: nonce, Home: home, Parent: parent, ParentDevice: uint64(parentStat.Dev), ParentInode: uint64(parentStat.Ino)}) {
		t.Fatal("private crash-helper nonce/home/parent differ", err)
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatal("private crash-helper guard has trailing data", err)
	}
	stdin, err := os.Stdin.Stat()
	if err != nil || stdin.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("original anonymous pipe is required", err)
	}
	currentParent, err := recoveryCrashPrivateHome(home)
	if err != nil || uint64(currentParent.Dev) != guard.ParentDevice || uint64(currentParent.Ino) != guard.ParentInode {
		t.Fatal("private crash-helper parent changed before marker", err)
	}
	marker, err := os.OpenFile(filepath.Join(filepath.Dir(home), "helper-launch"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("crash helper launched more than once", err)
	}
	if _, err = marker.WriteString(nonce + "\n"); err != nil {
		marker.Close()
		t.Fatal(err)
	}
	if err = marker.Close(); err != nil {
		t.Fatal(err)
	}
	currentParent, err = recoveryCrashPrivateHome(home)
	if err != nil || uint64(currentParent.Dev) != guard.ParentDevice || uint64(currentParent.Ino) != guard.ParentInode {
		t.Fatal("private crash-helper parent/home changed before Open", err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 1024})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately no deferred Close: this fixture must exercise a real owner
	// crash, not the normal cancellation/settlement path.
	q := recoveryCrashRequest(nonce)
	awaitNativeFixtureHeadroom(t, m, q.Resources)
	joined := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		close(entered)
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(joined) // only after the ACTUAL original anonymous pipe read returns
	}()
	<-entered
	h, err := m.AcquireRelayTransit(t.Context(), q, joined)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.CheckActive(t.Context()); err != nil {
		t.Fatal("fresh original relay authority not live", err)
	}
	l, err := h.Lease()
	if err != nil || l.State != "active" || l.Clock == nil || l.Request.Key != q.Key {
		t.Fatal(l, err)
	}
	select {
	case <-joined:
		t.Fatal("real original pipe joined before crash")
	default:
	}
	s, err := m.Status()
	if err != nil || s.Reserved != q.Resources {
		t.Fatal("fresh original relay was not charged", s.Reserved, err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(recoveryCrashReceipt{Nonce: nonce, Lease: l, Reserved: s.Reserved, CopyEntered: true, OriginalOpen: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
		t.Fatal("original pipe returned instead of Manager-owner crash")
	case <-t.Context().Done():
		t.Fatal("Manager-owner helper reached its bound instead of original crash", t.Context().Err())
	}
}

func TestLeaseClockNativeManagerOwnerCrashRetainsRelayJoin(t *testing.T) {
	if os.Getenv(recoveryCrashPrefix+"NONCE") != "" {
		recoveryCrashChild(t)
		return
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "arm")
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	parentStat, err := recoveryCrashPrivateHome(home)
	if err != nil {
		t.Fatal("unsafe parent crash fixture directory", err)
	}
	guard := recoveryCrashGuard{Nonce: nonce, Home: home, Parent: os.Getpid(), ParentDevice: uint64(parentStat.Dev), ParentInode: uint64(parentStat.Ino)}
	data, err := json.Marshal(guard)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "crash-helper.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	// This separate original sentinel is never owned by either Manager. Its
	// original FIFO/Wait fixture detects collateral TERM/KILL by requiring a
	// later normal exit from its own still-live input, with no replacement Wait.
	sentinelPID, finishSentinel := endingFailureForeignGroup(t)
	sentinel, err := usage.Capture(sentinelPID)
	if err != nil || sentinel.PID != sentinelPID || sentinel.PGID != sentinelPID {
		t.Fatal("independent original sentinel unavailable", sentinel, err)
	}
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close() // remains open across crash/recovery: no synthetic EOF join
	output, receiptWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	defer receiptWriter.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^"+recoveryCrashRoot+"$", "-test.timeout=30s", "-test.parallel=1")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, recoveryCrashPrefix) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, recoveryCrashPrefix+"NONCE="+nonce, recoveryCrashPrefix+"HOME="+home, recoveryCrashPrefix+"PARENT="+strconv.Itoa(os.Getpid()))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, receiptWriter, receiptWriter
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = childowner.Available(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	owner, err := childowner.New(cmd)
	if err != nil {
		endingFailureUnknownFixtures.Lock()
		endingFailureUnknownFixtures.commands = append(endingFailureUnknownFixtures.commands, cmd)
		endingFailureUnknownFixtures.Unlock()
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = owner.Signal(syscall.SIGKILL)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			result := owner.Wait(ctx)
			cancel()
			if !result.Reaped || result.CleanupError != nil {
				recoveryNativeRetainUnjoined(owner, cmd)
				t.Error("original crash-helper cleanup did not join", result)
			}
		}
	})
	if err = input.Close(); err != nil {
		t.Fatal(err)
	}
	if err = receiptWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err = output.SetReadDeadline(time.Now().Add(12 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(io.LimitReader(output, 16<<10))
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal("original Manager-owner did not emit bounded ready receipt", err)
	}
	var receipt recoveryCrashReceipt
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&receipt); err != nil {
		t.Fatal("invalid original Manager-owner ready receipt", err, string(line))
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatal("original Manager-owner receipt has trailing data", err)
	}
	q := recoveryCrashRequest(nonce)
	l := receipt.Lease
	if receipt.Nonce != nonce || !receipt.CopyEntered || !receipt.OriginalOpen || receipt.Reserved != q.Resources || l.ID == "" || l.State != "active" || l.Ended != nil || l.Request.Key != q.Key || l.Request.Kind != relayTransitKind || l.Request.Resources != q.Resources || l.Clock == nil || l.Expires == nil || !owner.Active() || cmd.Process.Pid == sentinelPID {
		t.Fatal("original live/unjoined Manager-owner receipt differs", receipt)
	}
	remaining, err := l.Clock.Remaining(recoveryNativeSample(t), l.Expires)
	if err != nil || remaining <= 0 {
		t.Fatal("original relay was not natively live at ready receipt", remaining, err)
	}
	recoveryNativeAwaitExpiry(t, l)
	// SIGKILL is applied ONLY through the exact retained fresh helper owner.
	// There is no Manager.Close, saved-PID signal, host service or power action.
	if err = owner.Signal(syscall.SIGKILL); err != nil {
		t.Fatal("original Manager owner was not live at intentional crash", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	result := owner.Wait(ctx) // sole Wait on original Manager-owner process
	waited = true
	cancel()
	if !result.Reaped {
		recoveryNativeRetainUnjoined(owner, cmd)
	}
	var exit *exec.ExitError
	if !result.Reaped || !errors.As(result.WaitError, &exit) || result.CleanupError != nil || cmd.ProcessState == nil || owner.Active() {
		t.Fatal("intentional Manager-owner crash did not positively join", result, cmd.ProcessState)
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("Manager owner exited through something other than original SIGKILL", cmd.ProcessState)
	}
	if !errors.Is(syscall.Kill(cmd.Process.Pid, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH) {
		t.Fatal("original crashed Manager PID/group remained after sole Wait")
	}
	if err = output.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	tail, err := io.ReadAll(reader)
	if err != nil || len(tail) != 0 {
		t.Fatal("crashed helper emitted unexpected test/race/termination output", err, string(tail))
	}
	// No owner can write a Go PASS after that positive SIGKILL/Wait result.
	// The retained parent writer is deliberately still open; process death,
	// not a fabricated EOF/join receipt, lost the original relay capability.
	for generation := 0; generation < 2; generation++ {
		m, err := Open(home, model.Resources{CPU: 1, Memory: 1024})
		if err != nil {
			t.Fatal(err)
		}
		closed := false
		closeManager := func() {
			t.Helper()
			if !closed {
				closed = true
				if err := m.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
		t.Cleanup(closeManager)
		m.tick()
		got := clockGet(t, m, l.ID)
		if got.State != "needs_attention" || got.Outcome != "relay_supervision_lost" || got.Ended != nil || got.PID != 0 || got.Process != nil || got.Request.Key != q.Key {
			t.Fatal("crashed original relay join was treated as settlement", got)
		}
		recoveryNativeUnchanged(t, l, got)
		m.mu.Lock()
		adopted := len(m.relayTransits) != 0 || len(m.jobs) != 0 || len(m.sshTransports) != 0
		m.mu.Unlock()
		if adopted {
			t.Fatal("crash recovery reconstructed original runtime capability")
		}
		_, err = m.Renew(l.ID, time.Minute)
		recoveryNativeError(t, err, "invalid_request")
		_, err = m.End(l.ID, "cancelled")
		recoveryNativeError(t, err, "needs_attention")
		_, err = m.Reconcile(l.ID)
		recoveryNativeError(t, err, "needs_attention")
		fakeJoined := make(chan struct{})
		close(fakeJoined)
		fake := &RelayTransit{manager: m, id: l.ID, key: q.Key, joined: fakeJoined, result: got}
		_, err = fake.FinishAfterJoin()
		recoveryNativeError(t, err, "relay_owner_changed")
		// The caller's request has no internal Kind: this must reach the real
		// recorded-key fence, not pass due to unrelated request validation.
		if q.Kind != "" {
			t.Fatal("crash fixture supplied a forbidden public marker")
		}
		_, err = m.AcquireRelayTransit(t.Context(), q, make(chan struct{}))
		recoveryNativeError(t, err, "idempotency_conflict")
		recoveryNativeUnchanged(t, l, clockGet(t, m, l.ID))
		recoveryNativeCharge(t, m, q.Resources)
		var physicalKey string
		if err = m.db.QueryRow(`SELECT request_key FROM leases WHERE id=?`, l.ID).Scan(&physicalKey); err != nil || physicalKey != q.Key {
			t.Fatal("crash recovery changed original operation key", physicalKey, err)
		}
		ls, err := m.list()
		if err != nil || len(ls) != 1 || ls[0].ID != l.ID {
			t.Fatal("crash recovery replayed admission", ls, err)
		}
		closeManager()
	}
	if data, err := os.ReadFile(filepath.Join(dir, "helper-launch")); err != nil || string(data) != nonce+"\n" {
		t.Fatal("Manager-owner launch marker changed", string(data), err)
	}
	current, err := usage.Capture(sentinelPID)
	if err != nil || !reflect.DeepEqual(current, sentinel) {
		t.Fatal("Manager-owner crash disturbed unrelated original sentinel", current, err)
	}
	finishSentinel() // original handle, positive normal exit and sole Wait
	if !errors.Is(syscall.Kill(sentinelPID, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-sentinelPID, 0), syscall.ESRCH) {
		t.Fatal("unrelated original sentinel remained after its positive join")
	}
	t.Log("genuine Manager-owner crash retained lost relay join across two recovery opens")
}
