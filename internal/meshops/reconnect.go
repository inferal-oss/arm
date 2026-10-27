package meshops

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
)

// ReconnectDraft is caller intent. Browser adapters expose no Binary/Home fields;
// those values are resolved only from retained native evidence or a connector.
type ReconnectDraft struct {
	Host        string     `json:"host"`
	Destination string     `json:"ssh_destination,omitempty"`
	Connector   string     `json:"connector,omitempty"`
	Binary      string     `json:"remote_binary,omitempty"`
	RemoteHome  string     `json:"remote_home,omitempty"`
	LocalIP     string     `json:"local_ip,omitempty"`
	RemoteIP    string     `json:"remote_ip,omitempty"`
	Public      bool       `json:"allow_public_listen"`
	Reason      string     `json:"reason"`
	Forward     mesh.Grant `json:"source_permissions_on_target"`
	Reverse     mesh.Grant `json:"target_permissions_on_source"`
}

type ReconnectBinding struct {
	Local      mesh.Identity            `json:"local_identity"`
	Prior      mesh.Peer                `json:"previous_enrollment"`
	Connection *mesh.LifecycleOperation `json:"previous_connection,omitempty"`
	Selected   mesh.Connector           `json:"selected_helper"`
}

type ReconnectRequest struct {
	Draft   ReconnectDraft   `json:"draft"`
	Binding ReconnectBinding `json:"binding"`
}

func exactReconnectHost(host string) bool {
	b, e := hex.DecodeString(host)
	return e == nil && len(b) == 16 && host == strings.ToLower(host)
}
func sameReconnectPrincipal(a, b mesh.Identity) bool {
	return a.Host == b.Host && a.Owner == b.Owner && a.Fingerprint == b.Fingerprint && a.SPKI == b.SPKI && a.Certificate == b.Certificate && a.Expires.Equal(b.Expires)
}
func validateReconnectDraft(d ReconnectDraft) error {
	if !exactReconnectHost(d.Host) || strings.TrimSpace(d.Reason) == "" || len(d.Reason) > 4096 {
		return model.Err("invalid_request", "reconnect requires an exact stable host ID and a bounded nonblank reason")
	}
	for _, address := range []string{d.LocalIP, d.RemoteIP} {
		if address != "" && (len(address) > 64 || net.ParseIP(address) == nil) {
			return model.Err("invalid_request", "explicit reconnect addresses must be IPv4 or IPv6 literals without a port or zone")
		}
	}
	if d.Connector != "" && (d.Destination != "" || d.Binary != "" || d.RemoteHome != "") {
		return model.Err("invalid_request", "choose a native connector or explicit SSH locator/helper options, not both")
	}
	if e := d.Forward.Validate(); e != nil {
		return e
	}
	if e := d.Reverse.Validate(); e != nil {
		return e
	}
	if d.Destination != "" {
		binary := d.Binary
		if binary == "" {
			binary = "arm"
		}
		if e := (mesh.Connector{ID: "reconnect", Destination: d.Destination, Binary: binary, Home: d.RemoteHome, Reason: d.Reason}).Validate(); e != nil {
			return e
		}
	}
	return nil
}
func (r ReconnectRequest) validate() error {
	if e := validateReconnectDraft(r.Draft); e != nil {
		return e
	}
	if r.Binding.Prior.Identity.Host != r.Draft.Host || r.Binding.Prior.State != "revoked" {
		return model.Err("invalid_request", "reconnect binding must identify the exact revoked enrollment")
	}
	if e := mesh.ValidateRegistry(mesh.Config{Identity: r.Binding.Local, Listen: r.Binding.Local.Address, Peers: []mesh.Peer{r.Binding.Prior}}); e != nil {
		return e
	}
	if e := r.Binding.Selected.Validate(); e != nil {
		return e
	}
	if (r.Draft.Connector == "" && r.Binding.Selected.ID != "reconnect") || (r.Draft.Connector != "" && r.Binding.Selected.ID != r.Draft.Connector) {
		return model.Err("invalid_request", "selected helper does not match native reconnect selector")
	}
	if (r.Draft.Destination != "" && r.Binding.Selected.Destination != r.Draft.Destination) || (r.Draft.Binary != "" && r.Binding.Selected.Binary != r.Draft.Binary) || (r.Draft.RemoteHome != "" && r.Binding.Selected.Home != r.Draft.RemoteHome) {
		return model.Err("invalid_request", "selected helper differs from explicit reconnect draft")
	}
	if r.Binding.Selected.Reason != r.Draft.Reason {
		return model.Err("invalid_request", "selected helper reason differs from reconnect draft")
	}
	if c := r.Binding.Connection; c != nil {
		if !mesh.ValidKey(c.Key) || (c.Kind != "connect" && c.Kind != "reconnect") || c.Phase != "connected" || c.Pair != r.Binding.Prior.Pair || c.LocalIdentity == nil || c.RemoteIdentity == nil || c.LocalHost != r.Binding.Local.Host || c.RemoteHost != r.Draft.Host || !sameReconnectPrincipal(*c.LocalIdentity, r.Binding.Local) || !sameReconnectPrincipal(*c.RemoteIdentity, r.Binding.Prior.Identity) {
			return model.Err("connection_metadata_changed", "prior connection does not bind the exact retained enrollment")
		}

		if r.Draft.Connector == "" {
			binary := c.Binary
			if binary == "" {
				binary = "arm"
			}
			if (r.Draft.Destination == "" && r.Binding.Selected.Destination != c.SSHLocator) || (r.Draft.Binary == "" && r.Binding.Selected.Binary != binary) || (r.Draft.RemoteHome == "" && r.Binding.Selected.Home != c.RemoteHome) {
				return model.Err("connection_metadata_changed", "selected helper differs from retained reconnect provenance")
			}
		}
	}
	if r.Binding.Connection == nil && r.Draft.Connector == "" && r.Draft.Destination == "" {
		return model.Err("ssh_locator_required", "reconnect requires explicit locator provenance")
	}
	return nil
}

