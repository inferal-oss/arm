package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGradleServiceHelpAndCardAreOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "must-stay-absent")
	t.Setenv("PATH", t.TempDir())
	for _, command := range [][]string{{"run", "--help"}, {"agents", "card", "gradle-build", "--markdown"}} {
		var out, stderr bytes.Buffer
		if code := Main(append([]string{"--home", home}, command...), strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatal(command, code, out.String(), stderr.String())
		}
		text := out.String() + stderr.String()
		for _, want := range []string{"gradle-service", "--detach", "--service", "ready"} {
			if !strings.Contains(text, want) {
				t.Fatal("missing workflow help", command, want, text)
			}
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline Gradle documentation created state", err)
	}
}

func TestDetachCannotChangeOrdinaryRunFollowing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "must-stay-absent")
	for _, command := range [][]string{{"run", "--detach", "--reason", "bad", "--", "sleep", "1"}, {"run", "--profile", "gradle-build", "--detach", "--reason", "bad", "--", "gradle", "build"}} {
		var out, stderr bytes.Buffer
		if code := Main(append([]string{"--home", home}, command...), strings.NewReader(""), &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "--detach requires --profile gradle-service") {
			t.Fatal("ordinary run detached", code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("invalid detach started backend", err)
	}
}
