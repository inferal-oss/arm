package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/providerpreparation"
)

type providerPairC1Fixture struct {
	m                       *Manager
	initial                 providerpreparation.Initial
	progress                providerpreparation.Progress
	wires                   [2]providerPairLeaseWire
	budgets                 [2]providerPairBudgetWire
	initialRaw, progressRaw []byte
}

func c1SHA(raw []byte) string { x := sha256.Sum256(raw); return hex.EncodeToString(x[:]) }

// Fixture producer for the explicit candidate encoding, independent of the C1
// reader's framing helper. This writes only temporary SQLite test data.
func c1Frame(domain string, parts ...[]byte) string {
	raw := append([]byte(nil), domain...)
	for _, p := range parts {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		raw = append(raw, n[:]...)
		raw = append(raw, p...)
	}
	return c1SHA(raw)
}
func c1JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func newProviderPairC1Fixture(t *testing.T) *providerPairC1Fixture {
	t.Helper()
	f := &providerPairC1Fixture{m: &Manager{db: providerPairDB(t), host: strings.Repeat("c", 32), owner: "42"}}
	for _, q := range []string{`CREATE TABLE metadata(key TEXT PRIMARY KEY,value)`, providerpreparation.TableDDL, providerpreparation.OperationIndexDDL, providerpreparation.MembersDDL, providerpreparation.RolesIndexDDL} {
		if _, e := f.m.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := f.m.db.Exec(`INSERT INTO metadata VALUES(?,?)`, providerpreparation.SchemaKey, "1"); e != nil {
		t.Fatal(e)
	}
	created := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	sample := leaseclock.Sample{Boot: "c1-structural-fixture", Tick: int64(100 * time.Second), Wall: created}
	deadline, e := leaseclock.New(sample, 300*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	f.initial = providerpreparation.Initial{Schema: 1, Pair: providerpreparation.Hex32(strings.Repeat("e", 32)), OperationKey: "c1-operation", Authority: providerpreparation.Hex32(f.m.host), Owner: "42", OpenEpoch: providerpreparation.Hex32(strings.Repeat("d", 32)), Profile: providerpreparation.InertProfile, Created: providerpreparation.Stamp(created.Format(time.RFC3339Nano)), LifetimeNS: "300000000000", DiskScope: "state-filesystem"}
	path := []string{ownerBudgetID(f.m.host, f.m.owner), "project-policy"}
	for i, role := range []providerpreparation.Role{providerpreparation.Worker, providerpreparation.Temp} {
		id := []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}[i]
		key := []string{"c1:worker", "c1:temp"}[i]
		kind := "provider-prepare-worker"
		amount := providerPairResourcesWire{CPU: 1, Memory: 64 << 20}
		if i == 1 {
			kind = "storage"
			amount = providerPairResourcesWire{Disk: 64 << 20}
		}
		f.budgets[i] = providerPairBudgetWire{Authority: f.m.host, Owner: f.m.owner, Selected: path[1], Path: append([]string(nil), path...), Bound: created.Add(-time.Duration(i+1) * time.Second)}
		f.wires[i] = providerPairLeaseWire{Ref: providerpreparation.LeaseRef{Schema: 1, Pair: f.initial.Pair, Role: role, Generation: "1"}, Clock: *deadline, Budget: f.budgets[i], Policies: []providerPairPolicyWire{{ID: path[0], Revision: 1}, {ID: path[1], Revision: 2}}, ID: id, Host: f.m.host, Owner: f.m.owner, Request: providerPairRequestWire{Budget: path[1], Host: f.m.host, Reason: "C1 inert fixture", Resources: amount, TTL: 300 * time.Second, Key: key, Kind: kind}, State: "provisioning", Created: created, Expires: deadline.Wall, Revision: 1, Capabilities: []string{}}
		f.initial.Members[i].Role = role
	}
	f.progress = providerpreparation.Progress{Schema: 1, Pair: f.initial.Pair, Generation: "1", Revision: "1", Phase: providerpreparation.Preparing, Reason: "none", At: f.initial.Created}
	f.rebind(t)
	return f
}
func (f *providerPairC1Fixture) rebind(t *testing.T) {
	t.Helper()
	for i := range f.wires {
		raw := c1JSON(t, f.wires[i])
		budget := c1JSON(t, f.budgets[i])
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(raw, &fields); e != nil {
			t.Fatal(e)
		}
		f.initial.Members[i].LeaseBytes = raw
		f.initial.Members[i].BudgetBytes = budget
		f.initial.Members[i].Row = providerpreparation.RowRef{ID: providerpreparation.Hex32(f.wires[i].ID), RequestKey: providerpreparation.Key(f.wires[i].Request.Key), RunKind: "sql-null", Revision: "1", DataSHA256: providerpreparation.Digest(c1SHA(raw)), BudgetSHA256: providerpreparation.Digest(c1SHA(budget)), ClockSHA256: providerpreparation.Digest(c1Frame("provider-preparing-clock-v1\x00", fields["elapsed_deadline"], fields["expires_at"]))}
	}
	for i, id := range f.budgets[0].Path {
		revision := fmt.Sprint(f.wires[0].Policies[i].Revision)
		f.initial.Parents[i] = &providerpreparation.ParentCharge{Budget: providerpreparation.Key(id), Revision: providerpreparation.Decimal(revision), Binding: providerpreparation.Digest(c1Frame("provider-preparing-parent-budget-v1\x00", []byte(id), []byte(revision), f.initial.Members[0].BudgetBytes, f.initial.Members[1].BudgetBytes)), Amounts: providerpreparation.Amounts{CPUMillis: "1000", MemoryBytes: "67108864", DiskBytes: "67108864"}}
	}
	f.initialRaw = c1JSON(t, f.initial)
	f.progressRaw = c1JSON(t, f.progress)
	f.persist(t)
}
func (f *providerPairC1Fixture) persist(t *testing.T) {
	t.Helper()
	tx, e := f.m.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM provider_preparation_members`, `DELETE FROM provider_preparations`, `DELETE FROM leases`, `DELETE FROM lease_budget_bindings`} {
		if _, e = tx.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	revision := int64(1)
	switch f.progress.Revision {
	case "2":
		revision = 2
	case "3":
		revision = 3
	}
	_, e = tx.Exec(`INSERT INTO provider_preparations VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, string(f.initial.Pair), string(f.initial.OperationKey), string(f.initial.Authority), string(f.initial.Owner), f.initial.Profile, 1, string(f.progress.Phase), string(f.initial.Members[0].Row.ID), string(f.initial.Members[1].Row.ID), 1000, 64<<20, 64<<20, string(f.initialRaw), c1SHA(f.initialRaw), string(f.progressRaw), revision, providerpreparation.RecordReserveBytes)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range f.initial.Members {
		if _, e = tx.Exec(`INSERT INTO provider_preparation_members VALUES(?,?,?,?)`, string(m.Row.ID), string(f.initial.Pair), string(m.Role), 1); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(`INSERT INTO leases VALUES(?,?,?,NULL)`, string(m.Row.ID), string(m.Row.RequestKey), string(m.LeaseBytes)); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(`INSERT INTO lease_budget_bindings VALUES(?,?)`, string(m.Row.ID), string(m.BudgetBytes)); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func c1Refuse(t *testing.T, f *providerPairC1Fixture) {
	t.Helper()
	v, e := f.m.observeProviderPairJournal(t.Context())
	if e == nil || len(v) != 0 {
		t.Fatal("damaged snapshot returned data", len(v), e)
	}
}

func TestProviderPairC1SnapshotAndOrderedBudgets(t *testing.T) {
	f := newProviderPairC1Fixture(t)
	f.m.leaseClockRead = func() (leaseclock.Sample, error) {
		t.Fatal("data reader attempted live entitlement")
		return leaseclock.Sample{}, nil
	}
	if bytes.Equal(f.initial.Members[0].BudgetBytes, f.initial.Members[1].BudgetBytes) {
		t.Fatal("fixture did not preserve distinct raw sidecars")
	}
	got, e := f.m.observeProviderPairJournal(t.Context())
	if e != nil || len(got) != 1 {
		t.Fatal(got, e)
	}
	if !bytes.Equal(got[0].rawInitial, f.initialRaw) || !bytes.Equal(got[0].rows[0].lease, f.initial.Members[0].LeaseBytes) || got[0].clocks[0] != f.wires[0].Clock {
		t.Fatal("raw/typed snapshot drift")
	}
	// A different raw sidecar serialization remains distinct and must survive
	// exactly; it is never replaced with a JSON remarshal before hashing.
	f.initial.Members[1].BudgetBytes = append(append([]byte(" \n"), f.initial.Members[1].BudgetBytes...), '\t')
	f.initial.Members[1].Row.BudgetSHA256 = providerpreparation.Digest(c1SHA(f.initial.Members[1].BudgetBytes))
	for _, p := range f.initial.Parents {
		if p != nil {
			p.Binding = providerpreparation.Digest(c1Frame("provider-preparing-parent-budget-v1\x00", []byte(p.Budget), []byte(p.Revision), f.initial.Members[0].BudgetBytes, f.initial.Members[1].BudgetBytes))
		}
	}
	f.initialRaw = c1JSON(t, f.initial)
	f.persist(t)
	got, e = f.m.observeProviderPairJournal(t.Context())
	if e != nil || len(got) != 1 || !bytes.Equal(got[0].rows[1].budget, f.initial.Members[1].BudgetBytes) {
		t.Fatal("sidecar bytes canonicalized", got, e)
	}
	// Native entitlement may have expired long ago; a retained observation is
	// still inspectable and must never be represented as an executable owner.
	f.progress.Phase, f.progress.Revision, f.progress.Reason = providerpreparation.Unknown, "2", "clock"
	f.progressRaw = c1JSON(t, f.progress)
	f.persist(t)
	if got, e = f.m.observeProviderPairJournal(t.Context()); e != nil || len(got) != 1 || got[0].progress.Phase != providerpreparation.Unknown {
		t.Fatal(got, e)
	}
}

func TestProviderPairC1EnvelopeStrictness(t *testing.T) {
	changes := []struct {
		name   string
		change func([]byte) []byte
	}{
		{"duplicate", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1)
		}},
		{"case alias", func(b []byte) []byte { return bytes.Replace(b, []byte(`"schema"`), []byte(`"Schema"`), 1) }},
		{"escaped key", func(b []byte) []byte { return bytes.Replace(b, []byte(`"schema"`), []byte(`"sche\u006da"`), 1) }},
		{"null scalar", func(b []byte) []byte { return bytes.Replace(b, []byte(`"schema":1`), []byte(`"schema":null`), 1) }},
		{"empty members", func(b []byte) []byte {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(b, &m)
			m["members"] = []byte(`[]`)
			raw, _ := json.Marshal(m)
			return raw
		}},
		{"trailing", func(b []byte) []byte { return append(b, []byte(` {}`)...) }},
		{"invalid UTF8", func(b []byte) []byte { return append(b, 255) }},
		{"depth", func(b []byte) []byte {
			return []byte(`{"extra":` + strings.Repeat("[", 10) + `0` + strings.Repeat("]", 10) + `}`)
		}},
	}
	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			f := newProviderPairC1Fixture(t)
			f.initialRaw = tt.change(f.initialRaw)
			f.persist(t)
			c1Refuse(t, f)
		})
	}
	for _, n := range []int{1, 3} {
		t.Run("member count/"+string(rune('0'+n)), func(t *testing.T) {
			f := newProviderPairC1Fixture(t)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(f.initialRaw, &fields)
			members := make([]providerpreparation.OriginalMember, n)
			for i := range members {
				members[i] = f.initial.Members[i%2]
			}
			fields["members"] = c1JSON(t, members)
			f.initialRaw = c1JSON(t, fields)
			f.persist(t)
			got, err := f.m.observeProviderPairJournal(t.Context())
			if err == nil || !strings.Contains(err.Error(), "pair array") && !strings.Contains(err.Error(), "pair fixed array") || len(got) != 0 {
				t.Fatal("missing pre-typed member count refusal", got, err)
			}
		})
	}
	t.Run("parents fixed length", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(f.initialRaw, &fields)
		fields["parents"] = c1JSON(t, f.initial.Parents[:15])
		f.initialRaw = c1JSON(t, fields)
		f.persist(t)
		c1Refuse(t, f)
	})
	t.Run("base64 CRLF", func(t *testing.T) {
		var out struct {
			Bytes []byte `json:"bytes"`
		}
		if e := decodeProviderPairJSON([]byte("{\"bytes\":\"YQ==\\n\"}"), &out, 128, 8, false); e == nil {
			t.Fatal("permissive base64 accepted")
		}
	})
	t.Run("base64 decoded byte boundary", func(t *testing.T) {
		var out struct {
			Budget []byte `json:"budget_bytes"`
		}
		for _, n := range []int{4096, 4097} {
			raw := c1JSON(t, struct {
				Budget []byte `json:"budget_bytes"`
			}{bytes.Repeat([]byte("b"), n)})
			e := decodeProviderPairJSON(raw, &out, 8192, 8, false)
			if n == 4096 && (e != nil || len(out.Budget) != 4096) {
				t.Fatal("exact budget bound refused", e)
			}
			out.Budget = nil
			if n == 4097 && (e == nil || !strings.Contains(e.Error(), "base64 size")) {
				t.Fatal("decoded-byte sentinel was not refused before typed decode", e)
			}
		}
	})
	t.Run("paired surrogate", func(t *testing.T) {
		var out struct {
			Value string `json:"value"`
		}
		if e := decodeProviderPairJSON([]byte(`{"value":"\ud83d\ude00"}`), &out, 128, 8, false); e != nil || out.Value != "😀" {
			t.Fatal(out, e)
		}
	})
	t.Run("surrogate", func(t *testing.T) {
		var out struct {
			Value string `json:"value"`
		}
		if e := decodeProviderPairJSON([]byte(`{"value":"\ud800"}`), &out, 128, 8, false); e == nil {
			t.Fatal("unmatched surrogate accepted")
		}
	})
}

