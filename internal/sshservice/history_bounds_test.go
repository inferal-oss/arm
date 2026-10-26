package sshservice

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"modernc.org/sqlite"
)

func TestSourceHistorySQLFullDoesNotConsumeHotReceiptOrOperation(t *testing.T) {
	home, b, _ := settledSourceHistoryFixture(t, 64)
	q := sourceArchiveRequest(t, home, b, "full-op")
	q.Reason = strings.Repeat("\"", 4096)
	e := withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error {
		m, e := initializeHistory(t.Context(), s, d)
		if e != nil {
			return e
		}
		s.historyMarker = &m
		c, e := openHistoryCatalog(t.Context(), home, b, m, true, false, s.guard)
		if e != nil {
			return e
		}
		defer c.close()
		var pages, max int
		if e = c.db.QueryRow(`PRAGMA page_count`).Scan(&pages); e != nil {
			return e
		}
		if e = c.db.QueryRow("PRAGMA max_page_count=" + strconv.Itoa(pages)).Scan(&max); e != nil {
			return e
		}
		if max != pages {
			t.Fatal("test did not enforce actual SQLite page ceiling")
		}
		now := time.Now().UTC()
		op := SourceHistoryOperation{Schema: 1, Request: q, RequestSHA256: historyRequestSHA(q), Actor: local.CurrentIdentity(), Created: now, Updated: now, Revision: 1, Phase: "prepared", PackID: local.ID(), Cleaned: []string{}}
		e = c.saveOp(t.Context(), op, true)
		var engine *sqlite.Error
		if !errors.As(e, &engine) || engine.Code()&255 != 13 {
			t.Fatal("expected actual SQLITE_FULL at enforced page ceiling", e)
		}
		var count int
		if e = c.db.QueryRow(`SELECT COUNT(*) FROM source_history_operations`).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			t.Fatal("SQL_FULL leaked operation")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(t.Context(), home, b)
	if e != nil || len(s.Completions) != 64 || s.History.Archived != 0 || len(s.History.Busy) != 0 {
		t.Fatalf("failed write lost original64 hot receipts: %v", e)
	}
}
func TestSourceHistoryLatePackRootReplacementRefusesAdmission(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	if _, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "born")); e != nil {
		t.Fatal(e)
	}
	root := home + "/" + SourceHistoryName + "/packs"
	if e := os.Rename(root, root+"-retained"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadState(t.Context(), home, b); e == nil {
		t.Fatal("replacement pack root treated as intact key history")
	}
	if e := ReserveSource(t.Context(), home, b, items[0]); e == nil || errors.Is(e, ErrIntentExists) {
		t.Fatal("corrupt namespace gave definite ordinary key result", e)
	}
}

func TestSourceHistoryJSONCollectionBoundBeforeTypedDecode(t *testing.T) {
	raw := []byte(`{"schema":1,"members":[` + strings.Repeat(`{},`, 10000) + `{}]}`)
	var p SourceHistoryPlan
	if e := decodeHistory(raw, &p); e == nil || !strings.Contains(e.Error(), "before typed allocation") {
		t.Fatal("oversized array not preflight-refused", e)
	}
	if p.Members != nil {
		t.Fatal("oversized untrusted array reached typed allocation")
	}
}
