package model

// WorkloadBinding describes the effective adapter-owned execution and paths.
// It does not make a JVM heap limit a physical-memory guarantee. Private
// receipt authentication material lives in the manager ledger, never here.
type WorkloadBinding struct {
	Adapter            string           `json:"adapter"`
	Role               string           `json:"role"`
	State              string           `json:"state"`
	ServiceLease       string           `json:"service_lease,omitempty"`
	Executable         string           `json:"executable"`
	CWD                string           `json:"cwd"`
	JavaHome           string           `json:"java_home"`
	Home               string           `json:"home"`
	ProjectCache       string           `json:"project_cache,omitempty"`
	BuildDirectory     string           `json:"build_directory,omitempty"`
	WorkerLimit        int              `json:"worker_limit"`
	HeapBytes          int64            `json:"heap_bytes"`
	MetaspaceBytes     int64            `json:"metaspace_bytes"`
	JVMArgs            []string         `json:"jvm_args"`
	ServiceProcess     *ProcessIdentity `json:"service_process,omitempty"`
	ServiceStartMillis int64            `json:"service_start_millis,omitempty"`
	GradleVersion      string           `json:"gradle_version,omitempty"`
	Detail             string           `json:"detail,omitempty"`
}
