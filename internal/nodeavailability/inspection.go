package nodeavailability

import "time"

// Inspection is the proposed optional local-gateway projection for one exact
// chosen node. It describes observations, never eligibility or a resource grant.
type Inspection struct {
	Schema           int            `json:"schema"`
	Host             string         `json:"host"`
	OwnerDeclared    *Declaration   `json:"owner_declared,omitempty"`
	DeclarationState string         `json:"declaration_state"`
	ConnectionState  string         `json:"connection_state"`
	Failure          string         `json:"failure,omitempty"`
	LastSuccess      *time.Time     `json:"last_success_at,omitempty"`
	NextAttempt      *time.Time     `json:"next_attempt_at,omitempty"`
	Resources        ResourceStates `json:"resource_freshness"`
}
type ResourceStates struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	Disk   string `json:"disk"`
}
