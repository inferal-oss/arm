package workspace

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkspacePhysicalTextKeysAndRequestAreExact(t *testing.T) {
	d, m, root, _ := activePair(t)
	if _, e := d.s.Deny(d.ctx, root.ID, MutationRequest{Key: "key-fixture-deny", Host: strings.Repeat("a", 32), Revision: func() int64 {
		v, e := d.s.Read(d.ctx)
		if e != nil {
			t.Fatal(e)
		}
		return v.Instances[0].Revision
	}(), Reason: "complete physical key roster"}); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		f             fixture
		table, column string
		limit         int
	}{
		{d, "workspace_instances", "id", 32},
		{d, "workspace_directory_members", "workspace", 32}, {d, "workspace_directory_members", "host", 32},
		{m, "workspace_trusted_members", "workspace", 32}, {m, "workspace_trusted_members", "host", 32},
		{m, "workspace_observations", "workspace", 32},
		{d, "workspace_operations", "workspace", 32}, {d, "workspace_operations", "principal", 512}, {d, "workspace_operations", "key", 128}, {d, "workspace_operations", "request", MaxRecordBytes},
		{d, "workspace_changes", "workspace", 32},
		{d, "workspace_denies", "workspace", 32}, {d, "workspace_denies", "host", 32},
		{d, "workspace_clocks", "workspace", 32},
	} {
		t.Run(tc.table+"/"+tc.column, func(t *testing.T) {
			var original, body string
			if e := tc.f.db.QueryRow(fmt.Sprintf("SELECT %s,data FROM %s LIMIT 1", tc.column, tc.table)).Scan(&original, &body); e != nil {
				t.Fatal("actual table must contain a safe control", e)
			}
			for _, kind := range []string{"null", "blob-same-bytes", "nul-suffix", "invalid-utf8", "empty", "overlong"} {
				t.Run(kind, func(t *testing.T) {
					if _, e := tc.f.s.Read(tc.f.ctx); e != nil {
						t.Fatal("safe control", e)
					}
					var value any
					expression := "?"
					switch kind {
					case "null":
						value = nil
					case "blob-same-bytes":
						value = []byte(original)
					case "nul-suffix":
						value = original + "\x00hidden"
					case "invalid-utf8":
						value = append([]byte(original), 0xff)
						expression = "CAST(? AS TEXT)"
					case "empty":
						value = ""
					case "overlong":
						value = strings.Repeat("x", tc.limit+1)
					}
					result, e := tc.f.db.Exec(fmt.Sprintf("UPDATE %s SET %s=%s WHERE data=?", tc.table, tc.column, expression), value, body)
					if e != nil {
						if kind != "null" || !strings.Contains(e.Error(), "NOT NULL constraint") {
							t.Fatal("malformed seed did not reach SQL", e)
						}
						t.Log("SQLite NOT NULL constraint refused null before reader")
						return
					}
					n, e := result.RowsAffected()
					if e != nil || n != 1 {
						t.Fatal("fixture must change exactly one row", n, e)
					}
					defer func() {
						if _, e := tc.f.db.Exec(fmt.Sprintf("UPDATE %s SET %s=? WHERE data=?", tc.table, tc.column), original, body); e != nil {
							t.Fatal("restore safe fixture", e)
						}
					}()
					if _, e := tc.f.s.Read(tc.f.ctx); e == nil {
						t.Fatal("physical key/request aliased complete JSON evidence")
					}
					if _, e := Open(tc.f.ctx, tc.f.db, tc.f.authority, tc.f.self, tc.f.clock); code(e) != "workspace_state_invalid" {
						t.Fatal("startup accepted malformed physical evidence", e)
					}
				})
			}
		})
	}
}

func TestWorkspacePhysicalJournalSequenceIsPositiveInteger(t *testing.T) {
	f := newStoreFixture(t)
	_, _ = activateDirectory(t, f)
	var sequence int64
	var body string
	if e := f.db.QueryRow(`SELECT sequence,data FROM workspace_changes ORDER BY sequence LIMIT 1`).Scan(&sequence, &body); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name  string
		value any
	}{{"zero", int64(0)}, {"negative", int64(-1)}, {"null", nil}, {"text", "not-an-integer"}, {"blob", []byte("1")}} {
		t.Run(tc.name, func(t *testing.T) {
			result, e := f.db.Exec(`UPDATE workspace_changes SET sequence=? WHERE data=?`, tc.value, body)
			if e != nil {
				if (tc.name != "text" && tc.name != "blob" && tc.name != "null") || !strings.Contains(e.Error(), "datatype mismatch") {
					t.Fatal("unexpected seed refusal", e)
				}
				t.Log("SQLite INTEGER PRIMARY KEY refused malformed update", e)
				return
			}
			n, e := result.RowsAffected()
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			defer func() {
				if _, e := f.db.Exec(`UPDATE workspace_changes SET sequence=? WHERE data=?`, sequence, body); e != nil {
					t.Fatal(e)
				}
			}()
			if tc.name == "null" {
				t.Log("SQLite assigned a new positive integer to NULL INTEGER PRIMARY KEY; no malformed key stored")
				return
			}
			if _, e := f.s.Read(f.ctx); e == nil {
				t.Fatal("nonpositive physical journal key accepted")
			}
		})
	}
}

func TestWorkspacePhysicalUTF8KeyAtByteBoundIsAccepted(t *testing.T) {
	f := newStoreFixture(t)
	key := strings.Repeat("é", 64)
	if len(key) != 128 || !utf8.ValidString(key) {
		t.Fatal("invalid test control")
	}
	if _, e := f.s.PrepareCreate(f.ctx, CreateRequest{Key: key, Name: "complete bytes", Policy: DefaultPolicy(), Reason: "legitimate UTF8 key at128byte bound"}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Read(f.ctx); e != nil {
		t.Fatal("physical byte bound rejected legitimate UTF8", e)
	}
}
