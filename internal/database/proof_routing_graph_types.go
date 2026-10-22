package database

import "time"

// Exact independent mirrors: optional pointers are omitted, never null; unknown
// future fields remain unsupported at this destructive-proof boundary.
type proofGraphNode struct {
	Host       string `json:"host"`
	Generation int64  `json:"membership_generation"`
	Pin        string `json:"certificate_sha256"`
	Relay      bool   `json:"relay_enabled"`
}
type proofGraphNeighbor struct {
	Host       string `json:"host"`
	Generation int64  `json:"membership_generation"`
	Pin        string `json:"certificate_sha256"`
	Pair       string `json:"pair"`
}
type proofGraphReport struct {
	Schema           int                  `json:"schema_version"`
	Workspace        string               `json:"workspace"`
	Generation       string               `json:"generation"`
	Source           string               `json:"source_host"`
	SourceGeneration int64                `json:"source_generation"`
	SourcePin        string               `json:"source_pin"`
	Sequence         int64                `json:"report_sequence"`
	Nonce            string               `json:"request_nonce"`
	Issued           time.Time            `json:"issued_at"`
	ValidUntil       time.Time            `json:"valid_until"`
	Neighbors        []proofGraphNeighbor `json:"neighbors"`
}
type proofGraphEdge struct {
	A    string `json:"a"`
	B    string `json:"b"`
	Pair string `json:"pair"`
}
type proofGraphSnapshot struct {
	Schema              int              `json:"schema_version"`
	Workspace           string           `json:"workspace"`
	Generation          string           `json:"generation"`
	Directory           string           `json:"directory_host"`
	DirectoryPin        string           `json:"directory_pin"`
	MembershipHash      string           `json:"membership_sha256"`
	MembershipRevision  int64            `json:"membership_revision"`
	Requester           string           `json:"requester"`
	RequesterGeneration int64            `json:"requester_generation"`
	Nonce               string           `json:"request_nonce"`
	Revision            int64            `json:"graph_revision"`
	Issued              time.Time        `json:"issued_at"`
	ValidUntil          time.Time        `json:"valid_until"`
	Nodes               []proofGraphNode `json:"nodes"`
	Edges               []proofGraphEdge `json:"edges"`
	Hash                string           `json:"sha256"`
}
type proofGraphBundle struct {
	Membership proofWSSnapshot    `json:"membership"`
	Snapshot   proofGraphSnapshot `json:"topology"`
}
type proofGraphReportRef struct {
	Source   string `json:"source_host"`
	Sequence int64  `json:"report_sequence"`
	Hash     string `json:"sha256"`
}

type proofGraphIssuance struct {
	Schema         int                   `json:"schema_version"`
	Workspace      string                `json:"workspace"`
	Generation     string                `json:"generation"`
	Authority      proofWSAuthority      `json:"local_authority"`
	LocalPin       string                `json:"local_pin"`
	Role           string                `json:"role"`
	EnableRevision int64                 `json:"enable_revision"`
	EnabledAt      time.Time             `json:"enabled_at"`
	Updated        time.Time             `json:"updated_at"`
	WallFloor      time.Time             `json:"wall_floor"`
	ReportSequence int64                 `json:"report_sequence"`
	IssuedCount    int64                 `json:"issued_count"`
	ReportRefs     []proofGraphReportRef `json:"report_refs"`
	Closed         bool                  `json:"closed"`
	LastReport     *proofGraphReport     `json:"last_report,omitempty"`
	ReportHash     string                `json:"report_sha256,omitempty"`
	LastIssue      *proofGraphBundle     `json:"last_issue,omitempty"`
}
type proofGraphStoredReport struct {
	Schema        int              `json:"schema_version"`
	Workspace     string           `json:"workspace"`
	Authority     proofWSAuthority `json:"directory_authority"`
	DirectoryPin  string           `json:"directory_pin"`
	Report        proofGraphReport `json:"report"`
	Hash          string           `json:"sha256"`
	Received      time.Time        `json:"received_at"`
	OriginalUntil time.Time        `json:"original_valid_until"`
}
type proofGraphObservation struct {
	DirectoryProbes []proofDirectoryProbe `json:"directory_probes,omitempty"`
	Schema          int                   `json:"schema_version"`
	Workspace       string                `json:"workspace"`
	Authority       proofWSAuthority      `json:"local_authority"`
	LocalPin        string                `json:"local_pin"`
	SourceReport    proofGraphReport      `json:"source_report"`
	Bundle          proofGraphBundle      `json:"bundle"`
	Received        time.Time             `json:"received_at"`
	RequestStart    time.Time             `json:"request_started_at"`
	WallFloor       time.Time             `json:"wall_floor"`
}

type proofDirectoryProbe struct {
	Schema             int              `json:"schema_version"`
	Workspace          string           `json:"workspace"`
	Generation         string           `json:"generation"`
	Source             string           `json:"source_host"`
	SourceGeneration   int64            `json:"source_generation"`
	SourcePin          string           `json:"source_pin"`
	Directory          string           `json:"directory_host"`
	DirectoryPin       string           `json:"directory_pin"`
	MembershipRevision int64            `json:"membership_revision"`
	SourceSequence     int64            `json:"source_report_sequence"`
	Nonce              string           `json:"request_nonce"`
	Observed           time.Time        `json:"observed_at"`
	Nodes              []proofGraphNode `json:"nodes"`
}
