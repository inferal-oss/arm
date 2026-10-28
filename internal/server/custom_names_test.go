package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/customnames"
	"getarm.org/internal/mesh"
)

func TestCustomNameAPIClosedRequestsLocalOwnerAndNoTrustMutation(t *testing.T) {
	for _, raw := range []string{
		`{"operation":"set","Operation":"clear"}`,
		`{"operation":"set","operation":"clear"}`,
		`{"operation":null}`,
		`{"operation":"set","kind":"lease","authority_host":"a","id":"b","binding_lease":"","name":"safe","expected_revision":0,"visible":true}`,
		`{"operation":"set","kind":"lease","authority_host":"a","id":"b","binding_lease":"","name":"safe","expected_revision":null}`,
	} {
		if _, err := (&Server{}).nameOperation(t.Context(), "name_mutate", json.RawMessage(raw)); err == nil {
			t.Fatal("invalid request reached naming state", raw)
		}
	}
	s := meshFixture(t)
	host, _ := s.Manager.CustomNameIdentity()
	if _, found, err := mesh.ReadRegistry(s.home); err != nil || found {
		t.Fatal("fixture has mesh state", found, err)
	}
	if _, err := s.nameOperation(t.Context(), "name_list", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	request := nameMutation{Operation: "set", Kind: "host", AuthorityHost: host, ID: host, Name: "workstation", ExpectedRevision: 0}
	raw, _ := json.Marshal(request)
	if _, err := s.nameOperation(t.Context(), "name_mutate", raw); err != nil {
		t.Fatal(err)
	}
	if _, found, err := mesh.ReadRegistry(s.home); err != nil || found {
		t.Fatal("presentation metadata initialized or changed trust", found, err)
	}
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	status = s.withServerCapabilities(status)
	view, diagnostic := s.customNamedView(t.Context(), "status", s.namedView("status", status))
	if diagnostic != nil || view.(namedStatus).DisplayName != "workstation" || view.(namedStatus).Host != host {
		t.Fatal("custom host view lost canonical identity", view, diagnostic)
	}
	if !reflect.DeepEqual(view.(namedStatus).Capabilities, status.Capabilities) || !strings.Contains(strings.Join(view.(namedStatus).Capabilities, " "), "node-availability-v1") {
		t.Fatal("typed custom status lost availability capabilities", view)
	}
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("a", 32)}, State: "active", Proven: true, Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-all", "lease-own"}}}
	if result, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "name_mutate", Params: raw}); err == nil || result != nil {
		t.Fatal("peer gained local naming writes", result, err)
	}
	remote, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "status", Host: host})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := meshWithoutPolicy(remote)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(s.namedPeerView("status", filtered))
	if bytes.Contains(encoded, []byte("workstation")) {
		t.Fatal("private source alias leaked through destination summary", string(encoded))
	}
	doc, err := s.Manager.CustomNames(t.Context())
	if err != nil || doc.Revision != 1 {
		t.Fatal("peer changed naming map", doc, err)
	}
}

