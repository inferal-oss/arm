package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

const nameHost = "11111111111111111111111111111111"
const namePeer = "22222222222222222222222222222222"
const nameLease = "33333333333333333333333333333333"
const nameOtherLease = "44444444444444444444444444444444"
const namePool = "pool_55555555555555555555555555555555"

func nameDocument(entries ...customnames.Entry) customnames.Document {
	revision := int64(0)
	if len(entries) > 0 {
		revision = 1
	}
	return customnames.Document{Version: 1, Owner: strconv.Itoa(os.Geteuid()), Host: nameHost, Revision: revision, Entries: append([]customnames.Entry{}, entries...)}
}
func nameEntry(kind, id, label string) customnames.Entry {
	return customnames.Entry{Kind: kind, AuthorityHost: nameHost, ID: id, Name: label, Active: true}
}
func customNameApp(home string) *app {
	return &app{home: home, ctx: context.Background(), in: strings.NewReader(""), out: &bytes.Buffer{}, err: &bytes.Buffer{}}
}
func customNameCode(err error) string {
	var detail *model.Error
	if errors.As(err, &detail) {
		return detail.Code
	}
	return ""
}

func TestCustomNameCLIArgumentValidationBeforeReads(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"name", "set", "lease", nameLease, "Build"},
		{"name", "set", "lease", nameLease, "Build", "--revision", "-1"},
		{"name", "set", "lease", nameLease, "Build", "--revision", "9223372036854775808"},
		{"name", "set", "thing", nameLease, "Build", "--revision", "0"},
		{"name", "set", "lease", nameLease, "bad name", "--revision", "0"},
		{"name", "set", "lease", nameLease, "localhost", "--revision", "0"},
		{"name", "set", "lease", nameLease, nameLease, "--revision", "0"},
		{"name", "set", "lease", nameLease, readablename.FromID(readablename.Lease, nameLease), "--revision", "0"},
		{"name", "set", "lease", nameLease, "Build", "--revision", "0", "--binding-lease", nameLease},
		{"name", "set", "resource", nameHost + "/cpu", "Build", "--revision", "0", "--binding-lease", "wrong"},
		{"name", "set", "lease", "", "Build", "--revision", "0"},
		{"name", "set", "lease", nameLease, "Build", "--revision", "0", "--host", ""},
		{"name", "clear", "lease", nameLease, "Build", "--revision", "0"},
		{"name", "clear", "lease", nameLease, "Build", "--revision", "0", "--host", "localhost"},
		{"name", "clear", "lease", "Build", "Build", "--revision", "0", "--host", nameHost},
		{"name", "clear", "host", namePeer, "Build", "--revision", "0", "--host", nameHost},
		{"name", "clear", "resource", nameHost + "/ports/native/tcp4/127.0.0.1:8080", "Build", "--revision", "0", "--host", nameHost},
		{"name", "list", "--kind", "thing"},
		{"name", "list", "--host", ""},
		{"name", "list", "surplus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			if code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &diagnostic); code != 2 || strings.Contains(diagnostic.String(), "backend") {
				t.Fatalf("invalid arguments reached I/O: code=%d %s", code, diagnostic.String())
			}
		})
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("argument validation created state", err)
	}
}

