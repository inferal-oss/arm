package manager

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/providerpreparation"
)

// These fixtures construct private originals and proposed schema only in
// disposable SQLite files. They do not supply admission or native join proof.
func c3Snapshot(t *testing.T, f *providerPairC1Fixture) providerPairCustodySnapshot {
	t.Helper()
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	tx, err := f.m.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	s := readProviderPairCustody(t.Context(), tx, f.m.host, f.m.owner)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return s
}

func c3Expected(t *testing.T, f *providerPairC1Fixture) providerPairDecoded {
	t.Helper()
	s := c3Snapshot(t, f)
	if s.problem != nil || len(s.pairs) != 1 {
		t.Fatal("expected strict fixture pair", s.problem)
	}
	return s.pairs[string(f.initial.Pair)]
}

func c3Events(t *testing.T, f *providerPairC1Fixture) {
	t.Helper()
	c2Exec(t, f, `CREATE TABLE events(type TEXT,entity TEXT,at TEXT,data TEXT)`)
}

func c3Stage(t *testing.T, f *providerPairC1Fixture, expected providerPairDecoded, cause providerPairStopCause) (providerPairMarkerStage, *sql.Tx, error) {
	t.Helper()
	tx, err := f.m.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	s, err := f.m.stageProviderPairRetentionLocked(t.Context(), tx, expected, cause, f.wires[0].Created.Add(time.Second))
	return s, tx, err
}

func c3MemberBytes(t *testing.T, f *providerPairC1Fixture) []string {
	t.Helper()
	rows, err := f.m.db.Query(`SELECT id,request_key,typeof(data),hex(data),typeof(run),hex(run) FROM leases UNION ALL SELECT lease,'budget',typeof(data),hex(data),'','' FROM lease_budget_bindings ORDER BY 1,2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var v [6]string
		if err := rows.Scan(&v[0], &v[1], &v[2], &v[3], &v[4], &v[5]); err != nil {
			t.Fatal(err)
		}
		result = append(result, strings.Join(v[:], ":"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func c3Scope(f *providerPairFixture, cancel context.CancelFunc) *providerPairLifecycleScope {
	entry := &providerPairCancellationEntry{original: f.original, operationKey: "original-operation", state: providerPairCancellationAdmitted, cancel: cancel}
	return &providerPairLifecycleScope{core: f.core, duties: map[*providerPairOriginal]*providerPairCancellationEntry{f.original: entry}}
}

func TestProviderPairC3CustodyAndTargets(t *testing.T) {
	f := c2Fixture(t, true)
	s := c3Snapshot(t, f)
	for _, target := range []providerPairLifecycleTarget{
		{leaseID: f.wires[0].ID}, {leaseID: f.wires[1].ID},
		{requestKey: f.wires[0].Request.Key}, {requestKey: f.wires[1].Request.Key},
		{requestKey: string(f.initial.OperationKey)},
	} {
		selected, err := s.selected(providerPairLifecycleSelection{targets: []providerPairLifecycleTarget{target, target}})
		if err != nil || len(selected) != 1 || selected[string(f.initial.Pair)].initial.Pair != f.initial.Pair {
			t.Fatal("member/key did not couple to the same retained pair", target, err)
		}
	}
	selected, err := s.selected(providerPairLifecycleSelection{all: true})
	if err != nil || len(selected) != 1 || s.projection.physical != (providerPairQuantity{1000, 64 << 20, 64 << 20}) {
		t.Fatal("pair charge changed during targeting", s.projection, err)
	}
	delete(selected, string(f.initial.Pair))
	if len(s.pairs) != 1 {
		t.Fatal("returned selection aliased custody map")
	}
	for _, invalid := range []providerPairLifecycleSelection{{}, {all: true, targets: []providerPairLifecycleTarget{{leaseID: f.wires[0].ID}}}, {targets: []providerPairLifecycleTarget{{leaseID: f.wires[0].ID, requestKey: "mixed"}}}, {targets: make([]providerPairLifecycleTarget, providerPairC2Rows+1)}} {
		if _, err := s.selected(invalid); err == nil {
			t.Fatal("unbounded/ambiguous selection accepted")
		}
	}
	t.Run("truly absent namespace retains ordinary checks", func(t *testing.T) {
		f := c2Fixture(t, false)
		c2Lease(t, f, strings.Repeat("1", 32), "active", model.Resources{Memory: 5}, false)
		s := c3Snapshot(t, f)
		pairs, err := s.selected(providerPairLifecycleSelection{all: true})
		if err != nil || len(pairs) != 0 || s.projection.physical.memoryBytes != 5 {
			t.Fatal("ordinary data coverage changed", s.problem, err)
		}
	})
	t.Run("operation key cannot alias another pair role", func(t *testing.T) {
		other := c2Fixture(t, true)
		other.initial.Pair = providerpreparation.Hex32(strings.Repeat("f", 32))
		other.initial.OperationKey = providerpreparation.Key(f.wires[0].Request.Key)
		other.progress.Pair = other.initial.Pair
		for i := range other.wires {
			other.wires[i].ID = strings.Repeat([]string{"1", "2"}[i], 32)
			other.wires[i].Ref.Pair = other.initial.Pair
			other.wires[i].Request.Key = []string{"other:worker", "other:temp"}[i]
		}
		other.rebind(t)
		c2Exec(t, f, `INSERT INTO provider_preparations VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, string(other.initial.Pair), string(other.initial.OperationKey), f.m.host, f.m.owner, other.initial.Profile, 1, "preparing", other.wires[0].ID, other.wires[1].ID, 1000, 64<<20, 64<<20, string(other.initialRaw), c1SHA(other.initialRaw), string(other.progressRaw), 1, providerpreparation.RecordReserveBytes)
		for _, member := range other.initial.Members {
			c2Exec(t, f, `INSERT INTO provider_preparation_members VALUES(?,?,?,?)`, string(member.Row.ID), string(other.initial.Pair), string(member.Role), 1)
			c2Exec(t, f, `INSERT INTO leases VALUES(?,?,?,NULL)`, string(member.Row.ID), string(member.Row.RequestKey), string(member.LeaseBytes))
			c2Exec(t, f, `INSERT INTO lease_budget_bindings VALUES(?,?)`, string(member.Row.ID), string(member.BudgetBytes))
		}
		s := c3Snapshot(t, f)
		if _, err := s.selected(providerPairLifecycleSelection{targets: []providerPairLifecycleTarget{{requestKey: f.wires[0].Request.Key}}}); err == nil || s.problem == nil {
			t.Fatal("ambiguous key selected a pair")
		}
	})
}

