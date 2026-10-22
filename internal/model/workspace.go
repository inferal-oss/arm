package model

import "time"

// WorkspaceAvailability is a bounded cached local ledger projection. It conveys
// no messaging, transport, resource or execution authority.
type WorkspaceAvailability struct {
	Ledger    string    `json:"ledger"`
	Identity  string    `json:"identity"`
	Closing   bool      `json:"closing"`
	Validated time.Time `json:"validated_at"`
	Error     *Error    `json:"error,omitempty"`
}
