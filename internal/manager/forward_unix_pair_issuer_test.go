package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/database"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func newForwardUnixPairFixture(t *testing.T) (*Manager, *ForwardListener, *ForwardUnixChild, chan struct{}, chan struct{}) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return newForwardUnixPairFixtureAt(t, root)
}

func newForwardUnixPairFixtureAt(t *testing.T, root string) (*Manager, *ForwardListener, *ForwardUnixChild, chan struct{}, chan struct{}) {
	t.Helper()
	var err error
	home := filepath.Join(root, "arm")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 16 << 20, Disk: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	joined, childJoined := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-childJoined:
		default:
			close(childJoined)
		}
		select {
		case <-joined:
		default:
			close(joined)
		}
		m.mu.Lock()
		closing := m.closing
		m.mu.Unlock()
		if !closing {
			_ = m.Close()
		}
	})
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	h, err := m.AcquireForwardListener(context.Background(), strings.Repeat("d", 32), "original pre-bind pair fixture", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.IssueUnixChild(context.Background(), childJoined)
	if err != nil {
		t.Fatal(err)
	}
	return m, h, c, joined, childJoined
}

func forwardUnixPairSettings() daemonconfig.Settings {
	return daemonconfig.Settings{Resources: model.Resources{CPU: 1, Memory: 16 << 20, Disk: 8 << 20}, Listen: "127.0.0.1:0"}
}

type forwardUnixStartupSnapshot struct {
	pair, source string
	events       []struct {
		sequence         int64
		kind, entity, at string
		data             sql.NullString
	}
}