func TestProviderPairC3UnknownNeverOrdinaryPermission(t *testing.T) {
	for _, damage := range []string{"missing member", "orphan", "BLOB marker", "partial namespace", "ordinary debt", "foreign owner", "named storage format"} {
		t.Run(damage, func(t *testing.T) {
			f := c2Fixture(t, true)
			switch damage {
			case "missing member":
				c2Exec(t, f, `DELETE FROM leases WHERE id=?`, f.wires[1].ID)
			case "orphan":
				c2Exec(t, f, `DELETE FROM provider_preparation_members`)
				c2Exec(t, f, `DELETE FROM provider_preparations`)
			case "BLOB marker":
				c2Exec(t, f, `UPDATE metadata SET value=CAST(value AS BLOB) WHERE key=?`, providerpreparation.SchemaKey)
			case "partial namespace":
				c2Exec(t, f, `DROP INDEX provider_preparations_operation_key`)
			case "ordinary debt":
				c2Exec(t, f, `INSERT INTO leases VALUES(?,NULL,'{}',NULL)`, strings.Repeat("1", 32))
			case "named storage format":
				c2Exec(t, f, `INSERT INTO metadata VALUES('workspace_format','3')`)
			case "foreign owner":
				f.m.owner = "43"
			}
			s := c3Snapshot(t, f)
			selected, err := s.selected(providerPairLifecycleSelection{targets: []providerPairLifecycleTarget{{leaseID: strings.Repeat("f", 32)}}})
			if s.problem == nil || err == nil || selected != nil {
				t.Fatal("unknown custody permitted ordinary fallback", s.problem, err)
			}
		})
	}
}

