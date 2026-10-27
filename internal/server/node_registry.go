package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
)

const nodeReadTTL = 30 * time.Second
const nodeReadTimeout = 3 * time.Second

type cachedNodeRead struct {
	MetadataFailed, ScopeFailed bool
	Fingerprint, Pair           string
	Revision                    int
	Metadata                    *model.NodeMetadata
	Scope                       *model.NodeAccessScope
	MetadataAt, ScopeAt         time.Time
	Failed                      bool
}

func nodeGrant(g mesh.Grant) model.NodeGrant {
	return model.NodeGrant{Permissions: append([]string{}, g.Permissions...), Contacts: append([]string{}, g.Contacts...), Limits: g.Limits, MaxTTL: g.MaxTTL, OperatorInbox: g.OperatorInbox}
}
func scopeGrant(g model.NodeGrant) mesh.Grant {
	return mesh.Grant{Permissions: g.Permissions, Contacts: g.Contacts, Limits: g.Limits, MaxTTL: g.MaxTTL, OperatorInbox: g.OperatorInbox}
}
func exactNodeID(host string) bool {
	b, e := hex.DecodeString(host)
	return e == nil && len(b) == 16 && strings.ToLower(host) == host
}
func (s *Server) registryState() (model.NodeRegistry, *meshService, mesh.Config, error) {
	metadata, err := s.nodeMetadata()
	if err != nil {
		return model.NodeRegistry{}, nil, mesh.Config{}, err
	}
	now := time.Now().UTC()
	owner := local.CurrentIdentity()
	account := &model.NodeExecutionAccount{UID: owner.UID, Username: owner.Username, Source: "authenticated-local-account"}
	result := model.NodeRegistry{Source: metadata.Host, MeshState: "unconfigured", At: now, Nodes: []model.NodeEntry{{Host: metadata.Host, Name: "localhost", Local: true, Trust: model.NodeTrust{State: "local", Account: account}, Metadata: &metadata, MetadataFreshness: nodeFreshness(now, false), ScopeFreshness: nodeFreshness(time.Time{}, false), Health: "local"}}}
	s.meshMu.Lock()
	service := s.mesh
	s.meshMu.Unlock()
	var config mesh.Config
	var found bool
	if service != nil {
		config = service.store.Config()
		found = true
	} else {
		config, found, err = mesh.ReadRegistry(s.home)
	}
	if err != nil {
		result.MeshState = "unavailable"
		result.Error = &model.Error{Code: "registry_unavailable", Message: "Existing mesh registry cannot be read safely; localhost remains available and no identity was initialized."}
		return result, nil, mesh.Config{}, nil
	}
	if !found {
		return result, nil, config, nil
	}
	if config.Identity.Host != metadata.Host || mesh.ValidateRegistry(config) != nil {
		result.MeshState = "unavailable"
		result.Error = &model.Error{Code: "wrong_host", Message: "Mesh registry does not match this authority or exceeds its bounded direct-peer inventory."}
		return result, nil, mesh.Config{}, nil
	}
	result.MeshState = "disabled"
	if service != nil {
		result.MeshState = "enabled"
	} else if config.ListenerEnabled() {
		result.MeshState = "unavailable"
	}
	result.Nodes[0].Name = config.Identity.Name
	identity := config.Identity
	result.Nodes[0].Trust = model.NodeTrust{State: "local", Account: account, Address: identity.Address, Fingerprint: identity.Fingerprint, SPKI: identity.SPKI, Expires: &identity.Expires}
	sort.Slice(config.Peers, func(i, j int) bool { return config.Peers[i].Identity.Host < config.Peers[j].Identity.Host })
	for _, peer := range config.Peers {
		entry := peerNodeEntry(peer)
		if result.MeshState != "enabled" {
			entry.Health = "mesh_unavailable"
		}
		s.applyNodeCache(&entry, peer)
		result.Nodes = append(result.Nodes, entry)
	}
	return result, service, config, nil
}
func peerNodeEntry(peer mesh.Peer) model.NodeEntry {
	grant := nodeGrant(peer.Grant)
	e := model.NodeEntry{Host: peer.Identity.Host, Name: peer.Identity.Name, Trust: model.NodeTrust{State: peer.State, Account: &model.NodeExecutionAccount{UID: peer.Identity.Owner, Username: peer.Identity.OwnerUsername, Source: "paired-identity-snapshot"}, Address: peer.Identity.Address, Pair: peer.Pair, Revision: peer.Revision, Fingerprint: peer.Identity.Fingerprint, SPKI: peer.Identity.SPKI, Expires: &peer.Identity.Expires, Inbound: &grant}, MetadataFreshness: nodeFreshness(time.Time{}, false), ScopeFreshness: nodeFreshness(time.Time{}, false), Health: "unobserved"}
	if peer.State != "active" {
		e.Health = peer.State
	} else if _, err := mesh.ValidateIdentity(peer.Identity); err != nil {
		e.Health = "identity_unavailable"
	}
	return e
}
func nodeFreshness(at time.Time, failed bool) model.NodeReadFreshness {
	f := model.NodeReadFreshness{Stale: true, StaleAfter: nodeReadTTL}
	if !at.IsZero() {
		stamp := at.UTC()
		f.Received = &stamp
		f.Age = time.Since(at)
		f.Known = true
		f.Stale = failed || f.Age < 0 || f.Age > nodeReadTTL
	}
	return f
}
func (s *Server) applyNodeCache(entry *model.NodeEntry, peer mesh.Peer) {
	s.nodeMu.Lock()
	defer s.nodeMu.Unlock()
	cached, ok := s.nodeCache[peer.Identity.Host]
	if !ok {
		return
	}
	if cached.Fingerprint != peer.Identity.Fingerprint || cached.Pair != peer.Pair || cached.Revision != peer.Revision || peer.State != "active" {
		delete(s.nodeCache, peer.Identity.Host)
		return
	}
	// Return detached observations so a reader cannot mutate the shared cache.
	raw, _ := json.Marshal(cached)
	var copy cachedNodeRead
	_ = json.Unmarshal(raw, &copy)
	entry.Metadata = copy.Metadata
	if entry.Metadata != nil {
		elapsed := time.Since(cached.MetadataAt)
		advanceNodeSize(&entry.Metadata.Inventory.Memory, elapsed)
		advanceNodeSize(&entry.Metadata.Inventory.StateFilesystem, elapsed)
	}
	entry.Outbound = copy.Scope
	failed := cached.Failed || entry.Health == "mesh_unavailable" || entry.Health == "identity_unavailable"
	entry.MetadataFreshness = nodeFreshness(cached.MetadataAt, failed && (cached.MetadataFailed || entry.Health == "mesh_unavailable" || entry.Health == "identity_unavailable"))
	entry.ScopeFreshness = nodeFreshness(cached.ScopeAt, failed && (cached.ScopeFailed || entry.Health == "mesh_unavailable" || entry.Health == "identity_unavailable"))
	if failed {
		entry.Health = "unavailable"
		entry.Error = &model.Error{Code: "node_read_unavailable", Message: "Current destination information is unavailable; cached observations do not authorize actions."}
	} else if entry.ScopeFreshness.Stale || (entry.Outbound != nil && scopeGrant(entry.Outbound.Grant).Has("observe-host") && entry.MetadataFreshness.Stale) {
		entry.Health = "stale"
	} else {
		entry.Health = "reachable"
	}
}
func (s *Server) nodeList() (model.NodeRegistry, error) {
	result, _, _, err := s.registryState()
	if err != nil {
		return result, err
	}
	if raw, encodeErr := json.Marshal(result); encodeErr != nil {
		return result, encodeErr
	} else if len(raw) > 3<<20 {
		return model.NodeRegistry{}, model.Err("response_too_large", "Complete registry exceeds3MiB; no node was truncated. Inspect an exact stable node separately.")
	}
	return result, nil
}

