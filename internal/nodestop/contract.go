// Package nodestop defines the reviewed, generation-bound node-stop contract.
// Destination admission binds current grants, exact generations and retained
// elapsed reviews. Native joined closure and exact-generation observations are
// independent evidence; validating a plan or receipt does not establish them.
package nodestop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"getarm.org/internal/model"
)

const (
	Schema          = "arm.node-stop.plan/v1"
	ReviewTTL       = 2 * time.Minute
	MaxPlanBytes    = 48 << 10
	MaxItems        = 64
	StartupContract = "arm-desired-state-gated-v1"
	SupervisorScope = "Stops this ARM authority and its connections. ARM-controlled startup paths honor the durable stopped intent; ownership of external OS service managers is unverified. No host-OS or service-manager shutdown is performed."
)

type Target struct {
	Host        string             `json:"host"`
	Certificate string             `json:"certificate_sha256"`
	SPKI        string             `json:"spki_sha256"`
	Owner       string             `json:"owner"`
	Home        string             `json:"home"`
	Directory   model.FileIdentity `json:"directory"`
	Generation  string             `json:"backend_generation"`
	Startup     string             `json:"startup_contract"`
}

type Actor struct {
	Host          string `json:"source_host"`
	Certificate   string `json:"certificate_sha256"`
	SPKI          string `json:"spki_sha256"`
	Pair          string `json:"pair"`
	GrantRevision int    `json:"grant_revision"`
}

// LeaseSummary is a bounded preview, never permission to inspect or mutate a
// lease. Full private census bytes remain bound by Impact.SHA256.
type LeaseSummary struct {
	Owner     string `json:"owner"`
	Username  string `json:"owner_username,omitempty"`
	Reason    string `json:"reason"`
	Agent     string `json:"agent,omitempty"`
	Project   string `json:"project,omitempty"`
	Truncated bool   `json:"truncated"`
}

// LeaseReadScope is supplied only in process under the authenticated peer lock.
// It is not decoded from requests and does not make node-stop a read grant.
type LeaseReadScope struct {
	Source string
	Own    bool
	All    bool
}

func (s LeaseReadScope) Allows(origin string) bool {
	return s.All || s.Own && origin == s.Source && s.Source != ""
}

type Obligation struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	State        string        `json:"state"`
	Revision     int64         `json:"revision"`
	LeaseDetails string        `json:"lease_details,omitempty"`
	Lease        *LeaseSummary `json:"lease,omitempty"`
}

type Blocker struct {
	Class  string `json:"class"`
	ID     string `json:"id"`
	Detail string `json:"detail"`
}

// Impact is the destination's complete bounded census, not a client claim that
// unknown work is free. The digest additionally binds provider/private details
// that must not leak through administrative inspection without the applicable
// observe-own origin binding or observe-all read grant.
type Impact struct {
	Complete    bool         `json:"complete"`
	SHA256      string       `json:"scope_sha256"`
	Outstanding int          `json:"outstanding"`
	Obligations []Obligation `json:"obligations"`
	Blockers    []Blocker    `json:"blockers"`
	Peers       []string     `json:"affected_peers"`
}

type Plan struct {
	Schema          string                `json:"schema"`
	Key             string                `json:"key"`
	Target          Target                `json:"target"`
	Actor           Actor                 `json:"actor"`
	DesiredRevision uint64                `json:"desired_state_revision"`
	Policy          model.ShutdownRequest `json:"policy"`
	Reason          string                `json:"reason"`
	Impact          Impact                `json:"impact"`
	Observed        time.Time             `json:"observed_at"`
	Expires         time.Time             `json:"expires_at"`
	Supervisor      string                `json:"supervisor_scope"`
}

type Apply struct {
	Key          string `json:"key"`
	ReviewSHA256 string `json:"review_sha256"`
	Plan         Plan   `json:"plan"`
}

// Current is supplied in-process by the destination while the same Store.WithPeer
// admission lock still protects the authenticated grant. It is never decoded
// from wire params. Commit must occur inside that lock, not after releasing it.
type Current struct {
	Target          Target `json:"-"`
	Actor           Actor  `json:"-"`
	NodeStopGranted bool   `json:"-"`
	DesiredRevision uint64 `json:"-"`
	DesiredRunning  bool   `json:"-"`
	Impact          Impact `json:"-"`
}

// Boundary is installed once by Serve while holding the real daemon.lock.
// Neither a serialized request nor an old/new Boundary can manufacture its
// in-process Admission capability. Durable duplicate lookup belongs to the
// journal transaction and must precede any new admission/effect.
type Boundary struct {
	target  Target
	elapsed elapsedPermits
}
type Admission struct {
	boundary *Boundary
	plan     Plan
	digest   string
}