func TestProviderPairC3CancellationBeforeReadFailure(t *testing.T) {
	for _, damage := range []string{"missing schema", "missing member", "nil database", "closed database", "core debt"} {
		t.Run(damage, func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			calls := 0
			scope := c3Scope(f, func() { calls++; f.cancel() })
			switch damage {
			case "missing member":
				if _, err := f.m.db.Exec(`DELETE FROM leases`); err != nil {
					t.Fatal(err)
				}
			case "nil database":
				f.m.db = nil
			case "closed database":
				if err := f.m.db.Close(); err != nil {
					t.Fatal(err)
				}
			case "core debt":
				f.core.fault = errors.New("retained bookkeeping fault")
			}
			s, err := f.m.cancelProviderPairCustody(t.Context(), scope, providerPairLifecycleSelection{all: true})
			if err == nil || s.problem == nil || calls != 1 || f.original.lifetime.Err() == nil {
				t.Fatal("read/debt failure erased original cancellation", calls, err)
			}
			if _, err := s.selected(providerPairLifecycleSelection{all: true}); err == nil {
				t.Fatal("failed read granted resource effects")
			}
			_, _ = f.m.cancelProviderPairCustody(t.Context(), scope, providerPairLifecycleSelection{all: true})
			if calls != 1 {
				t.Fatal("repeated intent cancelled more than once", calls)
			}
		})
	}
}

func TestProviderPairC3CancellationEpochAndPendingACK(t *testing.T) {
	for _, state := range []providerPairCancellationState{providerPairCancellationPending, providerPairCancellationUnknownACK, providerPairCancellationAdmitted} {
		t.Run(map[providerPairCancellationState]string{providerPairCancellationPending: "pending", providerPairCancellationUnknownACK: "unknown ACK", providerPairCancellationAdmitted: "admitted"}[state], func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			calls := 0
			scope := c3Scope(f, func() { calls++ })
			scope.duties[f.original].state = state
			if state != providerPairCancellationAdmitted {
				delete(f.core.originals, f.original.id)
			}
			f.m.mu.Lock()
			b, err := f.m.captureProviderPairCancellationsLocked(scope, providerPairLifecycleSelection{all: true})
			f.m.mu.Unlock()
			if err != nil || b.dispatch() != nil || calls != 1 {
				t.Fatal("current original duty was suppressed", calls, err)
			}
			if state != providerPairCancellationAdmitted && len(f.core.originals) != 0 {
				t.Fatal("pending duty published eligibility")
			}
		})
	}
	for _, damage := range []string{"epoch", "Manager", "replaced original", "nil callback", "unknown state"} {
		t.Run(damage, func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			calls := 0
			scope := c3Scope(f, func() { calls++ })
			switch damage {
			case "epoch":
				f.original.epoch = providerpreparation.Hex32(strings.Repeat("f", 32))
			case "Manager":
				scope.core.manager = &Manager{}
			case "replaced original":
				h := *f.original
				f.core.originals[h.id] = &h
			case "nil callback":
				scope.duties[f.original].cancel = nil
			case "unknown state":
				scope.duties[f.original].state = 99
			}
			f.m.mu.Lock()
			b, err := f.m.captureProviderPairCancellationsLocked(scope, providerPairLifecycleSelection{all: true})
			f.m.mu.Unlock()
			if err == nil || b.dispatch() != nil || calls != 0 {
				t.Fatal("foreign/reconstructed cancellation adopted", calls, err)
			}
		})
	}
}