func TestProviderPairC1LeaseBindings(t *testing.T) {
	for _, tt := range []struct {
		name   string
		damage func(*providerPairC1Fixture)
	}{
		{"quantity", func(f *providerPairC1Fixture) { f.wires[0].Request.Resources.CPU = 2 }},
		{"role", func(f *providerPairC1Fixture) { f.wires[0].Ref.Role = providerpreparation.Temp }},
		{"generation", func(f *providerPairC1Fixture) { f.wires[0].Ref.Generation = "01" }},
		{"deadline audit", func(f *providerPairC1Fixture) { f.wires[0].Expires = f.wires[0].Expires.Add(time.Second) }},
		{"deadline lifetime", func(f *providerPairC1Fixture) { f.wires[0].Clock.End++ }},
		{"clock boot", func(f *providerPairC1Fixture) { f.wires[0].Clock.Boot = "different-boot" }},
		{"request host", func(f *providerPairC1Fixture) { f.wires[0].Request.Host = strings.Repeat("f", 32) }},
		{"request key", func(f *providerPairC1Fixture) { f.wires[0].Request.Key = f.wires[1].Request.Key }},
		{"state", func(f *providerPairC1Fixture) { f.wires[0].State = "terminal" }},
		{"revision", func(f *providerPairC1Fixture) { f.wires[0].Revision = 2 }},
		{"policy revision", func(f *providerPairC1Fixture) { f.wires[1].Policies[1].Revision++ }},
		{"policy limit", func(f *providerPairC1Fixture) { n := int64(-1); f.wires[0].Policies[0].Limits.Memory = &n }},
		{"sidecar authority", func(f *providerPairC1Fixture) {
			f.budgets[0].Authority = strings.Repeat("f", 32)
			f.wires[0].Budget = f.budgets[0]
		}},
		{"sidecar ancestry", func(f *providerPairC1Fixture) {
			f.budgets[1].Path[1] = "other-policy"
			f.wires[1].Budget = f.budgets[1]
		}},
		{"null capabilities", func(f *providerPairC1Fixture) { f.wires[0].Capabilities = nil }},
		{"capabilities", func(f *providerPairC1Fixture) { f.wires[0].Capabilities = []string{"run"} }},
	} {
		t.Run(tt.name, func(t *testing.T) { f := newProviderPairC1Fixture(t); tt.damage(f); f.rebind(t); c1Refuse(t, f) })
	}
	for _, key := range []string{"pid", "process_identity", "artifact", "managed_dir", "usage", "cargo", "started_at"} {
		t.Run("forbidden presence/"+key, func(t *testing.T) {
			f := newProviderPairC1Fixture(t)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(f.initial.Members[0].LeaseBytes, &fields)
			fields[key] = []byte("null")
			f.initial.Members[0].LeaseBytes = c1JSON(t, fields)
			f.initial.Members[0].Row.DataSHA256 = providerpreparation.Digest(c1SHA(f.initial.Members[0].LeaseBytes))
			f.initialRaw = c1JSON(t, f.initial)
			f.persist(t)
			c1Refuse(t, f)
		})
	}
	t.Run("rounded CPU lexical", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		f.initial.Members[0].LeaseBytes = bytes.Replace(f.initial.Members[0].LeaseBytes, []byte(`"resources":{"cpu":1,`), []byte(`"resources":{"cpu":0.999999999999999999999999999999,`), 1)
		f.initial.Members[0].Row.DataSHA256 = providerpreparation.Digest(c1SHA(f.initial.Members[0].LeaseBytes))
		f.initialRaw = c1JSON(t, f.initial)
		f.persist(t)
		v, e := f.m.observeProviderPairJournal(t.Context())
		if e == nil || !strings.Contains(e.Error(), "CPU quantity") || len(v) != 0 {
			t.Fatal(v, e)
		}
	})
	t.Run("same revision original bytes", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		if _, e := f.m.db.Exec(`UPDATE leases SET data=data||' ' WHERE id=?`, string(f.initial.Members[0].Row.ID)); e != nil {
			t.Fatal(e)
		}
		c1Refuse(t, f)
	})
	t.Run("old budget revision remains inspectable", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		// No live budget definition lookup is allowed to rewrite this immutable
		// snapshot. C2 separately reconciles current policy accounting/ceilings.
		if _, e := f.m.db.Exec(`CREATE TABLE budgets(id TEXT PRIMARY KEY,data TEXT)`); e != nil {
			t.Fatal(e)
		}
		if _, e := f.m.db.Exec(`INSERT INTO budgets VALUES(?,?)`, "project-policy", `{"revision":99}`); e != nil {
			t.Fatal(e)
		}
		got, e := f.m.observeProviderPairJournal(t.Context())
		if e != nil || len(got) != 1 || got[0].leases[0].Policies[1].Revision != 2 {
			t.Fatal(got, e)
		}
	})
}

