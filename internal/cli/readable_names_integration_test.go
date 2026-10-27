package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/cli"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"getarm.org/internal/server"
)

func TestReadableNamesDisposableBackendPersistence(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	var stop context.CancelFunc
	var done chan error
	start := func() {
		ctx, cancel := context.WithCancel(context.Background())
		stop, done = cancel, make(chan error, 1)
		go func() {
			done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1 << 30, Disk: 1 << 30})
		}()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
				return
			}
			select {
			case err := <-done:
				stop()
				stop = nil
				t.Fatalf("backend exited during start: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				stop()
				t.Fatal("backend not ready")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	shutdown := func() {
		stop()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		stop = nil
	}
	t.Cleanup(func() {
		if stop != nil {
			stop()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	})
	call := func(result any, args ...string) {
		t.Helper()
		var out, errout bytes.Buffer
		if code := cli.Main(append([]string{"--home", home}, args...), bytes.NewReader(nil), &out, &errout); code != 0 {
			t.Fatalf("arm %q: code %d, out %s, err %s", args, code, out.String(), errout.String())
		}
		if result != nil {
			if err := json.Unmarshal(out.Bytes(), result); err != nil {
				t.Fatal(args, err, out.String())
			}
		}
	}
	start()
	var change model.PoolChange
	call(&change, "pool", "create", "browser slots", "--scope", "host", "--capacity", "1", "--reason", "readable-name integration", "--key", "names-pool")
	poolID := change.Pool.ID
	poolAlias := readablename.FromID(readablename.Pool, poolID)
	var lease model.Lease
	call(&lease, "acquire", "--cpu", "0", "--pool", poolAlias+"=1", "--reason", "readable-name permit", "--key", "names-lease")
	if len(lease.Request.Pools) != 1 || lease.Request.Pools[0].Pool != poolID {
		t.Fatal("alias persisted instead of original pool ID", lease.Request.Pools)
	}
	leaseID, hostID := lease.ID, lease.Host
	leaseAlias, hostAlias := readablename.FromID(readablename.Lease, leaseID), readablename.FromID(readablename.Host, hostID)
	var inspected model.Lease
	call(&inspected, "inspect", leaseAlias, "--host", hostAlias)
	if inspected.ID != leaseID || inspected.Host != hostID {
		t.Fatal("alias selected wrong lease", inspected)
	}
	for _, selector := range []string{"browser slots", poolAlias, poolID} {
		var pool model.PoolView
		call(&pool, "pool", "inspect", selector)
		if pool.Pool.ID != poolID || pool.Pool.Name != "browser slots" {
			t.Fatal("supplied pool name changed", pool.Pool)
		}
	}
	var before struct {
		Host, DisplayName string
		Leases            []struct {
			ID          string
			DisplayName string `json:"display_name"`
		}
	}
	// The JSON tag is explicit for additive wire labels; CLI status carries the
	// already-serialized view while typed lease output keeps its old contract.
	var status map[string]json.RawMessage
	call(&status, "status", "--host", hostAlias)
	if err := json.Unmarshal(status["host"], &before.Host); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(status["display_name"], &before.DisplayName); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(status["leases"], &before.Leases); err != nil {
		t.Fatal(err)
	}
	if before.Host != hostID || before.DisplayName != hostAlias || len(before.Leases) != 1 || before.Leases[0].ID != leaseID || before.Leases[0].DisplayName != leaseAlias {
		t.Fatal("missing stable labels", before)
	}
	call(&inspected, "release", leaseAlias)
	if inspected.ID != leaseID || inspected.State != "terminal" {
		t.Fatal(inspected)
	}
	shutdown()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "state.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = db.QueryRow(`SELECT data FROM leases WHERE id=?`, leaseID).Scan(&raw); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if strings.Contains(raw, "display_name") || strings.Contains(raw, leaseAlias) || strings.Contains(raw, poolAlias) {
		_ = db.Close()
		t.Fatal("derived aliases were persisted", raw)
	}
	_ = db.Close()
	start()
	call(&inspected, "inspect", leaseAlias, "--host", hostAlias)
	if inspected.ID != leaseID || inspected.Host != hostID || inspected.State != "terminal" {
		t.Fatal("restart changed alias binding", inspected)
	}
	call(&status, "status", "--host", hostAlias)
	var afterName string
	if err = json.Unmarshal(status["display_name"], &afterName); err != nil || afterName != before.DisplayName {
		t.Fatal("restart renamed host", afterName, err)
	}
	shutdown()
}
