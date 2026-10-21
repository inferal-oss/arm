package model

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
