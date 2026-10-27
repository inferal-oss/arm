package meshops

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// StopLeaseRequest refers to a single item of an already stored exact review.
// It cannot prepare/approve Stop, supply a replacement host or borrow its grant.
type StopLeaseRequest struct {
	Key            string `json:"key"`
	ReviewRevision int64  `json:"review_revision"`
	ReviewHash     string `json:"review_sha256"`
	Obligation     string `json:"obligation"`
}

// StopLeaseDetail is an explicit allowlist. Do not return a model.Lease here:
// it also contains managed paths, process identities and policy internals.
type StopLeasePool struct {
	ID       string `json:"pool"`
	Quantity int64  `json:"quantity"`
}
type StopLeasePort struct {
	State    string `json:"state"`
	Name     string `json:"name,omitempty"`
	Network  string `json:"network"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	RangeEnd int    `json:"range_end,omitempty"`
	Mode     string `json:"mode"`
}

type StopLeaseDetail struct {
	Key              string          `json:"key"`
	ReviewRevision   int64           `json:"review_revision"`
	ReviewHash       string          `json:"review_sha256"`
	Obligation       string          `json:"obligation"`
	ID               string          `json:"id"`
	Host             string          `json:"host"`
	Owner            string          `json:"owner"`
	Username         string          `json:"owner_username,omitempty"`
	Reason           string          `json:"reason"`
	Agent            string          `json:"agent,omitempty"`
	Project          string          `json:"project,omitempty"`
	Origin           string          `json:"origin_peer,omitempty"`
	Resources        model.Resources `json:"resources"`
	Kind             string          `json:"kind,omitempty"`
	Pools            []StopLeasePool `json:"pools,omitempty"`
	Ports            []StopLeasePort `json:"ports,omitempty"`
	State            string          `json:"state"`
	Outcome          string          `json:"outcome,omitempty"`
	Revision         int             `json:"revision"`
	ReviewedRevision int64           `json:"reviewed_revision"`
	Changed          bool            `json:"changed_since_review"`
	Created          time.Time       `json:"created_at"`
	Started          *time.Time      `json:"started_at,omitempty"`
	Expires          *time.Time      `json:"expires_at,omitempty"`
	Ended            *time.Time      `json:"ended_at,omitempty"`
}

func stopLeaseReview(home string, q StopLeaseRequest) (StopReview, nodestop.Obligation, error) {
	var review StopReview
	var item nodestop.Obligation
	if !mesh.ValidKey(q.Key) || q.ReviewRevision < 1 || !strings.HasPrefix(q.Obligation, "lease:") {
		return review, item, model.Err("invalid_request", "exact stored Stop review and lease obligation required")
	}
	op, exists, err := mesh.ReadCoordination(home, q.Key)
	if err != nil {
		return review, item, err
	}
	if !exists || op.Kind != "stop" || op.ReviewRevision != q.ReviewRevision || op.ReviewHash != q.ReviewHash {
		return review, item, model.Err("review_changed", "This Stop review changed or is unavailable. Refresh its exact source receipt; no action was taken.")
	}
	if err = nodestop.Decode(op.Review, &review); err != nil {
		return review, item, err
	}
	hash, err := mesh.ReviewDigest(review)
	if err != nil || hash != q.ReviewHash || review.Validate() != nil || review.Key != q.Key || len(op.Targets) != 1 || op.Targets[0] != "host:"+review.Plan.Target.Host {
		return review, item, model.Err("review_changed", "Stored Stop review does not match its exact target and digest")
	}
	var request StartRequest
	if mesh.Decode(op.Request, &request) != nil || request.Kind != "stop" || request.Key != q.Key || request.Stop == nil || request.Connect != nil || request.Reconnect != nil || request.Disconnect != nil || request.Provision != nil || request.Stop.Host != review.Plan.Target.Host || request.Stop.Reason != review.Plan.Reason || request.Stop.Policy != review.Plan.Policy {
		return review, item, model.Err("idempotency_conflict", "Original immutable Stop request differs")
	}
	digest, err := mesh.ReviewDigest(*request.Stop)
	if err != nil || digest != op.Digest {
		return review, item, model.Err("idempotency_conflict", "Original immutable Stop digest differs")
	}
	for _, v := range review.Plan.Impact.Obligations {
		if v.ID == q.Obligation && v.Kind == "lease" {
			item = v
			break
		}
	}
	if item.ID == "" {
		return review, item, model.Err("not_found", "Lease is not an obligation of this exact Stop review")
	}
	if item.LeaseDetails != "permitted" || item.Lease == nil {
		return review, item, model.Err("forbidden", "Lease details are not shared by this destination. Node-stop permission does not grant access to private owners or purposes.")
	}
	return review, item, nil
}

// InspectStopLease is a bounded synchronous read owned by the existing Service.
// It remains available while the independent Stop review worker awaits a human.
// Close waits for this original read; no SSH, new worker or mutation is started.
func (s *Service) InspectStopLease(q StopLeaseRequest) (StopLeaseDetail, error) {
	var empty StopLeaseDetail
	release := func() {}
	if s.Register != nil {
		var err error
		release, err = s.Register()
		if err != nil {
			return empty, err
		}
	}
	defer release()
	s.mu.Lock()
	if s.closing || s.Context == nil || s.Context.Err() != nil {
		s.mu.Unlock()
		return empty, model.Err("backend_stopping", "operator coordinator is closing")
	}
	if s.inspections >= 2 {
		s.mu.Unlock()
		return empty, model.Err("busy", "Two lease inspections are already finishing; try an explicit read later")
	}
	ctx, cancel := context.WithTimeout(s.Context, 4*time.Second)
	var c *Coordinator
	if s.New != nil {
		c = s.New(ctx)
	}
	if c != nil {
		c.Context = ctx
	}
	if c == nil || c.Call == nil || c.Inspect == nil {
		cancel()
		s.mu.Unlock()
		return empty, model.Err("node_stop_unavailable", "Lease inspector unavailable")
	}
	s.inspections++
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.inspections--; s.mu.Unlock(); s.wg.Done() }()
	review, item, err := stopLeaseReview(s.Home, q)
	if err != nil {
		return empty, err
	}
	p := review.Plan
	peer, own, err := c.stopPinnedPeer(p.Target.Host)
	if err != nil {
		return empty, err
	}
	if peer.Identity.Fingerprint != p.Target.Certificate || peer.Identity.SPKI != p.Target.SPKI || peer.Identity.Owner != p.Target.Owner || peer.Pair != p.Actor.Pair || own.Identity.Host != p.Actor.Host || own.Identity.Fingerprint != p.Actor.Certificate || own.Identity.SPKI != p.Actor.SPKI {
		return empty, model.Err("node_context_changed", "Source identity or exact destination enrollment changed; refresh the Stop receipt")
	}
	scope, err := c.stopLiveScope(peer, own)
	if err != nil {
		return empty, err
	}
	if scope.Revision != p.Actor.GrantRevision || scope.NodeStop == nil || scope.NodeStop.Generation != p.Target.Generation {
		return empty, model.Err("node_context_changed", "Destination grant revision or backend generation changed; these lease details are unavailable")
	}
	all := slices.Contains(scope.Grant.Permissions, "observe-all")
	ownRead := slices.Contains(scope.Grant.Permissions, "observe-own")
	if !all && !ownRead {
		return empty, model.Err("forbidden", "Destination no longer shares lease details with this source")
	}
	raw, _ := json.Marshal(struct {
		ID         string `json:"id"`
		Generation string `json:"backend_generation"`
	}{strings.TrimPrefix(item.ID, "lease:"), p.Target.Generation})
	var lease model.Lease
	// The actual destination checks ExpectedEnrollment and its current read grant
	// under WithPeer. Its response is projected below, never forwarded wholesale.
	err = c.call("mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{p.Target.Host, mesh.Request{Expected: &mesh.ExpectedEnrollment{Pair: scope.Pair, Revision: scope.Revision}, Operation: "inspect", Params: raw}}, &lease)
	if err != nil {
		return empty, err
	}
	if lease.ID != strings.TrimPrefix(item.ID, "lease:") || lease.Host != p.Target.Host || lease.Revision < 1 || !all && lease.Request.Peer != p.Actor.Host {
		return empty, model.Err("node_context_changed", "Destination lease identity or read scope differs; no details returned")
	}
	if _, _, err = stopLeaseReview(s.Home, q); err != nil {
		return empty, err
	}
	detail := StopLeaseDetail{Key: q.Key, ReviewRevision: q.ReviewRevision, ReviewHash: q.ReviewHash, Obligation: q.Obligation, ID: lease.ID, Host: lease.Host, Owner: lease.Owner, Username: lease.OwnerUsername, Reason: lease.Request.Reason, Agent: lease.Request.Agent, Project: lease.Request.Project, Origin: lease.Request.Peer, Resources: lease.Request.Resources, State: lease.State, Outcome: lease.Outcome, Revision: lease.Revision, ReviewedRevision: item.Revision, Changed: int64(lease.Revision) != item.Revision, Created: lease.Created, Started: lease.Started, Expires: lease.Expires, Ended: lease.Ended, Kind: lease.Request.Kind}
	for _, claim := range lease.Request.Pools {
		detail.Pools = append(detail.Pools, StopLeasePool{ID: claim.Pool, Quantity: claim.Quantity})
	}
	if len(lease.Ports) > 0 {
		for _, binding := range lease.Ports {
			detail.Ports = append(detail.Ports, StopLeasePort{Name: binding.Name, Network: binding.Network, Address: binding.Address, Port: binding.Port, RangeEnd: binding.RangeEnd, Mode: binding.Mode, State: binding.State})
		}
	} else {
		for _, claim := range lease.Request.Ports {
			detail.Ports = append(detail.Ports, StopLeasePort{Name: claim.Name, Network: claim.Network, Address: claim.Address, Port: claim.Port, RangeEnd: claim.RangeEnd, Mode: claim.Mode, State: "requested"})
		}
	}
	return detail, nil
}
