package authoritylifetime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func fixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "authority")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func original(t *testing.T, home string) *lifetime {
	t.Helper()
	h, err := acquire(home)
	if err != nil || h == nil {
		t.Fatal("actual native acquisition failed", err)
	}
	t.Cleanup(func() {
		report, err := h.retire()
		t.Logf("native_cleanup opened=%d closed=%d unknown=%d lock_acquired=%t complete=%t", report.opened, report.closed, report.unknown, report.lockAcquired, report.complete)
		if !report.complete || report.unknown != 0 || report.opened != report.closed {
			t.Error("original native cleanup incomplete", report, err)
		}
	})
	return h
}

func independentLock(t *testing.T, path string, mustContend bool) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	closeErr := f.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if mustContend && !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		t.Fatal("independent open did not contend with original lock", err)
	}
	if !mustContend && err != nil {
		t.Fatal("original native lock did not release", err)
	}
}

func TestNativeLifetimeOriginalAcquisitionAndRetirement(t *testing.T) {
	home := fixture(t)
	h := original(t, home)
	for range 8 {
		if err := h.validate(); err != nil {
			t.Fatal(err)
		}
	}
	if h.owner.directory.file == h.owner.lock.file || h.owner.opened-h.owner.closed != 2 {
		t.Fatal("actual original descriptor custody differs")
	}
	independentLock(t, filepath.Join(home, "daemon.lock"), true)
	second, err := acquire(home)
	var refused *acquisitionFailure
	if second != nil || !errors.As(err, &refused) || !refused.closure.complete || refused.closure.lockAcquired || refused.closure.opened != refused.closure.closed {
		t.Fatal("contended acquisition did not close its originals", second, err)
	}
	t.Logf("contended_cleanup opened=%d closed=%d complete=%t", refused.closure.opened, refused.closure.closed, refused.closure.complete)
	if err := h.validate(); err != nil {
		t.Fatal("contended attempt changed original", err)
	}
	report, err := h.retire()
	if err != nil || !report.complete || !report.lockAcquired || report.opened != report.closed {
		t.Fatal("actual original retirement failed", report, err)
	}
	independentLock(t, filepath.Join(home, "daemon.lock"), false)
	again, err := h.retire()
	if err != nil || again != report {
		t.Fatal("repeated retirement did not return original outcome", again, err)
	}
	if !errors.Is(h.validate(), errRetired) {
		t.Fatal("retired original reacquired authority")
	}
}

func TestNativeLifetimeRejectsCopiedForeignAndZeroHandles(t *testing.T) {
	home := fixture(t)
	h := original(t, home)
	copy := *h // only an owner pointer; no mutex/native ownership is copied
	for name, candidate := range map[string]*lifetime{"copy": &copy, "foreign": {owner: h.owner}, "zero": {}, "nil": nil} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(candidate.validate(), errNotOriginal) {
				t.Fatal("non-original validated")
			}
			if report, err := candidate.retire(); !errors.Is(err, errNotOriginal) || report.complete {
				t.Fatal("non-original retired native ownership", report, err)
			}
			if err := h.validate(); err != nil {
				t.Fatal("non-original refusal poisoned original", err)
			}
			independentLock(t, filepath.Join(home, "daemon.lock"), true)
		})
	}
}

func TestNativeLifetimeRefusesBoundedNoncanonicalPaths(t *testing.T) {
	home := fixture(t)
	for name, path := range map[string]string{
		"relative": "relative", "empty": "", "root": "/", "trailing": home + "/",
		"parent": home + "/../other", "nul": home + "\x00",
	} {
		t.Run(name, func(t *testing.T) {
			h, err := acquire(path)
			if h != nil {
				if report, cleanupErr := h.retire(); !report.complete {
					t.Error("unexpected policy receiver cleanup uncertain", report, cleanupErr)
				}
			}
			if h != nil || err == nil {
				t.Fatal("unbounded/noncanonical path acquired", h, err)
			}
		})
	}
}

// This fixture uses fd-relative setup/cleanup so every legal component and the
// actual final private lock exist even beyond a platform's absolute-path limit.
// It never supplies a descriptor or positive ownership bypass to acquire.
type policyPathFixture struct {
	root, path string
	files      []*os.File
	dirs       []struct {
		parent int
		name   string
		child  int
	}
	lockParent     int
	lockFile       int
	lockExists     bool
	opened, closed int
	retained       []*lifetime
}

