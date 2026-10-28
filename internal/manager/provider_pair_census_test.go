package manager

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/providerpreparation"
)

// All schema production below is private disposable fixture data. No Manager
// installer, format change, native clock, factory or admission publisher runs.
func c2Fixture(t *testing.T, pair bool) *providerPairC1Fixture {
	t.Helper()
	f := newProviderPairC1Fixture(t)
	for _, q := range []string{`CREATE TABLE budgets(id TEXT PRIMARY KEY,owner,parent,data)`, `CREATE TABLE pools(id TEXT PRIMARY KEY,scope,host,name,data)`, `CREATE TABLE pool_lineage(pool TEXT PRIMARY KEY,data)`, `CREATE TABLE pool_claim_bindings(lease TEXT PRIMARY KEY,data)`} {
		if _, e := f.m.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	at := f.wires[0].Created
	for i, id := range f.budgets[0].Path {
		b := model.Budget{ID: id, Authority: f.m.host, Owner: f.m.owner, Root: i == 0, Name: "fixture budget", State: "active", Revision: int64(i + 1), Created: at, Updated: at}
		var parent any
		if i > 0 {
			b.Parent = f.budgets[0].Path[i-1]
			parent = b.Parent
		}
		if _, e := f.m.db.Exec(`INSERT INTO budgets VALUES(?,?,?,?)`, id, b.Owner, parent, string(c1JSON(t, b))); e != nil {
			t.Fatal(e)
		}
	}
	if !pair {
		for _, q := range []string{`DELETE FROM provider_preparation_members`, `DELETE FROM provider_preparations`, `DELETE FROM leases`, `DELETE FROM lease_budget_bindings`, `DROP TABLE provider_preparation_members`, `DROP TABLE provider_preparations`, `DELETE FROM metadata WHERE key='provider_preparation_schema'`} {
			if _, e := f.m.db.Exec(q); e != nil {
				t.Fatal(e)
			}
		}
	}
	return f
}
func c2Lease(t *testing.T, f *providerPairC1Fixture, id, state string, q model.Resources, run bool) model.Lease {
	t.Helper()
	at := f.wires[0].Created
	b := model.BudgetBinding{Authority: f.m.host, Owner: f.m.owner, Selected: f.budgets[0].Selected, Path: append([]string{}, f.budgets[0].Path...), Bound: at}
	l := model.Lease{ID: id, Host: f.m.host, Owner: f.m.owner, Request: model.Request{Host: f.m.host, Budget: b.Selected, Key: "ordinary:" + id, Reason: "ordinary census fixture", Resources: q, TTL: time.Minute, Kind: "execution"}, Budget: &b, State: state, Created: at, Revision: 1, Capabilities: []string{}}
	if state == "terminal" {
		ended := at.Add(time.Minute)
		l.Ended = &ended
		l.Outcome = "completed"
	}
	var descriptor any
	if run {
		descriptor = string(c1JSON(t, RunRequest{Request: l.Request, Command: []string{"echo", "fixture"}, CWD: "/fixture"}))
	}
	if _, e := f.m.db.Exec(`INSERT INTO leases VALUES(?,?,?,?)`, l.ID, l.Request.Key, string(c1JSON(t, l)), descriptor); e != nil {
		t.Fatal(e)
	}
	if _, e := f.m.db.Exec(`INSERT INTO lease_budget_bindings VALUES(?,?)`, l.ID, string(c1JSON(t, b))); e != nil {
		t.Fatal(e)
	}
	return l
}
func c2Exec(t *testing.T, f *providerPairC1Fixture, query string, args ...any) {
	t.Helper()
	if _, e := f.m.db.Exec(query, args...); e != nil {
		t.Fatal(e)
	}
}
func c2Update(t *testing.T, f *providerPairC1Fixture, l model.Lease) {
	t.Helper()
	if _, e := f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(c1JSON(t, l)), l.ID); e != nil {
		t.Fatal(e)
	}
}
func c2Unknown(t *testing.T, f *providerPairC1Fixture) providerPairC2Projection {
	t.Helper()
	p, e := f.m.observeProviderPairInventory(t.Context())
	if e == nil || p.state != "unknown" || p.problem == nil {
		t.Fatal("unknown inventory appeared covered", p, e)
	}
	return p
}
func TestProviderPairC2CompleteCensusAndNoDoubleCount(t *testing.T) {
	f := c2Fixture(t, true)
	c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: .125, Memory: 10, Disk: 20}, true)
	c2Lease(t, f, strings.Repeat("2", 32), "queued", model.Resources{CPU: 100, Memory: 100, Disk: 100}, false)
	c2Lease(t, f, strings.Repeat("3", 32), "terminal", model.Resources{CPU: 100, Memory: 100, Disk: 100}, false)
	p, e := f.m.observeProviderPairInventory(t.Context())
	want := providerPairQuantity{1125, (64 << 20) + 10, (64 << 20) + 20}
	if e != nil || p.state != "covered" || p.physical != want || p.ordinaryRows != 5 || p.pairs != 1 || p.logicalBytes != providerpreparation.RecordReserveBytes {
		t.Fatal(p, e)
	}
	for _, id := range f.budgets[0].Path {
		if p.budgets[id].resources != want {
			t.Fatal("ancestor added to physical or member counted twice", id, p)
		}
	}
	if len(p.budgets) != 2 || len(p.pools) != 0 {
		t.Fatal(p)
	}
	// Mutating a returned accounting value cannot create or register an original.
	p.physical = providerPairQuantity{}
	again, e := f.m.observeProviderPairInventory(t.Context())
	if e != nil || again.physical != want || len(f.m.relayTransits) != 0 {
		t.Fatal(again, e)
	}
}
func TestProviderPairC2OrdinaryStorageDebtObservations(t *testing.T) {
	f := c2Fixture(t, false)
	for i, state := range []string{"active", "terminal"} {
		id := strings.Repeat(fmt.Sprint(i+4), 32)
		l := c2Lease(t, f, id, state, model.Resources{Disk: 32}, false)
		l.Request.Kind = "storage"
		zero := int64(0)
		l.Artifact = &model.Artifact{Name: id, Path: "/fixture/" + id, Identity: &model.FileIdentity{Inode: uint64(i + 1), Owner: 42, BirthKnown: true}, Phase: "owned", Updated: l.Created}
		if state == "terminal" {
			l.Outcome = "released"
			l.Artifact.Phase = "removed"
			l.Artifact.AllocatedBefore = &zero
			l.Artifact.NamespaceRemoved = true
		}
		c2Update(t, f, l)
	}
	p, e := f.m.observeProviderPairInventory(t.Context())
	if e != nil || p.state != "covered" || p.physical != (providerPairQuantity{diskBytes: 32}) {
		t.Fatal(p, e)
	}
}

