package sshservice

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/crypto/ssh"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateFixture(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestHostIdentityReadOnlyExplicitSetupAndAuthorityBinding(t *testing.T) {
	dir := privateFixture(t)
	host := strings.Repeat("a", 32)
	if _, err := LoadHostKey(dir, host); !os.IsNotExist(err) {
		t.Fatal("missing identity was created/repaired", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("read-only inventory wrote files")
	}
	a, err := initializeHostKey(dir, host)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, hostKeyFile))
	b, err := initializeHostKey(dir, host)
	if err != nil || !bytes.Equal(a.PublicKey().Marshal(), b.PublicKey().Marshal()) {
		t.Fatal("host key changed", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, hostKeyFile))
	if !bytes.Equal(before, after) {
		t.Fatal("existing key bytes changed")
	}
	if _, err = LoadHostKey(dir, strings.Repeat("b", 32)); err == nil {
		t.Fatal("key adopted by other authority")
	}
}
func TestHostIdentityUnsafePathsNeverReplaced(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public-file", "public-directory", "corrupt", "alias"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateFixture(t)
			host := strings.Repeat("a", 32)
			_, err := initializeHostKey(dir, host)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, hostKeyFile)
			before, _ := os.ReadFile(path)
			switch kind {
			case "symlink":
				os.Rename(path, path+".old")
				os.Symlink(path+".old", path)
			case "hardlink":
				os.Link(path, path+".link")
			case "public-file":
				os.Chmod(path, 0644)
			case "public-directory":
				os.Chmod(dir, 0755)
			case "corrupt":
				os.WriteFile(path, []byte("partial"), 0600)
			case "alias":
				var v map[string]any
				json.Unmarshal(before, &v)
				v["Host"] = strings.Repeat("b", 32)
				raw, _ := json.Marshal(v)
				os.WriteFile(path, raw, 0600)
			}
			current, _ := os.ReadFile(path)
			if _, err = LoadHostKey(dir, host); err == nil {
				t.Fatal("unsafe identity accepted")
			}
			if _, err = initializeHostKey(dir, host); err == nil {
				t.Fatal("unsafe key repaired")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(current, after) {
				t.Fatal("unsafe evidence overwritten")
			}
		})
	}
}
func pinFixture(t *testing.T, dir string) Pin {
	t.Helper()
	key, err := initializeHostKey(dir, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	return Pin{Schema: 1, Source: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Pair: strings.Repeat("c", 64), Revision: 1, TLSFingerprint: strings.Repeat("d", 64), TLSSPKI: strings.Repeat("e", 64), Account: "armuser", PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey()))), At: time.Now().UTC()}
}
func TestPinExplicitReviewCASAndNoSilentRotation(t *testing.T) {
	dir := privateFixture(t)
	next := pinFixture(t, dir)
	review, err := ReviewPin(dir, next)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := ReadPin(dir, next.Target); err != nil || p != nil {
		t.Fatal("review wrote a pin", p, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "ssh-pins.lock")); !os.IsNotExist(err) {
		t.Fatal("review created lock")
	}
	if err = commitPin(context.Background(), review); err != nil {
		t.Fatal(err)
	}
	stale, err := ReviewPin(dir, next)
	if err != nil {
		t.Fatal(err)
	}
	changed := stale
	changed.After.Pair = strings.Repeat("f", 64)
	if commitPin(context.Background(), changed) == nil {
		t.Fatal("reviewed proposed pin changed")
	}
	path := pinPath(dir, next.Target)
	raw, _ := os.ReadFile(path)
	altered := bytes.Replace(raw, []byte(`"revision":1`), []byte(`"revision":2`), 1)
	if bytes.Equal(raw, altered) {
		t.Fatal("invalid edit fixture")
	}
	os.WriteFile(path, altered, 0600)
	if commitPin(context.Background(), stale) == nil {
		t.Fatal("same-inode stale bytes overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, altered) {
		t.Fatal("changed pin lost")
	}
}

func TestPinConcurrentReviewsCannotOverwriteEachOther(t *testing.T) {
	dir := privateFixture(t)
	next := pinFixture(t, dir)
	first, err := ReviewPin(dir, next)
	if err != nil {
		t.Fatal(err)
	}
	next.Pair = strings.Repeat("f", 64)
	second, err := ReviewPin(dir, next)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, review := range []PinReview{first, second} {
		go func(r PinReview) { done <- commitPin(context.Background(), r) }(review)
	}
	success := 0
	for i := 0; i < 2; i++ {
		if <-done == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent first trust changed twice", success)
	}
	if p, err := ReadPin(dir, next.Target); err != nil || p == nil {
		t.Fatal("winning reviewed pin unreadable", err)
	}
}
func TestPinReviewRejectsReplacedServiceDirectoryBeforeWrites(t *testing.T) {
	dir := privateFixture(t)
	next := pinFixture(t, dir)
	review, err := ReviewPin(dir, next)
	if err != nil {
		t.Fatal(err)
	}
	old := dir + ".old"
	if err = os.Rename(dir, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(old) }) // own moved disposable fixture only
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if commitPin(context.Background(), review) == nil {
		t.Fatal("review adopted replaced directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("replacement namespace changed", entries, err)
	}
}