func TestCustomNameCLILocalCASAndExplicitReuse(t *testing.T) {
	var mu sync.Mutex
	doc := nameDocument()
	var mutations []nameMutation
	var calls []string
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, q.Operation)
		switch q.Operation {
		case "status":
			return model.Status{Host: nameHost, Leases: []model.Lease{{ID: nameLease}, {ID: nameOtherLease}}}, nil
		case "name_list":
			return nameSnapshot{Document: doc}, nil
		case "name_mutate":
			var request nameMutation
			if err := json.Unmarshal(q.Params, &request); err != nil {
				t.Error(err)
			}
			mutations = append(mutations, request)
			next, _, err := customnames.Mutate(doc, request.Operation, customnames.Entry{Kind: request.Kind, AuthorityHost: request.AuthorityHost, ID: request.ID, BindingLease: request.BindingLease, Name: request.Name}, request.ExpectedRevision)
			if errors.Is(err, customnames.ErrRevisionConflict) {
				return nil, &model.Error{Code: "name_revision_conflict", Message: "current revision changed"}
			}
			if err != nil {
				return nil, &model.Error{Code: "name_conflict", Message: err.Error()}
			}
			doc = next
			return nameSnapshot{Document: doc}, nil
		default:
			return nil, &model.Error{Code: "forbidden"}
		}
	})
	a := customNameApp(home)
	invoke := func(wantCode int, wantError string, args ...string) {
		t.Helper()
		code, err := a.names(args)
		if code != wantCode || customNameCode(err) != wantError {
			t.Fatalf("%v: code=%d err=%v", args, code, err)
		}
	}
	invoke(0, "", "set", "lease", nameLease, "Build", "--revision", "0")
	invoke(1, "name_revision_conflict", "set", "lease", nameLease, "Other", "--revision", "0")
	invoke(0, "", "set", "lease", "Build", "Renamed", "--revision", "1")
	invoke(1, "name_conflict", "set", "lease", nameOtherLease, "Build", "--revision", "2")
	invoke(0, "", "clear", "lease", nameLease, "Build", "--host", nameHost, "--revision", "2")
	invoke(0, "", "set", "lease", nameOtherLease, "Build", "--revision", "3")
	mu.Lock()
	defer mu.Unlock()
	if doc.Revision != 4 || len(doc.Entries) != 2 || mutations[2].ID != nameLease || mutations[4].ID != nameLease || mutations[5].ID != nameOtherLease {
		t.Fatal("rename/reuse changed canonical binding", doc, mutations)
	}
	for _, request := range mutations {
		if request.AuthorityHost != nameHost || request.BindingLease != "" {
			t.Fatal(request)
		}
	}
	if !reflect.DeepEqual(calls, []string{"status", "name_mutate", "status", "name_mutate", "status", "name_list", "name_mutate", "status", "name_mutate", "name_mutate", "status", "name_mutate"}) {
		t.Fatal("clear unexpectedly read inventory or mutation retried", calls)
	}
}

func TestCustomNameSelectorsJoinOnlyVisibleExactBindings(t *testing.T) {
	doc := nameDocument(nameEntry("lease", nameLease, "Build"), nameEntry("lease", nameOtherLease, "Hidden"), nameEntry("pool", namePool, "Slots"))
	a, ops := aliasBackend(t, map[string]any{"status": model.Status{Host: nameHost, Leases: []model.Lease{{ID: nameLease}}}, "pool_list": []model.PoolView{{Pool: model.Pool{ID: namePool, Name: "supplied slots"}}}, "name_list": nameSnapshot{Document: doc}})
	for _, selector := range []string{"Build", readablename.FromID(readablename.Lease, nameLease), nameLease} {
		f := flag.NewFlagSet("inspect", flag.ContinueOnError)
		_ = f.Parse([]string{selector})
		if err := a.resolveValidatedAliases(f); err != nil || f.Arg(0) != nameLease {
			t.Fatal(selector, f.Args(), err)
		}
	}
	for _, selector := range []string{"Slots", "supplied slots", readablename.FromID(readablename.Pool, namePool)} {
		f := flag.NewFlagSet("pool inspect", flag.ContinueOnError)
		_ = f.Parse([]string{selector})
		if err := a.resolveValidatedAliases(f); err != nil || f.Arg(0) != namePool {
			t.Fatal(selector, f.Args(), err)
		}
	}
	f := flag.NewFlagSet("inspect", flag.ContinueOnError)
	_ = f.Parse([]string{"Hidden"})
	if err := a.resolveValidatedAliases(f); err == nil || f.Arg(0) != "Hidden" {
		t.Fatal("metadata authorized invisible lease", f.Args(), err)
	}
	for _, op := range ops() {
		if op != "status" && op != "pool_list" && op != "name_list" {
			t.Fatal("resolver mutated", op)
		}
	}
	a, _ = aliasBackend(t, map[string]any{"status": model.Status{Host: nameHost, Leases: []model.Lease{{ID: nameLease}}}, "name_list": nameSnapshot{Document: doc, NamingDiagnostic: &model.Error{Code: "name_metadata_invalid"}}})
	f = flag.NewFlagSet("release", flag.ContinueOnError)
	_ = f.Parse([]string{"Build"})
	if err := a.resolveValidatedAliases(f); customNameCode(err) != "name_metadata_invalid" {
		t.Fatal("custom selector ignored corrupt map", err)
	}
	for _, selector := range []string{nameLease, readablename.FromID(readablename.Lease, nameLease)} {
		f = flag.NewFlagSet("release", flag.ContinueOnError)
		_ = f.Parse([]string{selector})
		if err := a.resolveValidatedAliases(f); err != nil || f.Arg(0) != nameLease {
			t.Fatal("independent canonical/default path consulted corrupt map", err)
		}
	}
	local := nameEntry("host", nameHost, "CustomHost")
	retired := customnames.Entry{Kind: "host", AuthorityHost: namePeer, ID: namePeer, Name: "RetiredPeer", Active: true}
	a, _ = aliasBackend(t, map[string]any{"node_list": model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Name: "SuppliedHost", Local: true}, {Host: namePeer, Trust: model.NodeTrust{State: "revoked"}}}}, "name_list": nameSnapshot{Document: nameDocument(local, retired)}})
	for _, selector := range []string{"CustomHost", "SuppliedHost", readablename.FromID(readablename.Host, nameHost)} {
		if id, err := a.resolveHost(selector); err != nil || id != nameHost {
			t.Fatal("host alternatives dropped", selector, id, err)
		}
	}
	if _, err := a.resolveHost("RetiredPeer"); err == nil {
		t.Fatal("custom map revived a retired peer")
	}
}

