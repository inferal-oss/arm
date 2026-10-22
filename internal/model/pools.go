package model

import "time"

// Pool is a cooperative counted resource. Global scope is a property of the
// resource, separate from the host on which a consuming command executes.
type Pool struct {
	ID            string    `json:"id"`
	Parent        string    `json:"parent,omitempty"`
	Name          string    `json:"name"`
	Scope         string    `json:"scope"`
	Host          string    `json:"host,omitempty"`
	Authority     string    `json:"authority"`
	Owner         string    `json:"owner"`
	OwnerUsername string    `json:"owner_username,omitempty"`
	Agent         string    `json:"agent,omitempty"`
	Session       string    `json:"agent_session,omitempty"`
	Registration  string    `json:"agent_registration,omitempty"`
	Project       string    `json:"project,omitempty"`
	Description   string    `json:"description,omitempty"`
	Unit          string    `json:"unit"`
	Capacity      int64     `json:"capacity"`
	Revision      int64     `json:"revision"`
	State         string    `json:"state"`
	Accounting    string    `json:"accounting"`
	Expiry        string    `json:"expiry_policy"`
	Created       time.Time `json:"created_at"`
	Updated       time.Time `json:"updated_at"`
}

type PoolClaim struct {
	Pool      string `json:"pool"`
	Quantity  int64  `json:"quantity"`
	Scope     string `json:"scope,omitempty"`
	Host      string `json:"host,omitempty"`
	Authority string `json:"authority,omitempty"`
}

// PoolHierarchyReview is complete concurrency evidence, never authorization.
// Pool binds the selected definition (or parent for child creation); the hash
// covers its whole root tree and exact queued/consuming claims.
type PoolHierarchyReview struct {
	Schema    int    `json:"schema_version"`
	Pool      string `json:"pool"`
	Root      string `json:"root"`
	Pools     int    `json:"pool_count"`
	Consumers int    `json:"consumer_count"`
	SHA256    string `json:"snapshot_sha256"`
}

type PoolReview struct {
	Unavailable string               `json:"unavailable,omitempty"`
	Hierarchy   *PoolHierarchyReview `json:"hierarchy,omitempty"`
	Authority   string               `json:"authority"`
	Consumers   []LeaseRevision      `json:"consumers"`
}

type PoolMutation struct {
	Parent         string      `json:"parent,omitempty"`
	ParentRevision *int64      `json:"parent_revision,omitempty"`
	Reviewed       *PoolReview `json:"reviewed,omitempty"`
	Operation      string      `json:"operation"`
	ID             string      `json:"id,omitempty"`
	Name           string      `json:"name,omitempty"`
	Scope          string      `json:"scope,omitempty"`
	Host           string      `json:"host,omitempty"`
	Unit           string      `json:"unit,omitempty"`
	Description    string      `json:"description,omitempty"`
	Capacity       *int64      `json:"capacity,omitempty"`
	Delta          *int64      `json:"delta,omitempty"`
	Revision       *int64      `json:"revision,omitempty"`
	Reason         string      `json:"reason"`
	Agent          string      `json:"agent,omitempty"`
	Project        string      `json:"project,omitempty"`
	Key            string      `json:"idempotency_key"`
}

type PoolChange struct {
	Partitions    []PoolPartitionChange `json:"partitions,omitempty"`
	Sequence      int64                 `json:"sequence"`
	Operation     string                `json:"operation"`
	Key           string                `json:"idempotency_key"`
	Actor         string                `json:"actor"`
	ActorUsername string                `json:"actor_username,omitempty"`
	Agent         string                `json:"agent,omitempty"`
	Session       string                `json:"agent_session,omitempty"`
	Registration  string                `json:"agent_registration,omitempty"`
	Project       string                `json:"project,omitempty"`
	Reason        string                `json:"reason"`
	At            time.Time             `json:"at"`
	Previous      *Pool                 `json:"before"`
	Pool          Pool                  `json:"after"`
	Delta         int64                 `json:"delta"`
}

type PoolView struct {
	Partition *PoolPartition `json:"partition,omitempty"`
	Ancestors []string       `json:"ancestors,omitempty"`
	Children  []string       `json:"children,omitempty"`
	Review    *PoolReview    `json:"review"`
	Pool      Pool           `json:"pool"`
	Reserved  int64          `json:"reserved"`
	Available int64          `json:"available"`
	Deficit   int64          `json:"deficit"`
	Users     []ResourceUse  `json:"users"`
	Queued    []ResourceUse  `json:"queued"`
}

type PoolHistory struct {
	Pool    string       `json:"pool"`
	Entries []PoolChange `json:"entries"`
	Cursor  int64        `json:"cursor"`
	More    bool         `json:"more"`
}