// ResolveReconnect checks known keys before current registry, locator or network
// resolution. A retry cannot capture a replacement enrollment as its old intent.
func ResolveReconnect(home, key string, draft ReconnectDraft) (ReconnectRequest, error) {
	if !mesh.ValidKey(key) {
		return ReconnectRequest{}, model.Err("invalid_request", "exact reconnect key is required")
	}
	if e := validateReconnectDraft(draft); e != nil {
		return ReconnectRequest{}, e
	}
	if op, ok, e := mesh.ReadCoordination(home, key); e != nil {
		return ReconnectRequest{}, e
	} else if ok {
		var q StartRequest
		if op.Kind != "reconnect" || mesh.Decode(op.Request, &q) != nil || q.Kind != "reconnect" || q.Key != key || q.Reconnect == nil {
			return ReconnectRequest{}, model.Err("idempotency_conflict", "key does not identify a saved reconnect draft")
		}
		r := *q.Reconnect
		a, _ := mesh.ReviewDigest(r.Draft)
		b, _ := mesh.ReviewDigest(draft)
		digest, e := mesh.ReviewDigest(r)
		if e != nil || a != b || digest != op.Digest {
			return ReconnectRequest{}, model.Err("idempotency_conflict", "reconnect key belongs to a different immutable draft")
		}
		if e = r.validate(); e != nil {
			return ReconnectRequest{}, e
		}
		return r, nil
	}
	config, ok, e := mesh.ReadRegistry(home)
	if e != nil {
		return ReconnectRequest{}, e
	}
	if !ok {
		return ReconnectRequest{}, model.Err("mesh_unavailable", "no retained peer registry exists")
	}
	peer, e := mesh.ResolveConfig(config, draft.Host)
	if e != nil {
		return ReconnectRequest{}, e
	}
	if peer.Identity.Host != draft.Host || peer.State != "revoked" {
		return ReconnectRequest{}, model.Err("enrollment_not_revoked", "reconnect requires this exact enrollment to be revoked; inspect active or pending trust separately")
	}
	if e = reconnectBlocked(home, draft.Host); e != nil {
		return ReconnectRequest{}, e
	}
	r := ReconnectRequest{Draft: draft, Binding: ReconnectBinding{Local: config.Identity, Prior: peer}}
	records, e := mesh.LifecycleConnections(home)
	if e != nil {
		return r, e
	}
	for _, c := range records {
		if c.Pair != peer.Pair || c.RemoteHost != draft.Host {
			continue
		}
		if r.Binding.Connection != nil {
			return r, model.Err("connection_metadata_ambiguous", "multiple connection receipts identify this enrollment; choose explicit recovery instead of guessing")
		}
		copy := c
		r.Binding.Connection = &copy
	}
	selected := mesh.Connector{ID: "reconnect", Destination: draft.Destination, Binary: draft.Binary, Home: draft.RemoteHome, Reason: draft.Reason}
	if draft.Connector != "" {
		all, e := mesh.ReadConnectors(home)
		if e != nil {
			return r, e
		}
		found := false
		for _, c := range all {
			if c.ID == draft.Connector {
				selected = c
				selected.Reason = draft.Reason
				found = true
				break
			}
		}
		if !found {
			return r, model.Err("not_found", "reviewed native connector not found")
		}
	} else if c := r.Binding.Connection; c != nil {
		if selected.Destination == "" {
			selected.Destination = c.SSHLocator
		}
		if selected.Binary == "" {
			selected.Binary = c.Binary
		}
		if selected.Home == "" {
			selected.Home = c.RemoteHome
		}
	}
	if selected.Destination == "" {
		return r, model.Err("ssh_locator_required", "no verified SSH locator is retained; supply --ssh or a reviewed native connector")
	}
	if selected.Binary == "" {
		selected.Binary = "arm"
	}
	r.Binding.Selected = selected
	if e = r.validate(); e != nil {
		return r, e
	}
	return r, nil
}
func (r ReconnectRequest) connectRequest() ConnectRequest {
	d, b := r.Draft, r.Binding
	return ConnectRequest{Destination: b.Selected.Destination, Binary: b.Selected.Binary, RemoteHome: b.Selected.Home, LocalIP: d.LocalIP, RemoteIP: d.RemoteIP, Reason: d.Reason, Forward: d.Forward, Reverse: d.Reverse, Public: d.Public, ReplaceRevoked: true, Reconnect: &r}
}

