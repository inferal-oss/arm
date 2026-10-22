package sshservice

import (
	"context"
	"fmt"
	"path/filepath"
)

// validateFull proves the complete catalog/immutable-payload relationship for
// explicit maintenance and an otherwise absent source key. Callers stream one
// bounded pack at a time outside source locks; this never manufactures
// settlement for hot/pending SSH work or incomplete archive operations.
func (c *historyCatalog) validateFull(ctx context.Context) error {
	summary, e := c.summary(ctx)
	if e != nil {
		return e
	}
	if e = c.checkInventory(ctx, summary); e != nil {
		return e
	}
	rows, e := c.db.QueryContext(ctx, `SELECT substr(key,1,129) FROM source_history_operations ORDER BY key LIMIT 257`)
	if e != nil {
		return e
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			rows.Close()
			return e
		}
		keys = append(keys, key)
		if len(keys) > SourceHistoryMaxPacks {
			rows.Close()
			return fmt.Errorf("source operation roster exceeds bound")
		}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	operations := map[string]SourceHistoryOperation{}
	expected := map[string]SourceHistoryMember{}
	packs := map[string]SourceHistoryPack{}
	emptyPlanCatalogs := 0
	var emptyOp *SourceHistoryOperation
	for _, key := range keys {
		if e = ctx.Err(); e != nil {
			return e
		}
		op, e := c.lookupOp(ctx, key)
		if e != nil {
			return e
		}
		operations[key] = op
		if op.Request.Plan.CatalogID == "" {
			emptyPlanCatalogs++
			copy := op
			emptyOp = &copy
		} else if op.Request.Plan.CatalogID != c.marker.CatalogID {
			return fmt.Errorf("source operation belongs to another catalog")
		}
		if op.Phase == "committed" || op.Phase == "completed" {
			row, e := c.pack(ctx, op.PackID)
			if e != nil {
				return e
			}
			if row.Operation != op.Request.Key || row.Members != len(op.Request.Plan.Members) || op.Receipt == nil || row.Receipt != *op.Receipt {
				return fmt.Errorf("source operation/pack settlement linkage differs")
			}
			packs[op.PackID] = row
			for _, m := range op.Request.Plan.Members {
				tuple := m.Source + "\x00" + m.Target + "\x00" + m.Key
				if _, ok := expected[tuple]; ok {
					return fmt.Errorf("duplicate permanent source tuple")
				}
				expected[tuple] = m
			}
		} else {
			var count int
			if e = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_history_packs WHERE id=?`, op.PackID).Scan(&count); e != nil {
				return e
			}
			if count != 0 {
				return fmt.Errorf("uncommitted archive has authoritative cold pack")
			}
		}
	}
	if emptyPlanCatalogs > 1 {
		return fmt.Errorf("multiple source catalog initialization requests")
	}
	if emptyOp != nil {
		for _, op := range operations {
			if op.Created.Before(emptyOp.Created) {
				return fmt.Errorf("empty catalog plan is not the initialization operation")
			}
		}
	}
	if len(packs) != summary.Packs || len(expected) != summary.Archived {
		return fmt.Errorf("source operation/ref/pack roster incomplete")
	}
	rows, e = c.db.QueryContext(ctx, `SELECT substr(source,1,33),substr(target,1,33),substr(key,1,129),substr(kind,1,33),substr(pack,1,33),substr(record,1,4097),substr(sha256,1,65),substr(intent_sha256,1,65) FROM source_history_refs ORDER BY source,target,key LIMIT 16385`)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for rows.Next() {
		var r SourceHistoryRef
		var raw string
		if e = ctx.Err(); e != nil {
			rows.Close()
			return e
		}
		if e = rows.Scan(&r.Source, &r.Target, &r.Key, &r.Kind, &r.Pack, &raw, &r.SHA256, &r.IntentSHA256); e != nil {
			rows.Close()
			return e
		}
		if e = decodeHistory([]byte(raw), &r.Record); e != nil {
			rows.Close()
			return e
		}
		tuple := r.Source + "\x00" + r.Target + "\x00" + r.Key
		m, ok := expected[tuple]
		row, knownPack := packs[r.Pack]
		if !ok || seen[tuple] || !knownPack || r.Source != c.marker.Binding.Host || r.Kind != m.Kind || r.Record != sourceHistoryKey(m) || r.SHA256 != m.SHA256 || r.IntentSHA256 != m.IntentSHA256 || row.Operation == "" {
			rows.Close()
			return fmt.Errorf("source permanent key reference differs from immutable operation")
		}
		op := operations[row.Operation]
		member := false
		for _, v := range op.Request.Plan.Members {
			if v.Source == r.Source && v.Target == r.Target && v.Key == r.Key {
				member = true
			}
		}
		if !member {
			rows.Close()
			return fmt.Errorf("source key points at another original pack")
		}
		seen[tuple] = true
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	if len(seen) != len(expected) {
		return fmt.Errorf("source permanent key references missing")
	}
	var total uint64
	for _, row := range packs {
		if e = ctx.Err(); e != nil {
			return e
		}
		total += row.Receipt.RecordsBytes + row.Receipt.ManifestBytes
		if total > 1<<30 {
			return fmt.Errorf("complete source history proof byte bound exceeded")
		}
		op := operations[row.Operation]
		// Native pack validation is byte consistency. Each original source payload
		// is independently checked for its exact destination terminal/seal relation.
		if e = verifySourceArchivePack(ctx, filepath.Join(c.path, "packs"), op); e != nil {
			return e
		}
	}
	return c.guard()
}

// ValidateSourceHistory performs a bounded complete NON-destructive audit.
// Destructive reset must mirror these raw schemas/relationships independently.
func ValidateSourceHistory(ctx context.Context, home string, b Binding) error {
	marker, e := historyCanonicalMarker(ctx, home, b)
	if e != nil {
		return e
	}
	if marker == nil {
		return nil
	}
	c, e := openHistoryCatalog(ctx, home, b, *marker, false, false, nil)
	if e != nil {
		return e
	}
	defer c.close()
	return c.validateFull(ctx)
}
