package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCurrentMemberCannotInventIntroducerOrEraseAudit(t *testing.T) {
	for _, kind := range []string{"member-introducer", "member-consent", "delete-member", "delete-trusted", "delete-journal", "clock-null", "missing-clock", "clock-schema", "mixed-schema", "unknown-trigger"} {
		t.Run(kind, func(t *testing.T) {
			d := newStoreFixture(t)
			root, _ := activateDirectory(t, d)
			m, _, admission := joinedCandidate(t, d, root)
			completeCandidate(t, d, root, m, admission)
			switch kind {
			case "member-introducer", "member-consent":
				var raw string
				if e := d.db.QueryRow(`SELECT data FROM workspace_directory_members WHERE workspace=? AND host=?`, root.ID, m.self.Host).Scan(&raw); e != nil {
					t.Fatal(e)
				}
				var member Member
				if e := Decode([]byte(raw), &member); e != nil {
					t.Fatal(e)
				}
				if kind == "member-introducer" {
					member.Introducer = true
				} else {
					member.ConsentHash = hash("different consent")
				}
				data, _ := json.Marshal(member)
				if _, e := d.db.Exec(`UPDATE workspace_directory_members SET data=? WHERE workspace=? AND host=?`, string(data), root.ID, m.self.Host); e != nil {
					t.Fatal(e)
				}
			case "delete-member":
				_, _ = d.db.Exec(`DELETE FROM workspace_directory_members WHERE host=?`, m.self.Host)
			case "delete-trusted":
				_, _ = d.db.Exec(`DELETE FROM workspace_trusted_members WHERE host=?`, m.self.Host)
			case "delete-journal":
				_, _ = d.db.Exec(`DELETE FROM workspace_changes WHERE sequence=(SELECT max(sequence) FROM workspace_changes)`)
			case "clock-null":
				_, _ = d.db.Exec(`UPDATE workspace_clocks SET data=json_set(data,'$.wall_floor',null)`)
			case "missing-clock":
				_, _ = d.db.Exec(`DELETE FROM workspace_clocks`)
			case "clock-schema":
				_, _ = d.db.Exec(`UPDATE workspace_clocks SET data=json_set(data,'$.schema_version',2)`)
			case "mixed-schema":
				_, _ = d.db.Exec(`DROP TABLE workspace_denies`)
			case "unknown-trigger":
				_, e := d.db.Exec(`CREATE TRIGGER workspace_surprise AFTER INSERT ON workspace_operations BEGIN UPDATE workspace_instances SET data='{}'; END`)
				if e != nil {
					t.Fatal(e)
				}
			}
			if _, e := d.s.Read(d.ctx); code(e) != "workspace_state_invalid" {
				t.Fatal("corrupt relationships accepted", kind, e)
			}
		})
	}
}
func TestUnknownAndNullNestedFieldsNeverBecomeZeroPermissions(t *testing.T) {
	policy, _ := json.Marshal(DefaultPolicy())
	cases := []string{strings.Replace(string(policy), `"host_inbox_enabled":true`, `"host_inbox_enabled":null`, 1), strings.Replace(string(policy), `"max_members":128`, `"Max_Members":128`, 1), strings.Replace(string(policy), `"max_members":128`, `"max_members":128,"max_members":1`, 1), strings.TrimSuffix(string(policy), "}") + `,"run":true}`}
	for _, raw := range cases {
		var p Policy
		if e := Decode([]byte(raw), &p); e == nil {
			t.Fatal("noncanonical/null policy accepted", raw)
		}
	}
}

func TestExplicitNullCannotReplaceOmittedWorkspaceEvidence(t *testing.T) {
	for _, c := range []struct{ table, path string }{
		{"workspace_operations", "$.member"},
		{"workspace_operations", "$.private.introducer"},
		{"workspace_changes", "$.before"},
		{"workspace_changes", "$.member_before"},
		{"workspace_changes", "$.member_after"},
		{"workspace_clocks", "$.last_issued_at"},
	} {
		t.Run(c.table+c.path, func(t *testing.T) {
			f := newStoreFixture(t)
			_, err := f.s.PrepareCreate(f.ctx, CreateRequest{Key: "create", Name: "fixture workspace", Policy: DefaultPolicy(), Reason: "null evidence regression"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.Read(f.ctx); err != nil {
				t.Fatal("valid writer evidence rejected", err)
			}
			if _, err = f.db.Exec(`UPDATE `+c.table+` SET data=json_set(data,?,NULL)`, c.path); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.Read(f.ctx); code(err) != "workspace_state_invalid" {
				t.Fatal("explicit null accepted in place of omitted producer evidence", err)
			}
		})
	}
}
func TestCopiedAuthorityAndChangedLocalIdentityCannotAcquireCurrentMembership(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	other := identityFixture(t, d.clock.Wall())
	a := d.authority
	a.Host = other.Host
	if _, e := Open(d.ctx, d.db, a, other, d.clock); code(e) != "workspace_state_invalid" {
		t.Fatal("copied central authority accepted", e)
	}
	// Historical public identities remain inspectable after a local cert change,
	// but no current workspace principal can borrow the old local identity.
	d.s.self = other
	if _, e := d.s.Read(d.ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := d.s.Issue(d.ctx, RefreshRequest{rootPrincipal(root), hash("request")}); code(e) != "forbidden" {
		t.Fatal("old local key binding silently reused", e)
	}
}