func (a *Coordinator) Reconnect(key string, r ReconnectRequest, yes, dry bool) (int, error) {
	owned, e := cloneReconnect(r)
	if e != nil {
		return 2, e
	}
	r = owned
	if e := r.validate(); e != nil {
		return 2, e
	}
	if a.attemptParent == "" {
		if op, ok, e := mesh.ReadCoordination(a.Home, key); e != nil {
			return 1, e
		} else if ok {
			digest, e := mesh.ReviewDigest(r)
			if e != nil || op.Kind != "reconnect" || op.Digest != digest {
				return 1, model.Err("idempotency_conflict", "reconnect key differs from its saved original intent")
			}
			if a.guard == nil || op.Worker == "" {
				if e = a.Print(Project(op)); e != nil {
					return 1, e
				}
				return 0, nil
			}
		}
	}
	return a.run(func() (int, error) { return a.connect(key, r.connectRequest(), yes, dry) })
}

// Constraints are re-read before startup and after review. An already admitted
// recovery may observe only its own exact key-derived replacement enrollment.
func (a *Coordinator) checkReconnect(key string, r *ReconnectRequest, own, remote mesh.LifecycleInspection) error {
	if r == nil {
		return nil
	}
	b := r.Binding
	if own.Identity == nil || remote.Identity == nil || !sameReconnectPrincipal(*own.Identity, b.Local) || !sameReconnectPrincipal(*remote.Identity, b.Prior.Identity) || remote.Host != r.Draft.Host || remote.Owner.UID != b.Prior.Identity.Owner {
		return model.Err("reconnect_identity_changed", "fresh node identity or OS owner differs from the retained enrollment; use a separate connect review")
	}
	if _, e := mesh.ValidateIdentity(*remote.Identity); e != nil {
		return e
	}
	if c := b.Connection; c != nil {
		saved, ok, e := mesh.ReadLifecycleOperation(a.Home, c.Key)
		if e != nil {
			return e
		}
		if !ok || !reflect.DeepEqual(saved, *c) {
			return model.Err("connection_metadata_changed", "the exact prior connection receipt changed")
		}
		if remote.Home != c.RemoteHome {
			return model.Err("reconnect_home_changed", "reported ARM home differs from the retained authority; use a separate connect review")
		}
	}
	pair := connectPair(key, own.Host, remote.Host)
	prior, have, e := mesh.ReadLifecycleOperation(a.Home, key)
	if e != nil {
		return e
	}
	digest, _ := mesh.ReviewDigest(*r)
	recovery := have && prior.Kind == "reconnect" && prior.Digest == digest && prior.ReviewedDigest != "" && prior.Pair == pair
	check := func(config *mesh.Config, other mesh.Identity, grant mesh.Grant, local bool) error {
		if config == nil {
			return model.Err("reconnect_enrollment_missing", "the retained peer enrollment is missing")
		}
		p, e := mesh.ResolveConfig(*config, other.Host)
		if e != nil {
			return e
		}
		if recovery && p.Pair == pair && sameReconnectPrincipal(p.Identity, other) && reflect.DeepEqual(p.Grant, grant) && (p.State == "pending" || p.State == "active") {
			return nil
		}
		if p.State != "revoked" {
			return model.Err("enrollment_not_revoked", "destination or source enrollment is still active/pending; finish the exact pending disconnect before reconnecting")
		}
		if !sameReconnectPrincipal(p.Identity, other) || p.Pair != b.Prior.Pair {
			return model.Err("reconnect_identity_changed", "previous enrollment changed before reconnect")
		}
		if local && !reflect.DeepEqual(p, b.Prior) {
			return model.Err("reconnect_enrollment_changed", "retained local enrollment changed since this draft")
		}
		return nil
	}
	if e = check(own.Config, *remote.Identity, r.Draft.Reverse, true); e != nil {
		return e
	}
	return check(remote.Config, *own.Identity, r.Draft.Forward, false)
}

