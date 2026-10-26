package cli_test

import (
	"bytes"
	"github.com/inferal-oss/arm/internal/cli"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchedulingDeadlineHelpAndInvalidTimestamp(t *testing.T) {
	for _, cmd := range [][]string{{"acquire"}, {"run"}, {"storage", "create"}, {"ports", "reserve"}} {
		var out, stderr bytes.Buffer
		if code := cli.Main(append(cmd, "--help"), strings.NewReader(""), &out, &stderr); code != 0 || !strings.Contains(out.String(), "--start-deadline") || !strings.Contains(out.String(), "RFC3339") {
			t.Fatalf("%v: %d %s %s", cmd, code, &out, &stderr)
		}
	}
	home := filepath.Join(t.TempDir(), "absent")
	var out, stderr bytes.Buffer
	code := cli.Main([]string{"--home", home, "acquire", "--reason", "bad timestamp", "--start-deadline", "5m"}, strings.NewReader(""), &out, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "absolute RFC3339") {
		t.Fatalf("code %d out %s error %s", code, &out, &stderr)
	}
}