func snapshotForwardUnixStartup(t *testing.T, db *sql.DB, sourceID string) forwardUnixStartupSnapshot {
	t.Helper()
	var snap forwardUnixStartupSnapshot
	if err := db.QueryRow(`SELECT data FROM forward_unix_pairs WHERE source_id=?`, sourceID).Scan(&snap.pair); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT data FROM leases WHERE id=?`, sourceID).Scan(&snap.source); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT sequence,type,entity,at,data FROM events ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var event struct {
			sequence         int64
			kind, entity, at string
			data             sql.NullString
		}
		if err = rows.Scan(&event.sequence, &event.kind, &event.entity, &event.at, &event.data); err != nil {
			t.Fatal(err)
		}
		snap.events = append(snap.events, event)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestForwardUnixPairPositiveDiskIssueAndNoBindAbort(t *testing.T) {
	m, h, c, joined, childJoined := newForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil || p == nil || p.data.DiskBytes != forwardUnixDiskPromise || p.data.Phase != "planned" {
		t.Fatalf("positive Disk pair unavailable: %+v %v", p, err)
	}
	if _, err = c.IssueStoragePair(context.Background()); err == nil {
		t.Fatal("original child issued a second pair")
	}
	source, err := m.Inspect(h.id)
	if err != nil || source.Request.StorageLease != p.storage {
		t.Fatalf("source attachment not acknowledged: %+v %v", source, err)
	}
	storage, err := m.Inspect(p.storage)
	if err != nil || storage.Request.Resources.Disk != forwardUnixDiskPromise || storage.Artifact == nil {
		t.Fatalf("dedicated storage not charged: %+v %v", storage, err)
	}
	if _, err = os.Lstat(filepath.Join(storage.ManagedDir, p.data.SocketName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-bind issuer created a socket pathname: %v", err)
	}
	if _, err = m.End(p.storage, "cancelled"); err == nil {
		t.Fatal("unfinished planned pair let storage cleanup begin")
	}
	if err = p.AbortBeforeBind(context.Background()); err != nil {
		t.Fatalf("original no-bind abort failed: %v", err)
	}
	if err = p.AbortBeforeBind(context.Background()); err != nil {
		t.Fatalf("acknowledged no-bind abort was not idempotent: %v", err)
	}
	source, err = m.Inspect(h.id)
	if err != nil || source.Request.StorageLease != "" {
		t.Fatalf("aborted source retained storage attachment: %+v %v", source, err)
	}
	var pairs int
	if err = m.db.QueryRow(`SELECT count(*) FROM forward_unix_pairs WHERE phase='terminal'`).Scan(&pairs); err != nil || pairs != 1 {
		t.Fatalf("no-bind terminal tombstone missing: %d %v", pairs, err)
	}
	close(childJoined)
	if err = c.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	offline, err := database.Offline(context.Background(), m.home)
	if err != nil {
		t.Fatal(err)
	}
	proof, proofErr := database.ProveDatabaseSettled(context.Background(), offline, m.owner)
	if err = offline.Close(); err != nil {
		t.Fatal(err)
	}
	if proofErr != nil || !proof.Complete || !proof.Settled {
		t.Fatalf("settled no-bind tombstone was not proved: %+v %v", proof, proofErr)
	}
}

func TestForwardUnixPairJoinedChildRetainsPairUntilNoBindAbort(t *testing.T) {
	m, h, c, joined, childJoined := newForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	close(childJoined)
	if err = c.FinishAfterJoin(); errorCode(err) != "forward_unix_pair_unavailable" {
		t.Fatalf("joined child released an unfinished pair: %v", err)
	}
	if c.released || h.unixChild != c {
		t.Fatal("unfinished pair lost original child custody")
	}
	if err = p.AbortBeforeBind(context.Background()); err != nil {
		t.Fatalf("joined original child could not prove no bind: %v", err)
	}
	if err = c.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestForwardUnixPairStartupAbortsOnlyExactPlannedNoBind(t *testing.T) {
	m, h, c, joined, childJoined := newForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	home := m.home
	owner := m.owner
	close(childJoined)
	close(joined)
	_ = m.Close() // The pair guard intentionally retains source/storage charge.
	if _, err = Open(home, model.Resources{CPU: 1, Memory: 16 << 20, Disk: 8 << 20}); errorCode(err) != "forward_unix_pair_unavailable" {
		t.Fatalf("unfenced Manager.Open recovered a planned pair: %v", err)
	}
	lock, err := local.Lock(home, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	m2, err := OpenConfiguredWithUnixRecovery(home, forwardUnixPairSettings(), lock)
	if err != nil {
		t.Fatalf("exclusive fenced no-bind recovery blocked compatible startup: %v", err)
	}
	defer m2.Close()
	var pairs int
	if err = m2.db.QueryRow(`SELECT count(*) FROM forward_unix_pairs WHERE phase='terminal'`).Scan(&pairs); err != nil || pairs != 1 {
		t.Fatalf("recovery did not retain terminal tombstone: %d %v", pairs, err)
	}
	source, err := m2.Inspect(h.id)
	if err != nil || source.Request.StorageLease != "" || source.Owner != owner {
		t.Fatalf("startup no-bind abort changed original source incorrectly: %+v %v", source, err)
	}
	storage, err := m2.Inspect(p.storage)
	if err != nil || storage.Request.Resources.Disk != forwardUnixDiskPromise {
		t.Fatalf("startup dropped storage obligation: %+v %v", storage, err)
	}
	// The original child and listener handles from the closed Manager cannot
	// be used to mint a new pair in the recovered process.
	if _, err = c.IssueStoragePair(context.Background()); err == nil {
		t.Fatal("closed original child reacquired pair authority")
	}
}

func TestForwardUnixPairRecoveryRefusalPreservesLedger(t *testing.T) {
	for _, kind := range []string{"workspace_format", "configuration_journal"} {
		t.Run(kind, func(t *testing.T) {
			m, _, c, joined, childJoined := newForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			home := m.home
			close(childJoined)
			close(joined)
			_ = m.Close()
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			expected := "workspace_format_unsupported"
			if kind == "workspace_format" {
				_, err = db.Exec(`INSERT INTO metadata(key,value) VALUES('workspace_format','2')`)
			} else {
				expected = "invalid_config_journal"
				_, err = db.Exec(`INSERT INTO events(type,entity,at,data) VALUES('daemon.configuration_applied','invalid','2026-01-01T00:00:00Z','{}')`)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := snapshotForwardUnixStartup(t, db, p.source)
			lock, err := local.Lock(home, "daemon.lock")
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if got, openErr := OpenConfiguredWithUnixRecovery(home, forwardUnixPairSettings(), lock); got != nil || errorCode(openErr) != expected {
				t.Fatalf("incompatible startup reached pair recovery: manager=%v error=%v", got, openErr)
			}
			after := snapshotForwardUnixStartup(t, db, p.source)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refused startup mutated pair, source attachment or events")
			}
		})
	}
}

func snapshotForwardUnixPairLedger(t *testing.T, db *sql.DB) [3]string {
	t.Helper()
	queries := [...]string{
		`SELECT COALESCE(json_group_array(json_array(source_id,storage_id,phase,source_key,storage_key,socket_name,disk_bytes,data)),'[]') FROM (SELECT * FROM forward_unix_pairs ORDER BY source_id)`,
		`SELECT COALESCE(json_group_array(json_array(id,request_key,data,run)),'[]') FROM (SELECT * FROM leases ORDER BY id)`,
		`SELECT COALESCE(json_group_array(json_array(sequence,type,entity,at,data)),'[]') FROM (SELECT * FROM events ORDER BY sequence)`,
	}
	var result [3]string
	for i, query := range queries {
		if err := db.QueryRow(query).Scan(&result[i]); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestForwardUnixPairMixedIntentRefusesBeforeAnyPlannedWrite(t *testing.T) {
	m, _, c1, joined1, childJoined1 := newForwardUnixPairFixture(t)
	p1, err := c1.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined2, childJoined2 := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-childJoined2:
		default:
			close(childJoined2)
		}
		select {
		case <-joined2:
		default:
			close(joined2)
		}
	})
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	h2, err := m.AcquireForwardListener(context.Background(), strings.Repeat("e", 32), "second original pair", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined2)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := h2.IssueUnixChild(context.Background(), childJoined2)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c2.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	planned, intentPair := p1, p2
	if p1.source > p2.source {
		planned, intentPair = p2, p1
	}
	intent := intentPair.data
	intent.Phase, intent.Revision = "bind_intent", 2
	intent.BindIntent = &forwardUnixBindIntent{Directory: intent.StorageArtifact, Allocated: 0, At: time.Now().UTC()}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE forward_unix_pairs SET phase='bind_intent',data=? WHERE source_id=? AND phase='planned'`, string(raw), intentPair.source); err != nil {
		t.Fatal(err)
	}
	// If recovery touches the earlier planned pair before rejecting the later
	// intent, this trigger changes the returned error and makes the test fail.
	if _, err = m.db.Exec(`CREATE TRIGGER forward_unix_test_planned_write BEFORE UPDATE ON forward_unix_pairs WHEN OLD.phase='planned' BEGIN SELECT RAISE(FAIL,'planned write attempted before native preflight'); END`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.db.Exec(`DROP TRIGGER forward_unix_test_planned_write`) }()
	before := snapshotForwardUnixPairLedger(t, m.db)
	tx, err := m.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := abortForwardUnixPlannedPairs(context.Background(), tx, m.home)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if errorCode(result) != "forward_unix_pair_unavailable" {
		t.Fatalf("mixed native state reached planned write or survey: %v", result)
	}
	after := snapshotForwardUnixPairLedger(t, m.db)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("mixed native refusal changed pair, lease or event ledger")
	}
	if planned.data.Phase != "planned" || intentPair.data.Phase != "planned" {
		t.Fatal("test changed original in-process pair handles")
	}
	close(childJoined1)
	close(joined1)
	close(childJoined2)
	close(joined2)
}

