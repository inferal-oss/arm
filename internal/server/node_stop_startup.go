package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/inferal-oss/arm/internal/storage"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// checkNodeStopStartup runs under Serve's retained daemon.lock BEFORE reset
// installation, Manager.Open, recovery or endpoint publication. It uses one
// WAL-aware read-only transaction, without a DB-sized temporary copy. SQLite
// bookkeeping may create/update private sidecars; logical entitlement rows and
// schema are not mutated. Unknown/corrupt evidence still refuses startup.
func checkNodeStopStartup(ctx context.Context, home string, lock *os.File, restart *nodestop.Restart) (resultErr error) {
	read, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	source, exists, err := database.OpenStartupRead(read, home, lock)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()
	if !exists {
		if restart != nil {
			return fmt.Errorf("restart review references an absent stopped ledger")
		}
		return nil
	}
	state, present, err := nodestop.ReadState(source)
	if err != nil {
		return err
	}
	if err = source.Validate(read); err != nil {
		return err
	}
	if present {
		var ledgerHost string
		if e := source.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&ledgerHost); e != nil || ledgerHost != state.Target.Host {
			return fmt.Errorf("stopped intent host differs from current ledger authority")
		}
		id, e := storage.DirectoryIdentity(int(source.Directory.FD.Fd()))
		if e != nil || id != state.Target.Directory || source.Directory.Path != state.Target.Home || state.Target.Owner != strconv.Itoa(os.Geteuid()) {
			return fmt.Errorf("desired-state ledger belongs to another native authority")
		}
	}
	if present && !state.Running {
		if restart != nil {
			// End the RO snapshot before the separately authorized writer CAS.
			if err = source.Close(); err != nil {
				return err
			}
			return applyNodeStopRestart(ctx, home, state, *restart)
		}
		traceStopStartupRefused(home, state)
		return fmt.Errorf("ARM is intentionally stopped at revision %d (key %q). Automatic startup was refused; inspect the original stop and explicitly review a fresh-generation restart", state.Revision, state.Key)
	}
	if restart != nil {
		return fmt.Errorf("restart review does not match a current stopped intent")
	}
	return nil
}

// applyNodeStopRestart is called only under Serve's retained daemon.lock. This
// is a strict desired-state CAS, not process signalling or old-stop recovery.
func applyNodeStopRestart(ctx context.Context, home string, expected nodestop.State, q nodestop.Restart) (resultErr error) {
	return applyNodeStopRestartWithin(ctx, home, expected, q, nil)
}

func applyNodeStopRestartWithin(ctx context.Context, home string, expected nodestop.State, q nodestop.Restart, lifetime *nodestop.ElapsedReview) (resultErr error) {
	dir, err := database.OpenDirectory(home)
	if err != nil {
		return err
	}
	defer dir.Close()
	id, err := storage.DirectoryIdentity(int(dir.FD.Fd()))
	if err != nil || id != expected.Target.Directory || dir.Path != expected.Target.Home {
		return fmt.Errorf("reviewed stopped authority directory changed")
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.Join(home, "state.db"), RawQuery: "mode=rw&_pragma=busy_timeout(1000)"}).String()
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = database.CheckWorkspaceFormat(ctx, tx); err != nil {
		return err
	}
	current, present, err := nodestop.ReadState(tx)
	if err != nil || !present {
		return fmt.Errorf("stopped intent unavailable: %w", err)
	}
	r, err := nodestop.Lookup(tx, current.Key)
	if err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("stopped intent changed before restart CAS")
	}
	if err = q.Validate(current, r, strconv.Itoa(os.Geteuid()), time.Now()); err != nil {
		return err
	}
	var original string
	if err = tx.QueryRow(`SELECT data FROM node_stop_state WHERE singleton=1`).Scan(&original); err != nil {
		return err
	}
	current.Running = true
	current.Revision++
	if err = current.Validate(r); err != nil {
		return err
	}
	updated, err := json.Marshal(current)
	if err != nil {
		return err
	}
	changed, err := tx.Exec(`UPDATE node_stop_state SET data=? WHERE singleton=1 AND data=?`, string(updated), original)
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("restart CAS did not match exactly one desired state")
	}
	if r.Phase == "accepted" {
		r.Phase = "interrupted"
		r.Updated = time.Now().UTC()
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE node_stop_operations SET data=? WHERE key=?`, string(raw), r.Plan.Key); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, "node_stop.restart_reviewed", current.Key, time.Now().UTC().Format(time.RFC3339Nano), string(raw)); err != nil {
		return err
	}
	if err = dir.Validate(); err != nil {
		return err
	}
	if lifetime != nil {
		if err = lifetime.Check(); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("reviewed restart commit outcome unknown; inspect exact desired revision before any new review: %w", err)
	}
	traceRestartCAS(home, current, db, lifetime)
	return nil
}
