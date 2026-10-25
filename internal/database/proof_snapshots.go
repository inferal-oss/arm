package database

import (
	"fmt"

	"github.com/yrashk/arm/internal/snapshotlease"
)

// Fixed independent catalog declarations. The proof consumer does not discover
// or adopt a new schema by asking the writer which objects should be accepted.
var snapshotCopyResetSchema = []string{
	`CREATE TABLE snapshot_copies (id TEXT PRIMARY KEY,request_key TEXT NOT NULL UNIQUE,work_lease TEXT NOT NULL UNIQUE,destination_lease TEXT NOT NULL UNIQUE,data TEXT NOT NULL)`,
	`CREATE TABLE snapshot_copy_parts (copy TEXT NOT NULL,part INTEGER NOT NULL,data BLOB NOT NULL,PRIMARY KEY(copy,part))`,
	`CREATE TABLE snapshot_copy_journal (sequence INTEGER PRIMARY KEY AUTOINCREMENT,data TEXT NOT NULL)`,
}
var snapshotCopyResetTables = []string{"snapshot_copies", "snapshot_copy_parts", "snapshot_copy_journal"}

func (p *proofReader) readSnapshots() error {
	// Closed reader supplies actual consistent SQL records/parts/ordinary lease
	// relations. Settlement is independently required below; no writer verdict,
	// PID, planned path or joined flag is treated as native cleanup permission.
	s, e := snapshotlease.ReadTransaction(p.ctx, p.tx, p.result.Host, p.result.Owner, p.home)
	if e != nil {
		return e
	}
	claimed := map[string]bool{}
	for _, r := range s.Copies {
		claimed[r.DestinationLease] = true
		if r.Phase != "cancelled-no-work" || r.PlanningAttempted || r.AllocationAttempted || r.Revision != 2 || r.FinalSlots != 0 {
			if e = p.block("snapshot", r.ID, "Private snapshot reservation remains charged; no copy/publication/native settlement is inferred"); e != nil {
				return e
			}
			continue
		}
		for _, id := range []string{r.WorkLease, r.DestinationLease} {
			l, ok := p.leases[id]
			if !ok || l.State != "terminal" || l.Outcome != "cancelled" || l.Ended == nil || !l.Ended.Equal(r.Updated) || l.Revision != 2 {
				return fmt.Errorf("snapshot cancelled promises differ from independently read ordinary leases")
			}
		}
	}
	for id := range p.snapshotDeferred {
		if !claimed[id] {
			return fmt.Errorf("orphan snapshot planned allocation cannot be treated as absent")
		}
	}
	for _, table := range snapshotCopyResetTables {
		if e = p.walk(table, func(proofRow) error { return nil }); e != nil {
			return e
		}
	}
	// This is a consumer validation fact, not writer/row supplied authority.
	// Only a fully present and validated optional namespace can name its
	// AUTOINCREMENT journal in sqlite_sequence. A forged row with absent
	// snapshot tables remains unsupported in readOther.
	p.snapshotSchema = s.Enabled
	return nil
}
