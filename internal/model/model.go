package model

import (
	"encoding/json"
	"getarm.org/internal/leaseclock"
	"strconv"
	"time"
)

type Resources struct {
	CPU    float64 `json:"cpu"`
	Memory int64   `json:"memory_bytes"`
	Disk   int64   `json:"disk_bytes"`
}

func (r Resources) Add(s Resources) Resources {
	return Resources{r.CPU + s.CPU, r.Memory + s.Memory, r.Disk + s.Disk}
}
func (r Resources) Fits(s Resources) bool {
	return r.CPU <= s.CPU && r.Memory <= s.Memory && r.Disk <= s.Disk
}

// Compare against remaining capacity before adding integer counters, so an
// overflowing sum cannot turn a full budget into apparent availability.
func (r Resources) FitsRemaining(used, capacity Resources) bool {
	return used.CPU >= 0 && used.Memory >= 0 && used.Disk >= 0 && used.Fits(capacity) &&
		used.CPU+r.CPU <= capacity.CPU && r.Memory <= capacity.Memory-used.Memory && r.Disk <= capacity.Disk-used.Disk
}

type Request struct {
	ForwardSource     *ForwardSourceBinding `json:"forward_source,omitempty"`
	Budget            string                `json:"budget,omitempty"`
	StartDeadline     *time.Time            `json:"start_deadline_at,omitempty"`
	PeerCertificate   string                `json:"origin_certificate,omitempty"`
	PeerGrantRevision int                   `json:"origin_grant_revision,omitempty"`
	OriginKey         string                `json:"origin_key,omitempty"`
	Peer              string                `json:"origin_peer,omitempty"`
	Host              string                `json:"host"`
	Reason            string                `json:"reason"`
	Project           string                `json:"project,omitempty"`
	Agent             string                `json:"agent,omitempty"`
	Resources         Resources             `json:"resources"`
	TTL               time.Duration         `json:"ttl_ns"`
	Key               string                `json:"idempotency_key"`
	Queue             bool                  `json:"queue"`
	Profile           string                `json:"profile,omitempty"`
	Kind              string                `json:"kind,omitempty"`
	StorageLease      string                `json:"storage_lease,omitempty"`
	ServiceLease      string                `json:"service_lease,omitempty"`
	Ports             []PortClaim           `json:"ports,omitempty"`
	PortLease         string                `json:"port_lease,omitempty"`
	Pools             []PoolClaim           `json:"pools,omitempty"`
	UsagePolicy       *UsagePolicy          `json:"usage_policy,omitempty"`
}

// ForwardSourceBinding is the durable, exact source-listener authority. The
// bearer token itself is never stored; only its SHA-256 digest is retained.
type ForwardSourceBinding struct {
	Workspace     string `json:"workspace"`
	Target        string `json:"target"`
	Service       string `json:"service"`
	GrantRevision int64  `json:"grant_revision"`
	Bind          string `json:"bind"`
	TokenSHA256   string `json:"token_sha256"`
}

type Lease struct {
	Clock          *leaseclock.Deadline   `json:"elapsed_deadline,omitempty"`
	Cargo          *CargoBinding          `json:"cargo,omitempty"`
	PoolBinding    *PoolClaimBinding      `json:"pool_binding,omitempty"`
	PoolError      *Error                 `json:"pool_error,omitempty"`
	Budget         *BudgetBinding         `json:"budget_binding,omitempty"`
	BudgetPolicy   []BudgetPolicyRevision `json:"budget_policy,omitempty"`
	BudgetError    *Error                 `json:"budget_error,omitempty"`
	QueueInfo      *QueueInfo             `json:"queue_info,omitempty"`
	ID             string                 `json:"id"`
	Host           string                 `json:"host"`
	Owner          string                 `json:"owner"`
	OwnerUsername  string                 `json:"owner_username,omitempty"`
	Request        Request                `json:"request"`
	State          string                 `json:"state"`
	Outcome        string                 `json:"outcome,omitempty"`
	RecoveryReason string                 `json:"recovery_reason,omitempty"`
	Failure        string                 `json:"failure,omitempty"`
	Created        time.Time              `json:"created_at"`
	Started        *time.Time             `json:"started_at,omitempty"`
	Expires        *time.Time             `json:"expires_at,omitempty"`
	Ended          *time.Time             `json:"ended_at,omitempty"`
	Revision       int                    `json:"revision"`
	PID            int                    `json:"pid,omitempty"`
	ManagedDir     string                 `json:"managed_dir,omitempty"`
	ExitCode       *int                   `json:"exit_code,omitempty"`
	Capabilities   []string               `json:"capabilities"`
	Artifact       *Artifact              `json:"artifact,omitempty"`
	Ports          []PortBinding          `json:"ports,omitempty"`
	Process        *ProcessIdentity       `json:"process_identity,omitempty"`
	Usage          *WorkloadUsage         `json:"usage,omitempty"`
	Workload       *WorkloadBinding       `json:"workload,omitempty"`
}