func TestCustomNameCLIReadDenialNeverMutatesOrStarts(t *testing.T) {
	for _, operation := range []string{"status", "node_list", "name_list"} {
		t.Run(operation, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, q.Operation)
				if q.Operation == operation {
					return nil, &model.Error{Code: "forbidden"}
				}
				if q.Operation == "status" {
					return model.Status{Host: nameHost, Leases: []model.Lease{{ID: nameLease}}}, nil
				}
				if q.Operation == "node_list" {
					return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Local: true}}}, nil
				}
				if q.Operation == "name_list" {
					return nameSnapshot{Document: nameDocument()}, nil
				}
				return nil, &model.Error{Code: "unexpected_mutation"}
			})
			kind, target := "lease", nameLease
			if operation == "node_list" {
				kind, target = "host", nameHost
			}
			if operation == "name_list" {
				target = "CustomLabel"
			}
			if code, err := customNameApp(home).names([]string{"set", kind, target, "Build", "--revision", "0"}); code == 0 || customNameCode(err) != "forbidden" {
				t.Fatal(code, err)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, call := range calls {
				if call == "name_mutate" {
					t.Fatal("denied inventory authorized naming", calls)
				}
			}
		})
	}
	home := filepath.Join(t.TempDir(), "absent")
	if code, err := customNameApp(home).names([]string{"set", "lease", nameLease, "Build", "--revision", "0"}); code == 0 || err == nil {
		t.Fatal("offline set succeeded", code, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline naming initialized state", err)
	}
}