func TestForwardUnixPairStartupRefusesAlteredPlannedTuple(t *testing.T) {
	m, _, c, joined, childJoined := newForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	home := m.home
	close(childJoined)
	close(joined)
	_ = m.Close()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE forward_unix_pairs SET socket_name='forward-ffffffffffffffffffffffffffffffff.sock' WHERE source_id=?`, p.source); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err := local.Lock(home, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = OpenConfiguredWithUnixRecovery(home, forwardUnixPairSettings(), lock); errorCode(err) != "forward_unix_pair_unavailable" {
		t.Fatalf("altered planned pair was adopted or repaired: %v", err)
	}
}

func TestForwardUnixPairRecoveryRejectsCorruptPhysicalProjection(t *testing.T) {
	for _, kind := range []string{"source_key", "storage_root", "run_binding"} {
		t.Run(kind, func(t *testing.T) {
			m, _, c, joined, childJoined := newForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			home := m.home
			close(childJoined)
			close(joined)
			_ = m.Close()
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if kind == "source_key" {
				_, err = db.Exec(`UPDATE forward_unix_pairs SET source_key=CAST(source_key AS BLOB) WHERE source_id=?`, p.source)
			} else if kind == "storage_root" {
				_, err = db.Exec(`UPDATE metadata SET value=CAST(value AS BLOB) WHERE key='storage_root'`)
			} else {
				_, err = db.Exec(`UPDATE leases SET run='{}' WHERE id=?`, p.source)
			}
			if err != nil {
				t.Fatal(err)
			}
			lock, err := local.Lock(home, "daemon.lock")
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if got, openErr := OpenConfiguredWithUnixRecovery(home, forwardUnixPairSettings(), lock); got != nil || errorCode(openErr) != "forward_unix_pair_unavailable" {
				t.Fatalf("corrupt physical pair projection was accepted: manager=%v error=%v", got, openErr)
			}
			var phase string
			if err = db.QueryRow(`SELECT phase FROM forward_unix_pairs WHERE source_id=?`, p.source).Scan(&phase); err != nil || phase != "planned" {
				t.Fatalf("failed typed recovery changed pair: %q %v", phase, err)
			}
		})
	}
}

func TestForwardUnixPairRecoveryRequiresEmptyOriginalDirectory(t *testing.T) {
	m, _, c, joined, childJoined := newForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	storage, err := m.Inspect(p.storage)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(storage.ManagedDir, p.data.SocketName), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	home := m.home
	close(childJoined)
	close(joined)
	_ = m.Close()
	lock, err := local.Lock(home, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = OpenConfiguredWithUnixRecovery(home, forwardUnixPairSettings(), lock); errorCode(err) != "forward_unix_pair_unavailable" {
		t.Fatalf("occupied original directory falsely proved no-bind: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var phase string
	if err = db.QueryRow(`SELECT phase FROM forward_unix_pairs WHERE source_id=?`, p.source).Scan(&phase); err != nil || phase != "planned" {
		t.Fatalf("failed recovery changed durable pair: %q %v", phase, err)
	}
}

func TestForwardUnixPairTerminalReaderRejectsChangedStorageIdentity(t *testing.T) {
	m, _, c, joined, childJoined := newForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = p.AbortBeforeBind(context.Background()); err != nil {
		t.Fatal(err)
	}
	home := m.home
	close(childJoined)
	close(joined)
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE leases SET data=json_set(data,'$.artifact.identity.inode',1) WHERE id=?`, p.storage); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(home, model.Resources{CPU: 1, Memory: 16 << 20, Disk: 8 << 20}); errorCode(err) != "forward_unix_pair_unavailable" {
		t.Fatalf("terminal tombstone accepted changed storage identity: %v", err)
	}
}
