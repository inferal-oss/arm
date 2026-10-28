package manager

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
)

func daemonSettings() daemonconfig.Settings {
	return daemonconfig.Settings{Resources: model.Resources{CPU: 2, Memory: 512}, Listen: "127.0.0.1:0"}
}
func daemonJournal(t *testing.T, m *Manager) []daemonConfigTransition {
	t.Helper()
	events, e := m.Events(0)
	if e != nil {
		t.Fatal(e)
	}
	var out []daemonConfigTransition
	for _, v := range events {
		if v.Type == daemonConfigEvent {
			var c daemonConfigTransition
			if e = decodeDaemonConfig(v.Data, &c); e != nil {
				t.Fatal(e)
			}
			out = append(out, c)
		}
	}
	return out
}

func TestDaemonConfigurationJournalInitialLegacyAndUnchangedStartup(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	s := daemonSettings()
	legacy, e := Open(home, model.Resources{CPU: 1})
	if e != nil {
		t.Fatal(e)
	}
	if e = legacy.Close(); e != nil {
		t.Fatal(e)
	}
	m, e := OpenConfigured(home, s)
	if e != nil {
		t.Fatal(e)
	}
	c := daemonJournal(t, m)
	if len(c) != 1 || c[0].Previous != nil || c[0].Operation != "initialize-observed-startup" || !reflect.DeepEqual(c[0].Settings, s) {
		t.Fatal(c)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	m, e = OpenConfigured(home, s)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if c = daemonJournal(t, m); len(c) != 1 {
		t.Fatal("unchanged restart appended a transition", c)
	}
}

func TestDaemonConfigurationLowerCeilingPreservesLeaseTermsAndDeficit(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	old := daemonSettings()
	m, e := OpenConfigured(home, old)
	if e != nil {
		t.Fatal(e)
	}
	memoryReading(m, 10000, 10000, 0)
	l, e := m.Acquire(model.Request{Key: "config-existing", Reason: "existing promise survives startup ceiling reduction", Resources: model.Resources{Memory: 256}, TTL: time.Hour}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	next := old
	next.Resources.Memory = 128
	next.Resources.CPU = 1
	m, e = OpenConfigured(home, next)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	memoryReading(m, 10000, 10000, 0)
	got, e := m.Inspect(l.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(l.Request, got.Request) || !reflect.DeepEqual(l.Clock, got.Clock) || !reflect.DeepEqual(l.Expires, got.Expires) || !reflect.DeepEqual(l.Budget, got.Budget) || !reflect.DeepEqual(l.BudgetPolicy, got.BudgetPolicy) || got.State != "active" {
		t.Fatal("changed existing grant", l, got)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.Memory != 256 || status.Memory.Deficit != 128 || status.Memory.Available != 0 {
		t.Fatal(status.Memory, e)
	}
	if _, e = m.Acquire(model.Request{Reason: "cannot spend existing promise twice", Resources: model.Resources{Memory: 1}, TTL: time.Minute}, nil); e == nil {
		t.Fatal("admitted into deficit")
	}
	c := daemonJournal(t, m)
	if len(c) != 2 || c[1].Revision != 2 || c[1].Previous == nil || !reflect.DeepEqual(*c[1].Previous, old) || !reflect.DeepEqual(c[1].Settings, next) {
		t.Fatal(c)
	}
}

func TestDaemonConfigurationFailedJournalDoesNotPublishCapacity(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	s := daemonSettings()
	m, e := OpenConfigured(home, s)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.db.Exec(`CREATE TRIGGER fixture_config_failure BEFORE INSERT ON events WHEN NEW.type='daemon.configuration_applied' BEGIN SELECT RAISE(ABORT,'fixture durable configuration failure'); END`); e != nil {
		t.Fatal(e)
	}
	next := s
	next.Resources.CPU = 1
	if e = m.applyStartupConfiguration(next); e == nil || m.capacity != s.Resources || len(daemonJournal(t, m)) != 1 {
		t.Fatal("partial applied configuration", m.capacity, e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	failed, e := OpenConfigured(home, next)
	if e == nil || failed != nil {
		t.Fatal("startup returned unjournaled manager", failed, e)
	}
	db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var count int
	if e = db.QueryRow(`SELECT COUNT(*) FROM events WHERE type=?`, daemonConfigEvent).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if _, e = db.Exec(`DROP TRIGGER fixture_config_failure`); e != nil {
		t.Fatal(e)
	}
}

func TestDaemonConfigurationMalformedLastEventCannotInventBaseline(t *testing.T) {
	for _, kind := range []string{"syntax", "unknown-schema", "missing-memory", "duplicate-nested", "oversize", "wrong-host", "null-previous", "zero-sequence", "sql-time", "different-time", "blob-data", "oversize-entity", "oversize-at"} {
		t.Run(kind, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			s := daemonSettings()
			m, e := OpenConfigured(home, s)
			if e != nil {
				t.Fatal(e)
			}
			c := daemonJournal(t, m)[0]
			b, _ := json.Marshal(c)
			raw := string(b)
			switch kind {
			case "syntax":
				raw = "{"
			case "unknown-schema":
				raw = strings.Replace(raw, `"schema":1`, `"schema":2`, 1)
			case "missing-memory":
				raw = strings.Replace(raw, `"memory_bytes":512,`, "", 1)
			case "duplicate-nested":
				raw = strings.Replace(raw, `"cpu":2`, `"cpu":2,"cpu":1`, 1)
			case "oversize":
				raw = strings.Repeat(" ", 8193)
			case "wrong-host":
				raw = strings.Replace(raw, c.Host, "wrong", 1)
			case "zero-sequence", "sql-time", "different-time", "blob-data", "oversize-entity", "oversize-at":
				// The payload remains well-formed; the SQL envelope is independently guarded.
			case "null-previous":
				raw = strings.Replace(raw, `"revision":1`, `"revision":2`, 1)
			}
			if _, e = m.db.Exec(`UPDATE events SET data=? WHERE type=?`, raw, daemonConfigEvent); e != nil {
				t.Fatal(e)
			}
			if kind == "zero-sequence" {
				_, e = m.db.Exec(`UPDATE events SET sequence=0 WHERE type=?`, daemonConfigEvent)
			}
			if kind == "sql-time" {
				_, e = m.db.Exec(`UPDATE events SET at='not-a-timestamp' WHERE type=?`, daemonConfigEvent)
			}
			if kind == "different-time" {
				_, e = m.db.Exec(`UPDATE events SET at='2000-01-01T00:00:00Z' WHERE type=?`, daemonConfigEvent)
			}
			if kind == "blob-data" {
				_, e = m.db.Exec(`UPDATE events SET data=CAST(data AS BLOB) WHERE type=?`, daemonConfigEvent)
			}
			if kind == "oversize-entity" {
				_, e = m.db.Exec(`UPDATE events SET entity=? WHERE type=?`, strings.Repeat("x", 257), daemonConfigEvent)
			}
			if kind == "oversize-at" {
				_, e = m.db.Exec(`UPDATE events SET at=? WHERE type=?`, strings.Repeat("x", 65), daemonConfigEvent)
			}
			if e != nil {
				t.Fatal(e)
			}
			m.Close()
			if got, e := OpenConfigured(home, s); e == nil || got != nil {
				t.Fatal("malformed last event bypassed", e)
			}
		})
	}
}

func TestDaemonConfigurationInvalidSettingsDoNotCreateHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	s := daemonSettings()
	s.Resources.CPU = 0
	if got, e := OpenConfigured(home, s); e == nil || got != nil {
		t.Fatal("invalid settings accepted")
	}
	if _, e := os.Lstat(home); !os.IsNotExist(e) {
		t.Fatal("created invalid home", e)
	}
}

func TestDaemonConfigurationBlobDiscriminatorCannotSkipLatest(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	s := daemonSettings()
	m, e := OpenConfigured(home, s)
	if e != nil {
		t.Fatal(e)
	}
	memoryReading(m, 10000, 10000, 0)
	l, e := m.Acquire(model.Request{Key: "config-blob-sentinel", Reason: "retain existing obligation during malformed startup", Resources: model.Resources{Memory: 64}, TTL: time.Hour}, nil)
	if e != nil {
		t.Fatal(e)
	}
	var before string
	if e = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	// Keep the earlier valid row, then append a byte-equal non-TEXT type. A
	// text-only WHERE would silently skip this newest malformed discriminator.
	if _, e = m.db.Exec(`INSERT INTO events(type,entity,at,data) SELECT CAST(type AS BLOB),entity,at,data FROM events WHERE type=? ORDER BY sequence DESC LIMIT 1`, daemonConfigEvent); e != nil {
		t.Fatal(e)
	}
	m.Close()
	next := s
	next.Resources.Memory = 1
	if got, e := OpenConfigured(home, next); e == nil || got != nil {
		t.Fatal("skipped malformed newest type", got, e)
	}
	db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var after string
	if e = db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&after); e != nil || before != after {
		t.Fatal("lease rewritten before failed configuration", e)
	}
	var count int
	if e = db.QueryRow(`SELECT COUNT(*) FROM events WHERE CAST(type AS BLOB)=CAST(? AS BLOB)`, daemonConfigEvent).Scan(&count); e != nil || count != 2 {
		t.Fatal("configuration evidence replaced", count, e)
	}
}