func TestCustomNameCLIResourceReservationIncarnation(t *testing.T) {
	locator := nameHost + "/ports/native/tcp4/127.0.0.1:8080"
	owner := model.ResourceUse{Lease: nameLease, Relationship: "reservation"}
	consumer := model.ResourceUse{Lease: nameOtherLease, Relationship: "consumer"}
	port := model.ResourceView{ID: locator, Host: nameHost, Kind: "port", Port: &model.PortBinding{}, Users: []model.ResourceUse{consumer, owner}}
	for _, tt := range []struct {
		name      string
		resources []model.ResourceView
		binding   string
		allowed   bool
	}{
		{"owner-is-not-first", []model.ResourceView{port}, "", true},
		{"matching-assertion", []model.ResourceView{port}, nameLease, true},
		{"consumer-assertion", []model.ResourceView{port}, nameOtherLease, false},
		{"no-reservation", []model.ResourceView{{ID: locator, Port: &model.PortBinding{}, Users: []model.ResourceUse{consumer}}}, "", false},
		{"infrastructure", []model.ResourceView{{ID: locator, Port: &model.PortBinding{}}}, "", false},
		{"conflicting-reservations", []model.ResourceView{port, {ID: locator, Port: &model.PortBinding{}, Users: []model.ResourceUse{{Lease: nameOtherLease, Relationship: "reservation"}}}}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got *nameMutation
			var mu sync.Mutex
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				switch q.Operation {
				case "status":
					return model.Status{Host: nameHost, Resources: tt.resources}, nil
				case "name_mutate":
					var request nameMutation
					_ = json.Unmarshal(q.Params, &request)
					mu.Lock()
					got = &request
					mu.Unlock()
					return nameSnapshot{Document: nameDocument()}, nil
				}
				return nil, &model.Error{Code: "forbidden"}
			})
			args := []string{"set", "resource", locator, "Listener", "--revision", "0"}
			if tt.binding != "" {
				args = append(args, "--binding-lease", tt.binding)
			}
			code, err := customNameApp(home).names(args)
			mu.Lock()
			defer mu.Unlock()
			if tt.allowed {
				if code != 0 || err != nil || got == nil || got.BindingLease != nameLease || got.ID != locator || got.AuthorityHost != nameHost {
					t.Fatal(code, err, got)
				}
			} else if code == 0 || got != nil {
				t.Fatal("unsafe incarnation assigned", code, err, got)
			}
		})
	}
	old := nameEntry("resource", locator, "OldListener")
	old.BindingLease = nameLease
	visible := []readablename.Candidate{{ID: locator}}
	joined, err := joinCustomCandidates("resource", nameHost, visible, map[string]string{locator: nameOtherLease}, nameSnapshot{Document: nameDocument(old)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readablename.Resolve("resource", "OldListener", joined, true); !errors.Is(err, readablename.ErrNotFound) {
		t.Fatal("old alias rebound to new port lease", joined, err)
	}
}

func TestCustomNameCLIRemoteAuthorityNeverSelectsWriter(t *testing.T) {
	for _, shape := range []string{"full", "summary"} {
		for _, kind := range []string{"pool", "resource"} {
			for _, selector := range []string{"id", "generated"} {
				t.Run(shape+"/"+kind+"/"+selector, func(t *testing.T) {
					var mu sync.Mutex
					var ops []string
					var mutation nameMutation
					home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
						mu.Lock()
						defer mu.Unlock()
						ops = append(ops, q.Operation)
						switch q.Operation {
						case "status":
							return model.Status{Host: nameHost}, nil
						case "node_list":
							return model.NodeRegistry{Source: nameHost, Nodes: []model.NodeEntry{{Host: nameHost, Local: true}, {Host: namePeer, Trust: model.NodeTrust{State: "active"}}}}, nil
						case "name_list":
							return nameSnapshot{Document: nameDocument()}, nil
						case "mesh_dispatch":
							var request struct {
								Host    string
								Request struct{ Operation string }
							}
							_ = json.Unmarshal(q.Params, &request)
							if request.Host != namePeer || request.Request.Operation != "status" {
								t.Error("unexpected remote operation", string(q.Params))
							}
							resource := model.ResourceView{ID: namePool, Host: namePeer, Kind: "custom"}
							if shape == "full" {
								resource.Custom = &model.PoolView{Pool: model.Pool{ID: namePool, Authority: namePeer, Name: "Remote slots"}}
							}
							return model.Status{Host: namePeer, Resources: []model.ResourceView{resource}}, nil
						case "name_mutate":
							_ = json.Unmarshal(q.Params, &mutation)
							return nameSnapshot{Document: nameDocument()}, nil
						default:
							return nil, &model.Error{Code: "forbidden"}
						}
					})
					target := namePool
					if selector == "generated" {
						target = readablename.FromID(readablename.Pool, namePool)
					}
					code, err := customNameApp(home).names([]string{"set", kind, target, "RemoteSlots", "--host", namePeer, "--revision", "0"})
					mu.Lock()
					beforeCompletion := append([]string(nil), ops...)
					got := mutation
					mu.Unlock()
					if code != 0 || err != nil || got.Kind != "pool" || got.ID != namePool || got.AuthorityHost != namePeer || got.BindingLease != "" || !reflect.DeepEqual(beforeCompletion, []string{"status", "status", "mesh_dispatch", "name_mutate"}) {
						t.Fatal(code, err, got, beforeCompletion)
					}
					out, diagnostic, completionCode := completionCLI(t, home, "__complete", "name", "set", kind, "--host", namePeer, "")
					wantDiagnostic := "Completion ended with directive: ShellCompDirectiveNoFileComp\n"
					if completionCode != 0 || diagnostic != wantDiagnostic || !strings.HasSuffix(out, ":4\n") || !strings.Contains(out, namePool) || !strings.Contains(out, readablename.FromID(readablename.Pool, namePool)) {
						t.Fatal("remote pool summary lost canonical completion", completionCode, out, diagnostic)
					}
					mu.Lock()
					defer mu.Unlock()
					for _, op := range ops[len(beforeCompletion):] {
						if op != "node_list" && op != "mesh_dispatch" && op != "name_list" {
							t.Fatal("completion selected a writer or local pool inventory", ops)
						}
					}
				})
			}
		}
	}
}