func TestProviderPairC3CancellationAfterUnlock(t *testing.T) {
	f := providerPairOriginalFixture(t)
	calls := 0
	unlocked := false
	scope := c3Scope(f, func() {
		calls++
		unlocked = f.m.mu.TryLock()
		if unlocked {
			f.m.mu.Unlock()
		}
	})
	_, _ = f.m.cancelProviderPairCustody(t.Context(), scope, providerPairLifecycleSelection{all: true})
	if calls != 1 || !unlocked {
		t.Fatal("original callback ran under Manager fence", calls, unlocked)
	}
	for _, mode := range []string{"worker ID", "temp ID", "worker key", "temp key", "operation", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			calls := 0
			scope := c3Scope(f, func() { calls++ })
			target := providerPairLifecycleTarget{}
			switch mode {
			case "worker ID":
				target.leaseID = f.original.rows[0].id
			case "temp ID":
				target.leaseID = f.original.rows[1].id
			case "worker key":
				target.requestKey = f.original.rows[0].requestKey
			case "temp key":
				target.requestKey = f.original.rows[1].requestKey
			case "operation":
				target.requestKey = "original-operation"
			case "unrelated":
				target.requestKey = "unrelated-operation"
			}
			_, _ = f.m.cancelProviderPairCustody(t.Context(), scope, providerPairLifecycleSelection{targets: []providerPairLifecycleTarget{target, target}})
			want := 1
			if mode == "unrelated" {
				want = 0
			}
			if calls != want {
				t.Fatal("accepted intent selected a different original", calls, want)
			}
		})
	}
	t.Run("capture survives replacement without lookup", func(t *testing.T) {
		f := providerPairOriginalFixture(t)
		old, replacement := 0, 0
		scope := c3Scope(f, func() { old++ })
		f.m.mu.Lock()
		b, err := f.m.captureProviderPairCancellationsLocked(scope, providerPairLifecycleSelection{all: true})
		scope.duties[f.original].cancel = func() { replacement++ }
		delete(scope.duties, f.original)
		delete(f.core.originals, f.original.id)
		f.m.mu.Unlock()
		if err != nil || b.dispatch() != nil || b.dispatch() != nil || old != 1 || replacement != 0 {
			t.Fatal("dispatch reconstructed a callback", old, replacement, err)
		}
	})
	t.Run("one panic cannot starve another original", func(t *testing.T) {
		f := providerPairOriginalFixture(t)
		calls := 0
		scope := c3Scope(f, func() { panic("fixture cancellation defect") })
		h := *f.original
		h.id = providerpreparation.Hex32(strings.Repeat("f", 32))
		f.core.originals[h.id] = &h
		scope.duties[&h] = &providerPairCancellationEntry{original: &h, operationKey: "another-operation", state: providerPairCancellationAdmitted, cancel: func() { calls++ }}
		f.core.fault = errors.New("bookkeeping failed")
		f.m.mu.Lock()
		b, err := f.m.captureProviderPairCancellationsLocked(scope, providerPairLifecycleSelection{all: true})
		f.m.mu.Unlock()
		if err == nil || b.dispatch() == nil || calls != 1 {
			t.Fatal("one failed original starved another", calls, err)
		}
		if b.dispatch() != nil || calls != 1 {
			t.Fatal("once identities changed", calls)
		}
	})
}

