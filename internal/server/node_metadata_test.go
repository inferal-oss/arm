package server

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/observe"
)

func TestNodeMetadataLocalBrowserMeshSchemaAndGrantBoundary(t *testing.T) {
	s := meshFixture(t)
	local, err := s.dispatch(t.Context(), model.WireRequest{Operation: "node_metadata"})
	if err != nil {
		t.Fatal(err)
	}
	browser, err := s.dispatch(context.WithValue(t.Context(), browserContextKey{}, true), model.WireRequest{Operation: "node_metadata", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := &meshService{owner: s}
	peer := mesh.Peer{Grant: mesh.Grant{Permissions: []string{"observe-host"}}}
	remote, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "node_metadata", Params: json.RawMessage(`null`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{local, browser, remote} {
		n, ok := value.(model.NodeMetadata)
		if !ok || n.SchemaVersion != 1 || n.Host == "" || n.Build.Version == "" || n.Platform.Architecture == "" || len(n.Capabilities) == 0 {
			t.Fatal(value)
		}
		a, b := n, local.(model.NodeMetadata)
		a.ReportedAt = b.ReportedAt
		a.Inventory.Memory.Age = b.Inventory.Memory.Age
		a.Inventory.StateFilesystem.Age = b.Inventory.StateFilesystem.Age
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("schema/facts differ: %+v %+v", a, b)
		}
	}
	for _, grant := range []mesh.Grant{{}, {Permissions: []string{"observe-own"}}, {Permissions: []string{"lease-own", "message", "contact-read"}}} {
		_, err = service.dispatch(t.Context(), mesh.Peer{Grant: grant}, mesh.Request{Operation: "node_metadata"})
		if api, ok := err.(*model.Error); !ok || api.Code != "forbidden" {
			t.Fatal("metadata escaped observe-host grant", err)
		}
	}
	// Metadata permission is a read only; it grants no access to native/mesh admin.
	for _, op := range []string{"agent_message", "mesh_setup_prepare", "budget_list"} {
		_, err = service.dispatch(t.Context(), peer, mesh.Request{Operation: op, Params: json.RawMessage(`{}`)})
		if err == nil {
			t.Fatal("metadata grant widened dispatch", op)
		}
	}
}

func TestNodeMetadataRejectsSelectorsAnnotationsAndTrailingInput(t *testing.T) {
	s := meshFixture(t)
	service := &meshService{owner: s}
	peer := mesh.Peer{Grant: mesh.Grant{Permissions: []string{"observe-host"}}}
	for _, raw := range []string{`{"target_host":"other"}`, `{"annotations":{"secret":"private"}}`, `{"operation":"run"}`, `{} {}`, `[]`, `"string"`} {
		for _, remote := range []bool{false, true} {
			var err error
			if remote {
				_, err = service.dispatch(t.Context(), peer, mesh.Request{Operation: "node_metadata", Params: json.RawMessage(raw)})
			} else {
				_, err = s.dispatch(t.Context(), model.WireRequest{Operation: "node_metadata", Params: json.RawMessage(raw)})
			}
			if api, ok := err.(*model.Error); !ok || api.Code != "invalid_request" {
				t.Fatal(raw, remote, err)
			}
		}
	}
}

func TestNodeMetadataFeatureInventorySharesActualServerCapabilities(t *testing.T) {
	s := meshFixture(t)
	for _, browser := range []bool{false, true} {
		s.authority = ""
		s.monitor = nil
		if browser {
			s.authority = "127.0.0.1:1234"
			s.monitor = &observe.Monitor{}
		}
		value, err := s.dispatch(t.Context(), model.WireRequest{Operation: "status"})
		if err != nil {
			t.Fatal(err)
		}
		status := value.(model.Status)
		metadata, err := s.nodeMetadata()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(status.Capabilities, metadata.Capabilities) {
			t.Fatal(status.Capabilities, metadata.Capabilities)
		}
		for _, capability := range []string{"pool-journal-pages", "automatic-mesh-lifecycle", "database-inspection-backup", "reported-node-metadata"} {
			if !slices.Contains(metadata.Capabilities, capability) {
				t.Fatal("missing server feature", capability, metadata.Capabilities)
			}
		}
		for _, capability := range []string{"browser-dashboard-observation", "browser-reviewed-lease-controls", "browser-reviewed-messaging", "browser-reviewed-pool-controls", "host-monitor-stream"} {
			if slices.Contains(metadata.Capabilities, capability) != browser {
				t.Fatal("incorrect conditional feature", capability, metadata.Capabilities)
			}
		}
	}
}
