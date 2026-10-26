package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicKubernetesKeygenRoute(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Main([]string{"--home", t.TempDir(), "--json", "mesh", "kubernetes", "keygen"}, strings.NewReader(""), &out, &stderr)
	if code != 0 {
		t.Fatalf("public route failed: %d %s", code, stderr.String())
	}
	var result struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Key) != 64 {
		t.Fatalf("public keygen result: %s %v", out.String(), err)
	}
}

func TestPublicKubernetesGroupHelpIsOffline(t *testing.T) {
	for _, args := range [][]string{{"mesh", "kubernetes", "--help"}, {"help", "mesh", "kubernetes"}, {"mesh", "kubernetes"}} {
		var out, stderr bytes.Buffer
		home := t.TempDir()
		code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		if code != 0 || !strings.Contains(out.String(), "contexts") || !strings.Contains(out.String(), "nodes") {
			t.Fatalf("%v: exit%d stdout=%q stderr=%q", args, code, out.String(), stderr.String())
		}
	}
}

func TestPublicKubernetesLeafHelpIsOffline(t *testing.T) {
	for _, action := range []string{"contexts", "nodes", "keygen", "plan", "apply", "lookup", "reconcile", "ship"} {
		var out, stderr bytes.Buffer
		code := Main([]string{"--home", t.TempDir(), "mesh", "kubernetes", action, "--help"}, strings.NewReader(""), &out, &stderr)
		if code != 0 || !strings.Contains(out.String(), "Usage: arm mesh kubernetes "+action) {
			t.Fatalf("%s: exit%d stdout=%q stderr=%q", action, code, out.String(), stderr.String())
		}
	}
}
