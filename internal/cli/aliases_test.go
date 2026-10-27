package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func aliasBackend(t *testing.T, results map[string]any) (*app, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var ops []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		var q model.WireRequest
		if wsjson.Read(r.Context(), c, &q) != nil {
			return
		}
		mu.Lock()
		ops = append(ops, q.Operation)
		result, ok := results[q.Operation]
		mu.Unlock()
		response := model.WireResponse{Version: 1, ID: q.ID, Result: result}
		if !ok {
			response.Result = nil
			response.Error = &model.Error{Code: "forbidden", Message: "fixture denies inventory"}
		}
		_ = wsjson.Write(r.Context(), c, response)
	}))
	t.Cleanup(s.Close)
	home := t.TempDir()
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: "ws" + strings.TrimPrefix(s.URL, "http"), Token: "fixture-token"}); err != nil {
		t.Fatal(err)
	}
	a := &app{home: home, ctx: context.Background(), out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	return a, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, ops...) }
}

func TestAliasSelectorsBecomeExactIDsBeforeRequests(t *testing.T) {
	leaseID := "11111111111111111111111111111111"
	hostID := "22222222222222222222222222222222"
	poolID := "pool_33333333333333333333333333333333"
	a, ops := aliasBackend(t, map[string]any{
		"status":    model.Status{Host: hostID, Leases: []model.Lease{{ID: leaseID}}},
		"pool_list": []model.PoolView{{Pool: model.Pool{ID: poolID, Name: "browser slots"}}},
		"node_list": model.NodeRegistry{Nodes: []model.NodeEntry{{Host: hostID, Name: "chosen-host", Local: true}}},
	})
	f := a.flags("run")
	host := f.String("host", "localhost", "")
	storage := f.String("storage", "", "")
	var pools poolFlags
	f.Var(&pools, "pool", "")
	if err := a.parse(f, []string{"--host", readablename.FromID(readablename.Host, hostID), "--storage", readablename.FromID(readablename.Lease, leaseID), "--pool", "browser slots=2", "program", "--host", "child-only"}, true); err != nil {
		t.Fatal(err)
	}
	if len(ops()) != 0 {
		t.Fatal("parser performed alias I/O before handler validation", ops())
	}
	if err := a.resolveValidatedAliases(f); err != nil {
		t.Fatal(err)
	}
	if *host != hostID || *storage != leaseID || len(pools) != 1 || pools[0].Pool != poolID || strings.Join(f.Args(), " ") != "program --host child-only" {
		t.Fatal(*host, *storage, pools, f.Args())
	}
	if localHost, err := a.executionHost(readablename.FromID(readablename.Host, hostID)); err != nil || localHost != "localhost" {
		t.Fatal("named local host lost native execution defaults", localHost, err)
	}
	for _, op := range ops() {
		if op != "status" && op != "pool_list" && op != "node_list" {
			t.Fatal("alias lookup mutated", op)
		}
	}
	for _, command := range []string{"inspect", "release", "cancel", "renew", "reconcile", "usage", "storage cleanup", "storage handoff", "storage retain", "storage export", "reviews policy", "reviews configure"} {
		f := flag.NewFlagSet(command, flag.ContinueOnError)
		_ = f.Parse([]string{readablename.FromID(readablename.Lease, leaseID)})
		if err := a.resolveValidatedAliases(f); err != nil || f.Arg(0) != leaseID {
			t.Fatal(command, f.Args(), err)
		}
	}
	for _, target := range []string{"browser slots", readablename.FromID(readablename.Pool, poolID)} {
		f := flag.NewFlagSet("pool inspect", flag.ContinueOnError)
		_ = f.Parse([]string{target})
		if err := a.resolveValidatedAliases(f); err != nil || f.Arg(0) != poolID {
			t.Fatal(f.Args(), err)
		}
	}
}
func TestAliasIncompleteAmbiguousAndUnauthorizedInventoryRefused(t *testing.T) {
	id := "11111111111111111111111111111111"
	other := "pool_22222222222222222222222222222222"
	for _, tt := range []struct {
		name            string
		results         map[string]any
		command, target string
	}{
		{"denied", map[string]any{}, "inspect", readablename.FromID(readablename.Lease, id)},
		{"ambiguous", map[string]any{"pool_list": []model.PoolView{{Pool: model.Pool{ID: "pool_" + id, Name: "shared"}}, {Pool: model.Pool{ID: other, Name: "shared", State: "deleted"}}}}, "pool inspect", "shared"},
		{"wrong-domain", map[string]any{"status": model.Status{Leases: []model.Lease{{ID: id}}}}, "inspect", readablename.FromID(readablename.Host, id)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, ops := aliasBackend(t, tt.results)
			f := flag.NewFlagSet(tt.command, flag.ContinueOnError)
			_ = f.Parse([]string{tt.target})
			if err := a.resolveValidatedAliases(f); err == nil || f.Arg(0) != tt.target {
				t.Fatal("unsafe resolution", err, f.Args())
			}
			for _, op := range ops() {
				if op != "status" && op != "pool_list" {
					t.Fatal(op)
				}
			}
		})
	}
	a, _ := aliasBackend(t, map[string]any{"node_list": model.NodeRegistry{Error: &model.Error{Code: "registry_unavailable"}, Nodes: []model.NodeEntry{{Host: id, Name: "partial", Local: true}}}})
	if _, err := a.resolveHost("partial"); err == nil {
		t.Fatal("partial registry established uniqueness")
	}
}
func TestAliasIDsAndHelpRemainOffline(t *testing.T) {
	a := &app{home: filepath.Join(t.TempDir(), "absent"), ctx: context.Background(), out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	f := a.flags("inspect")
	f.String("host", "localhost", "")
	id := "11111111111111111111111111111111"
	if err := a.parse(f, []string{id}, false); err != nil || f.Arg(0) != id {
		t.Fatal(err)
	}
	f = a.flags("inspect")
	if err := a.parse(f, []string{"alias-that-needs-inventory", "--help"}, false); err != flag.ErrHelp {
		t.Fatal(err)
	}
}

func TestAliasHostNamesKeepOfflineRecoveryAndCanonicalIDs(t *testing.T) {
	home := filepath.Join(t.TempDir(), "source")
	sourceID, peerID := strings.Repeat("1", 32), strings.Repeat("2", 32)
	source, err := mesh.Init(home, sourceID, "chosen-source", "127.0.0.1:41001", "127.0.0.1:41001")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	peer, err := mesh.Init(filepath.Join(t.TempDir(), "peer"), peerID, "chosen-peer", "127.0.0.1:41002", "127.0.0.1:41002")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if _, err = source.Pair(peer.Config().Identity, mesh.Grant{Permissions: []string{"observe-host"}}, strings.Repeat("ab", 32), "offline alias fixture"); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "mesh", "config.json")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{home: home, ctx: context.Background(), out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	for _, tt := range []struct{ target, want string }{
		{"chosen-source", sourceID}, {readablename.FromID(readablename.Host, sourceID), sourceID},
		{"chosen-peer", peerID}, {readablename.FromID(readablename.Host, peerID), peerID},
	} {
		got, err := a.resolveHost(tt.target)
		if err != nil || got != tt.want {
			t.Fatal(tt.target, got, err)
		}
	}
	for _, command := range []string{"ssh lookup", "ssh seal", "mesh reconnect"} {
		f := flag.NewFlagSet(command, flag.ContinueOnError)
		_ = f.Parse([]string{"chosen-peer"})
		if err := a.resolveValidatedAliases(f); err != nil || f.Arg(0) != peerID {
			t.Fatal(command, f.Args(), err)
		}
	}
	for _, target := range []string{"chosen-peer", readablename.FromID(readablename.Host, peerID)} {
		if got, err := a.executionHost(target); err != nil || got != peerID {
			t.Fatal("remote execution name contacted backend or changed ID", got, err)
		}
		for _, cwd := range []string{"", "relative-directory"} {
			args := []string{"--home", home, "run", "--host", target, "--reason", "invalid remote CWD fixture"}
			if cwd != "" {
				args = append(args, "--cwd", cwd)
			}
			args = append(args, "--", "/usr/bin/true")
			var out, errout bytes.Buffer
			if code := Main(args, strings.NewReader(""), &out, &errout); code != 2 || !strings.Contains(errout.String(), "remote run requires explicit absolute --cwd") || strings.Contains(errout.String(), "endpoint.json") {
				t.Fatal("remote CWD validation performed backend work", code, out.String(), errout.String())
			}
		}
	}
	// A generic exact-host field must keep the original ID, not "localhost".
	f := flag.NewFlagSet("agent endpoints", flag.ContinueOnError)
	host := f.String("host", "", "")
	_ = f.Parse([]string{"--host", "chosen-source"})
	if err := a.resolveValidatedAliases(f); err != nil || *host != sourceID {
		t.Fatal(*host, err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("alias read changed registry", err)
	}
	if _, err = os.Stat(filepath.Join(home, "endpoint.json")); !os.IsNotExist(err) {
		t.Fatal("offline resolution initialized backend", err)
	}
}

func TestAliasInvalidCommandsNeverReadBackendBeforeHandlerValidation(t *testing.T) {
	a, ops := aliasBackend(t, map[string]any{})
	lease := readablename.FromID(readablename.Lease, strings.Repeat("1", 32))
	pool := readablename.FromID(readablename.Pool, "pool_"+strings.Repeat("2", 32))
	host := readablename.FromID(readablename.Host, strings.Repeat("3", 32))
	for _, tt := range []struct {
		args []string
		code int
	}{
		{[]string{"renew", lease}, 2}, {[]string{"renew", lease, "--ttl", "-1m"}, 2},
		{[]string{"pool", "set", pool, "2", "--reason", "test"}, 2},
		{[]string{"pool", "adjust", pool, "0", "--reason", "test"}, 2},
		{[]string{"pool", "create", "child", "--parent", pool, "--capacity", "1", "--reason", "test"}, 2},
		{[]string{"pool", "create", "slots", "--scope", "global", "--host", host, "--capacity", "1", "--reason", "test"}, 2},
		{[]string{"acquire", "--pool", pool + "=1"}, 2},
		{[]string{"run", "--reason", "test", "--storage", lease}, 2},
		{[]string{"run", "--reason", "test", "--host", host}, 2},
		{[]string{"run", "--reason", "test", "--pool", pool + "=1", "--usage-grace", "1s", "--", "echo", "test"}, 2},
		{[]string{"run", "--reason", "test", "--storage", lease, "--", "/arm-fixture-no-such-executable"}, 2},
		{[]string{"storage", "retain", lease, "--ttl", "0s", "--reason", "test"}, 2},
		{[]string{"reviews", "configure", lease, "--revision", "1"}, 2},
		{[]string{"message", "operator", "--lease", lease}, 1},
		{[]string{"mesh", "revoke", host}, 2},
		{[]string{"mesh", "stop", host, "--timeout", "-1"}, 2},
		{[]string{"mesh", "uninstall", host, "--lookup"}, 2},
		{[]string{"ssh", "lookup", host}, 2},
		{[]string{"monitor", "--host", host, "--after", "1"}, 2},
		{[]string{"node", "--host", host, "extra"}, 2},
		{[]string{"agent", "endpoints", "--host", host, "--workspace", "invalid-workspace"}, 1},
		{[]string{"workspaces", "retire", "--host", host, "extra"}, 2},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var out, errout bytes.Buffer
			code := Main(append([]string{"--home", a.home}, tt.args...), strings.NewReader(""), &out, &errout)
			if code != tt.code || out.Len() != 0 || errout.Len() == 0 {
				t.Fatal(code, out.String(), errout.String())
			}
			if len(ops()) != 0 {
				t.Fatal("invalid command performed backend I/O", ops())
			}
		})
	}
}