func hashText(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(value) == 2*size && len(decoded) == size && value == strings.ToLower(value)
}
func keyText(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func (t Target) validate() error {
	uid, err := strconv.ParseUint(t.Owner, 10, 32)
	if !hashText(t.Host, 16) || !hashText(t.Certificate, 32) || !hashText(t.SPKI, 32) || !hashText(t.Generation, 16) || t.Startup != StartupContract || err != nil || strconv.FormatUint(uid, 10) != t.Owner || uint32(uid) != t.Directory.Owner || t.Directory.Inode == 0 || !filepath.IsAbs(t.Home) || filepath.Clean(t.Home) != t.Home || len(t.Home) > 4096 || !utf8.ValidString(t.Home) {
		return model.Err("node_stop_scope_invalid", "exact destination identity, account, private directory and gated backend generation are required")
	}
	return nil
}
func (a Actor) validate() error {
	if !hashText(a.Host, 16) || !hashText(a.Certificate, 32) || !hashText(a.SPKI, 32) || !hashText(a.Pair, 32) || a.GrantRevision < 1 {
		return model.Err("node_stop_actor_invalid", "exact authenticated peer enrollment and grant revision are required")
	}
	return nil
}
func clonePlan(p Plan) Plan {
	p.Impact.Obligations = slices.Clone(p.Impact.Obligations)
	for n := range p.Impact.Obligations {
		if v := p.Impact.Obligations[n].Lease; v != nil {
			copy := *v
			p.Impact.Obligations[n].Lease = &copy
		}
	}
	p.Impact.Blockers = slices.Clone(p.Impact.Blockers)
	p.Impact.Peers = slices.Clone(p.Impact.Peers)
	return p
}
func (i Impact) validate() error {
	if i.Obligations == nil || i.Blockers == nil || i.Peers == nil || i.Outstanding < 0 || i.Outstanding > 1000000 || len(i.Obligations) > MaxItems || len(i.Blockers) > MaxItems || len(i.Peers) > MaxItems || (i.Complete && (!hashText(i.SHA256, 32) || i.Outstanding != len(i.Obligations))) {
		return model.Err("node_stop_scope_incomplete", "bounded complete obligation census required; truncation is not an empty safe scope")
	}
	last := ""
	for _, item := range i.Obligations {
		if !keyText(item.ID) || len(item.Kind) < 1 || !utf8.ValidString(item.Kind) || len(item.Kind) > 64 || len(item.State) < 1 || !utf8.ValidString(item.State) || len(item.State) > 32 || item.Revision < 1 || item.ID <= last {
			return model.Err("node_stop_scope_invalid", "unique ordered typed obligations are required")
		}
		if item.LeaseDetails != "" && item.LeaseDetails != "permitted" && item.LeaseDetails != "not-permitted" || item.Lease != nil && (item.Kind != "lease" || item.LeaseDetails != "permitted") || item.LeaseDetails == "permitted" && item.Lease == nil || item.Kind != "lease" && item.LeaseDetails != "" {
			return model.Err("node_stop_scope_invalid", "lease previews require explicit read scope and typed obligations")
		}
		if v := item.Lease; v != nil {
			uid, err := strconv.ParseUint(v.Owner, 10, 32)
			if err != nil || strconv.FormatUint(uid, 10) != v.Owner || !utf8.ValidString(v.Username+v.Reason+v.Agent+v.Project) {
				return model.Err("node_stop_scope_invalid", "valid recorded lease account and preview text required")
			}
			raw, err := json.Marshal(v)
			if err != nil || len(raw) > 8192 {
				return model.Err("node_stop_scope_invalid", "bounded lease preview required")
			}
		}
		last = item.ID
	}
	last = ""
	for _, peer := range i.Peers {
		if !hashText(peer, 16) || peer <= last {
			return model.Err("node_stop_scope_invalid", "unique ordered affected peer identities are required")
		}
		last = peer
	}
	for _, blocker := range i.Blockers {
		if !slices.Contains([]string{"unknown", "unjoined", "namespace", "unsupported-supervisor"}, blocker.Class) || !keyText(blocker.ID) || blocker.Detail == "" || !utf8.ValidString(blocker.Detail) || len(blocker.Detail) > 256 {
			return model.Err("node_stop_scope_invalid", "typed bounded blockers required")
		}
	}
	return nil
}
func (p Plan) validate() error {
	if p.Schema != Schema || !keyText(p.Key) || strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 4096 || !utf8.ValidString(p.Reason) || (p.DesiredRevision == 0 || p.DesiredRevision >= MaxRevision) || p.Supervisor != SupervisorScope || p.Observed.IsZero() || !p.Expires.Equal(p.Observed.Add(ReviewTTL)) {
		return model.Err("invalid_request", "complete exact node-stop plan required")
	}
	if err := p.Target.validate(); err != nil {
		return err
	}
	if err := p.Actor.validate(); err != nil {
		return err
	}
	if p.Actor.Host == p.Target.Host {
		return model.Err("invalid_request", "use the local stop workflow for this authority")
	}
	if p.Policy.Force && p.Policy.Wait || p.Policy.Timeout < 0 || p.Policy.Timeout > 0 && !p.Policy.Wait {
		return model.Err("invalid_request", "force and wait are exclusive; a positive timeout requires wait")
	}
	if err := p.Impact.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > MaxPlanBytes {
		return model.Err("node_stop_scope_incomplete", "node-stop plan exceeds its closed frame bound")
	}
	return planFrameBudget(p)
}
func (p Plan) Digest() (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func NewBoundary(target Target) (*Boundary, error) {
	if err := target.validate(); err != nil {
		return nil, err
	}
	return &Boundary{target: target, elapsed: elapsedPermits{now: nativeReviewTick}}, nil
}
func (b *Boundary) Prepare(key string, actor Actor, desiredRevision uint64, policy model.ShutdownRequest, reason string, impact Impact, now time.Time) (out Plan, resultErr error) {
	var p Plan
	defer func() { traceBoundary(b, "prepare", &p, resultErr) }()
	if b == nil {
		return Plan{}, model.Err("node_stop_unavailable", "backend generation is unavailable")
	}
	p = Plan{Schema: Schema, Key: key, Target: b.target, Actor: actor, DesiredRevision: desiredRevision, Policy: policy, Reason: reason, Impact: impact, Observed: now.UTC(), Expires: now.UTC().Add(ReviewTTL), Supervisor: SupervisorScope}
	p = clonePlan(p)
	if err := p.validate(); err != nil {
		return Plan{}, err
	}
	digest, err := p.Digest()
	if err != nil {
		return Plan{}, err
	}
	if err = b.elapsed.issue(digest, p.Observed); err != nil {
		return Plan{}, model.Err("node_stop_review_expired", err.Error())
	}
	return p, nil
}
func (b *Boundary) Admit(q Apply, current Current, now time.Time) (out Admission, resultErr error) {
	defer func() { traceBoundary(b, "admit", &q.Plan, resultErr) }()
	if b == nil || !current.NodeStopGranted {
		return Admission{}, model.Err("forbidden", "peer lacks explicit current destination-local node-stop grant")
	}
	p := q.Plan
	digest, err := p.Digest()
	if err != nil {
		return Admission{}, err
	}
	if q.Key != p.Key || q.ReviewSHA256 != digest {
		return Admission{}, model.Err("node_stop_review_changed", "exact original key and reviewed plan digest are required")
	}
	if p.Target != b.target || current.Target != b.target {
		return Admission{}, model.Err("node_generation_changed", "backend identity, authority, account, home or generation changed; no stop was admitted")
	}
	if current.Actor != p.Actor {
		return Admission{}, model.Err("node_context_changed", "authenticated peer enrollment or grant revision changed")
	}
	if current.DesiredRevision != p.DesiredRevision || !current.DesiredRunning {
		return Admission{}, model.Err("node_stop_state_changed", "desired-state intent changed; use original-key lookup before another review")
	}
	if now.Before(p.Observed) || !now.Before(p.Expires) {
		return Admission{}, model.Err("node_stop_review_expired", "node-stop review expired or its clock observation changed")
	}
	left, leftErr := json.Marshal(p.Impact)
	right, rightErr := json.Marshal(current.Impact)
	if !p.Impact.Complete || !current.Impact.Complete || current.Impact.validate() != nil || leftErr != nil || rightErr != nil || !bytes.Equal(left, right) || len(p.Impact.Blockers) > 0 {
		return Admission{}, model.Err("node_stop_blocked", "unknown, changed or unjoined obligations require explicit reconciliation; force does not erase them")
	}
	if !p.Policy.Force && !p.Policy.Wait && p.Impact.Outstanding != 0 {
		return Admission{}, model.Err("active_leases", "outstanding obligations require a separately reviewed wait or force policy")
	}
	if err = b.elapsed.check(digest); err != nil {
		return Admission{}, model.Err("node_stop_review_expired", err.Error())
	}
	return Admission{boundary: b, plan: clonePlan(p), digest: digest}, nil
}
func (b *Boundary) Owns(a Admission) bool { return b != nil && a.boundary == b && a.digest != "" }
func (a Admission) Plan() (Plan, error) {
	if a.boundary == nil {
		return Plan{}, fmt.Errorf("no admitted in-process capability")
	}
	return clonePlan(a.plan), nil
}

// CheckAdmission binds the final transaction commit-attempt cut to the same
// retained elapsed permit. Durability/ack may complete later; accepted duty then
// survives expiry, grant changes or a lost reply and is never re-admitted.
func (b *Boundary) CheckAdmission(a Admission) (resultErr error) {
	defer func() { traceBoundary(b, "check-admission", &a.plan, resultErr) }()
	if !b.Owns(a) {
		return model.Err("node_generation_changed", "retained admission belongs to another boundary")
	}
	if err := b.elapsed.check(a.digest); err != nil {
		return model.Err("node_stop_review_expired", err.Error())
	}
	return nil
}
