package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestResidentOperatorUnconfirmedOrMalformedApplyHasNoEffects(t *testing.T) {
	for _, name := range []string{"unconfirmed", "malformed-expect", "unexpected-positional"} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "absent-home")
			args := []string{"--home", home, "resident", "authorize", "apply", "--profile", filepath.Join(home, "profile.json"), "--evidence", filepath.Join(home, "evidence.json"), "--key", "negative-operator", "--reason", "negative pre-effect validation"}
			switch name {
			case "malformed-expect":
				args = append(args, "--yes", "--expect", "malformed")
			case "unexpected-positional":
				args = append(args, "unexpected")
			}
			var out, stderr bytes.Buffer
			if code := Main(args, bytes.NewReader(nil), &out, &stderr); code == 0 {
				t.Fatal("invalid apply succeeded", out.String())
			}
			if _, err := os.Lstat(home); !os.IsNotExist(err) {
				t.Fatal("offline refusal created home/state", err)
			}
		})
	}
}
