package meshssh

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUninstallFixedHelperChecksDigestBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "arm")
	marker := filepath.Join(dir, "executed")
	script := []byte("#!/bin/sh\ncat > '" + marker + "'\n")
	if e := os.WriteFile(binary, script, 0700); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(script)
	hash := hex.EncodeToString(sum[:])
	for _, test := range []struct {
		name, digest     string
		symlink, allowed bool
	}{{"bad-digest", strings.Repeat("a", 64), false, false}, {"symlink", hash, true, false}, {"exact", hash, false, true}} {
		t.Run(test.name, func(t *testing.T) {
			path := binary
			if test.symlink {
				path = filepath.Join(dir, "link")
				if e := os.Symlink(binary, path); e != nil {
					t.Fatal(e)
				}
			}
			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", uninstallScript, "arm-uninstall", path, test.digest, runtime.GOOS)
			cmd.Stdin = strings.NewReader("exact original helper request")
			e := cmd.Run()
			if test.allowed {
				if e != nil {
					t.Fatal(e)
				}
				raw, e := os.ReadFile(marker)
				if e != nil || string(raw) != "exact original helper request" {
					t.Fatal("stdin scope changed", e)
				}
			} else {
				if e == nil {
					t.Fatal("unverified image executed")
				}
				if _, e = os.Stat(marker); !os.IsNotExist(e) {
					t.Fatal("failed gate executed helper", e)
				}
			}
		})
	}
}
