package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func TestStartReusesActualRunningAuthorityAndPreservesBudgets(t *testing.T) {
	home := dashboardTestBackend(t)
	var before, after model.Status
	if err := client.Call(t.Context(), home, "status", nil, &before); err != nil {
		t.Fatal(err)
	}
	policy, exists, err := mesh.ReadStartupPolicy(home)
	if err != nil || !exists {
		t.Fatal(exists, err)
	}
	endpoint, err := local.ReadEndpoint(home)
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Main([]string{"--home", home, "start", "--cpu", "1", "--json"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	current, err := local.ReadEndpoint(home)
	if err != nil || current != endpoint {
		t.Fatal("idempotent start replaced authority", current, err)
	}
	if err = client.Call(t.Context(), home, "status", nil, &after); err != nil || before.Capacity.CPU != after.Capacity.CPU {
		t.Fatal("idempotent start changed CPU budget", after.Capacity, err)
	}
	afterPolicy, exists, err := mesh.ReadStartupPolicy(home)
	if err != nil || !exists || !reflect.DeepEqual(policy, afterPolicy) {
		t.Fatal("idempotent start changed configured budget ceilings", afterPolicy, err)
	}
}
func TestStartRefusesBusyUnknownAuthorityAndCopiedOtherHomeEndpoint(t *testing.T) {
	home := filepath.Join(t.TempDir(), "busy")
	lock, err := local.Lock(home, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unix.Flock(int(lock.Fd()), unix.LOCK_UN); lock.Close() }()
	var out, stderr bytes.Buffer
	if code := Main([]string{"--home", home, "start", "--json"}, strings.NewReader(""), &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "busy") {
		t.Fatal(code, out.String(), stderr.String())
	}
	if _, err = os.Stat(filepath.Join(home, "state.db")); !os.IsNotExist(err) {
		t.Fatal("busy authority created state", err)
	}
	actual := dashboardTestBackend(t)
	endpoint, err := local.ReadEndpoint(actual)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "copied")
	if err = local.Prepare(copied); err != nil {
		t.Fatal(err)
	}
	if err = local.AtomicJSON(filepath.Join(copied, "endpoint.json"), endpoint); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := Main([]string{"--home", copied, "start", "--json"}, strings.NewReader(""), &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "node_generation_changed") {
		t.Fatal(code, out.String(), stderr.String())
	}
	if _, err = os.Stat(filepath.Join(copied, "state.db")); !os.IsNotExist(err) {
		t.Fatal("wrong-home status started authority", err)
	}
}
