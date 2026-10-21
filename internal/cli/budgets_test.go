package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBudgetTypedLimitsKeepZeroClearAndExactIntegers(t *testing.T) {
	limits, err := parseBudgetLimits(`{"cpu":0,"memory_bytes":null,"disk_bytes":0,"pools":{"exact":9007199254740993}}`)
	if err != nil || limits.CPU == nil || *limits.CPU != 0 || limits.Memory != nil || limits.Disk == nil || limits.Pools["exact"] != 9007199254740993 {
		t.Fatal(limits, err)
	}
	cleared, err := parseBudgetLimits(`{}`)
	if err != nil || cleared.CPU != nil || cleared.Memory != nil || cleared.Disk != nil || len(cleared.Pools) != 0 {
		t.Fatal(cleared, err)
	}
	for _, raw := range []string{`null`, `[]`, `{"cpu":-1}`, `{"memory_bytes":1.5}`, `{"disk_bytes":9223372036854775808}`, `{"pools":{"x":-1}}`, `{"unknown":1}`, `{"pools":{"x":null}}`, `{} {}`, `{"cpu":1e999}`} {
		if _, err = parseBudgetLimits(raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	if limits.Pools["exact"] <= int64(math.MaxInt32) {
		t.Fatal("integer truncated")
	}
}
func TestBudgetOfflineHelpAndInvalidSelectorsDoNotCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, action := range []string{"", "list", "inspect", "consumers", "history", "lookup", "create", "set", "rename", "describe", "drain", "resume", "delete"} {
		args := []string{"--home", home, "budget"}
		if action != "" {
			args = append(args, action)
		}
		args = append(args, "--help")
		var out, stderr bytes.Buffer
		if code := Main(args, strings.NewReader(""), &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "Usage: arm") {
			t.Fatalf("%v: %d %s %s", args, code, &out, &stderr)
		}
	}
	for _, line := range []string{"budget set self --reason test --revision 1", "budget set self --limits {} --reason test", "budget set self --limits null --reason test --revision 1", "budget create x --owner 0 --reason test", "budget list --owner 0", "budget inspect", "budget history self --after -1", "budget consumers self --limit 101", "budget lookup", "budget create x --limits {\"cpu\":-1} --reason test"} {
		var out, stderr bytes.Buffer
		args := append([]string{"--home", home}, strings.Fields(line)...)
		if code := Main(args, strings.NewReader(""), &out, &stderr); code != 2 || out.Len() != 0 || !json.Valid(stderr.Bytes()) {
			t.Fatalf("%s: %d %s %s", line, code, &out, &stderr)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline validation created state", err)
	}
}
