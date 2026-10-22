package sharelease

import (
	"github.com/yrashk/arm/internal/sharecodec"
	"github.com/yrashk/arm/internal/sharefs"
)

// PreparationMemoryPlan accounts separately for the ordinary origin preparation
// and the durable ledger's canonical validation/JSON/base64 plus SQL copies.
// It reserves conservative headroom, not an OS heap/RSS isolation guarantee.
type PreparationMemoryPlan struct {
	Policy                           string
	Base, Encoding, SQLCopies, Total int64
}

func PreparationMemory() PreparationMemoryPlan {
	base := sharecodec.AdmissionMemory().Preparation
	// Maximum-shape encoding measured ~147MB (Linux cold) /111MB (native) cumulative allocation, including
	// canonical validation and growing encoder pools. Twenty8MiB manifest units
	// cover that cycle even with cold pools. Three full base64 result copies cover
	// retained parts, driver conversion and SQLite binding buffers separately.
	encoding := int64(sharefs.MaxManifestBytes) * 20
	encoded := int64(MaxManifestParts) * MaxManifestEncodedPartBytes
	copies := 3 * encoded
	// Round the combined encoder+three exact maximum base64 copies upward.
	// Base64 padding matters:160MiB+32MiB+256bytes rounds to200MiB.
	round := func(n int64) int64 { return (n + (8 << 20) - 1) / (8 << 20) * (8 << 20) }
	return PreparationMemoryPlan{"share-ledger-memory-v1", base, encoding, copies, base + round(encoding+copies)}
}
