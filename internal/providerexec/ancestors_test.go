package providerexec

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func copyNativeForSelection(t *testing.T, path string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
}
func publicTempRoot() string {
	if runtime.GOOS == "darwin" {
		return "/private/tmp"
	}
	return "/tmp"
}

func TestImageAncestorStickyPrivateChildAndWritableChild(t *testing.T) {
	dir, err := os.MkdirTemp(publicTempRoot(), "arm-provider-ancestor-")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "provider")
	copyNativeForSelection(t, path)
	leaf, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		now, err := os.Lstat(dir)
		if err != nil || !os.SameFile(info, now) {
			t.Error("owned ancestor changed; retained", dir)
			return
		}
		if err = os.Chmod(dir, 0700); err != nil {
			t.Error(err)
			return
		}
		now, err = os.Lstat(path)
		if err != nil || !os.SameFile(leaf, now) {
			t.Error("owned leaf changed; retained", path)
			return
		}
		if err = os.Remove(path); err != nil {
			t.Error(err)
			return
		}
		if err = os.Remove(dir); err != nil {
			t.Error("nonempty owned temp retained", err)
		}
	})
	selected, err := PinImage(path, false)
	if err != nil {
		t.Fatal("private exact child under real root-owned sticky temp must work", err)
	}
	rootFound, currentFound := false, false
	for _, a := range selected.ancestors {
		rootFound = rootFound || a.Identity.Owner == 0
		currentFound = currentFound || a.Identity.Owner == uint32(os.Geteuid())
	}
	if !rootFound || !currentFound {
		t.Fatal("real root/current ancestry not captured")
	}
	if err = os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err = PinImage(path, false); err == nil {
		t.Fatal("sticky temp adopted a writable next child")
	}
	if err = selected.check(); err == nil {
		t.Fatal("existing selection ignored changed ancestor mode")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = selected.check(); err != nil {
		t.Fatal("restored exact private namespace failed", err)
	}
}

func TestImageAncestorReplacementAndSymlinkCannotInherit(t *testing.T) {
	for _, kind := range []string{"replaced", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			a := filepath.Join(root, "a")
			if err := os.MkdirAll(filepath.Join(a, "sub"), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a, "sub", "provider")
			copyNativeForSelection(t, path)
			selected, err := PinImage(path, false)
			if err != nil {
				t.Fatal(err)
			}
			old := a + ".old"
			if err = os.Rename(a, old); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err = os.Symlink(old, a); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.Mkdir(a, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.Rename(filepath.Join(old, "sub"), filepath.Join(a, "sub")); err != nil {
					t.Fatal(err)
				}
				current, err := PinImage(path, false)
				if err != nil {
					t.Fatal(err)
				}
				if current.identity != selected.identity || current.stamp != selected.stamp || current.sha != selected.sha {
					t.Fatal("test must retain exact unchanged executable while replacing only ancestor")
				}
			}
			if err = selected.check(); err == nil {
				t.Fatal("changed ancestor inherited selected identity")
			}
		})
	}
}

func TestImageAncestorForeignAndStickyPolicy(t *testing.T) {
	uid := uint32(os.Geteuid())
	foreign := uid + 1
	if foreign == 0 {
		foreign = 1
	}
	mk := func(owner uint32, mode uint32) directoryWitness {
		return directoryWitness{model.FileIdentity{Owner: owner, BirthKnown: true}, uint32(syscall.S_IFDIR) | mode, "typed-policy-only"}
	}
	root := mk(0, 01777)
	own := mk(uid, 0700)
	other := mk(foreign, 0700)
	writable := mk(uid, 0777)
	cases := []struct {
		name   string
		parent directoryWitness
		child  *directoryWitness
		ok     bool
	}{
		{"root protected", mk(0, 0755), &own, true}, {"current protected", own, &own, true},
		{"foreign protected", other, &own, false}, {"nonsticky writable", mk(0, 0777), &own, false},
		{"sticky safe private child", root, &own, true}, {"sticky foreign child", root, &other, false},
		{"sticky writable child", root, &writable, false}, {"sticky no child", root, nil, false},
		{"current sticky cannot assert root exception", mk(uid, 01777), &own, uid == 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := trustedDirectory(c.parent, c.child, uid); got != c.ok {
				t.Fatal(got, c.ok)
			}
		})
	}
	unknown := root
	unknown.Identity.BirthKnown = false
	if trustedDirectory(unknown, &own, uid) {
		t.Fatal("unknown native birth accepted")
	}
}
func TestImageAncestorBoundsBeforeNativeOpen(t *testing.T) {
	if _, _, err := openSelectedImage("/" + strings.Repeat("a/", maxImageAncestors) + "provider"); err == nil {
		t.Fatal("ancestor bound omitted")
	}
	if _, _, err := openSelectedImage("/" + strings.Repeat("a", 256) + "/provider"); err == nil {
		t.Fatal("component bound omitted")
	}
}
