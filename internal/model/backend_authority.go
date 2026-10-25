package model

// BackendAuthority is an authenticated local observation for idempotent Start.
// It does not contain endpoint credentials or transfer administrative rights.
type BackendAuthority struct {
	Host       string       `json:"host"`
	Owner      string       `json:"owner"`
	Home       string       `json:"home"`
	Directory  FileIdentity `json:"directory"`
	Generation string       `json:"backend_generation"`
}
