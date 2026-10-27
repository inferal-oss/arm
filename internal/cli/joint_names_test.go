package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"getarm.org/internal/customnames"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const jointOtherPool = "pool_66666666666666666666666666666666"

func TestCustomNameResourceExactIDBeatsOtherNamespaceAlias(t *testing.T) {
	cpu := nameHost + "/cpu"
	var mu sync.Mutex
	var mutations []nameMutation
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		mu.Lock()
		defer mu.Unlock()
		switch q.Operation {
		case "status":
			return model.Status{Host: nameHost, Resources: []model.ResourceView{{ID: cpu, Host: nameHost, Kind: "cpu"}, {ID: namePool, Kind: "custom", Custom: &model.PoolView{Pool: model.Pool{ID: namePool, Name: cpu, Authority: nameHost}}}}}, nil
		case "name_mutate":
			var request nameMutation
			_ = json.Unmarshal(q.Params, &request)
			mutations = append(mutations, request)
			return nameSnapshot{Document: nameDocument()}, nil
		default:
			t.Error("exact selector consulted alias metadata", q.Operation)
			return nil, &model.Error{Code: "forbidden"}
		}
	})
	for _, target := range []string{cpu, namePool} {
		var out, diagnostic bytes.Buffer
		if code := Main([]string{"--home", home, "name", "set", "resource", target, "Label", "--revision", "0"}, strings.NewReader(""), &out, &diagnostic); code != 0 {
			t.Fatal("exact ID lost precedence to another namespace's supplied alias", target, code, diagnostic.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(mutations) != 2 || mutations[0].Kind != "resource" || mutations[0].ID != cpu || mutations[1].Kind != "pool" || mutations[1].ID != namePool || mutations[0].BindingLease != "" || mutations[1].BindingLease != "" {
		t.Fatal("exact union selection changed kind or ID", mutations)
	}
}

func TestCustomNameJointPoolSelectorsRejectLaterSuppliedCollision(t *testing.T) {
	for _, mode := range []string{"supplied-only", "custom-only", "same-ID", "distinct-ID", "inactive", "wrong-authority"} {
		t.Run(mode, func(t *testing.T) {
			firstName, secondName := "Original", "Shared"
			entry := nameEntry("pool", namePool, "Shared")
			var entries []customnames.Entry
			want := jointOtherPool
			switch mode {
			case "custom-only":
				secondName = "Other"
				entries = []customnames.Entry{entry}
				want = namePool
			case "same-ID":
				firstName = "Shared"
				secondName = "Other"
				entries = []customnames.Entry{entry}
				want = namePool
			case "distinct-ID":
				entries = []customnames.Entry{entry}
			case "inactive":
				entry.Active = false
				entries = []customnames.Entry{entry}
			case "wrong-authority":
				entry.AuthorityHost = namePeer
				entries = []customnames.Entry{entry}
			}
			views := []model.PoolView{{Pool: model.Pool{ID: namePool, Name: firstName, Authority: nameHost}}, {Pool: model.Pool{ID: jointOtherPool, Name: secondName, Authority: nameHost}}}
			var mu sync.Mutex
			var targets []string
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				mu.Lock()
				defer mu.Unlock()
				switch q.Operation {
				case "status":
					return model.Status{Host: nameHost, Resources: []model.ResourceView{{ID: namePool, Kind: "custom", Custom: &views[0]}, {ID: jointOtherPool, Kind: "custom", Custom: &views[1]}}}, nil
				case "pool_list":
					return views, nil
				case "name_list":
					return nameSnapshot{Document: nameDocument(entries...)}, nil
				case "pool_inspect":
					var params struct {
						ID string `json:"id"`
					}
					_ = json.Unmarshal(q.Params, &params)
					targets = append(targets, params.ID)
					return views[0], nil
				case "acquire":
					var request model.Request
					_ = json.Unmarshal(q.Params, &request)
					if len(request.Pools) != 1 {
						t.Error("missing pool claim", request)
					} else {
						targets = append(targets, request.Pools[0].Pool)
					}
					return model.Lease{ID: nameLease, Host: nameHost, State: "active", Request: request}, nil
				case "name_mutate":
					var request nameMutation
					_ = json.Unmarshal(q.Params, &request)
					if request.Kind != "pool" || request.AuthorityHost != nameHost {
						t.Error("resource pool namespace not normalized", request)
					}
					targets = append(targets, request.ID)
					return nameSnapshot{Document: nameDocument(entries...)}, nil
				default:
					return nil, &model.Error{Code: "unexpected_operation"}
				}
			})
			for _, args := range [][]string{{"pool", "inspect", "Shared"}, {"acquire", "--cpu", "0", "--pool", "Shared=1", "--reason", "joint selector fixture", "--key", "joint-selector"}, {"name", "set", "pool", "Shared", "NewLabel", "--revision", "1"}, {"name", "set", "resource", "Shared", "NewLabel", "--revision", "1"}} {
				var out, diagnostic bytes.Buffer
				code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &diagnostic)
				if mode == "distinct-ID" {
					if code != 1 || !strings.Contains(diagnostic.String(), "ambiguous_name") || out.Len() != 0 {
						t.Fatal(args, code, out.String(), diagnostic.String())
					}
				} else if code != 0 {
					t.Fatal(args, code, diagnostic.String())
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if mode == "distinct-ID" {
				if len(targets) != 0 {
					t.Fatal("ambiguous selector reached action", targets)
				}
			} else if !reflect.DeepEqual(targets, []string{want, want, want, want}) {
				t.Fatal("supplied/custom alternatives drifted", mode, targets, want)
			}
		})
	}
}

func TestCustomNameJointHostSelectorsAndRunKeepOriginalSelector(t *testing.T) {
	for _, mode := range []string{"supplied-only", "custom-only", "same-ID", "distinct-ID", "identity-drift"} {
		t.Run(mode, func(t *testing.T) {
			localName, peerName := "OriginalLocal", "Shared"
			var entries []customnames.Entry
			want := namePeer
			if mode != "supplied-only" {
				entries = []customnames.Entry{nameEntry("host", nameHost, "Shared")}
			}
			if mode == "custom-only" {
				peerName = "Other"
				want = nameHost
			}
			if mode == "same-ID" {
				localName = "Shared"
				peerName = "Other"
				want = nameHost
			}
			registry := model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Name: localName, Local: true}, {Host: namePeer, Name: peerName, Trust: model.NodeTrust{State: "active"}}}}
			var mu sync.Mutex
			var runs []string
			runPhase := false
			runRegistryReads := 0
			zero := 0
			terminal := func(host string) model.Lease {
				return model.Lease{ID: nameLease, Host: host, State: "terminal", ExitCode: &zero}
			}
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				mu.Lock()
				defer mu.Unlock()
				switch q.Operation {
				case "node_list":
					if runPhase && mode == "identity-drift" {
						runRegistryReads++
						if runRegistryReads > 1 {
							copy := registry
							copy.Nodes = append([]model.NodeEntry{}, registry.Nodes...)
							copy.Nodes[1].Name = "Other"
							return copy, nil
						}
					}
					return registry, nil
				case "name_list":
					return nameSnapshot{Document: nameDocument(entries...)}, nil
				case "status":
					return model.Status{Host: nameHost}, nil
				case "run":
					var request model.Request
					_ = json.Unmarshal(q.Params, &request)
					runs = append(runs, request.Host)
					return terminal(nameHost), nil
				case "output":
					return []byte{}, nil
				case "inspect":
					return terminal(nameHost), nil
				case "mesh_dispatch":
					var envelope struct {
						Host    string
						Request mesh.Request
					}
					_ = json.Unmarshal(q.Params, &envelope)
					switch envelope.Request.Operation {
					case "run":
						var request model.Request
						_ = json.Unmarshal(envelope.Request.Params, &request)
						if request.Host != envelope.Host {
							t.Error("canonical routed host changed", request.Host, envelope.Host)
						}
						runs = append(runs, envelope.Host)
						return terminal(envelope.Host), nil
					case "output":
						return []byte{}, nil
					case "inspect":
						return terminal(envelope.Host), nil
					}
					return nil, &model.Error{Code: "unexpected_remote_operation"}
				default:
					return nil, &model.Error{Code: "unexpected_operation"}
				}
			})
			a := customNameApp(home)
			for _, resolve := range []func(string) (string, error){a.resolveHost, a.nameAuthority} {
				id, err := resolve("Shared")
				if mode == "distinct-ID" || mode == "identity-drift" {
					if customNameCode(err) != "host_unavailable" {
						t.Fatal(id, err)
					}
				} else if err != nil || id != want {
					t.Fatal(id, err, want)
				}
			}
			entry, err := a.nameTarget("host", "Shared", "localhost")
			if mode == "distinct-ID" || mode == "identity-drift" {
				if customNameCode(err) != "ambiguous_name" {
					t.Fatal(entry, err)
				}
			} else if err != nil || entry.ID != want || entry.AuthorityHost != want {
				t.Fatal(entry, err, want)
			}
			f := a.flags("node")
			host := f.String("host", "localhost", "")
			if err := a.parse(f, []string{"--host", "Shared"}, false); err != nil {
				t.Fatal(err)
			}
			err = a.resolveValidatedAliases(f)
			if mode == "distinct-ID" || mode == "identity-drift" {
				if customNameCode(err) != "host_unavailable" || *host != "Shared" {
					t.Fatal(*host, err)
				}
			} else if err != nil || *host != want {
				t.Fatal(*host, err)
			}
			mu.Lock()
			runPhase = true
			mu.Unlock()
			var out, diagnostic bytes.Buffer
			code := Main([]string{"--home", home, "run", "--host", "Shared", "--cwd", "/private/tmp", "--reason", "joint run fixture", "--key", "joint-run", "--", "/usr/bin/true"}, strings.NewReader(""), &out, &diagnostic)
			mu.Lock()
			defer mu.Unlock()
			if mode == "distinct-ID" || mode == "identity-drift" {
				if code != 1 || !strings.Contains(diagnostic.String(), "host_unavailable") || len(runs) != 0 {
					t.Fatal("classification erased alias before joint check", code, diagnostic.String(), runs)
				}
			} else {
				expected := want
				if want == nameHost {
					expected = "localhost"
				}
				if code != 0 || !reflect.DeepEqual(runs, []string{expected}) {
					t.Fatal("valid run lost exact checked identity", code, diagnostic.String(), runs, expected)
				}
			}
		})
	}
}

