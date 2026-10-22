package cli

import (
	"bytes"
	"encoding/json"
	"github.com/yrashk/arm/internal/mesh"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeshConnectorOfflineReviewAndImmutableNativePaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	invoke := func(args ...string) (int, string) {
		t.Helper()
		var out, err bytes.Buffer
		code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &err)
		return code, out.String() + err.String()
	}
	for _, args := range [][]string{{"mesh", "connector", "--help"}, {"help", "mesh", "connector"}, {"help", "mesh", "connector", "add"}, {"mesh", "connector", "list"}, {"mesh", "operations"}, {"mesh", "connector", "add", "fixture", "test-host", "--binary", "/private/test/arm", "--remote-home", "/private/test/state", "--reason", "private fixture", "--dry-run"}} {
		if code, out := invoke(args...); code != 0 {
			t.Fatal(code, out)
		}
		if _, e := os.Stat(home); !os.IsNotExist(e) {
			t.Fatal("offline review created state", e)
		}
	}
	if code, _ := invoke("mesh", "connector", "add", "fixture", "test-host", "--reason", "review"); code == 0 {
		t.Fatal("machine connector saved without --yes")
	}
	if code, out := invoke("mesh", "connector", "add", "fixture", "test-host", "--binary", "/private/test/arm", "--remote-home", "/private/test/state", "--reason", "private fixture", "--yes"); code != 0 {
		t.Fatal(code, out)
	}
	before, e := mesh.ReadConnectors(home)
	if e != nil || len(before) != 1 || before[0].Actor.UID == "" || before[0].At.IsZero() {
		t.Fatal(before, e)
	}
	if code, _ := invoke("mesh", "connector", "add", "fixture", "other-host", "--reason", "replace", "--yes"); code == 0 {
		t.Fatal("immutable connector replaced")
	}
	after, _ := mesh.ReadConnectors(home)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("failed replacement changed reviewed connector")
	}
}