// nodeInspect refreshes exactly one immutable host. Listing never fans out; this
// chosen read is bounded globally, per peer, and by a total deadline. It never
// retries mutation or falls back to localhost after any failure.
func (s *Server) nodeInspect(ctx context.Context, host string) (model.NodeEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, nodeReadTimeout)
	defer cancel()
	if !exactNodeID(host) {
		return model.NodeEntry{}, model.Err("invalid_request", "node_inspect requires an exact stable host ID")
	}
	registry, service, config, err := s.registryState()
	if err != nil {
		return model.NodeEntry{}, err
	}
	var entry model.NodeEntry
	found := false
	for _, candidate := range registry.Nodes {
		if candidate.Host == host {
			entry = candidate
			found = true
			break
		}
	}
	if !found {
		return entry, model.Err("host_unavailable", "Stable host is not in this direct registry; no local fallback was used")
	}
	if entry.Local {
		return entry, nil
	}
	if service == nil || entry.Trust.State != "active" {
		return entry, nil
	}
	peer, err := service.store.Resolve(host)
	if err != nil {
		return entry, model.Err("host_unavailable", "Direct peer identity is unavailable")
	}
	if _, err = mesh.ValidateIdentity(peer.Identity); err != nil {
		return entry, nil
	}
	s.nodeMu.Lock()
	if s.nodeSlots == nil {
		s.nodeSlots = make(chan struct{}, 2)
		s.nodeInFlight = map[string]bool{}
		s.nodeCache = map[string]cachedNodeRead{}
	}
	if s.nodeInFlight[host] {
		s.nodeMu.Unlock()
		return entry, model.Err("busy", "An inspection for this exact node is already in progress")
	}
	select {
	case s.nodeSlots <- struct{}{}:
	default:
		s.nodeMu.Unlock()
		return entry, model.Err("busy", "At most two chosen-node inspections may run at once")
	}
	s.nodeInFlight[host] = true
	s.nodeMu.Unlock()
	defer func() { s.nodeMu.Lock(); delete(s.nodeInFlight, host); <-s.nodeSlots; s.nodeMu.Unlock() }()
	cached := cachedNodeRead{Fingerprint: peer.Identity.Fingerprint, Pair: peer.Pair, Revision: peer.Revision}
	// Retain a prior matching observation after a failed refresh; mark it stale.
	s.nodeMu.Lock()
	if old, ok := s.nodeCache[host]; ok && old.Fingerprint == cached.Fingerprint && old.Pair == cached.Pair && old.Revision == cached.Revision {
		cached = old
	}
	s.nodeMu.Unlock()
	var scope model.NodeAccessScope
	raw, scopeErr := service.store.Call(ctx, host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if scopeErr == nil {
		scopeErr = decodeNodeRead(raw, &scope)
	}
	if scopeErr == nil && (scope.Source != config.Identity.Host || scope.Target != host || scope.Pair != peer.Pair || scope.Revision < 1 || scope.Observed.IsZero()) {
		scopeErr = model.Err("wrong_host", "Destination scope does not match the exact authenticated enrollment")
	}
	if scopeErr == nil {
		scopeErr = scopeGrant(scope.Grant).Validate()
	}
	if scopeErr == nil {
		cached.Scope = &scope
		cached.ScopeAt = time.Now()
	}
	var metadata model.NodeMetadata
	var metadataErr error
	if scopeErr == nil && scopeGrant(scope.Grant).Has("observe-host") {
		raw, metadataErr = service.store.Call(ctx, host, mesh.Request{Operation: "node_metadata", Params: json.RawMessage(`{}`)})
		if metadataErr == nil {
			metadataErr = decodeNodeRead(raw, &metadata)
		}
		if metadataErr == nil && (metadata.Host != host || metadata.SchemaVersion != 1 || metadata.ReportedAt.IsZero()) {
			metadataErr = model.Err("wrong_host", "Reported metadata differs from selected stable host")
		}
		if metadataErr == nil {
			cached.Metadata = &metadata
			cached.MetadataAt = time.Now()
		}
	} else if scopeErr == nil {
		// A grant reduction cannot leave formerly readable reported metadata as
		// apparently current. Preserve no private observation under removed scope.
		cached.Metadata = nil
		cached.MetadataAt = time.Time{}
	}
	cached.ScopeFailed = scopeErr != nil
	cached.MetadataFailed = scopeErr != nil || metadataErr != nil
	cached.Failed = cached.ScopeFailed || cached.MetadataFailed
	// A reply from an enrollment that changed while in flight cannot refresh a
	// newer registry row or context. No key or destination is silently rebound.
	s.meshMu.Lock()
	sameService := s.mesh == service
	current, currentErr := service.store.Resolve(host)
	if !sameService || currentErr != nil || current.State != "active" || current.Identity.Fingerprint != peer.Identity.Fingerprint || current.Pair != peer.Pair || current.Revision != peer.Revision {
		s.meshMu.Unlock()
		return model.NodeEntry{}, model.Err("node_context_changed", "Direct trust changed during the read; reopen the exact current node context")
	}
	s.nodeMu.Lock()
	if len(s.nodeCache) >= 64 {
		if _, exists := s.nodeCache[host]; !exists {
			for id := range s.nodeCache {
				delete(s.nodeCache, id)
				break
			}
		}
	}
	s.nodeCache[host] = cached
	s.nodeMu.Unlock()
	s.meshMu.Unlock()
	entry = peerNodeEntry(current)
	s.applyNodeCache(&entry, current)
	return entry, nil
}
func decodeNodeRead(raw []byte, value any) error {
	if len(raw) > 32<<10 {
		return model.Err("response_too_large", "Complete node read exceeds32KiB; no record was truncated")
	}
	if err := mesh.Decode(raw, value); err != nil {
		return err
	}
	// JSON escaping must not turn an accepted bounded input into a larger
	// retained/output envelope. In particular '<' expands under Go encoding.
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > 32<<10 {
		return model.Err("response_too_large", "Canonical node read exceeds32KiB; no record was truncated")
	}
	return nil
}

func advanceNodeSize(size *model.NodeInventorySize, elapsed time.Duration) {
	if elapsed < 0 || size.Age < 0 {
		size.Stale = true
		size.Known = false
		return
	}
	if elapsed > time.Duration(math.MaxInt64)-size.Age {
		size.Age = time.Duration(math.MaxInt64)
	} else {
		size.Age += elapsed
	}
	if size.StaleAfter <= 0 || size.Age > size.StaleAfter {
		size.Stale = true
		size.Known = false
	}
}