func TestProviderPairC3MarkerCASAllPredecessors(t *testing.T) {
	for _, damage := range []string{"operation key", "owner", "generation", "quantity", "digest", "same revision progress", "same revision initial", "member key", "member run", "member body", "sidecar", "membership", "raw BLOB"} {
		t.Run(damage, func(t *testing.T) {
			f := c2Fixture(t, true)
			c3Events(t, f)
			expected := c3Expected(t, f)
			c2Exec(t, f, `PRAGMA ignore_check_constraints=ON`)
			switch damage {
			case "operation key":
				c2Exec(t, f, `UPDATE provider_preparations SET operation_key='replacement'`)
			case "owner":
				c2Exec(t, f, `UPDATE provider_preparations SET owner='43'`)
			case "generation":
				c2Exec(t, f, `UPDATE provider_preparations SET generation=2`)
			case "quantity":
				c2Exec(t, f, `UPDATE provider_preparations SET cpu_millis=2000`)
			case "digest":
				c2Exec(t, f, `UPDATE provider_preparations SET initial_sha256=?`, strings.Repeat("0", 64))
			case "same revision progress":
				p := f.progress
				p.At = providerpreparation.Stamp(f.wires[0].Created.Add(time.Second).Format(time.RFC3339Nano))
				c2Exec(t, f, `UPDATE provider_preparations SET progress_data=?`, string(c1JSON(t, p)))
			case "same revision initial":
				f.initial.OpenEpoch = providerpreparation.Hex32(strings.Repeat("f", 32))
				raw := c1JSON(t, f.initial)
				c2Exec(t, f, `UPDATE provider_preparations SET initial_data=?,initial_sha256=?`, string(raw), c1SHA(raw))
			case "member key":
				c2Exec(t, f, `UPDATE leases SET request_key='replacement' WHERE id=?`, f.wires[0].ID)
			case "member run":
				c2Exec(t, f, `UPDATE leases SET run='{}' WHERE id=?`, f.wires[0].ID)
			case "member body":
				c2Exec(t, f, `UPDATE leases SET data=data||' ' WHERE id=?`, f.wires[0].ID)
			case "sidecar":
				c2Exec(t, f, `UPDATE lease_budget_bindings SET data=data||' ' WHERE lease=?`, f.wires[1].ID)
			case "membership":
				c2Exec(t, f, `DELETE FROM provider_preparation_members WHERE lease=?`, f.wires[1].ID)
			case "raw BLOB":
				c2Exec(t, f, `UPDATE provider_preparations SET progress_data=CAST(progress_data AS BLOB)`)
			}
			before := c3MemberBytes(t, f)
			s, tx, err := c3Stage(t, f, expected, providerPairCancel)
			if err == nil || s.changed || tx.Commit() == nil {
				t.Fatal("changed predecessor adopted", err)
			}
			if !reflect.DeepEqual(before, c3MemberBytes(t, f)) {
				t.Fatal("refusal changed a member")
			}
			var n int
			if err := f.m.db.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil || n != 0 {
				t.Fatal("refusal emitted event", n, err)
			}
		})
	}
	t.Run("actual zero-row CAS aborts entire transaction", func(t *testing.T) {
		f := c2Fixture(t, true)
		c3Events(t, f)
		expected := c3Expected(t, f)
		tx, err := f.m.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		p := f.progress
		p.At = providerpreparation.Stamp(f.wires[0].Created.Add(time.Second).Format(time.RFC3339Nano))
		var injected error
		// Structural clock seam injects a changed predecessor after strict
		// reads, exercising the actual SQLite CAS zero-row branch. This is
		// not a physical native-clock or concurrent-writer claim.
		f.m.leaseClockRead = func() (leaseclock.Sample, error) {
			_, injected = tx.ExecContext(t.Context(), `UPDATE provider_preparations SET progress_data=?`, string(c1JSON(t, p)))
			return leaseclock.Sample{Boot: f.wires[0].Clock.Boot, Tick: f.wires[0].Clock.End, Wall: f.wires[0].Created}, injected
		}
		f.m.mu.Lock()
		s, err := f.m.stageProviderPairRetentionLocked(t.Context(), tx, expected, providerPairExpired, f.wires[0].Created.Add(time.Second))
		f.m.mu.Unlock()
		if injected != nil || err == nil || !strings.Contains(err.Error(), "exactly one pair") || s.changed || tx.Commit() == nil {
			t.Fatal("zero-row CAS was not refused", injected, err)
		}
		if after := c3Expected(t, f); string(after.rawProgress) != string(expected.rawProgress) {
			t.Fatal("CAS refusal retained injected predecessor")
		}
	})
}

