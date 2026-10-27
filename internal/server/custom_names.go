package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/customnames"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

type nameSnapshot struct {
	customnames.Document
	NamingDiagnostic *model.Error `json:"naming_diagnostic,omitempty"`
}

type nameMutation struct {
	Operation        string `json:"operation"`
	Kind             string `json:"kind"`
	AuthorityHost    string `json:"authority_host"`
	ID               string `json:"id"`
	BindingLease     string `json:"binding_lease"`
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func decodeNameRequest(raw json.RawMessage, out any, allowed []string, required ...string) error {
	invalid := func() error {
		return model.Err("invalid_request", "provide one bounded naming request with exact supported fields and types")
	}
	if len(raw) == 0 || len(raw) > 8192 {
		return invalid()
	}
	fields := map[string]bool{}
	for _, field := range allowed {
		fields[field] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return invalid()
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || !fields[key] || seen[key] {
			return invalid()
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return invalid()
	}
	if _, err = decoder.Token(); err != io.EOF {
		return invalid()
	}
	for _, field := range required {
		if !seen[field] {
			return invalid()
		}
	}
	if json.Unmarshal(raw, out) != nil {
		return invalid()
	}
	return nil
}

func nameReadDiagnostic(err error) *model.Error {
	if err == nil {
		return nil
	}
	var api *model.Error
	if errors.As(err, &api) && api.Code == "name_metadata_invalid" {
		return &model.Error{Code: "name_metadata_invalid", Message: "Custom name metadata is invalid; exact IDs remain available. Inspect the database without repairing or dropping evidence."}
	}
	return &model.Error{Code: "name_metadata_unavailable", Message: "Custom names are unavailable; exact IDs remain available. Read a fresh naming snapshot before using custom selectors."}
}

func (s *Server) nameOperation(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	if operation == "name_list" {
		var query struct {
			Kind          string `json:"kind"`
			AuthorityHost string `json:"authority_host"`
			All           bool   `json:"all"`
		}
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			raw = json.RawMessage(`{}`)
		}
		if err := decodeNameRequest(raw, &query, []string{"kind", "authority_host", "all"}); err != nil {
			return nil, err
		}
		if query.Kind != "" && query.Kind != "host" && query.Kind != "lease" && query.Kind != "pool" && query.Kind != "resource" || query.AuthorityHost != "" && !exactNodeID(query.AuthorityHost) {
			return nil, model.Err("invalid_request", "select a supported kind and exact authority host")
		}
		doc, err := s.Manager.CustomNames(ctx)
		if err != nil {
			host, owner := s.Manager.CustomNameIdentity()
			return nameSnapshot{Document: customnames.Document{Version: 1, Owner: owner, Host: host}, NamingDiagnostic: nameReadDiagnostic(err)}, nil
		}
		entries := make([]customnames.Entry, 0, len(doc.Entries))
		for _, entry := range doc.Entries {
			if (query.Kind == "" || entry.Kind == query.Kind) && (query.AuthorityHost == "" || entry.AuthorityHost == query.AuthorityHost) && (query.All || entry.Active) {
				entries = append(entries, entry)
			}
		}
		doc.Entries = entries
		return nameSnapshot{Document: doc}, nil
	}
	var request nameMutation
	fields := []string{"operation", "kind", "authority_host", "id", "binding_lease", "name", "expected_revision"}
	if err := decodeNameRequest(raw, &request, fields, fields...); err != nil {
		return nil, err
	}
	if request.Kind == "resource" && strings.HasPrefix(request.ID, "pool_") {
		request.Kind = "pool"
	}
	target := customnames.Entry{Kind: request.Kind, AuthorityHost: request.AuthorityHost, ID: request.ID, BindingLease: request.BindingLease, Name: request.Name}
	if request.ExpectedRevision < 0 || manager.ValidateCustomNameTarget(request.Operation, target) != nil || customnames.ValidateName(target.Name) != nil {
		return nil, model.Err("invalid_request", "provide an exact supported name binding, safe name and expected map revision")
	}
	if request.Operation == "clear" {
		return s.Manager.MutateCustomName(ctx, "clear", target, request.ExpectedRevision, manager.NameObservation{})
	}
	host, _ := s.Manager.CustomNameIdentity()
	if target.AuthorityHost == host {
		observation := manager.NameObservation{}
		if target.Kind == "host" {
			registry, err := s.nodeList()
			if err != nil {
				return nil, err
			}
			observation, err = manager.ObserveNameHosts(registry)
			if err != nil {
				return nil, err
			}
		}
		return s.Manager.MutateCustomName(ctx, "set", target, request.ExpectedRevision, observation)
	}
	return s.remoteCustomName(ctx, host, target, request.ExpectedRevision)
}

func (s *Server) remoteCustomName(ctx context.Context, source string, target customnames.Entry, revision int64) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	registry, service, _, err := s.registryState()
	if err != nil || registry.Error != nil || service == nil {
		return nil, model.Err("name_inventory_unavailable", "an active pinned direct registry is required to assign a remote label")
	}
	var peer mesh.Peer
	var identity mesh.Identity
	var impacts []mesh.PeerImpact
	_, err = service.store.WithPeerSnapshot(target.AuthorityHost, func(current mesh.Peer, local mesh.Identity, peers []mesh.PeerImpact) (any, error) {
		if !current.Proven || current.State != "active" || local.Host != source {
			return nil, model.Err("name_inventory_unavailable", "a proven current source and destination are required")
		}
		if _, err := mesh.ValidateIdentity(current.Identity); err != nil {
			return nil, err
		}
		if _, err := mesh.ValidateIdentity(local); err != nil {
			return nil, err
		}
		peer, identity, impacts = current, local, peers
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	var observation manager.NameObservation
	if target.Kind == "host" {
		observation, err = manager.ObserveNameHosts(registry)
	} else {
		// Network reads complete before taking the final source-store and
		// Manager locks. The destination independently enforces its grant.
		readScope := func() (model.NodeAccessScope, error) {
			raw, err := service.store.Call(ctx, target.AuthorityHost, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
			var scope model.NodeAccessScope
			if err == nil {
				err = mesh.Decode(raw, &scope)
			}
			if err == nil && (scope.Source != source || scope.Target != target.AuthorityHost || scope.Pair != peer.Pair || scope.Revision < 1 || scope.Observed.IsZero() || scopeGrant(scope.Grant).Validate() != nil) {
				err = model.Err("node_context_changed", "destination naming read context differs from pinned selection")
			}
			return scope, err
		}
		scope, readErr := readScope()
		if readErr != nil {
			return nil, readErr
		}
		raw, readErr := service.store.Call(ctx, target.AuthorityHost, mesh.Request{Expected: &mesh.ExpectedEnrollment{Pair: scope.Pair, Revision: scope.Revision}, Operation: "status", Params: json.RawMessage(`{}`)})
		if readErr != nil {
			return nil, readErr
		}
		var view struct {
			Host      string          `json:"host"`
			Leases    json.RawMessage `json:"leases"`
			Resources json.RawMessage `json:"resources"`
		}
		if json.Unmarshal(raw, &view) != nil || view.Host != target.AuthorityHost {
			return nil, model.Err("name_inventory_unavailable", "complete selected destination snapshot is unavailable")
		}
		status := model.Status{Host: view.Host}
		if target.Kind == "lease" {
			if len(view.Leases) == 0 || bytes.Equal(bytes.TrimSpace(view.Leases), []byte("null")) || json.Unmarshal(view.Leases, &status.Leases) != nil {
				return nil, model.Err("name_inventory_unavailable", "authorized complete lease inventory is unavailable")
			}
		} else {
			if len(view.Resources) == 0 || bytes.Equal(bytes.TrimSpace(view.Resources), []byte("null")) || json.Unmarshal(view.Resources, &status.Resources) != nil {
				return nil, model.Err("name_inventory_unavailable", "authorized complete resource inventory is unavailable")
			}
		}
		currentScope, readErr := readScope()
		if readErr != nil {
			return nil, readErr
		}
		if currentScope.Revision != scope.Revision || !reflect.DeepEqual(currentScope.Grant, scope.Grant) {
			return nil, model.Err("node_context_changed", "destination grant changed during naming inspection")
		}
		observation, err = manager.ObserveNameStatus(source, status)
	}
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	s.meshMu.Lock()
	currentService := s.mesh
	s.meshMu.Unlock()
	if currentService != service {
		return nil, model.Err("node_context_changed", "source mesh changed during naming inspection")
	}
	return service.store.WithPeerSnapshot(target.AuthorityHost, func(current mesh.Peer, local mesh.Identity, peers []mesh.PeerImpact) (any, error) {
		if !current.Proven || !reflect.DeepEqual(current, peer) || !reflect.DeepEqual(local, identity) || !reflect.DeepEqual(peers, impacts) {
			return nil, model.Err("node_context_changed", "source pin, identity, grant or registry changed during naming inspection")
		}
		if _, err := mesh.ValidateIdentity(current.Identity); err != nil {
			return nil, err
		}
		if _, err := mesh.ValidateIdentity(local); err != nil {
			return nil, err
		}
		// Existing source-store -> Manager lock order. This callback performs
		// only local map validation/transaction: no RPC or mesh reentry.
		return s.Manager.MutateCustomName(ctx, "set", target, revision, observation)
	})
}