func TestProviderPairC1HeaderAndReciprocalTypes(t *testing.T) {
	for _, tt := range []struct {
		name, query string
		arg         any
	}{
		{"marker BLOB", `UPDATE metadata SET value=?`, []byte("1")},
		{"marker version", `UPDATE metadata SET value=?`, "2"},
		{"header BLOB", `UPDATE provider_preparations SET authority=?`, []byte(strings.Repeat("c", 32))},
		{"header numeric text", `UPDATE provider_preparations SET generation=?`, "one"},
		{"header numeric BLOB", `UPDATE provider_preparations SET cpu_millis=?`, []byte("1000")},
		{"header real", `UPDATE provider_preparations SET revision=?`, 1.5},
		{"header NULL", `UPDATE provider_preparations SET generation=?`, nil},
		{"header owner spelling", `UPDATE provider_preparations SET owner=?`, "042"},
		{"initial BLOB", `UPDATE provider_preparations SET initial_data=?`, []byte(`{}`)},
		{"initial hash", `UPDATE provider_preparations SET initial_sha256=?`, strings.Repeat("f", 64)},
		{"progress BLOB", `UPDATE provider_preparations SET progress_data=?`, []byte(`{}`)},
		{"progress oversized", `UPDATE provider_preparations SET progress_data=?`, strings.Repeat("x", providerpreparation.MaxProgressBytes+1)},
		{"member generation BLOB", `UPDATE provider_preparation_members SET generation=?`, []byte("1")},
		{"member role BLOB", `UPDATE provider_preparation_members SET role=? WHERE role='worker'`, []byte("worker")},
		{"member missing", `DELETE FROM provider_preparation_members WHERE role=?`, "worker"},
		{"member foreign pair", `UPDATE provider_preparation_members SET preparation=?`, strings.Repeat("f", 32)},
		{"raw budget BLOB", `UPDATE lease_budget_bindings SET data=? WHERE lease=(SELECT worker FROM provider_preparations)`, []byte(`{}`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newProviderPairC1Fixture(t)
			if _, e := f.m.db.Exec(`PRAGMA ignore_check_constraints=ON`); e != nil {
				t.Fatal(e)
			}
			// NOT NULL cannot be disabled. Recreate only this hostile fixture column
			// without constraints when testing SQL NULL, never the production schema.
			if tt.name == "header NULL" {
				if _, e := f.m.db.Exec(`ALTER TABLE provider_preparations RENAME TO original_headers`); e != nil {
					t.Fatal(e)
				}
				if _, e := f.m.db.Exec(`CREATE TABLE provider_preparations AS SELECT * FROM original_headers`); e != nil {
					t.Fatal(e)
				}
			}
			arg := tt.arg
			if tt.name == "initial BLOB" {
				arg = append([]byte(nil), f.initialRaw...)
			}
			if tt.name == "progress BLOB" {
				arg = append([]byte(nil), f.progressRaw...)
			}
			if tt.name == "raw budget BLOB" {
				arg = append([]byte(nil), f.initial.Members[0].BudgetBytes...)
			}
			if _, e := f.m.db.Exec(tt.query, arg); e != nil {
				t.Fatal(e)
			}
			c1Refuse(t, f)
		})
	}
	for _, mode := range []string{"foreign pair", "valid revoked marker under preparing header"} {
		t.Run("marker binding/"+mode, func(t *testing.T) {
			f := newProviderPairC1Fixture(t)
			p := f.progress
			if mode == "foreign pair" {
				p.Pair = providerpreparation.Hex32(strings.Repeat("f", 32))
			} else {
				p.Phase, p.Revision, p.Reason = providerpreparation.Revoked, "2", "cancelled"
			}
			if _, e := f.m.db.Exec(`UPDATE provider_preparations SET progress_data=?`, string(c1JSON(t, p))); e != nil {
				t.Fatal(e)
			}
			c1Refuse(t, f)
		})
	}
	t.Run("foreign member", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		if _, e := f.m.db.Exec(`INSERT INTO provider_preparation_members VALUES(?,?,?,?)`, strings.Repeat("f", 32), strings.Repeat("1", 32), "worker", 1); e != nil {
			t.Fatal(e)
		}
		c1Refuse(t, f)
	})
}