func TestAliasPoolNamesNeverReadWrongAuthorityInventory(t *testing.T) {
	localID, remoteID := strings.Repeat("1", 32), strings.Repeat("2", 32)
	poolID := "pool_" + strings.Repeat("3", 32)
	for _, target := range []string{remoteID, "known-peer", readablename.FromID(readablename.Host, remoteID)} {
		t.Run("remote-"+target, func(t *testing.T) {
			a, ops := aliasBackend(t, map[string]any{
				"status":    model.Status{Host: localID},
				"node_list": model.NodeRegistry{Nodes: []model.NodeEntry{{Host: localID, Local: true}, {Host: remoteID, Name: "known-peer"}}},
				"pool_list": []model.PoolView{{Pool: model.Pool{ID: poolID, Name: "local-slots"}}},
			})
			f := a.flags("acquire")
			f.String("host", "localhost", "")
			var claims poolFlags
			f.Var(&claims, "pool", "")
			if err := a.parse(f, []string{"--host", target, "--pool", "local-slots=1"}, false); err != nil {
				t.Fatal(err)
			}
			err := a.resolveValidatedAliases(f)
			var detail *model.Error
			if !errors.As(err, &detail) || detail.Code != "unsupported_remote_claim" {
				t.Fatal(err)
			}
			for _, op := range ops() {
				if op == "pool_list" {
					t.Fatal("remote claim read local pool names", ops())
				}
			}
			if claims[0].Pool != "local-slots" {
				t.Fatal("refused remote alias was rewritten", claims)
			}
		})
	}
	for _, target := range []string{"localhost", localID, readablename.FromID(readablename.Host, localID)} {
		t.Run("local-"+target, func(t *testing.T) {
			a, _ := aliasBackend(t, map[string]any{"status": model.Status{Host: localID}, "node_list": model.NodeRegistry{Nodes: []model.NodeEntry{{Host: localID, Local: true}}}, "pool_list": []model.PoolView{{Pool: model.Pool{ID: poolID, Name: "local-slots"}}}})
			f := a.flags("acquire")
			f.String("host", "localhost", "")
			var claims poolFlags
			f.Var(&claims, "pool", "")
			if err := a.parse(f, []string{"--host", target, "--pool", "local-slots=1"}, false); err != nil {
				t.Fatal(err)
			}
			if err := a.resolveValidatedAliases(f); err != nil || claims[0].Pool != poolID {
				t.Fatal(claims, err)
			}
		})
	}
	a, ops := aliasBackend(t, map[string]any{})
	f := a.flags("acquire")
	f.String("host", "localhost", "")
	var claims poolFlags
	f.Var(&claims, "pool", "")
	if err := a.parse(f, []string{"--host", remoteID, "--pool", poolID + "=1"}, false); err != nil {
		t.Fatal(err)
	}
	if err := a.resolveValidatedAliases(f); err != nil || len(ops()) != 0 || claims[0].Pool != poolID {
		t.Fatal("exact IDs changed remote backend contract", claims, ops(), err)
	}
}