func TestProviderPairC2AbsentNamespaceAndOrphanPresence(t *testing.T) {
	t.Run("truly absent empty namespace", func(t *testing.T) {
		f := c2Fixture(t, false)
		p, e := f.m.observeProviderPairInventory(t.Context())
		if e != nil || p.state != "covered" || p.ordinaryRows != 0 || p.physical != (providerPairQuantity{}) {
			t.Fatal(p, e)
		}
	})
	for _, field := range []string{`"provider_preparation":null`, `"provider_preparation":false`, `"provider_preparation":{"schema":99}`, `"Provider_Preparation":null`, `"provider_preparatio\u006e":false`, `"provider_preparation":{},"provider_preparation":null`} {
		t.Run(field, func(t *testing.T) {
			f := c2Fixture(t, false)
			l := c2Lease(t, f, strings.Repeat("1", 32), "terminal", model.Resources{CPU: 1, Memory: 64 << 20}, false)
			raw := c1JSON(t, l)
			raw = append(append(append([]byte{}, raw[:len(raw)-1]...), ','), []byte(field+"}")...)
			if _, e := f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID); e != nil {
				t.Fatal(e)
			}
			p := c2Unknown(t, f)
			if p.physical.cpuMillis < 1000 {
				t.Fatal("orphan readable promise vanished", p)
			}
		})
	}
	t.Run("orphan worker kind without Ref", func(t *testing.T) {
		f := c2Fixture(t, false)
		l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: 1}, false)
		l.Request.Kind = "provider-prepare-worker"
		c2Update(t, f, l)
		c2Unknown(t, f)
	})
	t.Run("empty index still has two orphan original rows", func(t *testing.T) {
		f := c2Fixture(t, true)
		if _, e := f.m.db.Exec(`DELETE FROM provider_preparation_members`); e != nil {
			t.Fatal(e)
		}
		if _, e := f.m.db.Exec(`DELETE FROM provider_preparations`); e != nil {
			t.Fatal(e)
		}
		c2Unknown(t, f)
	})
}
func TestProviderPairC2RetainsDamagedPairPromises(t *testing.T) {
	for _, mode := range []string{"missing worker", "terminal worker", "larger worker", "missing membership", "missing sidecar", "changed run", "alias role", "unknown phase", "BLOB marker and missing worker"} {
		t.Run(mode, func(t *testing.T) {
			f := c2Fixture(t, true)
			id := string(f.initial.Members[0].Row.ID)
			switch mode {
			case "missing worker":
				if _, e := f.m.db.Exec(`DELETE FROM leases WHERE id=?`, id); e != nil {
					t.Fatal(e)
				}
				if _, e := f.m.db.Exec(`DELETE FROM lease_budget_bindings WHERE lease=?`, id); e != nil {
					t.Fatal(e)
				}
			case "terminal worker":
				if _, e := f.m.db.Exec(`UPDATE leases SET data=json_set(data,'$.state','terminal','$.request.resources.cpu',0) WHERE id=?`, id); e != nil {
					t.Fatal(e)
				}
			case "larger worker":
				if _, e := f.m.db.Exec(`UPDATE leases SET data=json_set(data,'$.request.resources.cpu',2) WHERE id=?`, id); e != nil {
					t.Fatal(e)
				}
			case "missing membership":
				if _, e := f.m.db.Exec(`DELETE FROM provider_preparation_members WHERE lease=?`, id); e != nil {
					t.Fatal(e)
				}
			case "missing sidecar":
				if _, e := f.m.db.Exec(`DELETE FROM lease_budget_bindings WHERE lease=?`, id); e != nil {
					t.Fatal(e)
				}
			case "changed run":
				if _, e := f.m.db.Exec(`UPDATE leases SET run='{}' WHERE id=?`, id); e != nil {
					t.Fatal(e)
				}
			case "alias role":
				if _, e := f.m.db.Exec(`UPDATE leases SET data=json_set(data,'$.provider_preparation.role','temp') WHERE id=?`, id); e != nil {
					t.Fatal(e)
				}
			case "BLOB marker and missing worker":
				c2Exec(t, f, `UPDATE metadata SET value=CAST(value AS BLOB) WHERE key=?`, providerpreparation.SchemaKey)
				c2Exec(t, f, `DELETE FROM leases WHERE id=?`, id)
				c2Exec(t, f, `DELETE FROM lease_budget_bindings WHERE lease=?`, id)
			case "unknown phase":
				f.progress.Phase = providerpreparation.Unknown
				f.progress.Revision = "2"
				f.progress.Reason = "binding"
				f.progressRaw = c1JSON(t, f.progress)
				f.persist(t)
			}
			p := c2Unknown(t, f)
			minCPU := int64(1000)
			if mode == "larger worker" {
				minCPU = 2000
			}
			if p.physical.cpuMillis < minCPU || p.physical.memoryBytes < 64<<20 || p.physical.diskBytes < 64<<20 || p.logicalBytes != providerpreparation.RecordReserveBytes {
				t.Fatal("damaged pair shed retained floor", mode, p)
			}
		})
	}
}
func TestProviderPairC2OrdinaryTupleAndStrictJSON(t *testing.T) {
	for _, mode := range []string{"data BLOB", "id BLOB", "key BLOB", "run BLOB", "run request drift", "JSON id", "case state alias", "duplicate state", "null state", "unknown state", "unknown terminal outcome", "negative memory", "terminal artifact debt", "sidecar BLOB"} {
		t.Run(mode, func(t *testing.T) {
			f := c2Fixture(t, false)
			l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: .5, Memory: 10, Disk: 20}, mode == "run request drift")
			queries := map[string]string{"data BLOB": `UPDATE leases SET data=CAST(data AS BLOB)`, "id BLOB": `UPDATE leases SET id=CAST(id AS BLOB)`, "key BLOB": `UPDATE leases SET request_key=CAST(request_key AS BLOB)`, "run BLOB": `UPDATE leases SET run=CAST('{}' AS BLOB)`, "run request drift": `UPDATE leases SET run=json_set(run,'$.resources.cpu',.75)`, "JSON id": `UPDATE leases SET data=json_set(data,'$.id','other')`, "null state": `UPDATE leases SET data=json_set(data,'$.state',NULL)`, "unknown state": `UPDATE leases SET data=json_set(data,'$.state','future')`, "unknown terminal outcome": `UPDATE leases SET data=json_set(data,'$.state','terminal','$.ended_at','2026-10-28T12:01:00Z','$.outcome','future')`, "negative memory": `UPDATE leases SET data=json_set(data,'$.request.resources.memory_bytes',-1)`, "sidecar BLOB": `UPDATE lease_budget_bindings SET data=CAST(data AS BLOB)`}
			if q := queries[mode]; q != "" {
				if _, e := f.m.db.Exec(q); e != nil {
					t.Fatal(e)
				}
			} else if mode == "terminal artifact debt" {
				l.State = "terminal"
				at := l.Created.Add(time.Minute)
				l.Ended = &at
				l.Outcome = "released"
				l.Artifact = &model.Artifact{Phase: "owned", ContentObserved: true, Updated: l.Created}
				c2Update(t, f, l)
			} else {
				raw := c1JSON(t, l)
				field := `"State":"terminal"`
				if mode == "duplicate state" {
					field = `"state":"terminal"`
				}
				raw = append(append(raw[:len(raw)-1], ','), []byte(field+"}")...)
				if _, e := f.m.db.Exec(`UPDATE leases SET data=?`, string(raw)); e != nil {
					t.Fatal(e)
				}
			}
			c2Unknown(t, f)
		})
	}
}
func TestProviderPairC2BudgetAncestryAndSidecars(t *testing.T) {
	for _, mode := range []string{"missing sidecar", "sidecar owner", "sidecar path cycle", "current reparent", "definition BLOB", "indexed owner BLOB", "orphan sidecar", "orphan pool sidecar", "definition revision advanced"} {
		t.Run(mode, func(t *testing.T) {
			f := c2Fixture(t, true)
			l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: .1}, false)
			switch mode {
			case "missing sidecar":
				c2Exec(t, f, `DELETE FROM lease_budget_bindings WHERE lease=?`, l.ID)
			case "sidecar owner":
				c2Exec(t, f, `UPDATE lease_budget_bindings SET data=json_set(data,'$.owner','99') WHERE lease=?`, l.ID)
			case "sidecar path cycle":
				c2Exec(t, f, `UPDATE lease_budget_bindings SET data=json_set(data,'$.path[1]',json_extract(data,'$.path[0]')) WHERE lease=?`, l.ID)
			case "current reparent":
				c2Exec(t, f, `UPDATE budgets SET parent='other',data=json_set(data,'$.parent','other') WHERE id=?`, f.budgets[0].Selected)
			case "definition BLOB":
				c2Exec(t, f, `UPDATE budgets SET data=CAST(data AS BLOB)`)
			case "indexed owner BLOB":
				c2Exec(t, f, `UPDATE budgets SET owner=CAST(owner AS BLOB)`)
			case "orphan sidecar":
				c2Exec(t, f, `INSERT INTO lease_budget_bindings VALUES(?,?)`, strings.Repeat("f", 32), string(c1JSON(t, l.Budget)))
			case "orphan pool sidecar":
				c2Exec(t, f, `INSERT INTO pool_claim_bindings VALUES(?,?)`, strings.Repeat("f", 32), "{}")
			case "definition revision advanced":
				c2Exec(t, f, `UPDATE budgets SET data=json_set(data,'$.revision',99)`)
			}
			p, e := f.m.observeProviderPairInventory(t.Context())
			if mode == "definition revision advanced" {
				if e != nil || p.state != "covered" || p.physical.cpuMillis != 1100 {
					t.Fatal(p, e)
				}
			} else if e == nil || p.state != "unknown" {
				t.Fatal(mode, p, e)
			}
		})
	}
}
func c2Pools(t *testing.T, f *providerPairC1Fixture, l *model.Lease) (string, string) {
	t.Helper()
	at := l.Created
	root, child := "pool_"+strings.Repeat("8", 32), "pool_"+strings.Repeat("9", 32)
	for i, id := range []string{root, child} {
		p := model.Pool{ID: id, Name: fmt.Sprint("pool", i), Authority: f.m.host, Owner: f.m.owner, Scope: "host", Host: f.m.host, Unit: "slot", Capacity: 10, Revision: 1, State: "active", Accounting: "cooperative-counted-permits", Expiry: "return-after-lease-settlement", Created: at, Updated: at}
		path := []string{root}
		if i == 1 {
			p.Parent = root
			path = append(path, child)
		}
		if _, e := f.m.db.Exec(`INSERT INTO pools VALUES(?,?,?,?,?)`, id, p.Scope, p.Host, p.Name, string(c1JSON(t, p))); e != nil {
			t.Fatal(e)
		}
		lineage := model.PoolLineage{Pool: id, Parent: p.Parent, Authority: p.Authority, Owner: p.Owner, Scope: p.Scope, Host: p.Host, Unit: p.Unit, Path: path}
		if _, e := f.m.db.Exec(`INSERT INTO pool_lineage VALUES(?,?)`, id, string(c1JSON(t, lineage))); e != nil {
			t.Fatal(e)
		}
	}
	l.Request.Pools = []model.PoolClaim{{Pool: child, Quantity: 3, Authority: f.m.host, Scope: "host", Host: f.m.host}}
	binding := model.PoolClaimBinding{Authority: f.m.host, Owner: f.m.owner, Bound: at, Claims: []model.BoundPoolClaim{{Pool: child, Quantity: 3, Path: []string{root, child}}}}
	l.PoolBinding = &binding
	c2Update(t, f, *l)
	if _, e := f.m.db.Exec(`INSERT INTO pool_claim_bindings VALUES(?,?)`, l.ID, string(c1JSON(t, binding))); e != nil {
		t.Fatal(e)
	}
	return root, child
}
func TestProviderPairC2PoolRollupsRemainSeparate(t *testing.T) {
	for _, mode := range []string{"valid", "changed immutable lineage", "binding missing", "duplicate claim", "deleted parent"} {
		t.Run(mode, func(t *testing.T) {
			f := c2Fixture(t, true)
			l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: .25}, false)
			root, child := c2Pools(t, f, &l)
			switch mode {
			case "changed immutable lineage":
				c2Exec(t, f, `UPDATE pool_lineage SET data=json_set(data,'$.parent','other') WHERE pool=?`, child)
			case "binding missing":
				c2Exec(t, f, `DELETE FROM pool_claim_bindings`)
			case "duplicate claim":
				l.Request.Pools = append(l.Request.Pools, l.Request.Pools[0])
				c2Update(t, f, l)
			case "deleted parent":
				c2Exec(t, f, `UPDATE pools SET data=json_set(data,'$.state','deleted') WHERE id=?`, root)
			}
			p, e := f.m.observeProviderPairInventory(t.Context())
			if mode != "valid" {
				if e == nil || p.state != "unknown" {
					t.Fatal(p, e)
				}
				return
			}
			if e != nil || p.state != "covered" || p.physical.cpuMillis != 1250 || p.pools[child] != 3 || p.pools[root] != 0 {
				t.Fatal(p, e)
			}
			for _, id := range f.budgets[0].Path {
				if p.budgets[id].pools[root] != 3 || p.budgets[id].pools[child] != 3 {
					t.Fatal("pool ancestry omitted or added to physical", p)
				}
			}
		})
	}
}
func TestProviderPairC2BoundsPrecedeBodyAllocation(t *testing.T) {
	for _, mode := range []string{"row sentinel", "lease bytes", "sidecar bytes", "aggregate bytes", "partial optional group", "BLOB marker"} {
		t.Run(mode, func(t *testing.T) {
			f := c2Fixture(t, false)
			switch mode {
			case "row sentinel", "aggregate bytes":
				tx, e := f.m.db.Begin()
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				n := 4097
				size := 2
				if mode == "aggregate bytes" {
					n = 513
					size = 65536
				}
				for i := 0; i < n; i++ {
					if _, e = tx.Exec(`INSERT INTO leases VALUES(?,NULL,printf('%*s',?,''),NULL)`, fmt.Sprintf("%032x", i+1), size); e != nil {
						t.Fatal(e)
					}
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
			case "lease bytes":
				c2Exec(t, f, `INSERT INTO leases VALUES(?,NULL,printf('%65537s',''),NULL)`, strings.Repeat("1", 32))
			case "sidecar bytes":
				l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: 1}, false)
				c2Exec(t, f, `UPDATE lease_budget_bindings SET data=printf('%16385s','') WHERE lease=?`, l.ID)
			case "partial optional group":
				c2Exec(t, f, providerpreparation.TableDDL)
			case "BLOB marker":
				c2Exec(t, f, `INSERT INTO metadata VALUES(?,?)`, providerpreparation.SchemaKey, []byte("1"))
			}
			p := c2Unknown(t, f)
			if p.ordinaryRows != 0 {
				t.Fatal("loaded body before header/namespace refusal", p)
			}
		})
	}
}
func TestProviderPairC2CPUAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
	}{{"0", 0}, {"0.0001", 1}, {"1e-324", 1}, {"0.10000000000000001", 101}, {"1.25", 1250}} {
		t.Run(tc.raw, func(t *testing.T) {
			f := c2Fixture(t, false)
			l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: 1}, false)
			raw := bytes.Replace(c1JSON(t, l), []byte(`"cpu":1`), []byte(`"cpu":`+tc.raw), 1)
			_, e := f.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
			if e != nil {
				t.Fatal(e)
			}
			p, e := f.m.observeProviderPairInventory(t.Context())
			if e != nil || p.state != "covered" || p.physical.cpuMillis != tc.want {
				t.Fatal(p, e)
			}
		})
	}
	for _, raw := range []string{"-1", "1e401", "1e100000", "1e308"} {
		t.Run("refuse/"+raw, func(t *testing.T) {
			if _, e := providerPairC2CPU([]byte(raw)); e == nil {
				t.Fatal("unsafe CPU accepted", raw)
			}
		})
	}
	t.Run("saturating physical and ancestor totals", func(t *testing.T) {
		f := c2Fixture(t, false)
		for _, id := range []string{strings.Repeat("1", 32), strings.Repeat("2", 32)} {
			c2Lease(t, f, id, "active", model.Resources{Memory: math.MaxInt64}, false)
		}
		p := c2Unknown(t, f)
		if p.physical.memoryBytes != math.MaxInt64 {
			t.Fatal(p)
		}
	})
}
func TestProviderPairC2SnapshotAndCancellation(t *testing.T) {
	t.Run("same snapshot covers index and ordinary rows", func(t *testing.T) {
		f := c2Fixture(t, true)
		_, e := f.m.db.Exec(`PRAGMA journal_mode=WAL`)
		if e != nil {
			t.Fatal(e)
		}
		var seq int
		var schema, path string
		if e = f.m.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &path); e != nil {
			t.Fatal(e)
		}
		writer, e := sql.Open("sqlite", path)
		if e != nil {
			t.Fatal(e)
		}
		defer writer.Close()
		tx, e := f.m.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		first, e := readProviderPairInventory(t.Context(), tx, f.m.host, f.m.owner)
		if e != nil {
			t.Fatal(e)
		}
		id := strings.Repeat("f", 32)
		if _, e = writer.Exec(`INSERT INTO leases VALUES(?,NULL,'{}',NULL)`, id); e != nil {
			t.Fatal(e)
		}
		second, e := readProviderPairInventory(t.Context(), tx, f.m.host, f.m.owner)
		if e != nil || second.ordinaryRows != first.ordinaryRows || second.physical != first.physical {
			t.Fatal("ordinary census escaped index snapshot", second, e)
		}
		if e = tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		c2Unknown(t, f)
	})
	t.Run("blocked original connection cancels and releases fence", func(t *testing.T) {
		f := c2Fixture(t, true)
		conn, e := f.m.db.Conn(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		var p providerPairC2Projection
		var err error
		before := f.m.db.Stats().WaitCount
		go func() { defer close(done); p, err = f.m.observeProviderPairInventory(ctx) }()
		deadline := time.Now().Add(time.Second)
		for f.m.db.Stats().WaitCount == before {
			if time.Now().After(deadline) {
				t.Fatal("reader did not wait for owned connection")
			}
			runtime.Gosched()
		}
		cancel()
		providerPairWait(t, done)
		if err == nil || p.state != "unknown" {
			t.Fatal(p, err)
		}
		if !f.m.mu.TryLock() {
			t.Fatal("census retained Manager fence")
		}
		f.m.mu.Unlock()
	})
}

