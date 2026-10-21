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
			final, released, finalErr := readReleasedShutdown(a.home, report.ID)
			if finalErr != nil {
				return 1, finalErr
			}
			if released {
				return a.printStoppedShutdown(final)
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
				// Shutdown can finish between reading the endpoint and sending
				// this status request. Verify its durable result under the lock
				// before treating a lost final response as an interruption.
				final, released, finalErr := readReleasedShutdown(a.home, report.ID)
				if finalErr != nil {
					return 1, finalErr
				}
				if released {
					return a.printStoppedShutdown(final)
				}
				if consecutiveErrors >= 5 {
					return 1, model.Err("shutdown_status_unavailable", "cannot observe shutdown "+report.ID+"; backend intent continues. Reconnect and inspect before retrying.")
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

func (a *app) printStoppedShutdown(report model.ShutdownReport) (int, error) {
	report.Stopped = true
	report.Phase = "stopped"
	report.Detail = "Backend stopped; state and event history are retained."
	return 0, a.print(report)
}

// Both the ordinary endpoint-removal path and a failed final status request
// converge here. Holding the authority lock prevents a replacement backend
// from racing the endpoint and durable completion checks.
func readReleasedShutdown(home, id string) (model.ShutdownReport, bool, error) {
	var report model.ShutdownReport
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0600)
	if err != nil {
		return report, false, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return report, false, nil
		}
		return report, false, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if _, err := local.ReadEndpoint(home); !os.IsNotExist(err) {
		return report, true, model.Err("shutdown_interrupted", "backend exited with its endpoint still present; restart and inspect recovered allocations")
	}
	report, err = readFinalShutdown(home, id)
	return report, true, err
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