type Event struct {
	Sequence int64           `json:"sequence"`
	Type     string          `json:"type"`
	Entity   string          `json:"entity"`
	At       time.Time       `json:"at"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type Status struct {
	LeaseClockError *Error                 `json:"lease_clock_error,omitempty"`
	ReviewError     *Error                 `json:"review_error,omitempty"`
	PressureError   *Error                 `json:"host_pressure_error,omitempty"`
	CPUWarning      *CPUWarningEvidence    `json:"cpu_warning_evidence,omitempty"`
	Workspaces      *WorkspaceAvailability `json:"workspaces,omitempty"`
	PoolError       *Error                 `json:"pool_error,omitempty"`
	BudgetError     *Error                 `json:"budget_error,omitempty"`
	Host            string                 `json:"host"`
	Owner           string                 `json:"owner"`
	OwnerUsername   string                 `json:"owner_username,omitempty"`
	Draining        bool                   `json:"draining"`
	Capacity        Resources              `json:"capacity"`
	Reserved        Resources              `json:"reserved"`
	Leases          []Lease                `json:"leases"`
	Cursor          int64                  `json:"cursor"`
	Capabilities    []string               `json:"capabilities"`
	Resources       []ResourceView         `json:"resources"`
	Infrastructure  []PortBinding          `json:"infrastructure_ports"`
	Disk            *DiskBudget            `json:"disk_budget"`
	Memory          *MemoryBudget          `json:"memory_budget"`
	CPU             *CPUBudget             `json:"cpu_budget"`
}

type ResourceUse struct {
	Lease         string      `json:"lease"`
	Owner         string      `json:"owner"`
	OwnerUsername string      `json:"owner_username,omitempty"`
	Agent         string      `json:"agent,omitempty"`
	Project       string      `json:"project,omitempty"`
	Reason        string      `json:"reason"`
	Quantity      json.Number `json:"quantity"`
	State         string      `json:"state"`
	Expires       *time.Time  `json:"expires_at,omitempty"`
	Relationship  string      `json:"relationship,omitempty"`
}
type ResourceView struct {
	AvailabilityKnown *bool                `json:"availability_known,omitempty"`
	Stale             bool                 `json:"stale,omitempty"`
	ID                string               `json:"id"`
	Host              string               `json:"host"`
	Pool              string               `json:"pool"`
	Kind              string               `json:"kind"`
	Unit              string               `json:"unit"`
	BudgetTotal       json.Number          `json:"budget_total"`
	Reserved          json.Number          `json:"reserved"`
	Available         json.Number          `json:"available"`
	Deficit           json.Number          `json:"deficit,omitempty"`
	Accounting        string               `json:"accounting"`
	Observed          *float64             `json:"observed"`
	Users             []ResourceUse        `json:"users"`
	Measurement       *ResourceMeasurement `json:"measurement,omitempty"`
	Port              *PortBinding         `json:"port,omitempty"`
	Custom            *PoolView            `json:"custom,omitempty"`
}

func FloatQuantity(v float64) json.Number { return json.Number(strconv.FormatFloat(v, 'g', -1, 64)) }
func CountQuantity(v int64) json.Number   { return json.Number(strconv.FormatInt(v, 10)) }

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string   { return e.Code + ": " + e.Message }
func Err(code, msg string) error { return &Error{code, msg} }

type WireRequest struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
}
type WireResponse struct {
	Version      int          `json:"version"`
	ID           string       `json:"id,omitempty"`
	Result       any          `json:"result,omitempty"`
	Error        *Error       `json:"error,omitempty"`
	Event        *Event       `json:"event,omitempty"`
	Telemetry    *MonitorPage `json:"telemetry,omitempty"`
	Subscription string       `json:"subscription,omitempty"`
}
