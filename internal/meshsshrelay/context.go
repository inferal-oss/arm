package meshsshrelay

import "github.com/yrashk/arm/internal/workspace"

// Context is a local native read of fresh membership and, only when this node
// is the final destination, its currently enabled SSH service identity.
// It is review data, never a grant or transferable bearer capability.
type Context struct {
	Access          workspace.RouteAccess `json:"access"`
	Account         string                `json:"account,omitempty"`
	Owner           string                `json:"owner,omitempty"`
	Home            string                `json:"home,omitempty"`
	HostKeySHA256   string                `json:"ssh_host_key_sha256,omitempty"`
	PublicKey       string                `json:"ssh_public_key,omitempty"`
	ServiceRevision uint64                `json:"ssh_service_revision,omitempty"`
}