func TestProviderPairC2SavedRunExactCPUConsistency(t *testing.T) {
	for _, tc := range []struct {
		name, leaseCPU, runCPU string
		covered                bool
		millis                 int64
	}{
		{"underflow differs from zero", "1e-400", "0", false, 0},
		{"matching decimal underflow", "1e-400", "1e-400", true, 1},
		{"distinct exact promise around one", "1.0000000000000000000001", "1", false, 0},
		{"distinct precise decimals round to same float", "0.10000000000000001", "0.1", false, 0},
		{"same ceil is not a binding proof", "0.100000000000000001", "0.100000000000000002", false, 0},
		{"equivalent decimal and exponent", "0.10", "1e-1", true, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := c2Fixture(t, false)
			l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: 1}, true)
			raw := bytes.Replace(c1JSON(t, l), []byte(`"cpu":1`), []byte(`"cpu":`+tc.leaseCPU), 1)
			run := bytes.Replace(c1JSON(t, RunRequest{Request: l.Request, Command: []string{"echo", "fixture"}, CWD: "/fixture"}), []byte(`"cpu":1`), []byte(`"cpu":`+tc.runCPU), 1)
			c2Exec(t, f, `UPDATE leases SET data=?,run=? WHERE id=?`, string(raw), string(run), l.ID)
			p, e := f.m.observeProviderPairInventory(t.Context())
			if tc.covered {
				if e != nil || p.state != "covered" || p.physical.cpuMillis != tc.millis {
					t.Fatal(p, e)
				}
			} else if e == nil || p.state != "unknown" || !strings.Contains(e.Error(), "exact saved-run CPU promise differs") {
				t.Fatal("CPU mismatch not causally refused", p, e)
			}
		})
	}
}
func TestProviderPairC2RecordedPolicyRevisionCannotRollBack(t *testing.T) {
	for _, paired := range []bool{false, true} {
		for _, advance := range []bool{false, true} {
			t.Run(fmt.Sprintf("paired=%v/advance=%v", paired, advance), func(t *testing.T) {
				f := c2Fixture(t, paired)
				if !paired {
					l := c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{CPU: .1}, false)
					l.BudgetPolicy = []model.BudgetPolicyRevision{{ID: f.budgets[0].Path[0], Revision: 1}, {ID: f.budgets[0].Path[1], Revision: 2}}
					c2Update(t, f, l)
				}
				revision := 1
				if advance {
					revision = 99
				}
				c2Exec(t, f, `UPDATE budgets SET data=json_set(data,'$.revision',?) WHERE id=?`, revision, f.budgets[0].Selected)
				p, e := f.m.observeProviderPairInventory(t.Context())
				if advance {
					if e != nil || p.state != "covered" {
						t.Fatal("legitimate revision advance refused", p, e)
					}
				} else if e == nil || p.state != "unknown" {
					t.Fatal("recorded revision accepted above current", p, e)
				}
			})
		}
	}
}
func TestProviderPairC2MissingArtifactKeepsCustodyDebt(t *testing.T) {
	for _, mode := range []string{"managed directory", "managed storage capability", "consumer capability", "never provisioned cancel with Started", "never provisioned reject with Started"} {
		t.Run(mode, func(t *testing.T) {
			f := c2Fixture(t, false)
			l := c2Lease(t, f, strings.Repeat("1", 32), "terminal", model.Resources{Disk: 32}, false)
			l.Request.Kind = "storage"
			l.Outcome = "cancelled"
			started := l.Created.Add(time.Second)
			l.Started = &started
			switch mode {
			case "managed directory":
				l.ManagedDir = "/fixture/original-storage"
			case "managed storage capability":
				l.Capabilities = []string{"managed-storage"}
			case "consumer capability":
				l.Capabilities = []string{"explicit-storage-consumers"}
			case "never provisioned reject with Started":
				l.Outcome = "rejected"
			}
			c2Update(t, f, l)
			p, e := f.m.observeProviderPairInventory(t.Context())
			if strings.HasPrefix(mode, "never provisioned") {
				if e != nil || p.state != "covered" || p.physical.diskBytes != 0 {
					t.Fatal("activation timestamp alone invented custody", p, e)
				}
			} else if e == nil || p.state != "unknown" || p.physical.diskBytes != 32 {
				t.Fatal("missing artifact lost custody debt", p, e)
			}
		})
	}
}
