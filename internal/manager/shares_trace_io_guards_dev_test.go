//go:build arm_devtrace

package manager

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShareFormalInputGuards(t *testing.T) {
	root, err := filepath.Abs("../../.tla")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "input-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "input.json")
	valid := shareFormalState{Schema: "arm.share-preparation/v1"}
	raw, _ := json.Marshal([]shareFormalState{valid, valid})
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var states []shareFormalState
	if err = formalReadJSON(path, &states); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"duplicate":  []byte(`[{"schema":"x","schema":"y"}]`),
		"case-alias": []byte(`[{"Schema":"arm.share-preparation/v1"}]`),
		"missing":    []byte(`[{}]`), "null": []byte(`[null]`),
		"trailing": append(append([]byte(nil), raw...), []byte(` []`)...),
		"oversize": make([]byte, (1<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			if err = os.WriteFile(path, input, 0600); err != nil {
				t.Fatal(err)
			}
			if err = formalReadJSON(path, &states); err == nil {
				t.Fatal("unsafe formal evidence accepted")
			}
		})
	}
	link := filepath.Join(dir, "link.json")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err = formalReadJSON(link, &states); err == nil {
		t.Fatal("followed input symlink")
	}
	if err = formalWriteJSON(link, valid); err == nil {
		t.Fatal("followed output symlink")
	}
	out := filepath.Join(dir, "output.json")
	if err = formalWriteJSON(out, valid); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private output required", st, err)
	}
	if err = formalWriteJSON(out, valid); err == nil {
		t.Fatal("overwrote retained formal output")
	}
	if _, err = formalPath(filepath.Join(dir, "..", "..", "outside.json")); err == nil {
		t.Fatal("formal output escaped artifact namespace")
	}
	// Exercise the actual dev reader/writer in a child with its own working
	// directory; never rename or replace this checkout's artifact namespace.
	fake := filepath.Join(dir, "fake-repo")
	childCWD := filepath.Join(fake, "internal", "manager")
	if err = os.MkdirAll(childCWD, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(dir, filepath.Join(fake, ".tla")); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestShareFormalRootSymlinkChild$", "-test.timeout=10s")
	child.Dir = childCWD
	child.Env = append(os.Environ(), "ARM_FORMAL_ROOT_SYMLINK_CHILD=1")
	if output, childErr := child.CombinedOutput(); childErr != nil {
		t.Fatalf("root symlink child: %v\n%s", childErr, output)
	}
	if _, err = os.Lstat(filepath.Join(dir, "root-symlink-output.json")); !os.IsNotExist(err) {
		t.Fatal("symlink artifact root wrote outside its namespace", err)
	}
}

func TestShareFormalRootSymlinkChild(t *testing.T) {
	if os.Getenv("ARM_FORMAL_ROOT_SYMLINK_CHILD") != "1" {
		return
	}
	path := filepath.Join("..", "..", ".tla", "root-symlink-output.json")
	if _, err := formalPath(path); err == nil {
		t.Fatal("accepted symlink artifact root")
	}
	if err := formalWriteJSON(path, map[string]string{"state": "refuse"}); err == nil {
		t.Fatal("wrote through symlink artifact root")
	}
	var state shareFormalState
	if err := formalReadJSON(filepath.Join("..", "..", ".tla", "output.json"), &state); err == nil {
		t.Fatal("read through symlink artifact root")
	}
}
