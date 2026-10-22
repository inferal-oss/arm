package database

import (
	"encoding/json"
	"time"
)

// Exact independent current workspace JSON mirrors at the destructive proof boundary.
// Writer shape changes require explicit proof review; these are not type aliases.
type proofWSAuthority struct {
	Host string `json:"host"`
	UID  string `json:"uid"`
}
type proofWSIdentity struct {
	Host        string    `json:"host"`
	Name        string    `json:"display_name"`
	Certificate string    `json:"certificate"`
	Fingerprint string    `json:"certificate_sha256"`
	SPKI        string    `json:"spki_sha256"`
	Expires     time.Time `json:"certificate_expires"`
}
type proofWSPolicy struct {
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
type proofWSInstance struct {
	Schema         int              `json:"schema_version"`
	Authority      proofWSAuthority `json:"local_authority"`
	ID             string           `json:"workspace"`
	Generation     string           `json:"generation"`
	Name           string           `json:"name"`
	Role           string           `json:"role"`
	Directory      proofWSIdentity  `json:"directory"`
	Self           proofWSIdentity  `json:"self"`
	State          string           `json:"state"`
	Revision       int64            `json:"revision"`
	PolicyRevision int64            `json:"policy_revision"`
	Policy         proofWSPolicy    `json:"policy"`
	SelfGeneration int64            `json:"self_membership_generation"`
	ConsentHash    string           `json:"consent_sha256"`
	Created        time.Time        `json:"created_at"`
	Updated        time.Time        `json:"updated_at"`
}
type proofWSPrincipal struct {
	Workspace            string `json:"workspace"`
	Generation           string `json:"generation"`
	Host                 string `json:"source_host"`
	MembershipGeneration int64  `json:"membership_generation"`
	Fingerprint          string `json:"certificate_sha256"`
}
type proofWSMember struct {
	Schema               int              `json:"schema_version"`
	Workspace            string           `json:"workspace"`
	Generation           string           `json:"generation"`
	Identity             proofWSIdentity  `json:"identity"`
	MembershipGeneration int64            `json:"membership_generation"`
	State                string           `json:"state"`
	Introducer           bool             `json:"introducer"`
	HostInbox            bool             `json:"host_inbox_enabled"`
	IntroducedBy         proofWSPrincipal `json:"introduced_by"`
	AdmissionKey         string           `json:"admission_key"`
	ConsentHash          string           `json:"consent_sha256"`
	Revision             int64            `json:"revision"`
	Created              time.Time        `json:"created_at"`
	Updated              time.Time        `json:"updated_at"`
	RevokedAt            *time.Time       `json:"revoked_at,omitempty"`
	Reason               string           `json:"reason"`
}
type proofWSTrustedMember struct {
	Schema               int             `json:"schema_version"`
	Workspace            string          `json:"workspace"`
	Generation           string          `json:"generation"`
	Identity             proofWSIdentity `json:"identity"`
	MembershipGeneration int64           `json:"membership_generation"`
	State                string          `json:"state"`
	FirstSeen            int64           `json:"first_seen_revision"`
	LatestSeen           int64           `json:"latest_seen_revision"`
	RotationReview       string          `json:"rotation_review,omitempty"`
}
type proofWSClockRecord struct {
	Schema       int        `json:"schema_version"`
	Workspace    string     `json:"workspace"`
	Generation   string     `json:"generation"`
	WallFloor    time.Time  `json:"wall_floor"`
	LastIssued   *time.Time `json:"last_issued_at,omitempty"`
	IssuedCount  int64      `json:"issued_count"`
	LastRevision int64      `json:"last_revision"`
}
type proofWSDeny struct {
	Schema    int              `json:"schema_version"`
	Workspace string           `json:"workspace"`
	Host      string           `json:"host"`
	Revision  int64            `json:"revision"`
	Key       string           `json:"key"`
	Actor     proofWSAuthority `json:"actor"`
	Reason    string           `json:"reason"`
	Created   time.Time        `json:"created_at"`
}
type proofWSReview struct {
	Name           string           `json:"name"`
	RequestHash    string           `json:"request_sha256"`
	Schema         int              `json:"schema_version"`
	Kind           string           `json:"kind"`
	Workspace      string           `json:"workspace"`
	Generation     string           `json:"generation"`
	Source         proofWSIdentity  `json:"source"`
	Directory      proofWSIdentity  `json:"directory"`
	Policy         proofWSPolicy    `json:"policy"`
	PolicyRevision int64            `json:"policy_revision"`
	Candidate      *proofWSIdentity `json:"candidate,omitempty"`
	Introducer     bool             `json:"introducer"`
	HostInbox      bool             `json:"host_inbox_enabled"`
	Reason         string           `json:"reason"`
	Expires        time.Time        `json:"expires_at"`
}
type proofWSOperationPrivate struct {
	Nonce              string            `json:"proof_nonce,omitempty"`
	Boot               string            `json:"worker_boot"`
	Introducer         *proofWSPrincipal `json:"introducer,omitempty"`
	IntroducerRevision int64             `json:"introducer_revision"`
	CandidateRevision  int64             `json:"candidate_revision"`
	ProofCount         int               `json:"proof_count"`
	LastChallenge      string            `json:"last_challenge_sha256,omitempty"`
}
type proofWSOperation struct {
	Schema        int                      `json:"schema_version"`
	Workspace     string                   `json:"workspace"`
	Principal     string                   `json:"principal"`
	Key           string                   `json:"key"`
	Kind          string                   `json:"kind"`
	RequestHash   string                   `json:"request_sha256"`
	Request       json.RawMessage          `json:"request"`
	Revision      int64                    `json:"revision"`
	Phase         string                   `json:"phase"`
	Review        *proofWSReview           `json:"review,omitempty"`
	ReviewHash    string                   `json:"review_sha256,omitempty"`
	ReviewExpires time.Time                `json:"review_expires_at"`
	Result        *proofWSMember           `json:"member,omitempty"`
	Private       *proofWSOperationPrivate `json:"private,omitempty"`
	Error         string                   `json:"error,omitempty"`
	Created       time.Time                `json:"created_at"`
	Updated       time.Time                `json:"updated_at"`
}
type proofWSChange struct {
	Members        []proofWSMember        `json:"members,omitempty"`
	Trusted        []proofWSTrustedMember `json:"trusted,omitempty"`
	Schema         int                    `json:"schema_version"`
	Workspace      string                 `json:"workspace"`
	Kind           string                 `json:"kind"`
	Actor          string                 `json:"actor"`
	Key            string                 `json:"key"`
	Revision       int64                  `json:"revision"`
	At             time.Time              `json:"at"`
	Before         *proofWSInstance       `json:"before,omitempty"`
	After          proofWSInstance        `json:"after"`
	MemberBefore   *proofWSMember         `json:"member_before,omitempty"`
	MemberAfter    *proofWSMember         `json:"member_after,omitempty"`
	OperationPhase string                 `json:"operation_phase"`
}
type proofWSObservation struct {
	Schema       int             `json:"schema_version"`
	Workspace    string          `json:"workspace"`
	Generation   string          `json:"generation"`
	DirectoryPin string          `json:"directory_pin"`
	Revision     int64           `json:"revision"`
	SnapshotHash string          `json:"snapshot_sha256"`
	Received     time.Time       `json:"received_at"`
	Snapshot     proofWSSnapshot `json:"snapshot"`
}
type proofWSCreateRequest struct {
	Key    string        `json:"key"`
	Name   string        `json:"name"`
	Policy proofWSPolicy `json:"policy"`
	Reason string        `json:"reason"`
}
type proofWSJoinRequest struct {
	Key               string          `json:"key"`
	Workspace         string          `json:"workspace"`
	Generation        string          `json:"generation"`
	Name              string          `json:"name"`
	Directory         proofWSIdentity `json:"directory"`
	Policy            proofWSPolicy   `json:"policy"`
	PolicyRevision    int64           `json:"policy_revision"`
	Introducer        string          `json:"introducer_host"`
	RequestIntroducer bool            `json:"request_introducer"`
	Reason            string          `json:"reason"`
}
type proofWSAdmissionRequest struct {
	Key            string          `json:"key"`
	Candidate      proofWSIdentity `json:"candidate"`
	ConsentHash    string          `json:"consent_sha256"`
	Introducer     bool            `json:"introducer"`
	HostInbox      bool            `json:"host_inbox_enabled"`
	PolicyRevision int64           `json:"policy_revision"`
	Reason         string          `json:"reason"`
}
type proofWSMutationRequest struct {
	Key      string `json:"key"`
	Host     string `json:"host"`
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
}
type proofWSChallenge struct {
	Introducer          proofWSPrincipal `json:"introducer"`
	CandidateIntroducer bool             `json:"candidate_introducer"`
	HostInbox           bool             `json:"host_inbox_enabled"`
	Workspace           string           `json:"workspace"`
	Generation          string           `json:"generation"`
	Directory           proofWSIdentity  `json:"directory"`
	Candidate           proofWSIdentity  `json:"candidate"`
	ConsentHash         string           `json:"consent_sha256"`
	Nonce               string           `json:"nonce"`
	PolicyRevision      int64            `json:"policy_revision"`
	Expires             time.Time        `json:"expires_at"`
}
type proofWSProof struct {
	Workspace   string `json:"workspace"`
	Generation  string `json:"generation"`
	Source      string `json:"source_host"`
	Target      string `json:"target_host"`
	SourcePin   string `json:"source_pin"`
	TargetPin   string `json:"target_pin"`
	ConsentHash string `json:"consent_sha256"`
	Nonce       string `json:"nonce"`
}
type proofWSMemberCard struct {
	Identity             proofWSIdentity `json:"identity"`
	MembershipGeneration int64           `json:"membership_generation"`
	State                string          `json:"state"`
	Introducer           bool            `json:"introducer"`
	HostInbox            bool            `json:"host_inbox_enabled"`
	Revision             int64           `json:"revision"`
}
type proofWSRefreshRequest struct {
	Source proofWSPrincipal `json:"source"`
	Nonce  string           `json:"nonce"`
}
type proofWSSnapshot struct {
	Schema              int                 `json:"schema_version"`
	Workspace           string              `json:"workspace"`
	Generation          string              `json:"generation"`
	Directory           proofWSIdentity     `json:"directory"`
	Requester           string              `json:"requester_host"`
	RequesterGeneration int64               `json:"requester_generation"`
	Nonce               string              `json:"nonce"`
	Revision            int64               `json:"revision"`
	PolicyRevision      int64               `json:"policy_revision"`
	Policy              proofWSPolicy       `json:"policy"`
	Issued              time.Time           `json:"issued_at"`
	ValidUntil          time.Time           `json:"valid_until"`
	TTL                 time.Duration       `json:"ttl_ns"`
	Members             []proofWSMemberCard `json:"members"`
	Hash                string              `json:"snapshot_sha256"`
}