func TestProviderPairC3MarkerEventsAndIdempotence(t *testing.T) {
	f := c2Fixture(t, true)
	c3Events(t, f)
	before := c3MemberBytes(t, f)
	expected := c3Expected(t, f)
	for i, cause := range []providerPairStopCause{providerPairCancel, providerPairClose, providerPairPersistenceUnknown, providerPairBindingUnknown, providerPairCancel} {
		s, tx, err := c3Stage(t, f, expected, cause)
		if err != nil || s.changed != (i == 0 || i == 2) {
			t.Fatal("nonmonotone retaining state", i, s, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		// Fixture data follows only this positively acknowledged staged
		// successor. A fresh census cannot make unknown custody covered, and
		// no eligible original is published by these marker-only primitives.
		expected.progress = s.progress
		expected.rawProgress = append([]byte(nil), s.raw...)
		if !reflect.DeepEqual(before, c3MemberBytes(t, f)) {
			t.Fatal("marker rewrote protected members")
		}
	}
	var count, maxBytes int
	var raw, kind, initial, digest string
	if err := f.m.db.QueryRow(`SELECT count(*),max(length(CAST(data AS BLOB))) FROM events WHERE type='provider.preparation.retained' AND entity=?`, string(f.initial.Pair)).Scan(&count, &maxBytes); err != nil || count != 2 || maxBytes > providerpreparation.MaxEventBytes {
		t.Fatal(count, maxBytes, err)
	}
	if err := f.m.db.QueryRow(`SELECT progress_data,typeof(progress_data),initial_data,initial_sha256 FROM provider_preparations`).Scan(&raw, &kind, &initial, &digest); err != nil || kind != "text" || initial != string(f.initialRaw) || digest != c1SHA(f.initialRaw) {
		t.Fatal("TEXT CAS or initial changed", err)
	}
	snapshot := c3Snapshot(t, f)
	actual := snapshot.pairs[string(f.initial.Pair)]
	if actual.progress.Revision != "3" || actual.progress.Phase != providerpreparation.Unknown || raw != string(expected.rawProgress) || raw != string(actual.rawProgress) {
		t.Fatal("retaining progression differs")
	}
	if _, err := snapshot.selected(providerPairLifecycleSelection{all: true}); err == nil || snapshot.problem == nil || snapshot.projection.state != "unknown" || snapshot.projection.physical != (providerPairQuantity{1000, 64 << 20, 64 << 20}) {
		t.Fatal("unknown marker lost its promises or granted ordinary fallback", snapshot.projection, err)
	}
}

func TestProviderPairC3MarkerRefusalRetainsMembers(t *testing.T) {
	for _, failure := range []string{"event abort", "event ignored", "event mutation trigger", "temporary shadow", "missing events", "read only", "caller rollback", "commit refusal"} {
		t.Run(failure, func(t *testing.T) {
			f := c2Fixture(t, true)
			c3Events(t, f)
			expected := c3Expected(t, f)
			before := c3MemberBytes(t, f)
			switch failure {
			case "event abort":
				c2Exec(t, f, `DROP TABLE events`)
				c2Exec(t, f, `CREATE TABLE events(type TEXT CHECK(type<>'provider.preparation.retained'),entity TEXT,at TEXT,data TEXT)`)
			case "event ignored":
				c2Exec(t, f, `DROP TABLE events`)
				c2Exec(t, f, `CREATE TABLE events(type TEXT UNIQUE ON CONFLICT IGNORE,entity TEXT,at TEXT,data TEXT)`)
				c2Exec(t, f, `INSERT INTO events VALUES('provider.preparation.retained','prior','prior','{}')`)
			case "event mutation trigger":
				c2Exec(t, f, `CREATE TRIGGER mutate_member_from_event AFTER INSERT ON events BEGIN UPDATE leases SET data='{}'; END`)
			case "temporary shadow":
				c2Exec(t, f, `CREATE TEMP TABLE events(type TEXT,entity TEXT,at TEXT,data TEXT)`)
			case "missing events":
				c2Exec(t, f, `DROP TABLE events`)
			case "read only":
				c2Exec(t, f, `PRAGMA query_only=ON`)
			case "commit refusal":
				c2Exec(t, f, `PRAGMA foreign_keys=ON`)
				c2Exec(t, f, `CREATE TABLE required_event_parent(id TEXT PRIMARY KEY)`)
				c2Exec(t, f, `DROP TABLE events`)
				c2Exec(t, f, `CREATE TABLE events(type TEXT,entity TEXT REFERENCES required_event_parent(id) DEFERRABLE INITIALLY DEFERRED,at TEXT,data TEXT)`)
			}
			s, tx, err := c3Stage(t, f, expected, providerPairCancel)
			if failure == "caller rollback" || failure == "commit refusal" {
				if err != nil || !s.changed {
					t.Fatal("marker did not stage", err)
				}
				if failure == "commit refusal" {
					if tx.Commit() == nil {
						t.Fatal("deferred commit refusal not exercised")
					}
					// The database/sql transaction is terminal after Commit returns.
					// Clean up a driver transaction if SQLite retained it on the
					// disposable connection. Production cannot infer a rollback or
					// positive ACK from this failure; the stage remains only data.
					_, _ = f.m.db.Exec(`ROLLBACK`)
				} else {
					_ = tx.Rollback()
				}
			} else if err == nil || s.changed || tx.Commit() == nil {
				t.Fatal("failure left marker commit-able", err)
			}
			if !reflect.DeepEqual(before, c3MemberBytes(t, f)) {
				t.Fatal("failure altered member promises")
			}
			var progress string
			if err := f.m.db.QueryRow(`SELECT progress_data FROM provider_preparations`).Scan(&progress); err != nil || progress != string(expected.rawProgress) {
				t.Fatal("refused transaction published marker", err)
			}
		})
	}
	t.Run("global debt permits retention only", func(t *testing.T) {
		f := c2Fixture(t, true)
		c3Events(t, f)
		expected := c3Expected(t, f)
		c2Exec(t, f, `INSERT INTO leases VALUES(?,NULL,'{}',NULL)`, strings.Repeat("1", 32))
		s, tx, err := c3Stage(t, f, expected, providerPairCancel)
		if err != nil || !s.changed {
			t.Fatal("global debt suppressed strictly retaining marker", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if _, err := c3Snapshot(t, f).selected(providerPairLifecycleSelection{all: true}); err == nil {
			t.Fatal("marker converted global debt to permission")
		}
	})
}

func TestProviderPairC3PositiveExpiryNeedsFreshClock(t *testing.T) {
	for _, mode := range []string{"live", "expired", "different boot", "regressed", "read error", "damaged row before sample"} {
		t.Run(mode, func(t *testing.T) {
			f := c2Fixture(t, true)
			c3Events(t, f)
			expected := c3Expected(t, f)
			calls := 0
			sample := leaseclock.Sample{Boot: f.wires[0].Clock.Boot, Tick: f.wires[0].Clock.Anchor, Wall: f.wires[0].Created}
			switch mode {
			case "expired":
				sample.Tick = f.wires[0].Clock.End
			case "different boot":
				sample.Boot = "another-boot"
			case "regressed":
				f.m.leaseClockLast = sample
				sample.Tick--
			case "damaged row before sample":
				c2Exec(t, f, `UPDATE leases SET data='{}' WHERE id=?`, f.wires[0].ID)
			}
			f.m.leaseClockRead = func() (leaseclock.Sample, error) {
				calls++
				if mode == "read error" {
					return sample, errors.New("fixture native refusal")
				}
				return sample, nil
			}
			s, tx, err := c3Stage(t, f, expected, providerPairExpired)
			if mode == "expired" {
				if err != nil || !s.changed || s.progress.Reason != "expired" {
					t.Fatal("proven expiry did not retain", err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || s.changed || tx.Commit() == nil {
				t.Fatal("positive expiry invented", mode, err)
			}
			want := 1
			if mode == "damaged row before sample" {
				want = 0
			}
			if calls != want {
				t.Fatal("clock sample did not follow raw validation", calls, want)
			}
		})
	}
	t.Run("blocked SQL still dispatches captured original on context cut", func(t *testing.T) {
		f := providerPairOriginalFixture(t)
		conn, err := f.m.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		calls := 0
		scope := c3Scope(f, func() { calls++ })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		var resultErr error
		before := f.m.db.Stats().WaitCount
		go func() {
			defer close(done)
			_, resultErr = f.m.cancelProviderPairCustody(ctx, scope, providerPairLifecycleSelection{all: true})
		}()
		deadline := time.Now().Add(time.Second)
		for f.m.db.Stats().WaitCount == before {
			if time.Now().After(deadline) {
				t.Fatal("reader never waited for connection")
			}
			runtime.Gosched()
		}
		cancel()
		providerPairWait(t, done)
		if resultErr == nil || calls != 1 {
			t.Fatal("context cut erased captured cancellation", calls, resultErr)
		}
		if !f.m.mu.TryLock() {
			t.Fatal("context cut retained Manager fence")
		}
		f.m.mu.Unlock()
	})
}