func TestCustomNameJointMapFailureKeepsExactAndGeneratedRecovery(t *testing.T) {
	for _, mode := range []string{"denied", "diagnostic", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var maps, inspects int
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				mu.Lock()
				defer mu.Unlock()
				switch q.Operation {
				case "status":
					return model.Status{Host: nameHost}, nil
				case "pool_list":
					return []model.PoolView{{Pool: model.Pool{ID: namePool, Name: "Shared"}}}, nil
				case "node_list":
					return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Name: "Shared", Local: true}}}, nil
				case "name_list":
					maps++
					if mode == "denied" {
						return nil, &model.Error{Code: "forbidden"}
					}
					snapshot := nameSnapshot{Document: nameDocument()}
					if mode == "diagnostic" {
						snapshot.NamingDiagnostic = &model.Error{Code: "name_metadata_invalid"}
					} else {
						snapshot.Version = 99
					}
					return snapshot, nil
				case "pool_inspect":
					inspects++
					return model.PoolView{Pool: model.Pool{ID: namePool}}, nil
				}
				return nil, &model.Error{Code: "unexpected_operation"}
			})
			a := customNameApp(home)
			if _, err := a.resolveHost("Shared"); err == nil {
				t.Fatal("supplied host bypassed failed joint map")
			}
			if _, err := a.nameAuthority("Shared"); err == nil {
				t.Fatal("authority bypassed failed joint map")
			}
			if _, err := a.nameTarget("host", "Shared", "localhost"); err == nil {
				t.Fatal("name target bypassed failed joint map")
			}
			for _, target := range []string{"Shared", namePool, readablename.FromID(readablename.Pool, namePool)} {
				var out, diagnostic bytes.Buffer
				code := Main([]string{"--home", home, "pool", "inspect", target}, strings.NewReader(""), &out, &diagnostic)
				if (target == "Shared") != (code != 0) {
					t.Fatal(target, code, out.String(), diagnostic.String())
				}
			}
			mu.Lock()
			before := maps
			mu.Unlock()
			for _, target := range []string{nameHost, readablename.FromID(readablename.Host, nameHost)} {
				if id, err := a.resolveHost(target); err != nil || id != nameHost {
					t.Fatal(target, id, err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if maps != before || inspects != 2 {
				t.Fatal("canonical/generated recovery consulted map or supplied action escaped", maps, before, inspects)
			}
		})
	}
}

