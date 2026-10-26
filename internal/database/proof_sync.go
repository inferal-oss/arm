package database

import "fmt"

// Sync records may contain 64 MiB intents and retained native effects. Until
// their complete native cleanup proof is integrated, reset conservatively
// retains every used sync authority. Empty installed schemas remain provable.
func (p *proofReader) readSync() error {
	_, present := p.catalog["sync_operations"]
	if p.syncSchema != present {
		return fmt.Errorf("sync schema and marker do not match")
	}
	if !present {
		if p.syncUsed {
			return fmt.Errorf("sync use marker lacks namespace")
		}
		return nil
	}
	var operations int64
	anyRows := false
	for _, table := range []string{"sync_operations", "sync_effects", "sync_baselines", "sync_lease_bindings"} {
		var count int64
		if err := p.tx.QueryRowContext(p.ctx, `SELECT COUNT(*) FROM "`+table+`"`).Scan(&count); err != nil {
			return err
		}
		if count < 0 || count > MaxEvidenceRows {
			return fmt.Errorf("sync namespace exceeds proof row bound")
		}
		p.result.Tables[table] = count
		if count > 0 {
			anyRows = true
		}
		if table == "sync_operations" {
			operations = count
		}
	}
	if anyRows && !p.syncUsed {
		return fmt.Errorf("sync records lack permanent use marker")
	}
	if p.syncUsed && operations == 0 {
		return fmt.Errorf("used sync namespace lost operation evidence")
	}
	if p.syncUsed {
		return p.block("sync_history", "sync", "Retained sync operation and native cleanup evidence has no complete reset proof")
	}
	return nil
}
