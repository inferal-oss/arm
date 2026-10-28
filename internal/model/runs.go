package model

import "time"

// Run is an execution observation. A proxy run grants no resources and its
// reported state never authorizes signalling or proves descendant cleanup.
type Run struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	LeaseID       string     `json:"lease_id,omitempty"`
	Command       []string   `json:"command"`
	CWD           string     `json:"cwd"`
	Agent         string     `json:"agent,omitempty"`
	Reason        string     `json:"reason"`
	Host          string     `json:"host"`
	State         string     `json:"state"`
	Outcome       string     `json:"outcome,omitempty"`
	Created       time.Time  `json:"created_at"`
	Started       *time.Time `json:"started_at,omitempty"`
	Ended         *time.Time `json:"ended_at,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	LastReported  time.Time  `json:"last_reported_at,omitempty"`
	LogBytes      int64      `json:"log_bytes"`
	AcceptedBytes int64      `json:"accepted_bytes"`
	Truncated     bool       `json:"truncated"`
}

type RunLog struct {
	Run        Run    `json:"run"`
	Data       []byte `json:"data"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Done       bool   `json:"done"`
}

type RunCreateRequest struct {
	Key     string   `json:"key"`
	Command []string `json:"command"`
	CWD     string   `json:"cwd"`
	Agent   string   `json:"agent,omitempty"`
	Reason  string   `json:"reason"`
}

type RunAppendRequest struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
}

type RunFinishRequest struct {
	ID       string `json:"id"`
	ExitCode int    `json:"exit_code"`
	Outcome  string `json:"outcome"`
}