func (f *policyPathFixture) add(fd int, name string) int {
	f.files = append(f.files, os.NewFile(uintptr(fd), name))
	f.opened++
	return len(f.files) - 1
}

func (f *policyPathFixture) close(index int) error {
	if f.files[index] == nil {
		return nil
	}
	if err := f.files[index].Close(); err != nil {
		return err
	}
	f.files[index] = nil
	f.closed++
	return nil
}

func (f *policyPathFixture) retireReturned(t *testing.T, h *lifetime) {
	t.Helper()
	if h == nil {
		return
	}
	report, err := h.retire()
	t.Logf("native_cleanup opened=%d closed=%d unknown=%d lock_acquired=%t complete=%t", report.opened, report.closed, report.unknown, report.lockAcquired, report.complete)
	if !report.complete {
		f.retained = append(f.retained, h)
		t.Fatal("unexpected returned receiver retains original native cleanup", report, err)
	}
}

func (f *policyPathFixture) createLock(parent int) error {
	fd, err := unix.Openat(int(f.files[parent].Fd()), "daemon.lock", unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f.lockParent, f.lockFile, f.lockExists = parent, f.add(fd, "fixture.lock"), true
	return nil
}

func (f *policyPathFixture) removeLock() error {
	if err := f.close(f.lockFile); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(f.files[f.lockParent].Fd()), "daemon.lock", 0); err != nil {
		return err
	}
	f.lockExists = false
	return nil
}

func existingPolicyPath(t *testing.T, byteLimit bool) *policyPathFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "arm-native-bounds-")
	if err != nil {
		t.Fatal(err)
	}
	f := &policyPathFixture{root: root}
	t.Cleanup(func() {
		if len(f.retained) != 0 {
			t.Error("retained uncertain native owner; fixture preserved")
			return
		}
		if f.lockExists {
			if err := f.removeLock(); err != nil {
				t.Error("fixture lock cleanup failed", err)
				return
			}
		}
		for i := len(f.dirs) - 1; i >= 0; i-- {
			d := f.dirs[i]
			if d.child >= 0 {
				if err := f.close(d.child); err != nil {
					t.Error("fixture directory close failed", err)
					return
				}
			}
			if err := unix.Unlinkat(int(f.files[d.parent].Fd()), d.name, unix.AT_REMOVEDIR); err != nil {
				t.Error("fixture directory removal failed", err)
				return
			}
		}
		if len(f.files) > 0 {
			if err := f.close(0); err != nil {
				t.Error("fixture root close failed", err)
				return
			}
		}
		if err := os.Remove(f.root); err != nil {
			t.Error("fixture root removal failed", err)
			return
		}
		t.Logf("policy_fixture_cleanup opened=%d closed=%d complete=%t", f.opened, f.closed, f.opened == f.closed)
	})
	f.path, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	f.root = f.path
	fd, err := unix.Open(f.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent := f.add(fd, "fixture.root")
	name := "d"
	if byteLimit {
		name = strings.Repeat("b", 220)
	}
	for {
		next := f.path + "/" + name
		parts := len(strings.Split(strings.TrimPrefix(next, "/"), "/"))
		last := parts > maxPathParts
		if byteLimit {
			last = len(next) > maxPathBytes
		}
		if last {
			// This actual native prefix would acquire without the next single
			// excess component; no hypothetical or fabricated positive witness.
			if err := f.createLock(parent); err != nil {
				t.Fatal(err)
			}
			h, err := acquire(f.path)
			f.retireReturned(t, h)
			if err != nil || h == nil {
				t.Fatal("real within-limit prefix failed native acquisition", err)
			}
			if err := f.removeLock(); err != nil {
				t.Fatal(err)
			}
		}
		if err := unix.Mkdirat(int(f.files[parent].Fd()), name, 0700); err != nil {
			if byteLimit && (errors.Is(err, unix.ENAMETOOLONG) || errors.Is(err, unix.EOPNOTSUPP)) {
				t.Skip("platform cannot create meaningful byte-limit fixture:", err)
			}
			t.Fatal(err)
		}
		f.dirs = append(f.dirs, struct {
			parent int
			name   string
			child  int
		}{parent, name, -1})
		fd, err := unix.Openat(int(f.files[parent].Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			if byteLimit && (errors.Is(err, unix.ENAMETOOLONG) || errors.Is(err, unix.EOPNOTSUPP)) {
				t.Skip("platform cannot open meaningful byte-limit fixture:", err)
			}
			t.Fatal(err)
		}
		child := f.add(fd, "fixture.directory")
		f.dirs[len(f.dirs)-1].child = child
		parent, f.path = child, next
		if last {
			break
		}
	}
	if err := f.createLock(parent); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		index      int
		kind, mode uint32
	}{
		{parent, unix.S_IFDIR, 0700}, {f.lockFile, unix.S_IFREG, 0600},
	} {
		var stat unix.Stat_t
		if err := unix.Fstat(int(f.files[check.index].Fd()), &stat); err != nil {
			t.Fatal(err)
		}
		if uint32(stat.Mode)&unix.S_IFMT != check.kind || uint32(stat.Mode)&07777 != check.mode || stat.Uid != uint32(os.Geteuid()) || uint64(stat.Nlink) == 0 || check.kind == unix.S_IFREG && uint64(stat.Nlink) != 1 {
			t.Fatal("final native policy fixture is not privately owned with the required type/mode/links")
		}
	}
	return f
}

