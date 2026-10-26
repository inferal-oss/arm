package model

import "time"

// PeerSnapshot is intentionally a public, read-only projection. It contains
// quantities and timing, but no lease IDs, reasons, paths or recipients.
type PeerSnapshot struct {
	Version     int        `json:"version"`
	Host        string     `json:"host"`
	At          time.Time  `json:"at"`
	Held        Resources  `json:"held"`
	Queued      Resources  `json:"queued"`
	HeldCount   int        `json:"held_count"`
	QueuedCount int        `json:"queued_count"`
	Items       []PeerItem `json:"items"`
	Truncated   bool       `json:"truncated"`
	OtherClaims bool       `json:"other_claims"`
}

type PeerItem struct {
	State          string        `json:"state"`
	Resources      Resources     `json:"resources"`
	Expires        *time.Time    `json:"expires_at,omitempty"`
	QueuePosition  int           `json:"queue_position,omitempty"`
	EstimatedStart *time.Time    `json:"estimated_start_at,omitempty"`
	EstimateKind   string        `json:"estimate_kind,omitempty"`
	StartDeadline  *time.Time    `json:"start_deadline_at,omitempty"`
	RequestedTTL   time.Duration `json:"requested_ttl_ns,omitempty"`
	OtherClaims    bool          `json:"other_claims,omitempty"`
}

type PeerInstance struct {
	UID        uint32        `json:"uid"`
	Host       string        `json:"host"`
	Generation string        `json:"generation"`
	URL        string        `json:"url"`
	Snapshot   *PeerSnapshot `json:"snapshot,omitempty"`
	Error      string        `json:"error,omitempty"`
}

type PeerOverview struct {
	At                   time.Time      `json:"at"`
	Local                PeerSnapshot   `json:"local"`
	Instances            []PeerInstance `json:"instances"`
	PeerHeld             Resources      `json:"peer_held"`
	PeerQueued           Resources      `json:"peer_queued"`
	PeerHeldCount        int            `json:"peer_held_count"`
	PeerQueuedCount      int            `json:"peer_queued_count"`
	DiscoveryUnavailable string         `json:"discovery_unavailable,omitempty"`
	DiscoveryTruncated   bool           `json:"discovery_truncated"`
	TotalsComplete       bool           `json:"totals_complete"`
	Advisory             string         `json:"advisory"`
}
