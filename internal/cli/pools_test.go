package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/cli"
	"getarm.org/internal/model"
	"getarm.org/internal/server"
)

func TestCLICountedPoolsScopesSignedAdjustmentsAndExactNumbers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var childOutput string
	call := func(args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		if code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, &out, &stderr)
		}
		childOutput = stderr.String()
		return out.String()
	}
	var created model.PoolChange
	if err := json.Unmarshal([]byte(call("pool", "create", "browser-slots", "--scope", "global", "--capacity", "3", "--reason", "coordinate browsers")), &created); err != nil {
		t.Fatal(err)
	}
	if created.Pool.Scope != "global" || created.Pool.Host != "" || created.Actor == "" {
		t.Fatal("global definition is missing scope/audit")
	}
	id := created.Pool.ID
	var rejectedOut, rejectedErr bytes.Buffer
	code := cli.Main([]string{"--home", home, "pool", "adjust", id, "1", "--revision", "999", "--reason", "stale change", "--key", "recoverable-error-key"}, strings.NewReader(""), &rejectedOut, &rejectedErr)
	if code != 1 || rejectedOut.Len() != 0 || !strings.Contains(rejectedErr.String(), "recoverable-error-key") || !strings.Contains(rejectedErr.String(), "revision_conflict") {
		t.Fatalf("mutation error lost the retry key: %d %s %s", code, &rejectedOut, &rejectedErr)
	}
	var adjusted model.PoolChange
	args := []string{"pool", "adjust", id, "-1", "--reason", "one browser unavailable", "--key", "cli-adjust"}
	json.Unmarshal([]byte(call(args...)), &adjusted)
	if adjusted.Pool.Capacity != 2 || adjusted.Delta != -1 {
		t.Fatalf("signed positional delta: %+v", adjusted)
	}
	var retry model.PoolChange
	json.Unmarshal([]byte(call(args...)), &retry)
	if retry.Sequence != adjusted.Sequence {
		t.Fatal("CLI retry applied delta twice")
	}
	var lease model.Lease
	json.Unmarshal([]byte(call("acquire", "--pool", id+"=2", "--reason", "test browsers")), &lease)
	if lease.Request.Resources.CPU != 0 || len(lease.Request.Pools) != 1 || lease.Request.Pools[0].Quantity != 2 {
		t.Fatalf("custom-only claim reserved CPU or lost count: %+v", lease)
	}
	call("release", lease.ID)
	out := call("run", "--pool", id+"=1", "--reason", "supervised pool consumer", "--", "/bin/sh", "-c", `test -n "$ARM_POOLS" && printf '%s' "$ARM_POOLS"`)
	var claims []model.PoolClaim
	if !json.Valid([]byte(out)) || json.Unmarshal([]byte(childOutput), &claims) != nil || len(claims) != 1 || claims[0].Scope != "global" || claims[0].Quantity != 1 {
		t.Fatalf("supervised metadata: %s", out)
	}
	json.Unmarshal([]byte(call("pool", "create", "large", "--scope", "host", "--capacity", "9223372036854775807", "--reason", "exact count fixture")), &created)
	output := call("pool", "inspect", created.Pool.ID)
	if !strings.Contains(output, "9223372036854775807") {
		t.Fatalf("inspect rounded integer: %s", output)
	}
	output = call("resources")
	if !strings.Contains(output, "9223372036854775807") {
		t.Fatalf("resources rounded integer: %s", output)
	}
	history := call("pool", "history", id)
	if !strings.Contains(history, "one browser unavailable") || !strings.Contains(history, "coordinate browsers") {
		t.Fatal("journal omitted reasons")
	}
}
