// Package workspace implements the unrouted messaging-directory ledger.
// Workspace principals are deliberately distinct from direct mesh peers and
// convey no resource, execution, SSH, filesystem or native operator authority.
package workspace

import (
	"encoding/json"
	"time"
)

const (
	Schema         = 1
	MaxWorkspaces  = 8
	MaxMembers     = 128
	MaxChanges     = 1024
	MaxOperations  = 256
	MaxRecordBytes = 1 << 20
	MaxLedgerBytes = 32 << 20
	MaxClockSkew   = 5 * time.Second
)

type Authority struct {
	Host string `json:"host"`
	UID  string `json:"uid"`
}

// Identity contains only public node identity, never a transport address,
// execution account, private key, environment or agent session/configuration.
type Identity struct {
	Host        string    `json:"host"`
	Name        string    `json:"display_name"`
	Certificate string    `json:"certificate"`
	Fingerprint string    `json:"certificate_sha256"`
	SPKI        string    `json:"spki_sha256"`
	Expires     time.Time `json:"certificate_expires"`
}
type Policy struct {
	MaxMembers         int           `json:"max_members"`
	MaxPublishedAgents int           `json:"max_published_agents"`
	MembershipTTL      time.Duration `json:"membership_ttl_ns"`
	StreamTTL          time.Duration `json:"stream_ttl_ns"`
	RefreshInterval    time.Duration `json:"refresh_interval_ns"`
	ClockSkew          time.Duration `json:"max_clock_skew_ns"`
	AdmissionMode      string        `json:"admission_mode"`
	HostInbox          bool          `json:"host_inbox_enabled"`
	Relay              bool          `json:"relay_enabled"`
}

func DefaultPolicy() Policy {
	return Policy{128, 32, time.Minute, 30 * time.Second, 15 * time.Second, MaxClockSkew, "delegated_introducers", true, true}
}

type Instance struct {
	Schema         int       `json:"schema_version"`
	Authority      Authority `json:"local_authority"`
	ID             string    `json:"workspace"`
	Generation     string    `json:"generation"`
	Name           string    `json:"name"`
	Role           string    `json:"role"`
	Directory      Identity  `json:"directory"`
	Self           Identity  `json:"self"`
	State          string    `json:"state"`
	Revision       int64     `json:"revision"`
	PolicyRevision int64     `json:"policy_revision"`
	Policy         Policy    `json:"policy"`
	SelfGeneration int64     `json:"self_membership_generation"`
	ConsentHash    string    `json:"consent_sha256"`
	Created        time.Time `json:"created_at"`
	Updated        time.Time `json:"updated_at"`
}

type Principal struct {
	Workspace            string `json:"workspace"`
	Generation           string `json:"generation"`
	Host                 string `json:"source_host"`
	MembershipGeneration int64  `json:"membership_generation"`
	Fingerprint          string `json:"certificate_sha256"`
}

