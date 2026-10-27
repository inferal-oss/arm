//go:build linux && amd64

package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inferal-oss/arm/internal/leaseclock"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const suspendGuestRun = "/run/arm-suspend-guest"

// The fresh read-only CIDATA media binds one binary and one disposable guest.
// The root-only runtime copy additionally binds the actual current kernel boot.
// Neither an environment opt-in nor a saved PID can authorize a power write.
type suspendGuestBinding struct {
	Nonce        string `json:"nonce"`
	UUID         string `json:"uuid"`
	DiskSerial   string `json:"disk_serial"`
	BinarySHA256 string `json:"binary_sha256"`
	BootID       string `json:"boot_id,omitempty"`
}

func suspendGuestHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && len(s) == 64 && s == strings.ToLower(s)
}

func suspendGuestInputs(getenv func(string) string) (suspendGuestBinding, error) {
	b := suspendGuestBinding{Nonce: getenv("ARM_SUSPEND_NONCE"), UUID: getenv("ARM_SUSPEND_UUID"), DiskSerial: getenv("ARM_SUSPEND_DISK_SERIAL"), BinarySHA256: getenv("ARM_SUSPEND_BINARY_SHA256")}
	id, err := uuid.Parse(b.UUID)
	if getenv("ARM_DISPOSABLE_SUSPEND_GUEST") != "1" || !suspendGuestHex(b.Nonce) || !suspendGuestHex(b.BinarySHA256) || err != nil || id.String() != b.UUID {
		return b, errors.New("complete explicit disposable-guest binding required")
	}
	if b.DiskSerial != "arm-"+b.Nonce[:16] {
		return b, errors.New("fresh disposable root-disk serial differs")
	}
	return b, nil
}

func suspendGuestRead(path string, bound int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, bound+1))
	if err != nil || int64(len(b)) > bound {
		return nil, errors.New("guest identity input exceeds bound or cannot be read")
	}
	return b, nil
}

func suspendGuestText(path string) (string, error) {
	b, err := suspendGuestRead(path, 4096)
	return strings.TrimSpace(string(b)), err
}

