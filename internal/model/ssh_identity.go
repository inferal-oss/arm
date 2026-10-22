package model

// SSHDestinationIdentity is an authenticated public inventory snapshot. It is
// not a connection capability and contains neither host nor user private keys.
type SSHDestinationIdentity struct {
	Schema          int       `json:"schema"`
	Source          string    `json:"source"`
	Target          string    `json:"target"`
	Owner           string    `json:"owner"`
	Account         string    `json:"account"`
	Home            string    `json:"home"`
	Pair            string    `json:"pair"`
	Revision        int       `json:"revision"`
	Certificate     string    `json:"certificate"`
	SPKI            string    `json:"spki_sha256"`
	PublicKey       string    `json:"public_key"`
	ServiceRevision uint64    `json:"service_revision"`
	MaxTTL          int64     `json:"max_ttl_ns"`
	Limits          Resources `json:"limits"`
}
