package providerexec

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// An owned leaf does not make a publicly replaceable directory namespace safe.
func TestReviewRejectPublicWritableImageAncestor(t *testing.T) {
	base := "/tmp"
	if runtime.GOOS == "darwin" { base = "/private/tmp" }
	dir, err := os.MkdirTemp(base, "arm-provider-selection-review-")
	if err != nil { t.Fatal(err) }
	initialDir, err := os.Stat(dir)
	if err != nil { t.Fatal(err) }
	path := filepath.Join(dir, "provider")
	var initialFile os.FileInfo
	t.Cleanup(func() {
		current, e := os.Lstat(dir)
		if e != nil || !os.SameFile(initialDir, current) || !current.IsDir() { t.Error("fixture directory changed; retained", dir); return }
		if e = os.Chmod(dir, 0700); e != nil { t.Error(e); return }
		if current, e = os.Lstat(path); e == nil {
			if initialFile == nil || !os.SameFile(initialFile, current) { t.Error("fixture leaf changed; retained", path); return }
			if e = os.Remove(path); e != nil { t.Error(e); return }
		} else if !os.IsNotExist(e) { t.Error(e); return }
		if e = os.Remove(dir); e != nil { t.Error("nonempty/changed directory retained", e) }
	})
	executable, err := os.Executable()
	if err != nil { t.Fatal(err) }
	source, err := os.Open(executable)
	if err != nil { t.Fatal(err) }
	defer source.Close()
	target, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil { t.Fatal(err) }
	_, err = io.Copy(target, source)
	if err == nil { err = target.Sync() }
	target.Close()
	if err != nil { t.Fatal(err) }
	initialFile, err = os.Lstat(path)
	if err != nil { t.Fatal(err) }
	if err = os.Chmod(dir, 0777); err != nil { t.Fatal(err) }
	if selected, err := PinImage(path, false); err == nil { t.Fatalf("publicly writable image ancestor accepted: digest=%s; leaf ownership cannot protect its name", selected.SHA256()) }
}
