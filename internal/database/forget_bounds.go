package database

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Reserve the maximum progress/lock/seed overhead before accepting an intent.
// Otherwise a large admitted inventory could become impossible to journal
// halfway through erasure. Bounds apply to encoded bytes, including escaping.
func checkForgetReceiptBudget(plan ResetPlan) error {
	latest := time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC)
	r := ResetReceipt{Schema: ResetSchema, Plan: plan, Phase: "completed", Step: strings.Repeat("x", 1024), Revision: math.MaxInt64, Updated: latest, Completed: &latest, SeedReady: true, Moves: map[string]string{}, Archives: map[string]string{}, ForgetMoves: map[string]string{}, ForgetErased: map[string]bool{}}
	maximum := File{Name: resetFreshName, Present: true, Device: math.MaxUint64, Inode: math.MaxUint64, Owner: math.MaxUint32, Mode: unix.S_IFREG | 0600, Size: MaxDatabaseBytes, Allocated: MaxDatabaseBytes, Modified: math.MinInt64, Changed: math.MinInt64, SHA256: strings.Repeat("f", 64)}
	r.Seed = maximum
	for _, f := range plan.Files {
		r.Moves[f.Name] = "quarantined"
	}
	for _, tree := range plan.ForgetTrees {
		r.ForgetMoves[tree.Name] = "quarantined"
		r.ForgetErased[tree.Name+"/"] = true
		for _, entry := range tree.Entries {
			r.ForgetErased[tree.Name+"/"+entry.Path] = true
		}
	}
	for _, n := range plan.Namespaces {
		if n.Name == plan.Home+"/mesh-lifecycle.lock" && !n.Present {
			f := maximum
			f.Name = "mesh-lifecycle.lock"
			n.Present = true
			n.Identity = f
			n.Files = []File{f}
		}
		r.Protected = append(r.Protected, n)
		if n.Present && (n.Name == "mesh" || n.Name == "mesh-lifecycle") {
			r.Archives[n.Name] = "archived"
		}
	}
	// Match atomicResetJSON exactly: indentation and its trailing newline also
	// consume the durable record limit. Later progress timestamps may have more
	// fractional digits than the original plan timestamp.
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil || len(raw)+1 > MaxRecordBytes {
		return fmt.Errorf("complete forget inventory cannot fit its worst-case one-MiB recovery receipt; no intent created")
	}
	return nil
}