func reconnectIntent(request ConnectRequest) (string, any, StartRequest) {
	if request.Reconnect != nil {
		return "reconnect", *request.Reconnect, StartRequest{Kind: "reconnect", Reconnect: request.Reconnect}
	}
	return "connect", request, StartRequest{Kind: "connect", Connect: &request}
}

// Retain caller-owned grants and nested snapshots through the strict decoder.
func cloneReconnect(r ReconnectRequest) (ReconnectRequest, error) {
	raw, e := json.Marshal(r)
	if e != nil {
		return ReconnectRequest{}, e
	}
	var out ReconnectRequest
	e = mesh.Decode(raw, &out)
	return out, e
}

// ResumeReconnect is an explicit new attempt of the original immutable draft.
// Merely retrying Reconnect never enters this path.
func (a *Coordinator) ResumeReconnect(key string, r ReconnectRequest, yes, dry bool) (int, error) {
	op, ok, e := mesh.ReadCoordination(a.Home, key)
	if e != nil {
		return 1, e
	}
	digest, _ := mesh.ReviewDigest(r)
	if !ok || op.Kind != "reconnect" || op.Digest != digest || op.Phase != "incomplete" || op.Worker != "" || op.ResolvedBy != "" || op.ParentKey != "" {
		return 1, model.Err("review_changed", "only this exact inactive incomplete reconnect may prepare recovery")
	}
	previous := a.attemptParent
	a.attemptParent = key
	defer func() { a.attemptParent = previous }()
	return a.Reconnect(key, r, yes, dry)
}

// ReconnectBlocker describes retained workflow exclusion, never permission to
// settle, erase, resume or revoke the underlying native/resource obligations.
type ReconnectBlocker struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Phase  string `json:"phase"`
	Worker bool   `json:"worker_live"`
}

func ReconnectBlockers(home, host string) ([]ReconnectBlocker, error) {
	if !exactReconnectHost(host) {
		return nil, model.Err("invalid_request", "exact reconnect host ID required")
	}
	ops, e := mesh.ListCoordinations(home)
	if e != nil {
		return nil, e
	}
	out := []ReconnectBlocker{}
	for _, op := range ops {
		if op.ResolvedBy != "" {
			continue
		}
		switch op.Phase {
		case "completed", "declined", "cancelled", "rejected", "handed_off":
			continue
		}
		for _, target := range op.Targets {
			if target == "host:"+host {
				out = append(out, ReconnectBlocker{Key: op.Key, Kind: op.Kind, Phase: op.Phase, Worker: op.Worker != "" && time.Now().Before(op.WorkerExpires)})
				break
			}
		}
	}
	return out, nil
}
func reconnectBlocked(home, host string) error {
	blockers, e := ReconnectBlockers(home, host)
	if e != nil {
		return e
	}
	if len(blockers) == 0 {
		return nil
	}
	details := []string{}
	for _, b := range blockers {
		details = append(details, fmt.Sprintf("%q (%s, %s)", b.Key, b.Kind, b.Phase))
	}
	return model.Err("mesh_target_busy", "Reconnect is blocked by retained operation "+strings.Join(details, ", ")+". Inspect its exact key with arm mesh operations KEY. Settle the original operation or use the dashboard's explicit reviewed handoff, then prepare a fresh reconnect review. No prior obligation was released.")
}
