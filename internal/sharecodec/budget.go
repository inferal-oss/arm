package sharecodec

import "github.com/yrashk/arm/internal/sharefs"

// MemoryPlan is conservative admission headroom, not an OS heap limit. Payload,
// worker and queue bounds are enforced separately; Go allocator/GC/runtime and
// kernel/client caches are not byte-exact attribution or hostile-code isolation.
type MemoryPlan struct {
	Policy      string `json:"policy"`
	Preparation int64  `json:"preparation_bytes"`
	Session     int64  `json:"session_bytes"`
	Manifest    int64  `json:"manifest_max_bytes"`
	Entries     int    `json:"entries_max"`
	Chunks      int    `json:"chunks_max"`
	Credits     int    `json:"packet_credits"`
}

// AdmissionMemory covers full evidence+map+copy+hash/encoder transients, generous
// per-entry/chunk and fixed runtime headroom, then codec reply copies and active
// read buffers. Only ONE authority-owned Engine supplies global8 operations.
// The deliberately conservative publication reservation persists after prepare;
// no silent resource-pool resize or uncharged preparation is implied.
func AdmissionMemory() MemoryPlan {
	l := sharefs.DefaultLimits()
	// Sixteen manifest-sized units cover source/decoded evidence, private
	// copies and multiple growing JSON buffers even when GC drops encoder
	// pools mid-cycle. Measurement includes cumulative allocations, not just
	// a lower post-GC live-heap sample. This is intentionally conservative.
	base := int64(32<<20) + int64(l.ManifestBytes)*16 + int64(l.Entries)*768 + int64(l.Chunks)*96 + sharefs.ChunkBytes + 8<<20
	round := func(n int64) int64 { return (n + (8 << 20) - 1) / (8 << 20) * (8 << 20) }
	prep := round(base)
	session := round(prep + int64(Credits)*int64(MaxOutgoing)*3 + int64(l.Reads)*2*sharefs.ChunkBytes)
	return MemoryPlan{"share-memory-v1", prep, session, int64(l.ManifestBytes), l.Entries, l.Chunks, Credits}
}
