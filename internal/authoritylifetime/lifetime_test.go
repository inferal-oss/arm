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
		"parent": home + "/../other", "nul": home + "\x00", "bytes": "/" + strings.Repeat("a", maxPathBytes),
		"parts": "/" + strings.Repeat("a/", maxPathParts) + "a",
	} {
		t.Run(name, func(t *testing.T) {
			if h, err := acquire(path); h != nil || err == nil {
				t.Fatal("unbounded/noncanonical path acquired", h, err)
			}
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