func suspendGuestBindingFile(directory, name string, runtimeMarker bool) (suspendGuestBinding, error) {
	var b suspendGuestBinding
	d, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return b, err
	}
	defer unix.Close(d)
	var st unix.Stat_t
	if err = unix.Fstat(d, &st); err != nil || st.Uid != 0 || runtimeMarker && st.Mode&0777 != 0700 {
		return b, errors.New("guest binding directory ownership/mode differs")
	}
	if !runtimeMarker {
		var fs unix.Statfs_t
		if err = unix.Fstatfs(d, &fs); err != nil || fs.Type != 0x9660 || fs.Flags&unix.ST_RDONLY == 0 {
			return b, errors.New("binding media is not read-only iso9660 CIDATA")
		}
	}
	fd, err := unix.Openat(d, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return b, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Nlink != 1 || st.Size <= 0 || st.Size > 4096 || runtimeMarker && st.Mode&0777 != 0400 {
		return b, errors.New("guest binding must be one owned bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 4097))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&b); err != nil {
		return b, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return b, errors.New("guest binding contains trailing data")
	}
	return b, nil
}

// Resolve the actual mounted root's block device, including its partition
// parent. No assumed vda/sda name or unrelated attached sentinel disk suffices.
func suspendGuestRootSerial() (string, error) {
	var root unix.Stat_t
	if err := unix.Stat("/", &root); err != nil {
		return "", err
	}
	device := fmt.Sprintf("%d:%d", unix.Major(uint64(root.Dev)), unix.Minor(uint64(root.Dev)))
	mounts, err := suspendGuestRead("/proc/self/mountinfo", 1<<20)
	if err != nil {
		return "", err
	}
	matches := 0
	for _, line := range strings.Split(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 5 && fields[4] == "/" {
			if fields[2] != device {
				return "", errors.New("root mount device differs from root inode")
			}
			matches++
		}
	}
	if matches != 1 {
		return "", errors.New("one physical root mount required")
	}
	path, err := filepath.EvalSymlinks("/sys/dev/block/" + device)
	if err != nil || !strings.HasPrefix(path, "/sys/devices/") {
		return "", errors.New("mounted root has no kernel block-device identity")
	}
	if _, err = os.Stat(filepath.Join(path, "partition")); err == nil {
		path = filepath.Dir(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return suspendGuestText(filepath.Join(path, "serial"))
}

func suspendGuestOwnELF() (string, error) {
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return "", err
	}
	defer f.Close()
	var header [4]byte
	if _, err = io.ReadFull(f, header[:]); err != nil || string(header[:]) != "\x7fELF" {
		return "", errors.New("guest test is not the bound ELF executable")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(io.MultiReader(bytes.NewReader(header[:]), f), (256<<20)+1))
	if err != nil || n > 256<<20 {
		return "", errors.New("guest executable hash unavailable or exceeds bound")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func suspendGuestMatch(expected, media, marker suspendGuestBinding, boot string) error {
	if media != expected || marker.BootID == "" || marker.BootID != boot {
		return errors.New("immutable media or current-boot marker binding differs")
	}
	marker.BootID = ""
	if marker != expected {
		return errors.New("runtime marker does not match the immutable guest payload")
	}
	return nil
}

func suspendGuestVerify() (suspendGuestBinding, error) {
	expected, err := suspendGuestInputs(os.Getenv)
	if err != nil {
		return expected, err
	}
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 || os.Getuid() != 0 {
		return expected, errors.New("dedicated root Linux amd64 guest required")
	}
	media, err := suspendGuestBindingFile("/mnt/arm-suspend", "guard.json", false)
	if err != nil {
		return expected, err
	}
	marker, err := suspendGuestBindingFile(suspendGuestRun, "marker.json", true)
	if err != nil {
		return expected, err
	}
	boot, err := suspendGuestText("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return expected, err
	}
	bootUUID, err := uuid.Parse(boot)
	if err != nil || bootUUID.String() != boot {
		return expected, errors.New("current kernel boot UUID invalid")
	}
	if err = suspendGuestMatch(expected, media, marker, boot); err != nil {
		return expected, err
	}
	for path, want := range map[string]string{
		"/sys/class/dmi/id/sys_vendor":     "QEMU",
		"/sys/class/dmi/id/product_uuid":   expected.UUID,
		"/sys/class/dmi/id/product_serial": expected.Nonce,
		"/proc/1/comm":                     "systemd",
	} {
		got, err := suspendGuestText(path)
		if path == "/sys/class/dmi/id/product_uuid" {
			got = strings.ToLower(got) // UUID firmware spelling only.
		}
		if err != nil || got != want {
			return expected, fmt.Errorf("disposable guest identity differs: %s", path)
		}
	}
	product, err := suspendGuestText("/sys/class/dmi/id/product_name")
	if err != nil || !strings.Contains(strings.ToUpper(product), "Q35") {
		return expected, errors.New("dedicated Q35 guest required")
	}
	pid1, err := os.Stat("/proc/1/exe")
	if err != nil {
		return expected, err
	}
	systemd, err := os.Stat("/usr/lib/systemd/systemd")
	if err != nil || !os.SameFile(pid1, systemd) {
		return expected, errors.New("PID1 is not the guest systemd executable")
	}
	selfNS, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return expected, err
	}
	initNS, err := os.Readlink("/proc/1/ns/pid")
	if err != nil || selfNS != initNS {
		return expected, errors.New("guest test is not in PID1's namespace")
	}
	serial, err := suspendGuestRootSerial()
	if err != nil || serial != expected.DiskSerial {
		return expected, errors.New("mounted root is not the fresh disposable overlay disk")
	}
	binary, err := suspendGuestOwnELF()
	if err != nil || binary != expected.BinarySHA256 {
		return expected, errors.New("running ELF differs from immutable media payload")
	}
	states, err := suspendGuestText("/sys/power/state")
	if err != nil || !strings.Contains(" "+states+" ", " mem ") {
		return expected, errors.New("guest kernel does not expose mem suspend")
	}
	deep, err := suspendGuestText("/sys/power/mem_sleep")
	if err != nil || !strings.Contains(" "+deep+" ", " [deep] ") {
		return expected, errors.New("guest must already select deep S3")
	}
	return marker, nil
}

func suspendGuestEvent(binding suspendGuestBinding, name string, values map[string]any) {
	if values == nil {
		values = map[string]any{}
	}
	mode := os.Getenv("ARM_SUSPEND_MODE") // Metadata only, never an oracle switch.
	if mode != "positive" && mode != "monotonic-negative" {
		mode = "unspecified"
	}
	values["event"], values["mode"], values["nonce"], values["uuid"] = name, mode, binding.Nonce, binding.UUID
	values["boot_id"] = binding.BootID
	raw, _ := json.Marshal(values)
	fmt.Printf("ARM_VM_SUSPEND_EVENT %s\n", raw)
}

// Diagnostic frames are separate from the four acceptance events. Call sites
// remain outside Manager/owner locks; no stage changes authority or test flow.
func suspendGuestStage(binding suspendGuestBinding, name, phase string) {
	raw, _ := json.Marshal(map[string]any{"stage": name, "phase": phase, "mode": os.Getenv("ARM_SUSPEND_MODE"), "nonce": binding.Nonce, "uuid": binding.UUID, "boot_id": binding.BootID})
	fmt.Printf("ARM_VM_SUSPEND_STAGE %s\n", raw)
}

// The sole host input is one closed nonce line over a dedicated virtio port.
// It cannot select a command, path, duration, power state or test behavior.
func suspendGuestArm(binding suspendGuestBinding) error {
	path, err := filepath.EvalSymlinks("/dev/virtio-ports/arm.suspend.control")
	if err != nil || filepath.Dir(path) != "/dev" {
		return errors.New("dedicated guest control port unavailable")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return errors.New("guest control is not a character device")
	}
	name, err := suspendGuestText(fmt.Sprintf("/sys/dev/char/%d:%d/name", unix.Major(uint64(st.Rdev)), unix.Minor(uint64(st.Rdev))))
	if err != nil || name != "arm.suspend.control" {
		return errors.New("guest control port identity differs")
	}
	suspendGuestEvent(binding, "before_sleep", nil)
	until := time.Now().Add(10 * time.Second)
	var message []byte
	for time.Now().Before(until) {
		var part [128]byte
		n, err := unix.Read(fd, part[:])
		if n > 0 {
			message = append(message, part[:n]...)
			if len(message) > 128 {
				return errors.New("guest arm input exceeds bound")
			}
			if bytes.Contains(message, []byte("\n")) {
				if string(message) != "ARM_S3_ARM "+binding.Nonce+"\n" {
					return errors.New("guest arm nonce differs")
				}
				return nil
			}
		}
		if err == nil && n == 0 {
			return errors.New("guest control closed before its nonce arm")
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return err
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err = unix.Poll(poll, 100); err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return errors.New("guest control lost its original host connection")
		}
	}
	return errors.New("host did not arm this exact disposable guest")
}

type suspendGuestClocks struct {
	production leaseclock.Sample
	boot, mono time.Duration
	goNow      time.Time
}

func suspendGuestClockRead() (suspendGuestClocks, error) {
	var c suspendGuestClocks
	var err error
	c.production, err = leaseclock.Read()
	if err != nil {
		return c, err
	}
	var boot, mono unix.Timespec
	if err = unix.ClockGettime(unix.CLOCK_BOOTTIME, &boot); err != nil {
		return c, err
	}
	if err = unix.ClockGettime(unix.CLOCK_MONOTONIC, &mono); err != nil {
		return c, err
	}
	c.boot, c.mono, c.goNow = time.Duration(boot.Nano()), time.Duration(mono.Nano()), time.Now()
	return c, nil
}

// suspendGuestOriginalJobs is an observation of the two captured capabilities,
// never authority reconstructed from a PID. It must precede the power write and
// the first post-wake Manager action so prior exit/reap cannot fake cancellation.
func suspendGuestOriginalJobs(x *checkpointNativePair) error {
	if x == nil || x.m == nil || x.runtime == nil || x.runtime.leaseHold == nil || x.foreignPID <= 0 {
		return errors.New("complete original generic/SSH/sentinel capabilities required")
	}
	x.m.mu.Lock()
	defer x.m.mu.Unlock()
	h := x.runtime.leaseHold
	if x.runtime.ctx.Err() != nil || h.manager != x.m || h.runtime != x.runtime ||
		x.m.sshTransports[x.ssh.ID] != h || h.original.ID != x.ssh.ID || h.nativeReaped || h.staged || h.err != nil {
		return errors.New("original live SSH transport registration differs")
	}
	seen := map[int]bool{x.foreignPID: true}
	for i, l := range []model.Lease{x.generic, x.ssh} {
		j := x.genericJob
		if i == 1 {
			j = h.job
		}
		if j == nil || l.ID == "" || x.m.jobs[l.ID] != j || j.childOwner == nil || !j.childOwner.Active() ||
			j.cpuExited.Load() || j.cmd == nil || j.cmd.Process == nil || j.identity == nil ||
			j.cmd.Process.Pid <= 0 || j.identity.PID != j.cmd.Process.Pid || j.identity.PGID != j.cmd.Process.Pid ||
			l.PID != j.cmd.Process.Pid || !reflect.DeepEqual(l.Process, j.identity) ||
			l.Host != j.leaseHost || l.Owner != j.leaseOwner || !reflect.DeepEqual(l.Request, j.leaseRequest) {
			return errors.New("captured original native job is not still registered and unreaped")
		}
		if seen[j.cmd.Process.Pid] {
			return errors.New("generic/SSH/sentinel original PIDs are not distinct")
		}
		seen[j.cmd.Process.Pid] = true
		for _, done := range []<-chan struct{}{j.nativeDone, j.done} {
			if done == nil {
				return errors.New("original native job completion capability is absent")
			}
			select {
			case <-done:
				return errors.New("original native job already completed before expiry action")
			default:
			}
		}
	}
	return nil
}

// Opt-in only. The controller must independently attest its fresh QEMU process,
// new overlay/immutable seed, no networking/shares, QMP SUSPEND/suspended, actual
// 25s S3 and WAKEUP/running. This test never pauses a VM or changes a host clock.
func TestLeaseClockDisposableGuestS3(t *testing.T) {
	if os.Getenv("ARM_DISPOSABLE_SUSPEND_GUEST") == "" {
		t.Skip("requires an explicitly bound fresh disposable Q35 guest")
	}
	binding, err := suspendGuestVerify()
	if err != nil {
		t.Fatal("disposable guest guard refused", err)
	}
	suspendGuestEvent(binding, "ready", nil)
	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 16 << 20, Disk: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	// Reuse original native payload/join/cleanup helpers only. No controlled
	// lease clock, fake observer, stored-PID adoption or background tick is used.
	x := &checkpointNativePair{m: m, original: map[string]string{}, restore: map[string]string{}}
	t.Cleanup(func() { x.cleanup(t) })
	x.foreignPID, x.finishForeign = endingFailureForeignGroup(t)
	awaitNativeFixtureHeadroom(t, m, model.Resources{CPU: .51, Memory: 128})
	var once sync.Once
	released := make(chan struct{})
	x.release = func() { once.Do(func() { close(released) }) }
	actual := make(chan *sshRuntime, 1)
	inputs := make(chan *heldSSHInputCompletion, 1)
	const ttl = 20 * time.Second
	x.fixture = newSSHManagerFixtureWithAdmission(t, m, ttl, func(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel) (model.Lease, sshservice.Process, error) {
		held := &heldSSHInputCompletion{Channel: ch, entered: make(chan struct{}), release: released}
		q.Resources.Memory = 64
		l, p, err := m.AcquireSSH(ctx, q, e, held, model.Resources{CPU: 1, Memory: 1024}, time.Minute, time.Now().Add(time.Minute))
		if process, ok := p.(*sshProcess); ok {
			actual <- process.runtime
			inputs <- held
		}
		return l, p, err
	})
	t.Cleanup(func() { x.cleanup(t) }) // precedes fixture's server cleanup
	var genericCommand, sshCommand string
	x.genericCmd, genericCommand = checkpointNativePayload(t, t.TempDir())
	x.sshCmd, sshCommand = checkpointNativePayload(t, x.fixture.q.CWD)
	x.session, err = x.fixture.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	x.input, x.inputWriter = io.Pipe()
	x.session.Stdin = x.input
	q := model.Request{Key: "real-s3-generic", Reason: "disposable guest real suspend", Resources: model.Resources{CPU: .01, Memory: 64}, TTL: ttl}
	x.generic, err = m.Acquire(q, &RunRequest{Request: q, Command: []string{"/bin/sh", "-c", genericCommand}, CWD: filepath.Dir(x.genericCmd.fifo)})
	m.mu.Lock()
	x.genericJob = m.jobs[x.generic.ID]
	m.mu.Unlock()
	if err != nil || x.genericJob == nil {
		t.Fatal("real native generic admission failed", err)
	}
	x.genericCmd.awaitReady(t)
	if err = x.session.Start(sshCommand); err != nil {
		t.Fatal("real native SSH admission failed", err)
	}
	x.runtime, x.held = awaitSSHRuntime(t, actual), awaitSSHInput(t, inputs)
	x.sshCmd.awaitReady(t)
	_, _, x.ssh = x.fixture.snapshot()
	bare, err := m.Acquire(model.Request{Key: "real-s3-memory", Reason: "real suspend entitlement", Resources: model.Resources{Memory: 1}, TTL: ttl}, nil)
	if err != nil {
		t.Fatal(err)
	}
	originals := []model.Lease{x.generic, x.ssh, bare}
	if err = suspendGuestArm(binding); err != nil {
		t.Fatal(err)
	}
	fresh, err := suspendGuestVerify() // full recheck after blocking host arm
	if err != nil || fresh != binding {
		t.Fatal("guest guard changed before the sole power write", err)
	}
	claim, err := unix.Open(suspendGuestRun+"/power-write.claim", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		t.Fatal("one power write per fresh guest required", err)
	}
	if n, err := unix.Write(claim, []byte(binding.Nonce+"\n")); err != nil || n != len(binding.Nonce)+1 {
		unix.Close(claim)
		t.Fatal("guest power claim was not retained", err)
	}
	if err = unix.Close(claim); err != nil {
		t.Fatal(err)
	}
	power, err := unix.Open("/sys/power/state", unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(power)
	var fs unix.Statfs_t
	var st unix.Stat_t
	if err = unix.Fstatfs(power, &fs); err != nil || fs.Type != 0x62656572 {
		t.Fatal("power endpoint is not guest sysfs", err)
	}
	if err = unix.Fstat(power, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 {
		t.Fatal("power endpoint is not an owned sysfs attribute", err)
	}
	probe, cancel := context.WithTimeout(t.Context(), ttl)
	defer cancel()
	before, err := suspendGuestClockRead()
	if err != nil {
		t.Fatal(err)
	}
	minimum := ttl
	for _, l := range originals {
		n, err := l.Clock.Remaining(before.production, l.Expires)
		if err != nil || n < 10*time.Second {
			t.Fatal("less than ten live seconds before actual S3", n, err)
		}
		minimum = min(minimum, n)
	}
	if x.runtime.ctx.Err() != nil || probe.Err() != nil {
		t.Fatal("Go/SSH context expired before actual S3")
	}
	if err := suspendGuestOriginalJobs(x); err != nil {
		t.Fatal("original native capabilities are not live before S3", err)
	}
	// The only power-changing syscall in this file, after all guest guards and
	// the exact nonce arm. No retry, mem_sleep write, host endpoint or fallback.
	if n, err := unix.Write(power, []byte("mem\n")); err != nil || n != 4 {
		t.Fatal("guest S3 power write failed", n, err)
	}
	wakeBinding, err := suspendGuestVerify()
	if err != nil || wakeBinding != binding {
		t.Fatal("actual S3 changed the immutable guest or kernel boot binding", err)
	}
	after, err := suspendGuestClockRead()
	if err != nil {
		t.Fatal(err)
	}
	bootDelta, monoDelta := after.boot-before.boot, after.mono-before.mono
	productionDelta := time.Duration(after.production.Tick - before.production.Tick)
	remaining := make([]int64, len(originals))
	for i, l := range originals {
		n, err := l.Clock.Remaining(after.production, l.Expires)
		if err != nil {
			t.Fatal("post-S3 deadline provenance differs", err)
		}
		remaining[i] = int64(n)
	}
	suspendGuestEvent(binding, "after_wake", map[string]any{"raw_boottime_delta_ns": int64(bootDelta), "raw_monotonic_delta_ns": int64(monoDelta), "production_delta_ns": int64(productionDelta), "wall_delta_ns": int64(after.production.Wall.Sub(before.production.Wall)), "remaining_ns": remaining, "go_elapsed_ns": int64(time.Since(before.goNow)), "go_context_live": probe.Err() == nil, "ssh_context_live": x.runtime.ctx.Err() == nil, "native_boot_scope_before": before.production.Boot, "native_boot_scope_after": after.production.Boot})
	if before.production.Boot != after.production.Boot {
		t.Fatal("actual S3 changed guest boot or native time namespace")
	}
	if bootDelta-monoDelta < 20*time.Second || monoDelta < 0 || monoDelta >= minimum-5*time.Second || probe.Err() != nil || x.runtime.ctx.Err() != nil || time.Since(before.goNow)-monoDelta > time.Second {
		t.Fatal("actual guest S3 did not retain live monotonic/Go entitlement")
	}
	if productionDelta-monoDelta < 20*time.Second || productionDelta-bootDelta > time.Second || bootDelta-productionDelta > time.Second || remaining[0] != 0 || remaining[1] != 0 || remaining[2] != 0 {
		t.Fatal("production lease clock excluded real guest suspend")
	}
	suspendGuestStage(binding, "original_jobs", "before")
	if err := suspendGuestOriginalJobs(x); err != nil {
		t.Fatal("original native capabilities exited before the post-S3 expiry action", err)
	}
	suspendGuestStage(binding, "original_jobs", "after")
	// First Manager operation after S3: renewal must process elapsed expiry,
	// refuse revival, and request original generic/SSH cancellation itself.
	suspendGuestStage(binding, "renew_expiry", "before")
	if _, err = m.Renew(bare.ID, time.Minute); errorCode(err) != "lease_inactive" {
		t.Fatal("actual suspended entitlement renewed", err)
	}
	suspendGuestStage(binding, "renew_expiry", "after")
	suspendGuestStage(binding, "ssh_cancel", "before")
	awaitTransportSignal(t, x.runtime.ctx.Done(), "real S3 expiry did not cancel original SSH IO")
	suspendGuestStage(binding, "ssh_cancel", "after")
	for i, j := range x.jobs() {
		stage := "native_join_" + []string{"generic", "ssh"}[i]
		suspendGuestStage(binding, stage, "before")
		checkpointNativeJoined(t, j, true)
		suspendGuestStage(binding, stage, "after")
	}
	suspendGuestStage(binding, "generic_journal", "before")
	awaitTransportSignal(t, x.genericJob.done, "real S3 original generic journal did not join")
	suspendGuestStage(binding, "generic_journal", "after")
	suspendGuestStage(binding, "held_eof", "before")
	awaitTransportSignal(t, x.held.entered, "real S3 original SSH input did not reach EOF")
	suspendGuestStage(binding, "held_eof", "after")
	suspendGuestStage(binding, "held_charge", "before")
	assertSSHSettlementCharged(t, m, x.runtime)
	suspendGuestStage(binding, "held_charge", "after")
	suspendGuestStage(binding, "transport_join", "before")
	x.joinTransport(t)
	suspendGuestStage(binding, "transport_join", "after")
	suspendGuestStage(binding, "settlement_finished", "before")
	assertSSHSettlementFinished(t, x.fixture, x.runtime)
	suspendGuestStage(binding, "settlement_finished", "after")
	for i, l := range originals {
		stage := "final_lease_" + strconv.Itoa(i)
		suspendGuestStage(binding, stage, "before")
		if _, err := m.Renew(l.ID, time.Minute); errorCode(err) != "lease_inactive" {
			t.Fatal("expired original revived after positive joins", err)
		}
		stored, err := m.Inspect(l.ID)
		if err != nil || stored.State != "terminal" || stored.Ended == nil || !reflect.DeepEqual(stored.Clock, l.Clock) || stored.Expires == nil || !stored.Expires.Equal(*l.Expires) {
			t.Fatal("real S3 expiry rebased or lost an original deadline", err)
		}
		suspendGuestStage(binding, stage, "after")
	}
	suspendGuestStage(binding, "no_replay", "before")
	for _, command := range []*checkpointNativeCommand{x.genericCmd, x.sshCmd} {
		data, err := os.ReadFile(command.ready)
		if err != nil || string(data) != "ready\n" {
			t.Fatal("real S3 reconciliation replayed an original requested process", string(data), err)
		}
	}
	suspendGuestStage(binding, "no_replay", "after")
	suspendGuestStage(binding, "sentinel", "before")
	x.finishForeign()
	suspendGuestStage(binding, "sentinel", "after")
	suspendGuestEvent(binding, "complete", map[string]any{"leases_expired_without_rebase": 3, "original_native_and_transport_joins": 2, "held_io_charge_retained": true, "unrelated_original_sentinel_joined_normally": true})
}

// These pure guard controls perform no power operation and use no synthetic
// clock. They exercise refusal even when a caller supplies the opt-in flag.
func TestLeaseClockDisposableGuestBindingGuards(t *testing.T) {
	values := map[string]string{"ARM_DISPOSABLE_SUSPEND_GUEST": "1", "ARM_SUSPEND_NONCE": strings.Repeat("a", 64), "ARM_SUSPEND_UUID": "12345678-1234-1234-1234-123456789abc", "ARM_SUSPEND_DISK_SERIAL": "arm-" + strings.Repeat("a", 16), "ARM_SUSPEND_BINARY_SHA256": strings.Repeat("b", 64)}
	get := func(k string) string { return values[k] }
	b, err := suspendGuestInputs(get)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ARM_DISPOSABLE_SUSPEND_GUEST", "ARM_SUSPEND_NONCE", "ARM_SUSPEND_UUID", "ARM_SUSPEND_DISK_SERIAL", "ARM_SUSPEND_BINARY_SHA256"} {
		t.Run(key, func(t *testing.T) {
			prior := values[key]
			values[key] = "invalid"
			defer func() { values[key] = prior }()
			if _, err := suspendGuestInputs(get); err == nil {
				t.Fatal("incomplete disposable guest binding accepted")
			}
		})
	}
	boot := "87654321-4321-4321-4321-cba987654321"
	marker := b
	marker.BootID = boot
	if err := suspendGuestMatch(b, b, marker, boot); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		t.Run("marker-field-"+strconv.Itoa(i), func(t *testing.T) {
			changed := marker
			switch i {
			case 0:
				changed.Nonce = strings.Repeat("c", 64)
			case 1:
				changed.UUID = boot
			case 2:
				changed.DiskSerial += "x"
			case 3:
				changed.BinarySHA256 = strings.Repeat("d", 64)
			case 4:
				changed.BootID = b.UUID
			}
			if err := suspendGuestMatch(b, b, changed, boot); err == nil {
				t.Fatal("substituted runtime marker accepted")
			}
		})
	}
	if err := suspendGuestMatch(b, marker, marker, boot); err == nil {
		t.Fatal("runtime marker substituted for immutable media")
	}
}