type Member struct {
	Schema               int        `json:"schema_version"`
	Workspace            string     `json:"workspace"`
	Generation           string     `json:"generation"`
	Identity             Identity   `json:"identity"`
	MembershipGeneration int64      `json:"membership_generation"`
	State                string     `json:"state"`
	Introducer           bool       `json:"introducer"`
	HostInbox            bool       `json:"host_inbox_enabled"`
	IntroducedBy         Principal  `json:"introduced_by"`
	AdmissionKey         string     `json:"admission_key"`
	ConsentHash          string     `json:"consent_sha256"`
	Revision             int64      `json:"revision"`
	Created              time.Time  `json:"created_at"`
	Updated              time.Time  `json:"updated_at"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	Reason               string     `json:"reason"`
}

type TrustedMember struct {
	Schema               int      `json:"schema_version"`
	Workspace            string   `json:"workspace"`
	Generation           string   `json:"generation"`
	Identity             Identity `json:"identity"`
	MembershipGeneration int64    `json:"membership_generation"`
	State                string   `json:"state"`
	FirstSeen            int64    `json:"first_seen_revision"`
	LatestSeen           int64    `json:"latest_seen_revision"`
	RotationReview       string   `json:"rotation_review,omitempty"`
}

type ClockRecord struct {
	Schema       int        `json:"schema_version"`
	Workspace    string     `json:"workspace"`
	Generation   string     `json:"generation"`
	WallFloor    time.Time  `json:"wall_floor"`
	LastIssued   *time.Time `json:"last_issued_at,omitempty"`
	IssuedCount  int64      `json:"issued_count"`
	LastRevision int64      `json:"last_revision"`
}

type Deny struct {
	Schema    int       `json:"schema_version"`
	Workspace string    `json:"workspace"`
	Host      string    `json:"host"`
	Revision  int64     `json:"revision"`
	Key       string    `json:"key"`
	Actor     Authority `json:"actor"`
	Reason    string    `json:"reason"`
	Created   time.Time `json:"created_at"`
}

type Review struct {
	Name           string    `json:"name"`
	RequestHash    string    `json:"request_sha256"`
	Schema         int       `json:"schema_version"`
	Kind           string    `json:"kind"`
	Workspace      string    `json:"workspace"`
	Generation     string    `json:"generation"`
	Source         Identity  `json:"source"`
	Directory      Identity  `json:"directory"`
	Policy         Policy    `json:"policy"`
	PolicyRevision int64     `json:"policy_revision"`
	Candidate      *Identity `json:"candidate,omitempty"`
	Introducer     bool      `json:"introducer"`
	HostInbox      bool      `json:"host_inbox_enabled"`
	Reason         string    `json:"reason"`
	Expires        time.Time `json:"expires_at"`
}

type OperationPrivate struct {
	Nonce              string     `json:"proof_nonce,omitempty"`
	Boot               string     `json:"worker_boot"`
	Introducer         *Principal `json:"introducer,omitempty"`
	IntroducerRevision int64      `json:"introducer_revision"`
	CandidateRevision  int64      `json:"candidate_revision"`
	ProofCount         int        `json:"proof_count"`
	LastChallenge      string     `json:"last_challenge_sha256,omitempty"`
}

type Operation struct {
	Schema        int               `json:"schema_version"`
	Workspace     string            `json:"workspace"`
	Principal     string            `json:"principal"`
	Key           string            `json:"key"`
	Kind          string            `json:"kind"`
	RequestHash   string            `json:"request_sha256"`
	Request       json.RawMessage   `json:"request"`
	Revision      int64             `json:"revision"`
	Phase         string            `json:"phase"`
	Review        *Review           `json:"review,omitempty"`
	ReviewHash    string            `json:"review_sha256,omitempty"`
	ReviewExpires time.Time         `json:"review_expires_at"`
	Result        *Member           `json:"member,omitempty"`
	Private       *OperationPrivate `json:"private,omitempty"`
	Error         string            `json:"error,omitempty"`
	Created       time.Time         `json:"created_at"`
	Updated       time.Time         `json:"updated_at"`
}

// View never exposes candidate nonces/worker credentials. No transport endpoint
// is wired in this checkpoint; future adapters must project, not marshal records.
func (o Operation) View() Operation { o.Private = nil; return o }

type Change struct {
	Members        []Member        `json:"members,omitempty"`
	Trusted        []TrustedMember `json:"trusted,omitempty"`
	Schema         int             `json:"schema_version"`
	Workspace      string          `json:"workspace"`
	Kind           string          `json:"kind"`
	Actor          string          `json:"actor"`
	Key            string          `json:"key"`
	Revision       int64           `json:"revision"`
	At             time.Time       `json:"at"`
	Before         *Instance       `json:"before,omitempty"`
	After          Instance        `json:"after"`
	MemberBefore   *Member         `json:"member_before,omitempty"`
	MemberAfter    *Member         `json:"member_after,omitempty"`
	OperationPhase string          `json:"operation_phase"`
}

type Observation struct {
	Schema       int       `json:"schema_version"`
	Workspace    string    `json:"workspace"`
	Generation   string    `json:"generation"`
	DirectoryPin string    `json:"directory_pin"`
	Revision     int64     `json:"revision"`
	SnapshotHash string    `json:"snapshot_sha256"`
	Received     time.Time `json:"received_at"`
	Snapshot     Snapshot  `json:"snapshot"`
}

type CreateRequest struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Policy Policy `json:"policy"`
	Reason string `json:"reason"`
}
type JoinRequest struct {
	Key               string   `json:"key"`
	Workspace         string   `json:"workspace"`
	Generation        string   `json:"generation"`
	Name              string   `json:"name"`
	Directory         Identity `json:"directory"`
	Policy            Policy   `json:"policy"`
	PolicyRevision    int64    `json:"policy_revision"`
	Introducer        string   `json:"introducer_host"`
	RequestIntroducer bool     `json:"request_introducer"`
	Reason            string   `json:"reason"`
}
type AdmissionRequest struct {
	Key            string   `json:"key"`
	Candidate      Identity `json:"candidate"`
	ConsentHash    string   `json:"consent_sha256"`
	Introducer     bool     `json:"introducer"`
	HostInbox      bool     `json:"host_inbox_enabled"`
	PolicyRevision int64    `json:"policy_revision"`
	Reason         string   `json:"reason"`
}
type MutationRequest struct {
	Key      string `json:"key"`
	Host     string `json:"host"`
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
}

type Challenge struct {
	Recovery            *RecoveryBinding `json:"recovery,omitempty"`
	Introducer          Principal        `json:"introducer"`
	CandidateIntroducer bool             `json:"candidate_introducer"`
	HostInbox           bool             `json:"host_inbox_enabled"`
	Workspace           string           `json:"workspace"`
	Generation          string           `json:"generation"`
	Directory           Identity         `json:"directory"`
	Candidate           Identity         `json:"candidate"`
	ConsentHash         string           `json:"consent_sha256"`
	Nonce               string           `json:"nonce"`
	PolicyRevision      int64            `json:"policy_revision"`
	Expires             time.Time        `json:"expires_at"`
}
type Proof struct {
	Recovery    *RecoveryBinding `json:"recovery,omitempty"`
	Workspace   string           `json:"workspace"`
	Generation  string           `json:"generation"`
	Source      string           `json:"source_host"`
	Target      string           `json:"target_host"`
	SourcePin   string           `json:"source_pin"`
	TargetPin   string           `json:"target_pin"`
	ConsentHash string           `json:"consent_sha256"`
	Nonce       string           `json:"nonce"`
}

// MemberCard is the only directory-distributed member shape. Internal admission
// reasons, operation keys, consent digests and actor/account details stay local.
type MemberCard struct {
	Identity             Identity `json:"identity"`
	MembershipGeneration int64    `json:"membership_generation"`
	State                string   `json:"state"`
	Introducer           bool     `json:"introducer"`
	HostInbox            bool     `json:"host_inbox_enabled"`
	Revision             int64    `json:"revision"`
}

type RefreshRequest struct {
	Source Principal `json:"source"`
	Nonce  string    `json:"nonce"`
}
type Snapshot struct {
	Schema              int           `json:"schema_version"`
	Workspace           string        `json:"workspace"`
	Generation          string        `json:"generation"`
	Directory           Identity      `json:"directory"`
	Requester           string        `json:"requester_host"`
	RequesterGeneration int64         `json:"requester_generation"`
	Nonce               string        `json:"nonce"`
	Revision            int64         `json:"revision"`
	PolicyRevision      int64         `json:"policy_revision"`
	Policy              Policy        `json:"policy"`
	Issued              time.Time     `json:"issued_at"`
	ValidUntil          time.Time     `json:"valid_until"`
	TTL                 time.Duration `json:"ttl_ns"`
	Members             []MemberCard  `json:"members"`
	Hash                string        `json:"snapshot_sha256"`
}
