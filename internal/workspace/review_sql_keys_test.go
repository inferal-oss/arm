package workspace

import "testing"

func TestReviewRejectWorkspacePhysicalKeyNULAlias(t *testing.T) {
	f := newStoreFixture(t)
	root, _ := activateDirectory(t, f)
	if _, err := f.s.Read(f.ctx); err != nil {
		t.Fatal("safe control", err)
	}
	result, err := f.db.Exec(`UPDATE workspace_instances SET id=? WHERE id=?`, root.ID+"\x00hidden", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		t.Fatal("fixture must alter exactly one physical key", n, err)
	}
	var bytes int
	if err = f.db.QueryRow(`SELECT length(CAST(id AS BLOB)) FROM workspace_instances`).Scan(&bytes); err != nil || bytes != len(root.ID)+7 {
		t.Fatal("full malformed key not stored", bytes, err)
	}
	if records, err := f.s.Read(f.ctx); err == nil {
		t.Fatalf("malformed physical root key accepted as valid logical ID: instances=%d", len(records.Instances))
	}
}
