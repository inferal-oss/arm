package model

import "time"

// Host guardian wire types. Group and process usage are interval estimates
// from non-atomic host inventories, not exclusive or attributable charges.

const (
	GuardianOff     = "off"
	GuardianObserve = "observe"
	GuardianAdvise  = "advise"
	GuardianAct     = "act"

	GuardianManned   = "manned"
	GuardianUnmanned = "unmanned"
)

// Ladder steps in escalation order.
const (
	GuardianNotify       = "notify"
	GuardianDeprioritize = "deprioritize"
	GuardianAsk          = "ask"
	GuardianShed         = "shed"
	GuardianFreeze       = "freeze"
	GuardianTerminate    = "terminate"
	// Reverting steps.
	GuardianRestore = "restore"
	GuardianThaw    = "thaw"
)

// Action outcomes.
const (
	GuardianPlanned  = "planned"  // dry run: the mode does not permit this step
	GuardianPending  = "pending"  // recorded before execution; delivery or signals in progress
	GuardianApplied  = "applied"  // every target accepted the action
	GuardianPartial  = "partial"  // some targets accepted the action
	GuardianFailed   = "failed"   // no target accepted the action
	GuardianSkipped  = "skipped"  // no eligible target or capability
	GuardianReverted = "reverted" // a reversible action was undone
)

type GuardianProcess struct {
	Identity           ProcessIdentity `json:"identity"`
	PPID               int             `json:"ppid"`
	Name               string          `json:"name"`
	Executable         string          `json:"executable,omitempty"`
	Kind               string          `json:"kind,omitempty"` // profile role, such as "renderer"
	CPUCores           *float64        `json:"cpu_cores"`
	MemoryBytes        *uint64         `json:"memory_bytes"`
	DiskBytesPerSecond *float64        `json:"disk_bytes_per_second"`
}

type GuardianGroup struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Kind               string            `json:"kind"` // app-bundle, service-unit, process-group
	UID                string            `json:"uid"`
	Processes          int               `json:"processes"`
	CPUCores           *float64          `json:"cpu_cores"`
	MemoryBytes        *uint64           `json:"memory_bytes"`
	DiskBytesPerSecond *float64          `json:"disk_bytes_per_second"`
	Protected          string            `json:"protected,omitempty"` // reason when never a target
	Managed            bool              `json:"managed,omitempty"`   // contains ARM-supervised work
	Agent              string            `json:"agent,omitempty"`     // attributed agent contact
	AgentEvidence      string            `json:"agent_evidence,omitempty"`
	Profile            string            `json:"profile,omitempty"`
	State              []string          `json:"state,omitempty"` // active reversible steps
	Top                []GuardianProcess `json:"top"`
}

type GuardianAction struct {
	ID         string            `json:"id"`
	At         time.Time         `json:"at"`
	Incident   string            `json:"incident,omitempty"`
	Mode       string            `json:"mode"`
	Resource   string            `json:"resource,omitempty"`
	Level      string            `json:"level,omitempty"`
	Step       string            `json:"step"`
	Group      string            `json:"group,omitempty"`
	GroupName  string            `json:"group_name,omitempty"`
	Usage      string            `json:"usage,omitempty"`
	Targets    []ProcessIdentity `json:"targets,omitempty"`
	Reason     string            `json:"reason"`
	Outcome    string            `json:"outcome"`
	Detail     string            `json:"detail,omitempty"`
	Reversible bool              `json:"reversible"`
	RevertedAt *time.Time        `json:"reverted_at,omitempty"`
	RevertedBy string            `json:"reverted_by,omitempty"` // action that undid this one
	Undoes     string            `json:"undoes,omitempty"`
}

type GuardianIncident struct {
	ID         string     `json:"id"`
	Resource   string     `json:"resource"`
	Level      string     `json:"level"`
	Peak       string     `json:"peak"`
	Since      time.Time  `json:"since"`
	Recovering *time.Time `json:"recovering_since,omitempty"`
	Step       string     `json:"step,omitempty"` // last step taken or planned
	Group      string     `json:"group,omitempty"`
}

type GuardianBudget struct {
	Used   int           `json:"used"`
	Limit  int           `json:"limit"`
	Window time.Duration `json:"window_ns"`
}

type GuardianStatus struct {
	Mode      string             `json:"mode"`
	Role      string             `json:"role"`
	Ladder    []GuardianStepRule `json:"ladder"`
	At        time.Time          `json:"at"`
	Stale     bool               `json:"stale"`
	Pressure  MonitorPressure    `json:"pressure"`
	Groups    []GuardianGroup    `json:"groups"`
	Incidents []GuardianIncident `json:"incidents"`
	Actions   []GuardianAction   `json:"actions"`
	Budget    GuardianBudget     `json:"budget"`
	Inventory string             `json:"inventory_detail,omitempty"`
}

type GuardianStepRule struct {
	Step  string        `json:"step"`
	Level string        `json:"level"` // minimum pressure level
	After time.Duration `json:"after_ns"`
}

type GuardianUndoRequest struct {
	Action string `json:"action"`
}

// GuardianSettings is the [guardian] configuration table. Enabled is the
// operator's explicit choice; nil means undecided, which runs nothing.
type GuardianSettings struct {
	Enabled              *bool              `json:"enabled,omitempty"`
	Mode                 string             `json:"mode,omitempty"`
	Role                 string             `json:"role,omitempty"`
	Interval             time.Duration      `json:"interval_ns,omitempty"`
	Recover              time.Duration      `json:"recover_ns,omitempty"`
	Cooldown             time.Duration      `json:"cooldown_ns,omitempty"`
	FreezeMax            time.Duration      `json:"freeze_max_ns,omitempty"`
	TerminateGrace       time.Duration      `json:"terminate_grace_ns,omitempty"`
	MaxActionsPerHour    int                `json:"max_actions_per_hour,omitempty"`
	DesktopNotifications *bool              `json:"desktop_notifications,omitempty"`
	Protect              []string           `json:"protect,omitempty"`
	Ladder               []GuardianStepRule `json:"ladder,omitempty"`
}