func TestCustomNameJointCompletionRefusesCollisionsAndPartialNamespace(t *testing.T) {
	for _, mode := range []string{"collision", "host-collision", "pool-map-denied"} {
		t.Run(mode, func(t *testing.T) {
			poolEntry := nameEntry("pool", namePool, "Shared")
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				switch q.Operation {
				case "node_list":
					return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Name: "Original", Local: true}, {Host: namePeer, Name: "Shared", Trust: model.NodeTrust{State: "active"}}}}, nil
				case "pool_list":
					return []model.PoolView{{Pool: model.Pool{ID: namePool, Name: "Original"}}, {Pool: model.Pool{ID: jointOtherPool, Name: "Shared"}}}, nil
				case "status":
					return model.Status{Host: nameHost, Resources: []model.ResourceView{{ID: namePool, Kind: "custom", Custom: &model.PoolView{Pool: model.Pool{ID: namePool, Name: "Original"}}}, {ID: jointOtherPool, Kind: "custom", Custom: &model.PoolView{Pool: model.Pool{ID: jointOtherPool, Name: "Shared"}}}}}, nil
				case "name_list":
					var params nameListRequest
					_ = json.Unmarshal(q.Params, &params)
					if mode == "pool-map-denied" && params.Kind == "pool" {
						return nil, &model.Error{Code: "forbidden"}
					}
					entries := []customnames.Entry{poolEntry}
					if mode == "host-collision" {
						entries = append(entries, nameEntry("host", nameHost, "Shared"))
					}
					return nameSnapshot{Document: nameDocument(entries...)}, nil
				default:
					t.Error("completion escaped selected read scope", q.Operation)
					return nil, &model.Error{Code: "unexpected_operation"}
				}
			})
			args := []string{"__complete", "pool", "inspect", "Shared"}
			if mode == "host-collision" {
				args = []string{"__complete", "inspect", "--host", "Shared", ""}
			}
			if mode == "pool-map-denied" {
				args = []string{"__complete", "name", "set", "resource", "Shared"}
			}
			out, diagnostic, code := completionCLI(t, home, args...)
			if code != 0 || strings.Contains(out, "Shared\t") || strings.Contains(out, "Shared\n") || strings.Contains(diagnostic, "forbidden") {
				t.Fatal(mode, code, out, diagnostic)
			}
		})
	}
}

