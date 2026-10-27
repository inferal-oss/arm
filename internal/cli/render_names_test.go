package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"github.com/creack/pty"
)

type customNameTerminalOutput struct {
	bytes.Buffer
	descriptor uintptr
}

func (out *customNameTerminalOutput) Fd() uintptr { return out.descriptor }

func TestCustomNameHumanCLIUsesSameLeaseHostAndCPUReads(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	t.Setenv("NO_COLOR", "1")
	const exactRevision = 9223372036854775807
	lease := model.Lease{ID: nameLease, Host: nameHost, Revision: exactRevision, Request: model.Request{Reason: "exact canonical request"}}
	view := cliLeaseView{Lease: lease, DisplayName: "Build\x1b[2J\nline", HostDisplayName: "CustomHost"}
	status := map[string]any{"host": nameHost, "display_name": "CustomHost", "resources": []any{map[string]any{"id": nameHost + "/cpu", "host": nameHost, "kind": "cpu", "unit": "cores", "display_name": "Compute", "availability_known": true, "budget_total": json.Number("1"), "reserved": json.Number("0.25"), "available": json.Number("0.75"), "users": []any{map[string]any{"lease": nameLease, "lease_display_name": "ConsumerLabel", "quantity": json.Number("9223372036854775807"), "state": "active"}}}}, "leases": []cliLeaseView{view}}
	var mu sync.Mutex
	var calls []string
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, q.Operation)
		switch q.Operation {
		case "status":
			return status, nil
		case "inspect":
			var params struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(q.Params, &params)
			if params.ID != nameLease {
				t.Error("render name replaced request ID", params.ID)
			}
			return view, nil
		case "storage_list":
			return []cliLeaseView{view}, nil
		case "mesh_dispatch":
			var request struct {
				Host    string
				Request struct {
					Operation string
					Params    json.RawMessage
				}
			}
			_ = json.Unmarshal(q.Params, &request)
			var params struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(request.Request.Params, &params)
			if request.Host != namePeer || request.Request.Operation != "inspect" || params.ID != nameLease {
				t.Error("rendering changed routed target", string(q.Params))
			}
			remote := lease
			remote.Host = namePeer
			return cliLeaseView{Lease: remote, DisplayName: "RemoteLease", HostDisplayName: "RemoteHost"}, nil
		default:
			return nil, &model.Error{Code: "unexpected_read"}
		}
	})
	invoke := func(args ...string) string {
		t.Helper()
		out := &customNameTerminalOutput{descriptor: slave.Fd()}
		var diagnostic bytes.Buffer
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), out, &diagnostic); code != 0 || diagnostic.Len() != 0 {
			t.Fatal(args, code, out.String(), diagnostic.String())
		}
		return out.String()
	}
	text := invoke("status")
	for _, want := range []string{"Host: CustomHost (" + nameHost + ")", "Compute (live) (" + nameHost + "/cpu)", `Build\u001b[2J\nline (` + nameLease + ")", "ConsumerLabel (" + nameLease + ")", "9223372036854775807"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status lost %q: %s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") || strings.Contains(text, "Build\x1b[2J\nline") {
		t.Fatal("raw control text reached terminal", text)
	}
	text = invoke("inspect", nameLease)
	for _, want := range []string{`Name: Build\u001b[2J\nline`, "ID: " + nameLease, "Host: CustomHost (" + nameHost + ")", "Revision: 9223372036854775807"} {
		if !strings.Contains(text, want) {
			t.Fatalf("inspect lost %q: %s", want, text)
		}
	}
	text = invoke("storage", "list")
	if !strings.Contains(text, `Name: Build\u001b[2J\nline`) || !strings.Contains(text, nameLease) {
		t.Fatal("typed lease array dropped provided label", text)
	}
	text = invoke("inspect", nameLease, "--json")
	var exact model.Lease
	if err := json.Unmarshal([]byte(text), &exact); err != nil || !reflect.DeepEqual(exact, lease) {
		t.Fatal("human labels changed canonical JSON model", exact, err, text)
	}
	text = invoke("inspect", nameLease, "--host", namePeer)
	if !strings.Contains(text, "Name: RemoteLease") || !strings.Contains(text, "Host: RemoteHost ("+namePeer+")") || strings.Contains(text, "CustomHost") || !strings.Contains(text, "ID: "+nameLease) {
		t.Fatal("routed response borrowed local labels or lost exact IDs", text)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"status", "inspect", "storage_list", "inspect", "status", "status", "mesh_dispatch"}) {
		t.Fatal("rendering introduced RPCs", calls)
	}
}