func TestCustomNameCompletionCatalogAndReadScope(t *testing.T) {
	lease := nameEntry("lease", nameLease, "Build")
	host := nameEntry("host", nameHost, "CustomHost")
	pool := nameEntry("pool", namePool, "CustomSlots")
	resource := nameEntry("resource", nameHost+"/cpu", "Compute")
	doc := nameDocument(lease, host, pool, resource)
	var mu sync.Mutex
	var ops []string
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, q.Operation)
		switch q.Operation {
		case "node_list":
			return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Name: "SuppliedHost", Local: true}}}, nil
		case "status":
			return model.Status{Host: nameHost, Leases: []model.Lease{{ID: nameLease}}, Resources: []model.ResourceView{{ID: nameHost + "/cpu"}}}, nil
		case "pool_list":
			return []model.PoolView{{Pool: model.Pool{ID: namePool, Name: "SuppliedSlots"}}}, nil
		case "name_list":
			return nameSnapshot{Document: doc}, nil
		}
		return nil, &model.Error{Code: "forbidden"}
	})
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "name", ""}, "set\t"},
		{[]string{"__complete", "name", "set", ""}, "resource"},
		{[]string{"__complete", "name", "list", "--kind", ""}, "lease"},
		{[]string{"__complete", "name", "set", "--"}, "--binding-lease"},
		{[]string{"__complete", "name", "set", "lease", ""}, "Build\tlease name"},
		{[]string{"__complete", "inspect", ""}, "Build\tlease name"},
		{[]string{"__complete", "inspect", "--host", "CustomHost", ""}, "Build\tlease name"},
		{[]string{"__complete", "node", "--host", ""}, "SuppliedHost\thost name"},
		{[]string{"__complete", "node", "--host", ""}, "CustomHost\thost name"},
		{[]string{"__complete", "pool", "inspect", ""}, "SuppliedSlots\tpool name"},
		{[]string{"__complete", "pool", "inspect", ""}, "CustomSlots\tpool name"},
		{[]string{"__complete", "name", "set", "resource", ""}, "Compute\tresource name"},
		{[]string{"__complete", "name", "clear", "lease", "--host", nameHost, ""}, nameLease},
		{[]string{"__complete", "name", "clear", "lease", nameLease, "--host", nameHost, ""}, "Build"},
	} {
		out, diagnostic, code := completionCLI(t, home, tt.args...)
		if code != 0 || !strings.Contains(out, tt.want) || strings.Contains(out+diagnostic, "fixture-token") {
			t.Fatalf("%v: %d %s %s", tt.args, code, out, diagnostic)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, op := range ops {
		if op != "name_list" && op != "node_list" && op != "status" && op != "pool_list" {
			t.Fatal("completion effect", op)
		}
	}
}

func TestCustomNameCompletionPermissionsCorruptionAndDeadline(t *testing.T) {
	for _, mode := range []string{"denied-inventory", "corrupt-map", "dangling", "wrong-authority", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			entry := nameEntry("lease", nameLease, "PrivateLabel")
			if mode == "dangling" {
				entry.ID = nameOtherLease
			}
			if mode == "wrong-authority" {
				entry.AuthorityHost = namePeer
			}
			home := completionEndpoint(t, func(ctx context.Context, q model.WireRequest) (any, *model.Error) {
				if q.Operation == "status" {
					if mode == "denied-inventory" {
						return nil, &model.Error{Code: "forbidden"}
					}
					return model.Status{Host: nameHost, Leases: []model.Lease{{ID: nameLease}}}, nil
				}
				if q.Operation == "name_list" {
					if mode == "timeout" {
						select {
						case <-ctx.Done():
						case <-time.After(2 * completionDeadline):
						}
						return nil, nil
					}
					snapshot := nameSnapshot{Document: nameDocument(entry)}
					if mode == "corrupt-map" {
						snapshot.NamingDiagnostic = &model.Error{Code: "name_metadata_invalid", Message: "private raw bytes"}
					}
					return snapshot, nil
				}
				return nil, &model.Error{Code: "forbidden"}
			})
			start := time.Now()
			out, diagnostic, code := completionCLI(t, home, "__complete", "inspect", "")
			if code != 0 || strings.Contains(out+diagnostic, "PrivateLabel") || strings.Contains(out+diagnostic, "private raw") || time.Since(start) > time.Second {
				t.Fatal("unsafe completion", mode, time.Since(start), code, out, diagnostic)
			}
		})
	}
}
