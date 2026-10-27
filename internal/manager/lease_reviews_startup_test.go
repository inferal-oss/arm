package manager

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/model"
)

// Capture actual optional schema and values after deliberate corruption. Core
// lease recovery may change its own tables, never these retained unknown facts.
func reviewOptionalEvidence(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	for _, query := range []string{
		`SELECT type,name,tbl_name,sql FROM sqlite_master WHERE name LIKE 'lease_review%' OR tbl_name LIKE 'lease_review%' ORDER BY name`,
		`SELECT typeof(key),hex(CAST(key AS BLOB)),typeof(value),hex(CAST(value AS BLOB)) FROM metadata WHERE CAST(key AS BLOB)=CAST('lease_review_schema' AS BLOB) ORDER BY typeof(key),hex(CAST(value AS BLOB))`,
		`SELECT * FROM lease_review_policies ORDER BY lease`,
		`SELECT * FROM lease_reviews ORDER BY sequence`,
	} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		out = append(out, query, fmt.Sprint(cols))
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			raw, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out = append(out, string(raw))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestLeaseReviewUnavailableStartupPreservesOrdinaryLifecycle(t *testing.T) {
	for _, scenario := range []string{"unsupported-marker", "blob-value-marker", "blob-key-marker", "duplicate-marker-key-alias", "partial-group", "malformed-unused-table"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			capacity := model.Resources{CPU: 1, Memory: 1024, Disk: 2048}
			m, err := Open(home, capacity)
			if err != nil {
				t.Fatal(err)
			}
			c := pollContact(t, m, "startup-review")
			expires := reviewLease(t, m, c, "startup-expire")
			release := reviewLease(t, m, c, "startup-release")
			dueReview(t, m, expires)
			reviewTick(t, m, time.Now())
			if len(reviews(t, m, model.LeaseReviewQuery{}).Items) != 1 {
				t.Fatal("actual pending history missing")
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "unsupported-marker":
				_, err = db.Exec(`UPDATE metadata SET value=? WHERE key='lease_review_schema'`, strings.Repeat("<unrecognized>", 8192))
			case "blob-value-marker":
				_, err = db.Exec(`UPDATE metadata SET value=x'31' WHERE key='lease_review_schema'`)
			case "blob-key-marker":
				_, err = db.Exec(`UPDATE metadata SET key=CAST('lease_review_schema' AS BLOB) WHERE key='lease_review_schema'`)
			case "duplicate-marker-key-alias":
				_, err = db.Exec(`INSERT INTO metadata(key,value) VALUES(CAST('lease_review_schema' AS BLOB),'1')`)
			case "partial-group":
				_, err = db.Exec(`DROP INDEX lease_review_policy_active`)
			case "malformed-unused-table":
				// Same names and indexable columns, so CREATE IF NOT EXISTS alone used to
				// accept it. Keep an opaque historical value that no feature may repair.
				_, err = db.Exec(`DROP TABLE lease_review_policies`)
				if err == nil {
					_, err = db.Exec(`CREATE TABLE lease_review_policies(lease TEXT PRIMARY KEY,generation TEXT,enabled INTEGER,data TEXT)`)
				}
				if err == nil {
					_, err = db.Exec(`CREATE INDEX lease_review_policy_active ON lease_review_policies(enabled,lease)`)
				}
				if err == nil {
					_, err = db.Exec(`INSERT INTO lease_review_policies VALUES('opaque','not-an-integer',0,'unparsed preserved evidence')`)
				}
			}
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			before := reviewOptionalEvidence(t, db)
			// Make an existing ordinary lease overdue while stopped. Opening must
			// still recover it and run ordinary expiry despite the unavailable feature.
			past := time.Now().Add(-time.Minute).UTC()
			expires.Expires = &past
			raw, err := json.Marshal(expires)
			if err == nil {
				_, err = db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), expires.ID)
			}
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			m, err = Open(home, capacity)
			if err != nil {
				t.Fatal("optional evidence blocked ordinary startup", err)
			}
			t.Cleanup(func() {
				if err := m.Close(); err != nil {
					t.Error(err)
				}
			})
			status, err := m.Status()
			if err != nil {
				t.Fatal(err)
			}
			if status.ReviewError == nil || status.ReviewError.Code != "reviews_unavailable" || len(status.ReviewError.Message) > 256 || strings.Contains(status.ReviewError.Message, "unrecognized") {
				t.Fatal("unbounded or absent availability diagnostic", status.ReviewError)
			}
			for _, capability := range status.Capabilities {
				if capability == "local-lease-reviews-v1" {
					t.Fatal("unavailable feature advertised")
				}
			}
			assertUnavailable := func(err error) {
				t.Helper()
				var e *model.Error
				if !errors.As(err, &e) || e.Code != "reviews_unavailable" || e.Message != reviewUnavailableMessage {
					t.Fatal("review operation failed outside bounded availability contract", err)
				}
			}
			_, err = m.LeaseReviews(model.LeaseReviewQuery{})
			assertUnavailable(err)
			_, err = m.InspectLeaseReview("absent")
			assertUnavailable(err)
			_, err = m.LeaseReviewPolicy(release.ID)
			assertUnavailable(err)
			_, err = m.ConfigureLeaseReview(model.LeaseReviewConfigure{Lease: release.ID, Revision: release.Revision, Generation: reviewGen(0), Enabled: true})
			assertUnavailable(err)
			_, err = m.ReplyLeaseReview(model.LeaseReviewReply{})
			assertUnavailable(err)
			// Open performs recovery but deliberately does not start scheduling.
			// Drive the ordinary production lifecycle deterministically; CPU-only
			// expiry is synchronous, so inspect its actual result without a timer.
			m.tick()
			expired, err := m.Inspect(expires.ID)
			if err != nil || expired.State != "terminal" || expired.Outcome != "expired" || expired.Revision != expires.Revision+1 || expired.Expires == nil || !expired.Expires.Equal(past) {
				t.Fatal("ordinary expiry changed its original deadline or unexpected revision", expired, err)
			}
			if l, e := m.End(release.ID, "released"); e != nil || l.State != "terminal" || l.Outcome != "released" || l.Revision != release.Revision+1 || l.Expires == nil || release.Expires == nil || !l.Expires.Equal(*release.Expires) {
				t.Fatal("ordinary release failed", l, e)
			}
			fresh, e := m.Acquire(request("ordinary-after-review-failure"), nil)
			if e != nil || fresh.State != "active" {
				t.Fatal("ordinary admission failed", fresh, e)
			}
			if _, e = m.End(fresh.ID, "released"); e != nil {
				t.Fatal(e)
			}
			m.tick() // Sticky state must not be cleared by a successful ordinary tick.
			_, err = m.LeaseReviews(model.LeaseReviewQuery{})
			assertUnavailable(err)
			var after []string
			func() { m.mu.Lock(); defer m.mu.Unlock(); after = reviewOptionalEvidence(t, m.db) }()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("optional evidence was repaired, removed or rewritten")
			}
		})
	}
}

