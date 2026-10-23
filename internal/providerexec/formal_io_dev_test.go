//go:build arm_devtrace

package providerexec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Development evidence lives only beneath the existing ignored .tla directory.
// Explicit output selection never replaces an existing/symlink artifact.
func workerFormalWrite(path string, value any) error {
	root, err := filepath.Abs("../../.tla")
	if err != nil {
		return err
	}
	st, err := os.Lstat(root)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("real .tla artifact directory required")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("worker evidence must stay under .tla")
	}
	current := root
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		st, err = os.Lstat(current)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("worker evidence has missing/non-directory/symlink ancestor")
		}
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	// The serialized file includes the trailing newline written below.
	if len(raw) >= 4<<20 {
		return fmt.Errorf("worker evidence including newline exceeds 4 MiB")
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	_, writeErr := f.Write(append(raw, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func TestWorkerFormalArtifactOutputGuards(t *testing.T) {
	root, err := filepath.Abs("../../.tla")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	own, err := os.MkdirTemp(root, "provider-worker-io-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(own); err != nil {
			t.Error(err)
		}
	})
	outside := filepath.Join(t.TempDir(), "sentinel")
	if err = os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if workerFormalWrite(outside, map[string]string{"state": "refused"}) == nil {
		t.Fatal("artifact escaped ignored namespace")
	}
	leaf := filepath.Join(own, "link")
	if err = os.Symlink(outside, leaf); err != nil {
		t.Fatal(err)
	}
	if workerFormalWrite(leaf, map[string]string{"state": "refused"}) == nil {
		t.Fatal("symlink artifact replaced")
	}
	linkDir := filepath.Join(own, "link-dir")
	if err = os.Symlink(filepath.Dir(outside), linkDir); err != nil {
		t.Fatal(err)
	}
	if workerFormalWrite(filepath.Join(linkDir, "new"), true) == nil {
		t.Fatal("symlink ancestor used")
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "unchanged" {
		t.Fatal("foreign sentinel changed", err)
	}
	output := filepath.Join(own, "observed.json")
	if err = workerFormalWrite(output, map[string]bool{"observed": true}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if workerFormalWrite(output, false) == nil {
		t.Fatal("existing artifact overwritten")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("exclusive artifact changed", err)
	}
	tooLarge := filepath.Join(own, "oversized")
	if workerFormalWrite(tooLarge, strings.Repeat("x", 4<<20)) == nil {
		t.Fatal("oversized evidence accepted")
	}
	if _, err = os.Lstat(tooLarge); !os.IsNotExist(err) {
		t.Fatal("oversized artifact created", err)
	}
}

func TestWorkerFormalArtifactExactFileBoundary(t *testing.T) {
	root, err := filepath.Abs("../../.tla")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	own, err := os.MkdirTemp(root, "provider-worker-boundary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(own); err != nil {
			t.Error(err)
		}
	})
	const fileCap = 4 << 20
	for _, tc := range []struct {
		name    string
		content int
		accept  bool
	}{
		{"newline_fills_cap", fileCap - 3, true},
		{"payload_at_cap", fileCap - 2, false},
		{"payload_above_cap", fileCap - 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := strings.Repeat("x", tc.content)
			encoded, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(own, tc.name+".json")
			err = workerFormalWrite(output, value)
			if !tc.accept {
				if len(encoded)+1 <= fileCap {
					t.Fatal("negative fixture does not exceed complete file cap")
				}
				if err == nil {
					t.Fatal("oversized complete file accepted")
				}
				if _, err = os.Lstat(output); !os.IsNotExist(err) {
					t.Fatal("refused boundary artifact created", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) != fileCap || raw[len(raw)-1] != '\n' {
				t.Fatalf("complete positive boundary size=%d, want %d including newline", len(raw), fileCap)
			}
			if !bytes.Equal(raw[:len(raw)-1], encoded) {
				t.Fatal("serialized payload changed")
			}
			var decoded string
			if err = json.Unmarshal(raw, &decoded); err != nil || decoded != value {
				t.Fatal("complete boundary file is not the original valid JSON", err)
			}
		})
	}
}
