package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/manager"
)

func pendingFixture(t *testing.T, kind string) pendingRecord {
	t.Helper()
	var request any = poolPlanFixture(t).request
	if kind == "message" {
		request = manager.MessageRequest{Key: "message-exact-key", Audience: "poll-test", Body: "Private retained message body", Targets: []agents.Contact{{ID: "poll-test", Adapter: "generic", Delivery: "poll", Session: "original-session", Registration: "original-registration"}}}
	}
	r, err := newPendingRecord(kind, "localhost", request)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestPendingIntentExactConcurrentAndSettlement(t *testing.T) {
	home := pendingHome(t)
	pool := pendingFixture(t, "pool")
	message := pendingFixture(t, "message")
	if err := persistPending(home, pool, false); err != nil {
		t.Fatal(err)
	}
	if err := persistPending(home, message, false); err != nil {
		t.Fatal(err)
	}
	records, err := loadPending(home)
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	if err = persistPending(home, pool, false); err == nil {
		t.Fatal("initial send reused unknown intent")
	}
	if err = persistPending(home, pool, true); err != nil {
		t.Fatal(err)
	}
	other := pool
	var q map[string]any
	json.Unmarshal(other.Request, &q)
	q["reason"] = "different reason"
	other.Request, _ = json.Marshal(q)
	if err = persistPending(home, other, true); err == nil {
		t.Fatal("different request reused key")
	}
	if err = settlePending(home, other); err == nil {
		t.Fatal("different request cleared intent")
	}
	if err = settlePending(home, pool); err != nil {
		t.Fatal(err)
	}
	records, err = loadPending(home)
	if err != nil || len(records) != 1 || !samePending(records[0], message) {
		t.Fatal("settlement erased other operation", records, err)
	}
	// Multiple readers/settlers never remove a different per-operation record.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); loadPending(home); settlePending(home, pool) }()
	}
	wg.Wait()
	records, err = loadPending(home)
	if err != nil || len(records) != 1 || !samePending(records[0], message) {
		t.Fatal(records, err)
	}
}
func TestPendingPathsFailClosedWithoutFollowingSymlinksOrLinks(t *testing.T) {
	for _, kind := range []string{"directory-symlink", "public-directory", "record-symlink", "record-hardlink", "record-public", "lock-symlink", "lock-hardlink", "lock-directory", "corrupt", "oversize", "unexpected"} {
		t.Run(kind, func(t *testing.T) {
			home := pendingHome(t)
			r := pendingFixture(t, "pool")
			dir := filepath.Join(home, pendingDirectory)
			target := filepath.Join(t.TempDir(), "target")
			os.WriteFile(target, []byte("do not touch"), 0600)
			if kind == "directory-symlink" {
				os.Symlink(filepath.Dir(target), dir)
			} else {
				os.Mkdir(dir, 0700)
				raw, _ := json.Marshal(r)
				switch kind {
				case "public-directory":
					os.Chmod(dir, 0755)
				case "record-symlink":
					os.Symlink(target, filepath.Join(dir, r.name()))
				case "record-hardlink":
					os.Link(target, filepath.Join(dir, r.name()))
				case "record-public":
					os.WriteFile(filepath.Join(dir, r.name()), raw, 0644)
				case "lock-symlink":
					os.Symlink(target, filepath.Join(dir, ".lock"))
				case "lock-hardlink":
					os.Link(target, filepath.Join(dir, ".lock"))
				case "lock-directory":
					os.Mkdir(filepath.Join(dir, ".lock"), 0700)
				case "corrupt":
					os.WriteFile(filepath.Join(dir, r.name()), []byte("{invalid"), 0600)
				case "oversize":
					os.WriteFile(filepath.Join(dir, r.name()), []byte(strings.Repeat("x", pendingRecordLimit+1)), 0600)
				case "unexpected":
					os.WriteFile(filepath.Join(dir, ".intent-interrupted"), []byte("incomplete"), 0600)
				}
			}
			if _, err := loadPending(home); err == nil {
				t.Fatal("unsafe history accepted")
			}
			if err := persistPending(home, r, false); err == nil {
				t.Fatal("unsafe history allowed new send")
			}
			raw, _ := os.ReadFile(target)
			if string(raw) != "do not touch" {
				t.Fatal("external target changed")
			}
		})
	}
}
func TestPendingBoundsPrivateLockAndSchema(t *testing.T) {
	home := pendingHome(t)
	r := pendingFixture(t, "pool")
	if err := persistPending(home, r, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, pendingDirectory)
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err = loadPending(home); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("locked store was not bounded", err)
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	for i := 0; i < 65; i++ {
		os.WriteFile(filepath.Join(dir, "unexpected-"+strings.Repeat("x", i)), nil, 0600)
	}
	if _, err = loadPending(home); err == nil || !strings.Contains(err.Error(), "exceeds 64") {
		t.Fatal("directory read unbounded", err)
	}
	raw, _ := json.Marshal(r)
	if _, err = decodePending(append(raw, []byte(" {}")...)); err == nil {
		t.Fatal("trailing schema accepted")
	}
	r.Version = 2
	if _, err = decodePending(mustPendingJSON(r)); err == nil {
		t.Fatal("future version accepted")
	}
	r = pendingFixture(t, "message")
	var message manager.MessageRequest
	json.Unmarshal(r.Request, &message)
	message.Targets[0].CWD = strings.Repeat("x", pendingRequestLimit)
	if _, err = newPendingRecord("message", "localhost", message); err == nil {
		t.Fatal("oversized exact send allowed")
	}
}
func mustPendingJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

func pendingHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestPendingAttemptGenerationProtectsOtherProcessRetry(t *testing.T) {
	home := pendingHome(t)
	first := pendingFixture(t, "message")
	first, err := reservePending(home, first, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reservePending(home, first, true)
	if err != nil || second.Attempts != 2 {
		t.Fatal(second, err)
	}
	if err = settlePendingOutcome(home, first, true); err == nil {
		t.Fatal("initial refusal erased another dispatch attempt")
	}
	records, err := loadPending(home)
	if err != nil || len(records) != 1 || records[0].Attempts != 2 {
		t.Fatal(records, err)
	}
	if err = settlePending(home, second); err != nil {
		t.Fatal("matching durable outcome cannot settle all attempts", err)
	}
}

func TestPendingProcessWorker(t *testing.T) {
	home := os.Getenv("ARM_TEST_PENDING_HOME")
	if home == "" {
		t.Skip("subprocess fixture")
	}
	r := pendingFixture(t, "message")
	r.Authority = os.Getenv("ARM_TEST_PENDING_AUTHORITY")
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := persistPending(home, r, false)
		if err == nil {
			return
		}
		if !strings.Contains(err.Error(), "busy") || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestPendingConcurrentProcessesPreserveIndependentIntents(t *testing.T) {
	home := pendingHome(t)
	var commands []*exec.Cmd
	for i := 0; i < 6; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPendingProcessWorker$")
		cmd.Env = append(os.Environ(), "ARM_TEST_PENDING_HOME="+home, "ARM_TEST_PENDING_AUTHORITY=fixture-"+strconv.Itoa(i))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	records, err := loadPending(home)
	if err != nil || len(records) != 6 {
		t.Fatal("concurrent intent lost", len(records), err)
	}
	if err = settlePending(home, records[0]); err != nil {
		t.Fatal(err)
	}
	after, err := loadPending(home)
	if err != nil || len(after) != 5 {
		t.Fatal("settlement lost another process", after, err)
	}
}

func TestPendingDurabilityFailuresNeverAuthorizeDispatch(t *testing.T) {
	home := pendingHome(t)
	r := pendingFixture(t, "message")
	if _, err := reservePendingWith(home, r, false, pendingDurability{fileSync: func(*os.File) error { return os.ErrPermission }}); err == nil {
		t.Fatal("file sync failure allowed dispatch")
	}
	records, err := loadPending(home)
	if err != nil || len(records) != 0 {
		t.Fatal("pre-rename failure leaked final intent", records, err)
	}
	saved, err := reservePendingWith(home, r, false, pendingDurability{dirSync: func(*os.Root) error { return os.ErrPermission }})
	if err == nil {
		t.Fatal("directory sync failure allowed dispatch")
	}
	records, err = loadPending(home)
	if err != nil || len(records) != 1 || !samePending(records[0], saved) {
		t.Fatal("renamed intent lost instead of restored unknown", records, err)
	}
	if err = persistPending(home, r, false); err == nil {
		t.Fatal("uncertain intent replaced after durability failure")
	}
}