func TestCustomNameExplicitOfflineRecoveryRejectsAuthenticatedFailures(t *testing.T) {
	home := filepath.Join(t.TempDir(), "source")
	source, err := mesh.Init(home, nameHost, "OriginalLocal", "127.0.0.1:41001", "127.0.0.1:41001")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	peer, err := mesh.Init(filepath.Join(t.TempDir(), "peer"), namePeer, "Shared", "127.0.0.1:41002", "127.0.0.1:41002")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if _, err = source.Pair(peer.Config().Identity, mesh.Grant{Permissions: []string{"observe-host"}}, strings.Repeat("ab", 32), "explicit offline selector fixture"); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, "mesh", "config.json")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	a := customNameApp(home)
	if id, err := a.resolveHostRecovery("Shared"); err != nil || id != namePeer {
		t.Fatal("missing endpoint lost explicit offline recovery", id, err)
	}
	if _, err := a.resolveHost("Shared"); err == nil {
		t.Fatal("generic supplied host silently used offline registry")
	}
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := "ws" + strings.TrimPrefix(closed.URL, "http")
	closed.Close()
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: url, Token: "test-secret"}); err != nil {
		t.Fatal(err)
	}
	if id, err := a.resolveHostRecovery("Shared"); err != nil || id != namePeer {
		t.Fatal("dial-unavailable lost explicit offline recovery", id, err)
	}
	for _, mode := range []string{"forbidden", "diagnostic", "malformed-map", "auth", "eof", "malformed-response", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "auth" {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				connection, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer connection.CloseNow()
				if mode == "eof" {
					return
				}
				var q model.WireRequest
				if wsjson.Read(r.Context(), connection, &q) != nil {
					return
				}
				if mode == "malformed-response" {
					_ = connection.Write(r.Context(), websocket.MessageText, []byte(`not JSON`))
					return
				}
				response := model.WireResponse{Version: 1, ID: q.ID}
				if q.Operation == "node_list" {
					response.Result = model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Name: "OriginalLocal", Local: true}, {Host: namePeer, Name: "Shared", Trust: model.NodeTrust{State: "active"}}}}
				} else {
					snapshot := nameSnapshot{Document: nameDocument()}
					if mode == "forbidden" {
						response.Error = &model.Error{Code: "forbidden"}
					} else if mode == "diagnostic" {
						snapshot.NamingDiagnostic = &model.Error{Code: "name_metadata_invalid"}
					} else if mode == "malformed-map" {
						snapshot.Version = 99
					}
					response.Result = snapshot
				}
				_ = wsjson.Write(r.Context(), connection, response)
			}))
			t.Cleanup(backend.Close)
			if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: "ws" + strings.TrimPrefix(backend.URL, "http"), Token: "test-secret"}); err != nil {
				t.Fatal(err)
			}
			current := customNameApp(home)
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				current.ctx = ctx
			}
			if id, err := current.resolveHostRecovery("Shared"); err == nil || id != "" {
				t.Fatal("authenticated/protocol/context failure authorized offline fallback", mode, id, err)
			}
		})
	}
	after, err := os.ReadFile(config)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("name recovery changed registry", err)
	}
	if offlineNamingBackend(context.Canceled) || offlineNamingBackend(context.DeadlineExceeded) || offlineNamingBackend(model.Err("forbidden", "")) {
		t.Fatal("cancellation or denial classified as offline")
	}
}
