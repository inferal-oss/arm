package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	sqlite "modernc.org/sqlite"
)

// Public-API primitive fixtures, not positive Manager/native-claim bindings.
// Every pool/connector/returned connection below is actual official SQLite.
// No claim, role stop, service grant, native handle or Closed witness is faked.
func standardConnectorFixture(t *testing.T, dsn string) *ResidentOpen {
	t.Helper()
	standard, err := sqlite.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	o := &ResidentOpen{standardConnector: standard, phase: "connector_fixture"}
	o.self = o
	o.connector = &residentConnector{owner: o, original: standard}
	o.closeObservation.NativeOutcome = "unknown"
	o.pool = sql.OpenDB(o.connector)
	o.pool.SetMaxOpenConns(1)
	o.pool.SetMaxIdleConns(1)
	o.pool.SetConnMaxLifetime(0)
	o.pool.SetConnMaxIdleTime(0)
	t.Cleanup(func() {
		// Fixture API disposal only. Return does not prove pool-task/native join.
		if err := o.pool.Close(); err != nil {
			t.Error(err)
		}
	})
	return o
}

func TestResidentStandardConnectorKeepsActualConnInterfacesAndTypes(t *testing.T) {
	o := standardConnectorFixture(t, ":memory:")
	if err := o.pool.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if o.pool.Driver() != o.standardConnector.Driver() {
		t.Fatal("registered original Driver changed")
	}
	conn, err := o.pool.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(raw any) error {
		if raw != o.connect.returned {
			return fmt.Errorf("actual returned driver.Conn was wrapped or replaced")
		}
		if _, ok := raw.(driver.QueryerContext); !ok {
			return fmt.Errorf("official optional QueryerContext missing")
		}
		if _, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}); !ok {
			return fmt.Errorf("official concrete Backup API/type missing")
		}
		return nil
	})
	closeErr := conn.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	_, err = o.pool.ExecContext(t.Context(), "SELECT FROM")
	var official *sqlite.Error
	if !errors.As(err, &official) {
		t.Fatal("official SQLite error type changed", err)
	}
	seen := o.InspectOriginal()
	if !seen.PoolRetained || !seen.Connector.ConnectAttempted || seen.Connector.ConnectPending || !seen.Connector.ReturnedConnectionRetained || !seen.Connector.PrivateNativeStateOpaque || o.binding != nil || o.claim != nil {
		t.Fatal(seen)
	}
}

func TestResidentStandardCancelledConnectRetainsOriginalCall(t *testing.T) {
	o := standardConnectorFixture(t, ":memory:")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	returned, err := o.connector.Connect(ctx)
	if returned != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(returned, err)
	}
	first := o.connect
	seen := o.InspectOriginal()
	if !seen.Connector.ConnectAttempted || seen.Connector.ConnectPending || seen.Connector.FirstConnectError != err || seen.FirstError != err || !seen.Connector.PrivateNativeStateOpaque {
		t.Fatal(seen)
	}
	if _, err = o.connector.Connect(t.Context()); !errors.Is(err, ErrResidentConnectionAttempted) {
		t.Fatal("cancelled original attempt retried", err)
	}
	if o.connect != first {
		t.Fatal("original cancelled call replaced")
	}
}

func TestResidentStandardOpenFailureKeepsPoolAndOpaqueNativeObligation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-parent", "ledger.sqlite")
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw"}
	o := standardConnectorFixture(t, u.String())
	first := o.pool.PingContext(t.Context())
	if first == nil {
		t.Fatal("unopenable official SQLite path succeeded")
	}
	seen := o.InspectOriginal()
	if !seen.PoolRetained || !seen.ConnectorRetained || !seen.Connector.ConnectAttempted || seen.Connector.ConnectPending || !seen.Connector.PrivateNativeStateOpaque || seen.FirstError == nil || seen.Close.NativeOutcome != "unknown" {
		t.Fatal(seen)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("mode=rw created an absent ledger", err)
	}
	call := o.connect
	if err := o.pool.PingContext(t.Context()); !errors.Is(err, ErrResidentConnectionAttempted) {
		t.Fatal("failed original physical open was repeated", err)
	}
	if o.connect != call {
		t.Fatal("failed original Connect object replaced")
	}
}

func TestResidentStandardPoolDiscardCannotReconnect(t *testing.T) {
	o := standardConnectorFixture(t, ":memory:")
	if err := o.pool.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := o.connect
	conn, err := o.pool.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	if err = conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		t.Fatal(err)
	}
	if err = o.pool.PingContext(t.Context()); !errors.Is(err, ErrResidentConnectionAttempted) {
		t.Fatal("database/sql admitted a replacement connection", err)
	}
	seen := o.InspectOriginal()
	if o.connect != original || seen.Connector.AdmissionRefusals == 0 || !seen.Connector.ReturnedConnectionRetained || !seen.Connector.PrivateNativeStateOpaque || seen.Close.NativeOutcome != "unknown" {
		t.Fatal(seen)
	}
	// Keeping the original object is not a claim it remains live after pool
	// discard. This test intentionally demonstrates degraded DB availability,
	// not successful job continuity through an unrelated database failure.
}

func TestResidentStandardPoolCloseDoesNotCertifyNativeClosure(t *testing.T) {
	o := standardConnectorFixture(t, ":memory:")
	if err := o.pool.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := o.pool.Close(); err != nil {
		t.Fatal(err)
	}
	seen := o.InspectOriginal()
	if !seen.PoolRetained || !seen.Connector.ReturnedConnectionRetained || !seen.Connector.PrivateNativeStateOpaque || seen.Close.NativeOutcome != "unknown" {
		t.Fatal(seen)
	}
}