func TestNativeLifetimePolicyBoundsExistingPaths(t *testing.T) {
	for _, kind := range []string{"parts", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			f := existingPolicyPath(t, kind == "bytes")
			parts := len(strings.Split(strings.TrimPrefix(f.path, "/"), "/"))
			if kind == "parts" && (parts != maxPathParts+1 || len(f.path) > maxPathBytes) {
				t.Fatal("component witness does not isolate component cap")
			}
			if kind == "bytes" && (len(f.path) <= maxPathBytes || parts > maxPathParts) {
				t.Fatal("byte witness does not isolate byte cap")
			}
			h, err := acquire(f.path)
			f.retireReturned(t, h) // also cleans an accidentally successful regression
			var native *acquisitionFailure
			if h != nil || err == nil || errors.As(err, &native) {
				t.Fatal("policy cap did not refuse before native ownership", err)
			}
			t.Logf("policy_bounds_rejected bytes=%d parts=%d before_native=true", len(f.path), parts)
		})
	}
}

func TestNativeLifetimePartialAcquisitionClosesOriginals(t *testing.T) {
	for _, kind := range []string{"missing-directory", "directory-file", "missing-lock", "directory-mode", "lock-mode", "lock-directory", "lock-fifo", "lock-symlink", "lock-hardlink", "directory-symlink", "ancestor-symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := fixture(t)
			path := home
			lock := filepath.Join(home, "daemon.lock")
			switch kind {
			case "missing-directory":
				path = filepath.Join(home, "missing")
			case "directory-file":
				path = lock
			case "missing-lock":
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
			case "directory-mode":
				if err := os.Chmod(home, 0770); err != nil {
					t.Fatal(err)
				}
			case "lock-mode":
				if err := os.Chmod(lock, 0640); err != nil {
					t.Fatal(err)
				}
			case "lock-directory", "lock-fifo", "lock-symlink":
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "lock-directory":
					if err := os.Mkdir(lock, 0700); err != nil {
						t.Fatal(err)
					}
				case "lock-fifo":
					if err := unix.Mkfifo(lock, 0600); err != nil {
						t.Fatal(err)
					}
				case "lock-symlink":
					if err := os.Symlink("missing", lock); err != nil {
						t.Fatal(err)
					}
				}
			case "lock-hardlink":
				if err := os.Link(lock, filepath.Join(home, "linked")); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink", "ancestor-symlink":
				parent, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(parent, "alias")
				if err := os.Symlink(home, path); err != nil {
					t.Fatal(err)
				}
				if kind == "ancestor-symlink" {
					path = filepath.Join(path, "descendant")
				}
			}
			h, err := acquire(path)
			var failure *acquisitionFailure
			if h != nil || !errors.As(err, &failure) || !failure.closure.complete || failure.closure.unknown != 0 || failure.closure.opened != failure.closure.closed {
				t.Fatal("partial native acquisition lost cleanup", h, err)
			}
			t.Logf("partial_cleanup opened=%d closed=%d unknown=%d complete=%t", failure.closure.opened, failure.closure.closed, failure.closure.unknown, failure.closure.complete)
			if kind == "missing-directory" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("acquisition created missing directory", err)
				}
			}
			if kind == "missing-lock" {
				if _, err := os.Lstat(lock); !os.IsNotExist(err) {
					t.Fatal("acquisition created missing lock", err)
				}
			}
		})
	}
}

