package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

func (a *app) bulkEnd(action, key string) (int, error) {
	if key == "" {
		key = local.ID()
	}
	var report model.BulkEndReport
	if err := a.call("bulk_end", model.BulkEndRequest{Action: action, Key: key}, &report); err != nil {
		var api *model.Error
		if errors.As(err, &api) {
			return 1, model.Err(api.Code, api.Message+" (bulk operation key "+key+")")
		}
		return 1, fmt.Errorf("bulk operation key %s: %w", key, err)
	}
	if err := a.print(report); err != nil {
		return 1, err
	}
	for _, item := range report.Results {
		if item.Error != nil {
			return 1, model.Err("partial_failure", "some leases remain unresolved; inspect the per-lease report and retry the same --key "+key)
		}
	}
	return 0, nil
}

func (a *app) stopBackend(request model.ShutdownRequest) (int, error) {
	ep, err := local.ReadEndpoint(a.home)
	if err != nil {
		return 1, err
	}
	var report model.ShutdownReport
	if err = a.call("shutdown", request, &report); err != nil {
		return 1, err
	}
	phase := ""
	consecutiveErrors := 0
	for {
		if !a.machine() && report.Phase != phase {
			fmt.Fprintln(a.err, a.paint("1;36", "Shutdown "+report.ID+" · "+report.Phase)+"\n"+report.Detail)
			phase = report.Phase
		}
		if report.Phase == "blocked" {
			if err = a.print(report); err != nil {
				return 1, err
			}
			return 1, model.Err("needs_attention", "backend remains available with admission paused; reconcile retained allocations or explicitly hand off storage")
		}
		current, readErr := local.ReadEndpoint(a.home)
		if os.IsNotExist(readErr) {
			// Endpoint removal precedes releasing the daemon authority lock.
			// Both facts are required; a missing file alone is not shutdown.
			lock, lockErr := os.OpenFile(filepath.Join(a.home, "daemon.lock"), os.O_RDWR, 0600)
			if lockErr != nil {
				return 1, lockErr
			}
			lockErr = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if lockErr == nil {
				final, finalErr := readFinalShutdown(a.home, report.ID)
				_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
				if finalErr != nil {
					lock.Close()
					return 1, finalErr
				}
				report = final
			}
			lock.Close()
			if lockErr == nil {
				report.Stopped = true
				report.Phase = "stopped"
				report.Detail = "Backend stopped; state and event history are retained."
				return 0, a.print(report)
			}
		} else if readErr != nil {
			return 1, readErr
		} else {
			if current.Token != ep.Token || current.URL != ep.URL || current.PID != ep.PID {
				return 1, model.Err("shutdown_interrupted", "backend authority changed while waiting; inspect the restarted backend before requesting another shutdown")
			}
			var next model.ShutdownReport
			if err = a.call("shutdown_status", nil, &next); err == nil {
				consecutiveErrors = 0
				if next.ID != report.ID {
					return 1, model.Err("shutdown_interrupted", "shutdown identity changed while waiting")
				}
				report = next
			} else {
				consecutiveErrors++
				var api *model.Error
				if errors.As(err, &api) {
					return 1, model.Err(api.Code, api.Message+" (shutdown "+report.ID+")")
				}
				if consecutiveErrors >= 5 {
					return 1, model.Err("shutdown_status_unavailable", "cannot observe shutdown "+report.ID+"; backend intent continues. Reconnect and inspect before retrying.")
				}
				lock, openErr := os.OpenFile(filepath.Join(a.home, "daemon.lock"), os.O_RDWR, 0600)
				if openErr == nil {
					unlocked := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
					if unlocked {
						_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
					}
					lock.Close()
					if unlocked {
						return 1, model.Err("shutdown_interrupted", "backend exited before verified shutdown; state is retained. Restart and inspect recovered allocations.")
					}
				}
			}
		}
		select {
		case <-a.ctx.Done():
			return 1, model.Err("shutdown_wait_interrupted", "client stopped waiting; backend shutdown "+report.ID+" continues independently")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Once the authority lock is released, read the durable final result without
// creating or modifying the database. A missed WebSocket frame must not lose
// provider results, and an external SIGTERM must not look like settled leases.
func readFinalShutdown(home, id string) (model.ShutdownReport, error) {
	var report model.ShutdownReport
	dsn := url.URL{Scheme: "file", Path: filepath.Join(home, "state.db"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dsn.String())
	if err == nil {
		defer db.Close()
		var data []byte
		err = db.QueryRow(`SELECT value FROM metadata WHERE key='shutdown'`).Scan(&data)
		if err == nil {
			err = json.Unmarshal(data, &report)
		}
	}
	if err != nil || report.ID != id || report.Phase != "ready" {
		return report, model.Err("shutdown_interrupted", "backend exited before verified shutdown completion; state is retained. Restart and inspect recovered allocations.")
	}
	return report, nil
}
