package sharemount

// MemoryPlan is source-side mount admission headroom, distinct from the remote
// Origin/codec lease. It is not a measured heap/RSS or kernel-cache hard limit.
// A transport with larger buffers must add its own reservation before opening.
type MemoryPlan struct {
	Policy          string `json:"policy"`
	FUSERequests    int64  `json:"fuse_requests"`
	BackendResults  int64  `json:"backend_results"`
	MetadataHandles int64  `json:"metadata_handles"`
	Transport       int64  `json:"transport"`
	RuntimeHeadroom int64  `json:"runtime_headroom"`
	Session         int64  `json:"session"`
}

// AdmissionMemory accounts once for the aggregate 16 MiB FUSE request pool,
// separately for eight 1 MiB backend read results, bounded metadata/handles,
// 16 MiB of client codec/transport headroom and 64 MiB of Go runtime headroom.
// Already delivered bytes, kernel structures/page caches and the remote origin
// are separate accounting domains. The direct-I/O adapter adds no data cache.
func AdmissionMemory() MemoryPlan {
	p := MemoryPlan{Policy: "share-mount-memory-v1", FUSERequests: 16 << 20, BackendResults: 8 << 20, MetadataHandles: 16 << 20, Transport: 16 << 20, RuntimeHeadroom: 64 << 20}
	p.Session = p.FUSERequests + p.BackendResults + p.MetadataHandles + p.Transport + p.RuntimeHeadroom
	return p
}
