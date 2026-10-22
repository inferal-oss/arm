package cargoprofile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCargoPolicyExclusivePrivateFileAndNormalNetworkEnvironment(t *testing.T) {
	root, id := allocationFixture(t)
	owned, e := CreateOwnedPaths(context.Background(), root, id, strings.Repeat("a", 32), func(BirthStep) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte("[build]\njobs=1\n")
	evidence, e := createOwnedPolicy(context.Background(), owned, raw)
	if e != nil {
		t.Fatal(e)
	}
	if evidence.SHA256 == "" || evidence.Mode == 0 {
		t.Fatal(evidence)
	}
	if _, e = createOwnedPolicy(context.Background(), owned, raw); e == nil {
		t.Fatal("policy reused/overwritten")
	}
	if e = os.Chmod(filepath.Join(owned.Control.Path, "policy.toml"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = inspectOwnedPolicy(owned); e == nil {
		t.Fatal("nonprivate policy accepted")
	}
	got := strings.Join(compilationEnvironment([]string{"CARGO_NET_OFFLINE=false", "RUSTUP_AUTO_INSTALL=1"}, Toolchain{}), "\n")
	if !strings.Contains(got, "CARGO_NET_OFFLINE=false") || !strings.Contains(got, "RUSTUP_AUTO_INSTALL=0") || strings.Contains(got, "CARGO_NET_OFFLINE=true") {
		t.Fatal("normal compilation forced offline or tool installation enabled")
	}
	for _, selector := range []string{"RUSTUP_HOME", "RUSTUP_TOOLCHAIN"} {
		source := filepath.Join(root, "source-"+selector)
		write(t, filepath.Join(source, ".cargo", "config.toml"), "[env]\n"+selector+"={value='private-canary',force=true}\n")
		if _, e = Discover(context.Background(), source, filepath.Join(root, "cache"), []string{"HOME=" + root}); e == nil || strings.Contains(e.Error(), "private-canary") {
			t.Fatal("config Rustup selection accepted/disclosed", e)
		}
	}
}