func TestNativeLifetimeChangedIdentityStaysSticky(t *testing.T) {
	for _, kind := range []string{"directory-mode", "lock-mode", "lock-link", "directory-name", "directory-missing", "lock-name", "lock-missing", "lock-symlink", "lock-type"} {
		t.Run(kind, func(t *testing.T) {
			home := fixture(t)
			h := original(t, home)
			lock := filepath.Join(home, "daemon.lock")
			var restore func() error
			switch kind {
			case "directory-mode":
				if err := os.Chmod(home, 0750); err != nil {
					t.Fatal(err)
				}
				restore = func() error { return os.Chmod(home, 0700) }
			case "lock-mode":
				if err := os.Chmod(lock, 0640); err != nil {
					t.Fatal(err)
				}
				restore = func() error { return os.Chmod(lock, 0600) }
			case "lock-link":
				linked := filepath.Join(home, "linked")
				if err := os.Link(lock, linked); err != nil {
					t.Fatal(err)
				}
				restore = func() error { return os.Remove(linked) }
			case "directory-name", "directory-missing":
				retained := home + "-retained"
				if err := os.Rename(home, retained); err != nil {
					t.Fatal(err)
				}
				if kind == "directory-name" {
					if err := os.Mkdir(home, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(lock, nil, 0600); err != nil {
						t.Fatal(err)
					}
					independentLock(t, lock, false)
				}
				independentLock(t, filepath.Join(retained, "daemon.lock"), true)
				restore = func() error {
					if kind == "directory-name" {
						if err := os.Remove(lock); err != nil {
							return err
						}
						if err := os.Remove(home); err != nil {
							return err
						}
					}
					return os.Rename(retained, home)
				}
			case "lock-name", "lock-missing", "lock-symlink", "lock-type":
				retained := filepath.Join(home, "retained.lock")
				if err := os.Rename(lock, retained); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "lock-name":
					if err := os.WriteFile(lock, nil, 0600); err != nil {
						t.Fatal(err)
					}
				case "lock-symlink":
					if err := os.Symlink("retained.lock", lock); err != nil {
						t.Fatal(err)
					}
				case "lock-type":
					if err := os.Mkdir(lock, 0700); err != nil {
						t.Fatal(err)
					}
				}
				independentLock(t, retained, true)
				restore = func() error {
					if kind != "lock-missing" {
						if err := os.Remove(lock); err != nil {
							return err
						}
					}
					return os.Rename(retained, lock)
				}
			}
			first := h.validate()
			if first == nil {
				t.Fatal("changed original identity validated")
			}
			if err := restore(); err != nil {
				t.Fatal(err)
			}
			if err := h.validate(); err != first {
				t.Fatal("restored pathname cleared sticky failure", err)
			}
			independentLock(t, lock, true)
			report, err := h.retire()
			if err == nil || !report.complete {
				t.Fatal("sticky refusal lost positive original cleanup", report, err)
			}
			independentLock(t, lock, false)
		})
	}
}

func TestNativeLifetimeConcurrentValidateAndRetire(t *testing.T) {
	home := fixture(t)
	h := original(t, home)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-start
			for range 8 {
				if err := h.validate(); err != nil && !errors.Is(err, errRetired) {
					t.Error(err)
				}
			}
		})
	}
	wg.Go(func() {
		<-start
		if report, err := h.retire(); err != nil || !report.complete {
			t.Error(report, err)
		}
	})
	close(start)
	wg.Wait()
	if !errors.Is(h.validate(), errRetired) {
		t.Fatal("concurrent retirement reactivated original")
	}
	independentLock(t, filepath.Join(home, "daemon.lock"), false)
}