func TestLeaseReviewInitializationDoesNotHideCoreDatabaseErrors(t *testing.T) {
	m := newManager(t)
	// A real failed transaction/query is not the typed optional-schema evidence
	// error. Use a separate closed database rather than perturb the live fixture.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	unavailable := &Manager{db: db}
	if err := unavailable.initLeaseReviews(); err == nil || errors.Is(err, database.ErrLeaseReviewSchema) {
		t.Fatal("core Begin error misclassified", err)
	}
	m.mu.Lock()
	tx, err := m.db.Begin()
	if err == nil {
		err = tx.Rollback()
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.CheckLeaseReviewSchema(t.Context(), tx); err == nil || errors.Is(err, database.ErrLeaseReviewSchema) {
		t.Fatal("core Query error misclassified", err)
	}
}

func TestLeaseReviewCoreSchemaFailureStillRejectsStartup(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	capacity := model.Resources{CPU: 1, Memory: 1024, Disk: 2048}
	m, err := Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE metadata RENAME COLUMN value TO unsupported_core_value`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home, capacity)
	if err == nil {
		reopened.Close()
		t.Fatal("core metadata damage was hidden as optional review failure")
	}
	if errors.Is(err, database.ErrLeaseReviewSchema) {
		t.Fatal("core metadata failure was classified as optional evidence", err)
	}
}

var reviewRollbackFault = errors.New("injected optional-schema transaction rollback failure")

type reviewRollbackDriver struct{}
type reviewRollbackConn struct{ mode string }
type reviewRollbackTx struct{ mode string }
type reviewMarkerRows struct{ emitted bool }

func init() { sql.Register("arm-review-schema-rollback-fault", reviewRollbackDriver{}) }
func (reviewRollbackDriver) Open(mode string) (driver.Conn, error) {
	return &reviewRollbackConn{mode: mode}, nil
}
func (c *reviewRollbackConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *reviewRollbackConn) Close() error              { return nil }
func (c *reviewRollbackConn) Begin() (driver.Tx, error) { return reviewRollbackTx{mode: c.mode}, nil }
func (t reviewRollbackTx) Commit() error                { return nil }
func (t reviewRollbackTx) Rollback() error {
	if t.mode == "rollback-error" {
		return reviewRollbackFault
	}
	return nil
}
func (c *reviewRollbackConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &reviewMarkerRows{}, nil
}
func (*reviewMarkerRows) Columns() []string { return []string{"markers", "supported"} }
func (*reviewMarkerRows) Close() error      { return nil }
func (r *reviewMarkerRows) Next(values []driver.Value) error {
	if r.emitted {
		return io.EOF
	}
	r.emitted = true
	// Two exact-byte aliases are recognized unsupported evidence. The driver
	// must still get a real Rollback, whose failure cannot become optional.
	copy(values, []driver.Value{int64(2), int64(1)})
	return nil
}
func TestLeaseReviewRollbackFailureRemainsCore(t *testing.T) {
	for _, mode := range []string{"clean-rollback", "rollback-error"} {
		t.Run(mode, func(t *testing.T) {
			db, err := sql.Open("arm-review-schema-rollback-fault", mode)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			m := &Manager{db: db}
			err = m.initLeaseReviews()
			if mode == "clean-rollback" {
				if !errors.Is(err, database.ErrLeaseReviewSchema) {
					t.Fatal("recognized optional refusal lost", err)
				}
			} else if !errors.Is(err, reviewRollbackFault) || errors.Is(err, database.ErrLeaseReviewSchema) {
				t.Fatal("rollback failure was hidden as optional schema evidence", err)
			}
		})
	}
}
