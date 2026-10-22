package model

// NodeSelection binds a selected destination to the local authority that was
// inspected. IDs are selectors, not credentials or transferable permission.
type NodeSelection struct {
	Host   string `json:"host"`
	Source string `json:"source_host"`
}

// NodeContext is an immutable connection's authority observation. Only the
// authenticated server creates it; clients cannot submit it to acquire rights.
type NodeContext struct {
	Host           string `json:"host"`
	Source         string `json:"source_host"`
	Local          bool   `json:"local"`
	Pair           string `json:"pair,omitempty"`
	Pin            string `json:"certificate_fingerprint,omitempty"`
	LocalRevision  int    `json:"local_revision,omitempty"`
	RemoteRevision int    `json:"remote_revision,omitempty"`
}

type NodeContextReport struct {
	Context     NodeContext      `json:"context"`
	AccessScope *NodeAccessScope `json:"access_scope,omitempty"`
	ReadOnly    bool             `json:"read_only"`
	Unavailable []string         `json:"unavailable"`
}
