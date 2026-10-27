package routing

import (
	"time"

	"getarm.org/internal/workspace"
	"getarm.org/internal/workspacegraph"
)

const MaxGraphRecordBytes = 1 << 20

// GraphBundle keeps exact complete membership evidence with topology. A
// separately updated workspace observation cannot replace this historical
// snapshot or make saved reachability authorize a stream after restart.
type GraphBundle struct {
	Membership workspace.Snapshot      `json:"membership"`
	Snapshot   workspacegraph.Snapshot `json:"topology"`
}

type GraphReportRef struct {
	Source   string `json:"source_host"`
	Sequence int64  `json:"report_sequence"`
	Hash     string `json:"sha256"`
}

type GraphIssuance struct {
	Schema         int                    `json:"schema_version"`
	Workspace      string                 `json:"workspace"`
	Generation     string                 `json:"generation"`
	Authority      workspace.Authority    `json:"local_authority"`
	LocalPin       string                 `json:"local_pin"`
	Role           string                 `json:"role"`
	EnableRevision int64                  `json:"enable_revision"`
	EnabledAt      time.Time              `json:"enabled_at"`
	Updated        time.Time              `json:"updated_at"`
	WallFloor      time.Time              `json:"wall_floor"`
	ReportSequence int64                  `json:"report_sequence"`
	IssuedCount    int64                  `json:"issued_count"`
	ReportRefs     []GraphReportRef       `json:"report_refs"`
	Closed         bool                   `json:"closed"`
	LastReport     *workspacegraph.Report `json:"last_report,omitempty"`
	ReportHash     string                 `json:"report_sha256,omitempty"`
	LastIssue      *GraphBundle           `json:"last_issue,omitempty"`
}

type GraphReport struct {
	Schema        int                   `json:"schema_version"`
	Workspace     string                `json:"workspace"`
	Authority     workspace.Authority   `json:"directory_authority"`
	DirectoryPin  string                `json:"directory_pin"`
	Report        workspacegraph.Report `json:"report"`
	Hash          string                `json:"sha256"`
	Received      time.Time             `json:"received_at"`
	OriginalUntil time.Time             `json:"original_valid_until"`
}

type GraphObservation struct {
	DirectoryProbes []DirectoryProbe      `json:"directory_probes,omitempty"`
	Schema          int                   `json:"schema_version"`
	Workspace       string                `json:"workspace"`
	Authority       workspace.Authority   `json:"local_authority"`
	LocalPin        string                `json:"local_pin"`
	SourceReport    workspacegraph.Report `json:"source_report"`
	Bundle          GraphBundle           `json:"bundle"`
	Received        time.Time             `json:"received_at"`
	RequestStart    time.Time             `json:"request_started_at"`
	WallFloor       time.Time             `json:"wall_floor"`
}

type GraphRecords struct {
	Issuance     []GraphIssuance
	Reports      []GraphReport
	Observations []GraphObservation
}

func graphIssuer(v Records, id string) (GraphIssuance, bool) {
	if v.Graph != nil {
		for _, g := range v.Graph.Issuance {
			if g.Workspace == id {
				return g, true
			}
		}
	}
	return GraphIssuance{}, false
}

// GraphReportDigest is canonical consistency evidence, not a signature or grant.
func GraphReportDigest(q workspacegraph.Report) string {
	if q.ValidateStructure() != nil {
		return ""
	}
	return hash(q)
}
