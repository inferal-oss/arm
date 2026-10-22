package model

import "time"

// SSHExecutionRequest is durable admission evidence, never an executable
// command or reusable stream capability. Command/terminal hashes bind retries.
type SSHExecutionRequest struct {
	Source          string        `json:"source"`
	Target          string        `json:"target"`
	Pair            string        `json:"pair"`
	Revision        int           `json:"revision"`
	Certificate     string        `json:"certificate"`
	Account         string        `json:"account"`
	Key             string        `json:"key"`
	PublicKeySHA256 string        `json:"public_key_sha256"`
	Reason          string        `json:"reason"`
	TTL             time.Duration `json:"ttl_ns"`
	Resources       Resources     `json:"resources"`
	CWD             string        `json:"cwd"`
	Kind            string        `json:"kind"`
	CommandSHA256   string        `json:"command_sha256"`
	TerminalSHA256  string        `json:"terminal_sha256"`
}

// SSHRunDescriptor intentionally contains no command, environment, or I/O.
// A descriptor deserialized from wire/storage never authorizes launch.
type SSHRunDescriptor struct {
	Schema         int    `json:"schema"`
	Source         string `json:"source"`
	Key            string `json:"key"`
	Kind           string `json:"kind"`
	CommandSHA256  string `json:"command_sha256"`
	TerminalSHA256 string `json:"terminal_sha256"`
}
type SSHOperation struct {
	Schema     int                 `json:"schema"`
	Request    SSHExecutionRequest `json:"request"`
	Lease      string              `json:"lease"`
	AdmittedAt time.Time           `json:"admitted_at"`
}
type SSHLookup struct {
	Found     bool          `json:"found"`
	Operation *SSHOperation `json:"operation,omitempty"`
	Lease     *Lease        `json:"lease,omitempty"`
}
