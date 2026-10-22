package model

import "time"

// PoolPartition describes one disjoint partition. Held backs only this node in
// its immediate parent; ancestor values overlap and must never be summed.
type PoolPartition struct {
	Configured    int64 `json:"configured"`
	Direct        int64 `json:"direct_reserved"`
	ChildBacking  int64 `json:"child_backing"`
	Committed     int64 `json:"committed"`
	Held          int64 `json:"held_backing"`
	Available     int64 `json:"uncarved_available"`
	Deficit       int64 `json:"deficit"`
	PendingReturn int64 `json:"pending_return"`
}

// PoolLineage is immutable definition identity, stored independently of mutable
// descriptions, capacity and state. Root-to-node paths include the node itself.
type PoolLineage struct {
	Pool      string   `json:"pool"`
	Parent    string   `json:"parent,omitempty"`
	Authority string   `json:"authority"`
	Owner     string   `json:"owner"`
	Scope     string   `json:"scope"`
	Host      string   `json:"host,omitempty"`
	Unit      string   `json:"unit"`
	Path      []string `json:"path"`
}

type PoolClaimBinding struct {
	Authority string           `json:"authority"`
	Owner     string           `json:"owner"`
	Bound     time.Time        `json:"bound_at"`
	Claims    []BoundPoolClaim `json:"claims"`
}

type BoundPoolClaim struct {
	Pool     string   `json:"pool"`
	Quantity int64    `json:"quantity"`
	Path     []string `json:"path"`
}

// PoolPartitionChange is an exact before/after observation of affected backing,
// recorded with the definition mutation; it is not an independent counter.
type PoolPartitionChange struct {
	Pool   string         `json:"pool"`
	Before *PoolPartition `json:"before"`
	After  PoolPartition  `json:"after"`
}

// Hierarchy read pages are observations, never authorization snapshots. Cursors
// use immutable IDs; concurrent changes may require restarting a page walk.
type PoolTreeQuery struct {
	ID          string `json:"id,omitempty"`
	After       string `json:"after,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Descendants bool   `json:"descendants,omitempty"`
}
type PoolTreeNode struct {
	Pool      Pool          `json:"pool"`
	Partition PoolPartition `json:"partition"`
	Ancestors []string      `json:"ancestors"`
	Children  int           `json:"child_count"`
}
type PoolTreePage struct {
	Parent string         `json:"parent"`
	Nodes  []PoolTreeNode `json:"nodes"`
	Cursor string         `json:"cursor"`
	More   bool           `json:"more"`
}
type PoolConsumer struct {
	Use    ResourceUse      `json:"use"`
	Claims []BoundPoolClaim `json:"direct_claims"`
}
type PoolConsumerPage struct {
	Pool        string         `json:"pool"`
	Descendants bool           `json:"descendants"`
	Entries     []PoolConsumer `json:"entries"`
	Cursor      string         `json:"cursor"`
	More        bool           `json:"more"`
}
