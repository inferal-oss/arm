package manager

import (
	"encoding/json"
	"time"

	"github.com/yrashk/arm/internal/snapshotlease"
)

// Private observation payloads, inert without a test-registered arm_devtrace
// observer. Times are the existing admission reads, not re-evaluated evidence.
type snapshotDeadlineCut struct {
	Source         string     `json:"source"`
	SourceRevision int        `json:"source_revision"`
	RequestSHA256  string     `json:"request_sha256"`
	WorkExpires    time.Time  `json:"work_expires"`
	SourceExpires  *time.Time `json:"source_expires"`
	WorkObserved   time.Time  `json:"work_observed"`
	SourceObserved *time.Time `json:"source_observed"`
}
type snapshotTraceCut struct {
	Request     json.RawMessage       `json:"request,omitempty"`
	Key         string                `json:"key,omitempty"`
	Revision    int64                 `json:"revision,omitempty"`
	Reason      string                `json:"reason,omitempty"`
	Record      *snapshotlease.Record `json:"record,omitempty"`
	Present     *bool                 `json:"present,omitempty"`
	Action      string                `json:"operation"`
	Deadline    *snapshotDeadlineCut  `json:"deadline,omitempty"`
	APIError    string                `json:"api_error"`
	ReportError string                `json:"report_error"`
}

func snapshotTraceError(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}
