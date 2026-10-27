package cli

import (
	"bytes"
	"encoding/json"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMeshLifecycleInspectAndHelpNeverCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"mesh", "connect", "--help"}, {"mesh", "disconnect", "--help"}, {"mesh", "reconnect", "--help"}, {"help", "mesh", "reconnect"}, {"help", "mesh", "connect"}, {"mesh", "bootstrap"}} {
		var out, errout bytes.Buffer
		input := ""
		if args[len(args)-1] == "bootstrap" {
			input = `{"Action":"lifecycle_inspect"}`
		}
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(input), &out, &errout); code != 0 {
			t.Fatalf("%v %d %s", args, code, &errout)
		}
		if input != "" {
			var value map[string]any
			if json.Unmarshal(out.Bytes(), &value) != nil || value["running"] != false || value["protocol"] != float64(1) {
				t.Fatal(out.String())
			}
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("read-only operation created state", err)
		}
	}
	for _, input := range []string{`{"Action":"lifecycle_inspect","Injected":true}`, `{"Action":"lifecycle_inspect"} {}`, `{"Action":"lifecycle_ensure"}`} {
		var out, errout bytes.Buffer
		if code := Main([]string{"--home", home, "mesh", "bootstrap"}, strings.NewReader(input), &out, &errout); code == 0 {
			t.Fatal("invalid/unreviewed helper accepted", input)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("invalid helper wrote state", err)
		}
	}
}

func TestMeshReconnectInvalidAndAbsentDraftsCreateNoState(t *testing.T) {
	for _, args := range [][]string{{"reconnect", "node-name", "--dry-run"}, {"reconnect", strings.Repeat("a", 32), "--dry-run"}, {"reconnect", strings.Repeat("A", 32), "--dry-run"}, {"reconnect", strings.Repeat("a", 32), "--resume"}, {"reconnect", strings.Repeat("a", 32), "--local-ip", "not-an-ip", "--dry-run"}, {"reconnect", strings.Repeat("a", 32), "--remote-ip", "[::1]", "--dry-run"}} {
		home := filepath.Join(t.TempDir(), "absent")
		var out, errout bytes.Buffer
		if code := Main(append([]string{"--home", home, "mesh"}, args...), strings.NewReader(""), &out, &errout); code == 0 {
			t.Fatal("invalid/no-registry reconnect succeeded", args, out.String())
		}
		if _, e := os.Lstat(home); !os.IsNotExist(e) {
			t.Fatal("reconnect validation created state", args, e)
		}
	}
}

func TestMeshReconnectKnownKeyOfflineDefaultsAndExplicitConflict(t *testing.T) {
	home := filepath.Join(t.TempDir(), "source")
	remote := filepath.Join(t.TempDir(), "target")
	if e := local.Prepare(home); e != nil {
		t.Fatal(e)
	}
	if e := local.Prepare(remote); e != nil {
		t.Fatal(e)
	}
	s, e := mesh.Init(home, strings.Repeat("a", 32), "source", "127.0.0.1:32101", "127.0.0.1:32101")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	target, e := mesh.Init(remote, strings.Repeat("b", 32), "target", "127.0.0.1:32102", "127.0.0.1:32102")
	if e != nil {
		t.Fatal(e)
	}
	defer target.Close()
	reason := "previous exact explicitly reviewed purpose"
	draft := meshops.ReconnectDraft{Host: target.Config().Identity.Host, Destination: "never-contact-this-fixture", Reason: reason, Forward: mesh.Grant{Permissions: []string{"observe-own"}, MaxTTL: 2 * time.Hour}, Reverse: mesh.Grant{Permissions: []string{}}}
	r := meshops.ReconnectRequest{Draft: draft, Binding: meshops.ReconnectBinding{Local: s.Config().Identity, Prior: mesh.Peer{Identity: target.Config().Identity, State: "revoked", Pair: strings.Repeat("c", 64), Revision: 4, Grant: mesh.Grant{Permissions: []string{}}}, Selected: mesh.Connector{ID: "reconnect", Destination: draft.Destination, Binary: "arm", Home: remote, Reason: reason}}}
	key := "saved-reconnect"
	digest, e := mesh.ReviewDigest(r)
	if e != nil {
		t.Fatal(e)
	}
	guard, e := mesh.ClaimCoordination(home, key, "reconnect", digest, "host:"+draft.Host)
	if e != nil {
		t.Fatal(e)
	}
	request, e := json.Marshal(meshops.StartRequest{Key: key, Kind: "reconnect", Reconnect: &r})
	if e != nil {
		t.Fatal(e)
	}
	if e = guard.Change(func(op *mesh.Coordination) error {
		op.Request = request
		op.Phase = "completed"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(home, "mesh", "config.json")
	if e = os.Rename(config, config+".retained-fixture"); e != nil {
		t.Fatal(e)
	}
	before, _, e := mesh.ReadCoordination(home, key)
	if e != nil {
		t.Fatal(e)
	}
	beforeRaw, _ := json.Marshal(before)
	// A failing executable would leave this sentinel if any lookup reached SSH.
	bin := t.TempDir()
	marker := filepath.Join(bin, "called")
	if e = os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nprintf called > '"+marker+"'\nexit 91\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin)
	for _, extra := range [][]string{nil, {"--reason", reason}, {"--dry-run"}} {
		var out, errs bytes.Buffer
		args := append([]string{"--home", home, "--json", "mesh", "reconnect", draft.Host, "--key", key}, extra...)
		if code := Main(args, strings.NewReader(""), &out, &errs); code != 0 {
			t.Fatal(code, out.String(), errs.String())
		}
		var got meshops.Operation
		if e = json.Unmarshal(out.Bytes(), &got); e != nil || got.Key != key || got.Kind != "reconnect" {
			t.Fatal("not saved receipt", e, out.String())
		}
	}
	for _, extra := range [][]string{{"--reason", "different"}, {"--ssh", "other-fixture"}, {"--grant", "observe-host"}} {
		var out, errs bytes.Buffer
		args := append([]string{"--home", home, "--json", "mesh", "reconnect", draft.Host, "--key", key}, extra...)
		if code := Main(args, strings.NewReader(""), &out, &errs); code == 0 || !strings.Contains(errs.String()+out.String(), "idempotency_conflict") {
			t.Fatal("changed explicit intent accepted", code, out.String(), errs.String())
		}
	}
	after, _, e := mesh.ReadCoordination(home, key)
	if e != nil {
		t.Fatal(e)
	}
	afterRaw, _ := json.Marshal(after)
	if !bytes.Equal(beforeRaw, afterRaw) {
		t.Fatal("offline lookup changed durable receipt")
	}
	if _, e = os.Lstat(marker); !os.IsNotExist(e) {
		t.Fatal("offline retry invoked SSH", e)
	}
	if _, e = os.Lstat(config); !os.IsNotExist(e) {
		t.Fatal("offline retry recreated trust", e)
	}
}