func TestCustomNameTypedRenderAdaptersKeepIdentityAndSnapshotScope(t *testing.T) {
	lease := model.Lease{ID: nameLease, Host: nameHost, Revision: 9223372036854775807, Request: model.Request{Resources: model.Resources{Memory: 9223372036854775807}}}
	mode := "local"
	var mu sync.Mutex
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		mu.Lock()
		defer mu.Unlock()
		switch mode {
		case "local":
			return cliLeaseView{Lease: lease, DisplayName: "LocalLease", HostDisplayName: "LocalHost"}, nil
		case "remote":
			remote := lease
			remote.Host = namePeer
			return cliLeaseView{Lease: remote, DisplayName: "RemoteLease", HostDisplayName: "RemoteHost"}, nil
		case "missing":
			return lease, nil
		case "nil":
			return []cliLeaseView(nil), nil
		case "empty":
			return []cliLeaseView{}, nil
		}
		return nil, &model.Error{Code: "forbidden"}
	})
	a := customNameApp(home)
	var result model.Lease
	if err := a.call("inspect", nil, &result); err != nil || !reflect.DeepEqual(result, lease) {
		t.Fatal("typed adapter changed exact model", result, err)
	}
	if got := a.humanName(readablename.Lease, nameHost, nameLease, "", ""); got != "LocalLease" {
		t.Fatal(got)
	}
	mu.Lock()
	mode = "remote"
	mu.Unlock()
	if err := a.call("inspect", nil, &result); err != nil || result.ID != lease.ID || result.Host != namePeer || result.Revision != lease.Revision {
		t.Fatal(result, err)
	}
	if got := a.humanName(readablename.Lease, namePeer, nameLease, "", ""); got != "RemoteLease" {
		t.Fatal(got)
	}
	if got := a.humanName(readablename.Lease, nameHost, nameLease, "", ""); got != "" && got != readablename.FromID(readablename.Lease, nameLease) {
		t.Fatal("remote response borrowed local label", got)
	}
	if got := a.humanName(readablename.Host, nameHost, nameHost, "", ""); got == "RemoteHost" {
		t.Fatal("host labels crossed authority")
	}
	mu.Lock()
	mode = "missing"
	mu.Unlock()
	if err := a.call("inspect", nil, &result); err != nil {
		t.Fatal(err)
	}
	if got := a.humanName(readablename.Lease, nameHost, nameLease, "", ""); got != readablename.FromID(readablename.Lease, nameLease) {
		t.Fatal("unlabelled response retained stale label", got)
	}
	for _, current := range []string{"nil", "empty"} {
		mu.Lock()
		mode = current
		mu.Unlock()
		rows := []model.Lease{lease}
		if err := a.call("storage_list", nil, &rows); err != nil || (rows == nil) != (current == "nil") || len(rows) != 0 || len(a.renderNames) != 0 {
			t.Fatal("typed array lost nil/empty or retained old names", current, rows, err, a.renderNames)
		}
	}
}

func TestCustomNameHumanProvidedSuppliedAndGeneratedPriority(t *testing.T) {
	a := customNameApp("")
	generated := readablename.FromID(readablename.Host, nameHost)
	for _, tt := range []struct{ provided, supplied, want string }{{"CustomHost", "LegacyHost", "CustomHost"}, {generated, "LegacyHost", "LegacyHost"}, {"", "LegacyHost", "LegacyHost"}, {"", "", generated}} {
		if got := a.humanName(readablename.Host, nameHost, nameHost, tt.provided, tt.supplied); got != tt.want {
			t.Fatal(tt, got)
		}
	}
	var out bytes.Buffer
	a.resourceTable(&out, []any{map[string]any{"id": namePool, "kind": "custom", "display_name": readablename.FromID(readablename.Pool, namePool), "custom": map[string]any{"pool": map[string]any{"name": "Legacy pool"}}}})
	if !strings.Contains(out.String(), "Legacy pool ("+namePool+")") {
		t.Fatal("generated label masked supplied pool name", out.String())
	}
}
