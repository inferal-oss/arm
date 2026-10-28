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
	_, locators := p.catalog["sync_workspaces"]
	if p.syncWorkspaceSchema != locators {
		return fmt.Errorf("sync workspace schema and version marker do not match")
	}
	_, charged := p.catalog["sync_charged_scopes"]
	if p.syncChargedSchema != charged {
		return fmt.Errorf("charged sync scope schema and version marker do not match")
	}
	var operations int64
	var chargedRows int64
	anyRows := false
	tables := []string{"sync_operations", "sync_effects", "sync_baselines", "sync_lease_bindings"}
	if locators {
		tables = append(tables, "sync_workspaces")
	}
	if charged {
		tables = append(tables, "sync_charged_scopes")
	}
	for _, table := range tables {
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
		if table == "sync_charged_scopes" {
			chargedRows = count
		}
	}
	if anyRows && !p.syncUsed {
		return fmt.Errorf("sync records lack permanent use marker")
	}
	if p.syncUsed && operations == 0 && chargedRows == 0 {
		return fmt.Errorf("used sync namespace lost operation evidence")
	}
	if p.syncUsed {
		return p.block("sync_history", "sync", "Retained sync operation and native cleanup evidence has no complete reset proof")
	}
	return nil
}
