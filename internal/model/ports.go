package model

import "time"

type PortClaim struct {
	Name      string `json:"name"`
	Network   string `json:"network"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	RangeEnd  int    `json:"range_end,omitempty"`
	Mode      string `json:"mode"`
	Namespace string `json:"namespace,omitempty"`
	DualStack bool   `json:"dual_stack,omitempty"`
}
type PortBinding struct {
	PortClaim
	State string `json:"state"`
	// SocketID is diagnostic metadata, never authority to signal a process or
	// reclaim a socket. Some kernels do not provide a useful inode here.
	SocketID string `json:"socket_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type ActivatedPort struct {
	PortBinding
	FD *int `json:"fd"`
}

type SocketOwner struct {
	PID           int    `json:"pid"`
	UID           string `json:"uid"`
	StartIdentity string `json:"start_identity,omitempty"`
}
type SocketObservation struct {
	ID                string        `json:"id"`
	Network           string        `json:"network"`
	Address           string        `json:"address"`
	Port              int           `json:"port"`
	UID               string        `json:"uid,omitempty"`
	Owners            []SocketOwner `json:"owners"`
	Claims            []string      `json:"claims"`
	OverlappingLeases []string      `json:"overlapping_leases"`
	DualStack         *bool         `json:"dual_stack"`
}
type PortInventory struct {
	Host                string              `json:"host"`
	Namespace           string              `json:"namespace"`
	At                  time.Time           `json:"at"`
	Method              string              `json:"method"`
	Scope               string              `json:"scope"`
	Complete            bool                `json:"complete"`
	AttributionComplete bool                `json:"attribution_complete"`
	Detail              string              `json:"detail"`
	Sockets             []SocketObservation `json:"sockets"`
}
