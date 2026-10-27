package workspaceapi

import (
	"getarm.org/internal/workspacegraph"
	"time"
)

// GraphRead inspects bounded observations. It does not open a stream or confer
// membership, transport, agent, resource or execution authority.
type GraphRead struct {
	Workspace string `json:"workspace"`
	Section   string `json:"section"`
	Target    string `json:"target,omitempty"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
	Hash      string `json:"sha256,omitempty"`
}
type GraphPage struct {
	HistoricalDirectoryPaths [][]string            `json:"historical_directory_paths,omitempty"`
	Workspace                string                `json:"workspace"`
	Section                  string                `json:"section"`
	Hash                     string                `json:"sha256"`
	MembershipHash           string                `json:"membership_sha256"`
	Revision                 int64                 `json:"revision"`
	ObservedAt               time.Time             `json:"observed_at"`
	ValidUntil               time.Time             `json:"valid_until"`
	Current                  bool                  `json:"current_observation"`
	Authority                string                `json:"authority"`
	Detail                   string                `json:"detail"`
	Offset                   int                   `json:"offset"`
	Total                    int                   `json:"total"`
	Next                     *int                  `json:"next,omitempty"`
	Nodes                    []workspacegraph.Node `json:"nodes,omitempty"`
	Edges                    []workspacegraph.Edge `json:"edges,omitempty"`
	Paths                    [][]string            `json:"paths,omitempty"`
}