func TestAliasHostErrorsPreservePeerAndSSHCodes(t *testing.T) {
	localID, firstID, secondID := strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32)
	config := mesh.Config{Identity: mesh.Identity{Host: localID, Name: "local-name"}, Peers: []mesh.Peer{
		{Identity: mesh.Identity{Host: firstID, Name: "duplicate"}}, {Identity: mesh.Identity{Host: secondID, Name: "duplicate"}},
	}}
	a, ops := aliasBackend(t, map[string]any{"node_list": model.NodeRegistry{Nodes: []model.NodeEntry{
		{Host: localID, Name: "local-name", Local: true}, {Host: firstID, Name: "duplicate"}, {Host: secondID, Name: "duplicate"},
	}}})
	for _, target := range []string{"missing-peer", "duplicate"} {
		_, oldPeerErr := mesh.ResolveConfig(config, target)
		var oldPeerDetail *model.Error
		if !errors.As(oldPeerErr, &oldPeerDetail) || oldPeerDetail.Code != "host_unavailable" {
			t.Fatal("legacy peer fixture changed", oldPeerErr)
		}
		for _, resolve := range []func(string) (string, error){a.resolveHost, a.executionHost} {
			_, err := resolve(target)
			var detail *model.Error
			if !errors.As(err, &detail) || detail.Code != oldPeerDetail.Code || !strings.Contains(detail.Message, "full stable host ID") {
				t.Fatal("peer name error contract changed", target, err)
			}
		}
		for _, command := range []string{"node", "status", "resources"} {
			var out, errout bytes.Buffer
			code := Main([]string{"--home", a.home, command, "--host", target}, strings.NewReader(""), &out, &errout)
			var envelope struct {
				Error *model.Error `json:"error"`
			}
			if err := json.Unmarshal(errout.Bytes(), &envelope); err != nil || code != 1 || envelope.Error == nil || envelope.Error.Code != oldPeerDetail.Code || out.Len() != 0 {
				t.Fatal(command, target, code, out.String(), errout.String(), err)
			}
		}
		_, oldSSHError := historyTarget(config, target)
		var oldSSHDetail *model.Error
		if !errors.As(oldSSHError, &oldSSHDetail) || oldSSHDetail.Code != "not_found" {
			t.Fatal("legacy SSH history fixture changed", oldSSHError)
		}
		for _, command := range []string{"ssh", "ssh lookup", "ssh seal"} {
			f := flag.NewFlagSet(command, flag.ContinueOnError)
			_ = f.Parse([]string{target})
			err := a.resolveValidatedAliases(f)
			var detail *model.Error
			if !errors.As(err, &detail) || detail.Code != oldSSHDetail.Code || !strings.Contains(detail.Message, "full stable host ID") {
				t.Fatal("SSH selector error contract changed", command, target, err)
			}
		}
	}
	for _, op := range ops() {
		if op != "node_list" {
			t.Fatal("failed host resolution performed another operation", ops())
		}
	}
	for _, tt := range []struct {
		err  error
		code string
	}{{readablename.ErrNotFound, "name_not_found"}, {readablename.ErrAmbiguous, "ambiguous_name"}} {
		var detail *model.Error
		if !errors.As(aliasError(tt.err), &detail) || detail.Code != tt.code {
			t.Fatal("lease/pool alias errors changed", tt.err)
		}
	}
}
