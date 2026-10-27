package model

import "time"

// NodeAccessScope reports only this authenticated source's current rights at a
// destination. It is an observation, never a transferable authorization token.
type NodeStopAvailability struct {
	Supported       bool   `json:"supported"`
	Reason          string `json:"reason"`
	Generation      string `json:"backend_generation"`
	DesiredRevision uint64 `json:"desired_state_revision"`
	HistoryUsed     int    `json:"history_used"`
	HistoryCapacity int    `json:"history_capacity"`
}

type NodeAccessScope struct {
	NodeStop *NodeStopAvailability `json:"node_stop,omitempty"`
	Source   string                `json:"source_host"`
	Target   string                `json:"target_host"`
	Pair     string                `json:"pair"`
	Revision int                   `json:"revision"`
	Grant    NodeGrant             `json:"grant"`
	Observed time.Time             `json:"observed_at"`
}

type NodeGrant struct {
	Permissions   []string      `json:"permissions"`
	Limits        Resources     `json:"limits"`
	MaxTTL        time.Duration `json:"max_ttl_ns"`
	Contacts      []string      `json:"contact_ids"`
	OperatorInbox bool          `json:"operator_inbox"`
}

type NodeRegistry struct {
	Source    string      `json:"source_host"`
	MeshState string      `json:"mesh_state"`
	Nodes     []NodeEntry `json:"nodes"`
	At        time.Time   `json:"registry_at"`
	Error     *Error      `json:"error,omitempty"`
}

type NodeEntry struct {
	DisplayName       string            `json:"display_name,omitempty"`
	Host              string            `json:"host"`
	Name              string            `json:"name"`
	Local             bool              `json:"local"`
	Trust             NodeTrust         `json:"trust"`
	Metadata          *NodeMetadata     `json:"metadata"`
	Outbound          *NodeAccessScope  `json:"outbound_scope"`
	MetadataFreshness NodeReadFreshness `json:"metadata_freshness"`
	ScopeFreshness    NodeReadFreshness `json:"scope_freshness"`
	Health            string            `json:"health"`
	Error             *Error            `json:"error,omitempty"`
}

type NodeExecutionAccount struct {
	UID      string `json:"uid"`
	Username string `json:"username,omitempty"`
	Source   string `json:"source"`
}

type NodeTrust struct {
	Account     *NodeExecutionAccount `json:"execution_account"`
	State       string                `json:"state"`
	Address     string                `json:"address,omitempty"`
	Pair        string                `json:"pair,omitempty"`
	Revision    int                   `json:"revision,omitempty"`
	Fingerprint string                `json:"certificate_fingerprint,omitempty"`
	SPKI        string                `json:"spki_sha256,omitempty"`
	Expires     *time.Time            `json:"expires_at"`
	// Inbound is the peer's rights HERE, never this source's rights THERE.
	Inbound *NodeGrant `json:"inbound_grant"`
}

type NodeReadFreshness struct {
	Known      bool          `json:"known"`
	Stale      bool          `json:"stale"`
	Received   *time.Time    `json:"received_at"`
	Age        time.Duration `json:"age_ns"`
	StaleAfter time.Duration `json:"stale_after_ns"`
}