func TestProviderPairC1CensusBeforeBodies(t *testing.T) {
	f := newProviderPairC1Fixture(t)
	// Forty fixed logical reservations exceed4MiB. Bodies remain deliberately
	// malformed; the header aggregate must refuse before decoding any body.
	if _, e := f.m.db.Exec(`DELETE FROM provider_preparation_members`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.m.db.Exec(`DELETE FROM provider_preparations`); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("%032x", i+1)
		worker := fmt.Sprintf("%032x", 1000+i*2)
		temp := fmt.Sprintf("%032x", 1001+i*2)
		if _, e := f.m.db.Exec(`INSERT INTO provider_preparations VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, fmt.Sprint("op-", i), f.m.host, f.m.owner, providerpreparation.InertProfile, 1, "preparing", worker, temp, 1000, 64<<20, 64<<20, "{}", c1SHA([]byte("{}")), "{}", 1, providerpreparation.RecordReserveBytes); e != nil {
			t.Fatal(e)
		}
	}
	got, e := f.m.observeProviderPairJournal(t.Context())
	if e == nil || !strings.Contains(e.Error(), "aggregate logical reservation") || len(got) != 0 {
		t.Fatal("did not refuse at header boundary", got, e)
	}
}

func TestProviderPairC1ClockAndAncestorPreimages(t *testing.T) {
	t.Run("ancestor golden framing", func(t *testing.T) {
		got := providerPairAncestorSHA("budget-root", "7", []byte("{}\n"), []byte("{ }"))
		if got != "d67d0bb00e37053cb4162b2de9f7b2a4e79709a253d49d727196678eb20f416c" {
			t.Fatal(got)
		}
		changed := []string{providerPairAncestorSHA("budget-root", "7", []byte("{ }"), []byte("{}\n")), providerPairAncestorSHA("budget-other", "7", []byte("{}\n"), []byte("{ }")), providerPairAncestorSHA("budget-root", "8", []byte("{}\n"), []byte("{ }"))}
		for _, v := range changed {
			if v == got {
				t.Fatal("role/parent/revision lost from preimage")
			}
		}
		if providerPairAncestorSHA("budget-root", "7", []byte("ab"), []byte("c")) == providerPairAncestorSHA("budget-root", "7", []byte("a"), []byte("bc")) {
			t.Fatal("framing length ambiguity")
		}
	})
	t.Run("clock golden raw values", func(t *testing.T) {
		got, e := providerPairClockSHA([]byte(`{"elapsed_deadline":{ "x": 1 },"expires_at":"stamp"}`))
		if e != nil || got != "d574095f7311aa710f9efcfcce86efb5b6a71f3c088acf0b9503b5e3dfe5e474" {
			t.Fatal(got, e)
		}
	})
	for _, name := range []string{"swapped sides", "parent revision", "clock digest"} {
		t.Run(name, func(t *testing.T) {
			f := newProviderPairC1Fixture(t)
			switch name {
			case "swapped sides":
				p := f.initial.Parents[0]
				p.Binding = providerpreparation.Digest(c1Frame("provider-preparing-parent-budget-v1\x00", []byte(p.Budget), []byte(p.Revision), f.initial.Members[1].BudgetBytes, f.initial.Members[0].BudgetBytes))
			case "parent revision":
				f.initial.Parents[0].Revision = "2"
			case "clock digest":
				f.initial.Members[0].Row.ClockSHA256 = providerpreparation.Digest(strings.Repeat("f", 64))
			}
			f.initialRaw = c1JSON(t, f.initial)
			f.persist(t)
			c1Refuse(t, f)
		})
	}
}

func TestProviderPairC1ReadSnapshotAndCancellation(t *testing.T) {
	t.Run("single snapshot", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		if _, e := f.m.db.Exec(`PRAGMA journal_mode=WAL`); e != nil {
			t.Fatal(e)
		}
		var seq int
		var schema, path string
		if e := f.m.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &path); e != nil {
			t.Fatal(e)
		}
		writer, e := sql.Open("sqlite", path)
		if e != nil {
			t.Fatal(e)
		}
		defer writer.Close()
		writer.SetMaxOpenConns(1)
		tx, e := f.m.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		first, e := readProviderPairJournalSnapshot(t.Context(), tx, f.m.host, f.m.owner)
		if e != nil || len(first) != 1 {
			t.Fatal(first, e)
		}
		progress := f.progress
		progress.Phase, progress.Revision, progress.Reason = providerpreparation.Revoked, "2", "cancelled"
		if _, e = writer.Exec(`UPDATE provider_preparations SET state='revoked',revision=2,progress_data=?`, string(c1JSON(t, progress))); e != nil {
			t.Fatal(e)
		}
		second, e := readProviderPairJournalSnapshot(t.Context(), tx, f.m.host, f.m.owner)
		if e != nil || len(second) != 1 || second[0].progress.Phase != providerpreparation.Preparing {
			t.Fatal("snapshot adopted concurrent marker", second, e)
		}
		if e = tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		current, e := f.m.observeProviderPairJournal(t.Context())
		if e != nil || len(current) != 1 || current[0].progress.Phase != providerpreparation.Revoked {
			t.Fatal("fresh read lost durable marker", current, e)
		}
	})
	t.Run("blocked connection", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		conn, e := f.m.db.Conn(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		var result []providerPairDecoded
		var err error
		before := f.m.db.Stats().WaitCount
		go func() { defer close(done); result, err = f.m.observeProviderPairJournal(ctx) }()
		deadline := time.Now().Add(time.Second)
		for f.m.db.Stats().WaitCount == before {
			if time.Now().After(deadline) {
				t.Fatal("reader did not reach held connection")
			}
			runtime.Gosched()
		}
		cancel()
		providerPairWait(t, done)
		if err == nil || len(result) != 0 {
			t.Fatal(result, err)
		}
		if !f.m.mu.TryLock() {
			t.Fatal("cancelled snapshot retained manager fence")
		}
		f.m.mu.Unlock()
	})
}

func TestProviderPairC1FixedCollections(t *testing.T) {
	t.Run("sixteen ancestors", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		path := []string{ownerBudgetID(f.m.host, f.m.owner)}
		for i := 1; i < 16; i++ {
			path = append(path, fmt.Sprint("policy-", i))
		}
		for i := range f.wires {
			f.budgets[i].Path = append([]string(nil), path...)
			f.budgets[i].Selected = path[15]
			f.wires[i].Budget = f.budgets[i]
			f.wires[i].Request.Budget = path[15]
			f.wires[i].Policies = nil
			for j, id := range path {
				f.wires[i].Policies = append(f.wires[i].Policies, providerPairPolicyWire{ID: id, Revision: int64(j + 1)})
			}
		}
		f.rebind(t)
		got, e := f.m.observeProviderPairJournal(t.Context())
		if e != nil || len(got) != 1 || len(got[0].budgets[0].Path) != 16 {
			t.Fatal(got, e)
		}
	})
	t.Run("seventeenth ancestor before decode", func(t *testing.T) {
		raw := []byte(`{"authority":"` + strings.Repeat("c", 32) + `","owner":"42","selected":"p","path":[` + strings.TrimSuffix(strings.Repeat(`"p",`, 17), ",") + `],"bound_at":"2026-10-28T12:00:00Z"}`)
		var b providerPairBudgetWire
		e := decodeProviderPairJSON(raw, &b, 4096, 16, false)
		if e == nil || !strings.Contains(e.Error(), "array exceeds bound") || b.Path != nil {
			t.Fatal("allocated typed oversized ancestry", b, e)
		}
	})
	t.Run("empty declared namespace is observation only", func(t *testing.T) {
		f := newProviderPairC1Fixture(t)
		for _, q := range []string{`DELETE FROM provider_preparation_members`, `DELETE FROM provider_preparations`} {
			if _, e := f.m.db.Exec(q); e != nil {
				t.Fatal(e)
			}
		}
		got, e := f.m.observeProviderPairJournal(t.Context())
		if e != nil || len(got) != 0 {
			t.Fatal(got, e)
		}
		// Ordinary orphan LeaseRef rows are deliberately not declared covered by
		// this empty namespace result. C2 must census them before any headroom claim.
		var n int
		if e = f.m.db.QueryRow(`SELECT count(*) FROM leases`).Scan(&n); e != nil || n != 2 {
			t.Fatal(n, e)
		}
	})
}
