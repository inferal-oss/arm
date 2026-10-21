package model

import "time"

// BudgetLimits adds admission-policy ceilings. Missing means no additional
// restriction; explicit zero prohibits a new positive claim. Unlike the host's
// memory configuration, a zero memory budget never selects automatic sizing.
// A policy budget does not hold or manufacture a resource reservation.
type BudgetLimits struct {
	CPU    *float64         `json:"cpu,omitempty"`
	Memory *int64           `json:"memory_bytes,omitempty"`
	Disk   *int64           `json:"disk_bytes,omitempty"`
	Pools  map[string]int64 `json:"pools,omitempty"`
}

type Budget struct {
	ID            string       `json:"id"`
	Authority     string       `json:"authority"`
	Owner         string       `json:"owner"`
	OwnerUsername string       `json:"owner_username,omitempty"`
	Parent        string       `json:"parent,omitempty"`
	Root          bool         `json:"root"`
	Name          string       `json:"name"`
	Description   string       `json:"description,omitempty"`
	Limits        BudgetLimits `json:"limits"`
	State         string       `json:"state"`
	Revision      int64        `json:"revision"`
	Created       time.Time    `json:"created_at"`
	Updated       time.Time    `json:"updated_at"`
}

// BudgetBinding is backend-resolved immutable lineage, not a caller-supplied
// owner or a delegation of an existing resource lease's reservation.
type BudgetBinding struct {
	Authority string    `json:"authority"`
	Owner     string    `json:"owner"`
	Selected  string    `json:"selected"`
	Path      []string  `json:"path"`
	Bound     time.Time `json:"bound_at"`
}

type BudgetPolicyRevision struct {
	ID       string       `json:"id"`
	Revision int64        `json:"revision"`
	Limits   BudgetLimits `json:"limits"`
}

type BudgetAmounts struct {
	Resources Resources        `json:"resources"`
	Pools     map[string]int64 `json:"pools"`
}

type BudgetMutation struct {
	Operation   string        `json:"operation"`
	ID          string        `json:"id,omitempty"`
	Parent      string        `json:"parent,omitempty"`
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Limits      *BudgetLimits `json:"limits,omitempty"`
	Revision    *int64        `json:"revision,omitempty"`
	Reason      string        `json:"reason"`
	Agent       string        `json:"agent,omitempty"`
	Project     string        `json:"project,omitempty"`
	Key         string        `json:"idempotency_key"`
}

type BudgetChange struct {
	Sequence      int64     `json:"sequence"`
	Operation     string    `json:"operation"`
	Key           string    `json:"idempotency_key"`
	Actor         string    `json:"actor"`
	ActorUsername string    `json:"actor_username,omitempty"`
	Agent         string    `json:"agent,omitempty"`
	Session       string    `json:"agent_session,omitempty"`
	Registration  string    `json:"agent_registration,omitempty"`
	Project       string    `json:"project,omitempty"`
	Reason        string    `json:"reason"`
	At            time.Time `json:"at"`
	Previous      *Budget   `json:"before"`
	Budget        Budget    `json:"after"`
}

type BudgetHistory struct {
	Budget  string         `json:"budget"`
	Entries []BudgetChange `json:"entries"`
	Cursor  int64          `json:"cursor"`
	More    bool           `json:"more"`
}

type BudgetMutationLookup struct {
	Found   bool            `json:"found"`
	Request *BudgetMutation `json:"request,omitempty"`
	Change  *BudgetChange   `json:"change,omitempty"`
}

type BudgetUse struct {
	Lease         string        `json:"lease"`
	Owner         string        `json:"owner"`
	OwnerUsername string        `json:"owner_username,omitempty"`
	Agent         string        `json:"agent,omitempty"`
	Project       string        `json:"project,omitempty"`
	Reason        string        `json:"reason"`
	State         string        `json:"state"`
	Expires       *time.Time    `json:"expires_at,omitempty"`
	Amounts       BudgetAmounts `json:"amounts"`
}

type BudgetConstraint struct {
	Budget    Budget        `json:"budget"`
	Charged   BudgetAmounts `json:"charged"`
	Available BudgetLimits  `json:"policy_available"`
	Deficit   BudgetAmounts `json:"deficit"`
}
type BudgetView struct {
	Budget     Budget             `json:"budget"`
	Path       []BudgetConstraint `json:"path"`
	Charged    BudgetAmounts      `json:"charged"`
	Available  BudgetLimits       `json:"policy_available"`
	Deficit    BudgetAmounts      `json:"deficit"`
	Children   int                `json:"child_count"`
	Users      int                `json:"charged_lease_count"`
	Queued     int                `json:"queued_lease_count"`
	Accounting string             `json:"accounting"`
	Observed   time.Time          `json:"observed_at"`
}
type BudgetList struct {
	Budgets []Budget `json:"budgets"`
	Root    string   `json:"owner_root"`
	Cursor  string   `json:"cursor"`
	More    bool     `json:"more"`
}
type BudgetConsumers struct {
	Budget string      `json:"budget"`
	Users  []BudgetUse `json:"leases"`
	Cursor string      `json:"cursor"`
	More   bool        `json:"more"`
}
type BudgetQuery struct {
	ID       string `json:"id"`
	Parent   string `json:"parent,omitempty"`
	After    string `json:"after,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	All      bool   `json:"all,omitempty"`
}
