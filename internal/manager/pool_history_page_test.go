package manager

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func TestPoolJournalPagesSparseBidirectionalAndExactBoundaries(t *testing.T) {
	m := newManager(t)
	a := makePool(t, m, "paged-a", "global", math.MaxInt64)
	b := makePool(t, m, "paged-b", "host", 1)
	for i := 0; i < 113; i++ {
		for _, p := range []*model.Pool{&a, &b} {
			change, err := m.MutatePool(model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(-1), Reason: "Sparse page journal", Key: local.ID()})
			if p == &b { // Keep the second pool available without a capacity failure.
				if err != nil {
					change, err = m.MutatePool(model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(1), Reason: "Interleaved other ledger", Key: local.ID()})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			*p = change.Pool
		}
	}
	first, err := m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: a.ID})
	if err != nil || len(first.Entries) != 100 || first.PreviousBefore != nil || first.NextAfter == nil {
		t.Fatal(first, err)
	}
	if first.Entries[0].Pool.Capacity != math.MaxInt64 || first.Entries[0].Actor != m.owner {
		t.Fatal("large count/actor lost", first.Entries[0])
	}
	for i, e := range first.Entries {
		if e.Pool.ID != a.ID || i > 0 && e.Sequence <= first.Entries[i-1].Sequence {
			t.Fatal("wrong/sorted page", e)
		}
	}
	second, err := m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: a.ID, After: first.NextAfter})
	if err != nil || len(second.Entries) != 14 || second.PreviousBefore == nil || second.NextAfter != nil {
		t.Fatal(second, err)
	}
	previous, err := m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: a.ID, Before: second.PreviousBefore})
	if err != nil || len(previous.Entries) != 100 || previous.Entries[0].Sequence != first.Entries[0].Sequence || previous.Entries[99].Sequence != first.Entries[99].Sequence {
		t.Fatal("previous boundary guessed from sparse sequences", previous, err)
	}
	maximum := int64(math.MaxInt64)
	empty, err := m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: a.ID, After: &maximum})
	if err != nil || len(empty.Entries) != 0 || empty.PreviousBefore == nil || *empty.PreviousBefore != maximum || empty.NextAfter != nil {
		t.Fatal("empty maximum boundary", empty, err)
	}
	last, err := m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: a.ID, Before: empty.PreviousBefore})
	if err != nil || len(last.Entries) != 100 || last.NextAfter != nil || last.Entries[99].Sequence != second.Entries[13].Sequence {
		t.Fatal("last page inclusive boundary", last, err)
	}
	beforeFirst := int64(1) // Pool b starts at global sequence 2, so this is an empty valid backward page.
	empty, err = m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: b.ID, Before: &beforeFirst})
	if err != nil || len(empty.Entries) != 0 || empty.NextAfter == nil || *empty.NextAfter != beforeFirst || empty.PreviousBefore != nil {
		t.Fatal("empty backward page lost forward navigation", empty, err)
	}
	for _, r := range []model.PoolHistoryPageRequest{{ID: a.ID, After: count(-1)}, {ID: a.ID, Before: count(0)}, {ID: a.ID, After: count(0), Before: count(1)}, {ID: ""}} {
		if _, e := m.PoolHistoryPage(r); errorCode(e) != "invalid_request" {
			t.Fatal("invalid paging request", r, e)
		}
	}
}
func TestPoolJournalPagesBoundBytesWithoutDroppingEntries(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "byte-bounded", "global", 1)
	for i := 0; i < 120; i++ {
		change, err := m.MutatePool(model.PoolMutation{Operation: "describe", ID: p.ID, Description: strings.Repeat("d", 4096), Revision: count(p.Revision), Reason: strings.Repeat("r", 4096), Key: local.ID()})
		if err != nil {
			t.Fatal(err)
		}
		p = change.Pool
	}
	var after *int64
	seen := map[int64]bool{}
	pages := 0
	for {
		page, err := m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: p.ID, After: after})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page)
		if len(raw) > poolJournalPageBytes || len(page.Entries) > 100 {
			t.Fatal("page budget exceeded", len(raw), len(page.Entries))
		}
		for _, entry := range page.Entries {
			if seen[entry.Sequence] {
				t.Fatal("duplicate forward entry", entry.Sequence)
			}
			seen[entry.Sequence] = true
			if entry.Previous != nil && (len(entry.Reason) != 4096 || len(entry.Pool.Description) != 4096) {
				t.Fatal("entry silently shortened")
			}
		}
		pages++
		if page.NextAfter == nil {
			break
		}
		if pages > 10 {
			t.Fatal("cursor failed to progress")
		}
		after = page.NextAfter
	}
	if pages < 2 || len(seen) != 121 {
		t.Fatal("history inaccessible", pages, len(seen))
	}
}

func TestPoolJournalPagesRefuseOversizedOrMismatchedRecords(t *testing.T) {
	for _, scenario := range []string{"oversized", "oversized-text", "other-pool", "other-authority", "other-owner", "wrong-before"} {
		t.Run(scenario, func(t *testing.T) {
			m := newManager(t)
			p := makePool(t, m, "corrupt-journal", "host", 1)
			var original []byte
			if err := m.db.QueryRow(`SELECT data FROM pool_changes WHERE pool=?`, p.ID).Scan(&original); err != nil {
				t.Fatal(err)
			}
			var change model.PoolChange
			if err := json.Unmarshal(original, &change); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "other-pool":
				change.Pool.ID = "different"
			case "other-authority":
				change.Pool.Authority = "different"
			case "other-owner":
				change.Pool.Owner = "different"
			case "wrong-before":
				previous := change.Pool
				previous.ID = "different"
				change.Previous = &previous
			}
			raw, err := json.Marshal(change)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "oversized" {
				raw = append(raw, []byte(strings.Repeat(" ", poolJournalPageBytes))...)
			}
			var payload any = raw
			if scenario == "oversized-text" {
				payload = `{"ignored":"` + strings.Repeat("界", poolJournalPageBytes/2) + `"}`
			}
			if _, err = m.db.Exec(`UPDATE pool_changes SET data=? WHERE pool=?`, payload, p.ID); err != nil {
				t.Fatal(err)
			}
			_, err = m.PoolHistoryPage(model.PoolHistoryPageRequest{ID: p.ID})
			want := "invalid_accounting"
			if strings.HasPrefix(scenario, "oversized") {
				want = "response_too_large"
			}
			if errorCode(err) != want {
				t.Fatalf("got %v, want %s", err, want)
			}
		})
	}
}
