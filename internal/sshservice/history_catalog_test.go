package sshservice

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceHistoryCatalogBornPolicyReadOnlyReopen(t *testing.T) {
	home, b := stateFixture(t)
	e := withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error { _, e := initializeHistory(t.Context(), s, d); return e })
	if e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(t.Context(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	if s.History == nil || s.History.Archived != 0 || len(s.History.Busy) != 0 {
		t.Fatal(s.History)
	}
	m, e := historyCanonicalMarker(t.Context(), home, b)
	if e != nil || m == nil {
		t.Fatal(m, e)
	}
	c, e := openHistoryCatalog(t.Context(), home, b, *m, false, false, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.close()
	if _, e = c.db.ExecContext(context.Background(), `INSERT INTO source_history_metadata(key,value) VALUES('unsafe','write')`); e == nil {
		t.Fatal("readonly catalog writable")
	}
	if _, e = os.Stat(filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName+"-journal")); !os.IsNotExist(e) {
		t.Fatal("readonly inspection created sidecar", e)
	}
}