func TestCustomNameRawRemoteReadPreservesExactFieldsAndPortIncarnation(t *testing.T) {
	host, lease, replacement := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	pool := "pool_" + strings.Repeat("d", 32)
	port := host + "/ports/local/tcp4/127.0.0.1:12345"
	index := makeCustomNameIndex(customnames.Document{Entries: []customnames.Entry{
		{Kind: "host", AuthorityHost: host, ID: host, Name: "remote-build-host", Active: true},
		{Kind: "lease", AuthorityHost: host, ID: lease, Name: "remote-build", Active: true},
		{Kind: "pool", AuthorityHost: host, ID: pool, Name: "remote-permits", Active: true},
		{Kind: "resource", AuthorityHost: host, ID: port, BindingLease: lease, Name: "original-port-label", Active: true},
	}})
	raw := json.RawMessage(`{"host":"` + host + `","leases":[{"id":"` + lease + `","host":"` + host + `","revision":7,"request":{"signed":"unaltered","resources":{"disk_bytes":9223372036854775807}}}],"resources":[{"id":"` + pool + `","host":"","kind":"custom","budget_total":9223372036854775807,"users":null}],"extra":{"huge":9007199254740993,"signed":{"payload":"unchanged"}}}`)
	status, err := readRawNameView(raw)
	if err != nil {
		t.Fatal(err)
	}
	status.label("display_name", index.name("host", host, host, ""))
	leases, err := rawNameArray(status.field("leases"), func(raw json.RawMessage) (json.RawMessage, error) { return index.remoteLease(host, raw) })
	if err != nil {
		t.Fatal(err)
	}
	resources, err := index.remoteResources(host, status.field("resources"))
	if err != nil {
		t.Fatal(err)
	}
	status.set("leases", leases)
	status.set("resources", resources)
	view := status.encoded()
	if !reflect.DeepEqual(withoutDisplayFields(jsonNumbers(t, view)), jsonNumbers(t, raw)) {
		t.Fatal("raw adapter changed opaque fields, quantities or null shapes", string(view))
	}
	for _, expected := range []string{"remote-build-host", "remote-build", "remote-permits", "9223372036854775807", "9007199254740993", `"signed":{"payload":"unchanged"}`} {
		if !bytes.Contains(view, []byte(expected)) {
			t.Fatal("label or exact opaque field missing", expected, string(view))
		}
	}
	row := func(owner string) json.RawMessage {
		return json.RawMessage(`{"id":"` + port + `","host":"` + host + `","kind":"port","users":[{"lease":"` + replacement + `","relationship":"activation"},{"lease":"` + owner + `","relationship":"reservation"}]}`)
	}
	for _, tt := range []struct {
		name string
		rows []json.RawMessage
		want bool
	}{
		{"current", []json.RawMessage{row(lease)}, true},
		{"replacement", []json.RawMessage{row(replacement)}, false},
		{"conflicting-duplicate-locator", []json.RawMessage{row(lease), row(replacement)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.rows)
			view, err := index.remoteResources(host, raw)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(view, []byte("original-port-label")) != tt.want {
				t.Fatal("port name retargeted or trusted slice order", string(view))
			}
		})
	}
	for _, operation := range []string{"acquire", "run", "release", "renew", "cancel"} {
		result, err := (&Server{}).sourceNamedView(t.Context(), host, operation, raw)
		if err != nil || !bytes.Equal(result.(json.RawMessage), raw) {
			t.Fatal("immutable mutation receipt entered naming read path", operation, err)
		}
	}
	t.Run("shape-failures", func(t *testing.T) {
		s := meshFixture(t)
		authority, _ := s.Manager.CustomNameIdentity()
		request, _ := json.Marshal(nameMutation{Operation: "set", Kind: "host", AuthorityHost: authority, ID: authority, Name: "custom-read-host", ExpectedRevision: 0})
		if _, err := s.nameOperation(t.Context(), "name_mutate", request); err != nil {
			t.Fatal(err)
		}
		for _, tt := range []struct{ name, operation, raw string }{
			{"status-missing-host", "status", `{"private":"unchanged"}`},
			{"status-null-host", "status", `{"host":null}`},
			{"status-number-host", "status", `{"host":1}`},
			{"status-wrong-host", "status", `{"host":"` + strings.Repeat("f", 32) + `"}`},
			{"inspect-missing-id", "inspect", `{"host":"` + authority + `"}`},
			{"inspect-null-id", "inspect", `{"host":"` + authority + `","id":null}`},
			{"inspect-number-id", "inspect", `{"host":"` + authority + `","id":9223372036854775807}`},
			{"inspect-number-host", "inspect", `{"host":1,"id":"` + lease + `"}`},
			{"resources-null-kind", "resources", `[{"host":"` + authority + `","id":"` + authority + `/cpu","kind":null}]`},
			{"resources-missing-kind", "resources", `[{"host":"` + authority + `","id":"` + authority + `/cpu"}]`},
			{"resources-number-id", "resources", `[{"host":"` + authority + `","id":1,"kind":"cpu"}]`},
			{"resources-consumer-number-lease", "resources", `[{"host":"` + authority + `","id":"` + authority + `/cpu","kind":"cpu","users":[{"lease":1}]}]`},
		} {
			t.Run(tt.name, func(t *testing.T) {
				result, err := s.sourceNamedView(t.Context(), authority, tt.operation, json.RawMessage(tt.raw))
				view, ok := result.(sourceNamedRead)
				if err != nil || !ok || view.diagnostic == nil || view.diagnostic.Code != "name_metadata_unavailable" || !bytes.Equal(view.raw, []byte(tt.raw)) {
					t.Fatal("cosmetic shape validation changed the original read result", result, err)
				}
				if strings.Contains(view.diagnostic.Message, "private") {
					t.Fatal("raw response escaped fixed naming diagnostic")
				}
			})
		}
		infrastructure := json.RawMessage(`[{"id":"` + authority + `/ports/local/tcp4/127.0.0.1:12345","host":"` + authority + `","kind":"port","users":[{"lease":"","state":"infrastructure"}]}]`)
		result, err := s.sourceNamedView(t.Context(), authority, "resources", infrastructure)
		view, ok := result.(json.RawMessage)
		if err != nil || !ok || !bytes.Contains(view, []byte("custom-read-host")) || !reflect.DeepEqual(withoutDisplayFields(jsonNumbers(t, view)), jsonNumbers(t, infrastructure)) {
			t.Fatal("valid infrastructure acquired an incarnation or lost its empty lease", result, err)
		}
	})
}
