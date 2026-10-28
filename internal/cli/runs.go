package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"getarm.org/internal/childowner"
	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func (a *app) runsCommand(args []string) error {
	f := a.flags("runs")
	f.Bool("json", false, "return run records as JSON")
	if err := a.parse(f, args, false); err != nil {
		return err
	}
	var runs []model.Run
	if err := a.call("runs", nil, &runs); err != nil {
		return err
	}
	return a.print(runs)
}

func (a *app) logCommand(args []string) error {
	f := a.flags("log")
	var follow bool
	f.BoolVar(&follow, "f", false, "follow new output until the run finishes or reporting is lost")
	f.BoolVar(&follow, "follow", false, "follow new output until the run finishes or reporting is lost")
	offset := f.Int64("offset", 0, "start at this byte offset")
	if err := a.parse(f, args, false); err != nil {
		return err
	}
	if *offset < 0 {
		return model.Err("invalid_request", "--offset must be nonnegative")
	}
	var run model.Run
	if err := a.call("run_inspect", map[string]string{"id": f.Arg(0)}, &run); err != nil {
		return err
	}
	// Pin the canonical ID once. A non-following read is a bounded snapshot.
	end := run.LogBytes
	for {
		var page model.RunLog
		if err := a.call("run_output", map[string]any{"id": run.ID, "offset": *offset}, &page); err != nil {
			return err
		}
		if page.Run.ID != run.ID || page.Offset != *offset || page.NextOffset != *offset+int64(len(page.Data)) {
			return model.Err("invalid_response", "run log cursor mismatch")
		}
		data := page.Data
		if !follow && int64(len(data)) > max(0, end-*offset) {
			data = data[:max(0, end-*offset)]
		}
		if len(data) > 0 {
			if n, err := a.out.Write(data); err != nil {
				return err
			} else if n != len(data) {
				return io.ErrShortWrite
			}
		}
		*offset = page.NextOffset
		if (!follow && *offset >= end) || (page.Done && *offset >= page.Run.LogBytes) {
			if page.Run.Truncated {
				fmt.Fprintln(a.err, "Run log was truncated at its retention limit.")
			}
			if page.Run.State == "interrupted" {
				fmt.Fprintln(a.err, "Run reporting was lost; command exit is unknown.")
			}
			return nil
		}
		if len(page.Data) > 0 {
			continue
		}
		if err := runPoll(a.ctx); err != nil {
			return err
		}
	}
}

func runPoll(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Calls used during final pipe draining and cancellation outlive the CLI signal
// context briefly so a positively joined child can still publish its result.
func (a *app) runReport(operation string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Call(ctx, a.home, operation, params, result)
}

type runAppender struct {
	mu     sync.Mutex
	app    *app
	run    model.Run
	dest   io.Writer
	cancel context.CancelFunc
	err    error
}

func (w *runAppender) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(data) > 0 {
		chunk := data[:min(len(data), 64<<10)]
		var next model.Run
		err := w.app.runReport("run_append", map[string]any{"id": w.run.ID, "offset": w.run.AcceptedBytes, "data": chunk}, &next)
		if err == nil && (next.ID != w.run.ID || next.AcceptedBytes != w.run.AcceptedBytes+int64(len(chunk))) {
			err = model.Err("invalid_response", "run append acknowledgement mismatch")
		}
		if err == nil {
			w.run = next
			var n int
			n, err = w.dest.Write(chunk)
			if err == nil && n != len(chunk) {
				err = io.ErrShortWrite
			}
		}
		if err != nil {
			w.err = err
			w.cancel()
			return written, err
		}
		written += len(chunk)
		data = data[len(chunk):]
	}
	return written, nil
}

// Quiet commands may produce no bytes for a long time. Reporting liveness is
// separate from process liveness: losing this heartbeat never proves exit.
func (w *runAppender) heartbeat(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.mu.Lock()
			if w.err == nil {
				var next model.Run
				err := w.app.runReport("run_append", map[string]any{"id": w.run.ID, "offset": w.run.AcceptedBytes, "data": []byte{}}, &next)
				if err == nil && (next.ID != w.run.ID || next.AcceptedBytes != w.run.AcceptedBytes) {
					err = model.Err("invalid_response", "run heartbeat acknowledgement mismatch")
				}
				if err != nil {
					w.err = err
					w.cancel()
				} else {
					w.run = next
				}
			}
			w.mu.Unlock()
		}
	}
}

func (a *app) proxyCommand(args []string) (int, error) {
	f := a.flags("proxy")
	quiet := f.Bool("quiet", false, "save output without printing it; still wait and return the command exit status")
	cwd := f.String("cwd", "", "command working directory (default: current directory)")
	agent := f.String("agent", "", "registered agent contact for attribution")
	reason := f.String("reason", "", "required purpose")
	f.Bool("json", false, "send command output to stderr and final run JSON to stdout")
	if err := a.parse(f, args, true); err != nil {
		return 2, err
	}
	if strings.TrimSpace(*reason) == "" {
		return 2, model.Err("invalid_request", "--reason is required")
	}
	command := append([]string(nil), f.Args()...)
	if len(command) == 0 {
		return 2, model.Err("invalid_request", "proxy requires -- COMMAND [ARG...]")
	}
	var err error
	if *cwd == "" {
		*cwd, err = os.Getwd()
		if err != nil {
			return 1, err
		}
	}
	*cwd, err = filepath.Abs(*cwd)
	if err != nil {
		return 2, err
	}
	executable := command[0]
	if strings.ContainsRune(executable, os.PathSeparator) && !filepath.IsAbs(executable) {
		executable = filepath.Join(*cwd, executable)
	}
	executable, err = exec.LookPath(executable)
	if err != nil {
		return 2, err
	}
	command[0], err = filepath.Abs(executable)
	if err != nil {
		return 2, err
	}
	if err := childowner.Available(); err != nil {
		return 1, err
	}
	if err := a.ctx.Err(); err != nil {
		return 130, err
	}
	var run model.Run
	if err := a.call("run_create", model.RunCreateRequest{Key: local.ID(), Command: command, CWD: *cwd, Agent: *agent, Reason: *reason}, &run); err != nil {
		return 1, err
	}
	if !a.machine() {
		fmt.Fprintf(a.err, "Run %s (%s)\n", terminalSafe(run.Name), run.ID)
	}
	finish := func(code int, outcome string) (int, error) {
		if err := a.runReport("run_finish", map[string]any{"id": run.ID, "exit_code": code, "outcome": outcome}, &run); err != nil {
			return 1, fmt.Errorf("run %s result could not be recorded: %w", run.ID, err)
		}
		if err := a.print(run); err != nil {
			return 1, err
		}
		return code, nil
	}
	// Use the same original-child ownership boundary as leased launches. The
	// fixed shell gate prevents user execution before the owner is established.
	reader, gate, err := os.Pipe()
	if err != nil {
		_, _ = finish(1, "launch_failed")
		return 1, err
	}
	defer reader.Close()
	defer gate.Close()
	argv := append([]string{"-c", `IFS= read -r arm_gate <&3 || exit 125; [ "$arm_gate" = go ] || exit 125; exec 3<&-; exec "$@"`, "arm-proxy"}, command...)
	cmd := exec.Command("/bin/sh", argv...)
	cmd.Dir, cmd.Stdin = *cwd, a.in
	cmd.ExtraFiles = []*os.File{reader}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	dest := a.out
	if a.machine() {
		dest = a.err
	}
	if *quiet {
		dest = io.Discard
	}
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	output := &runAppender{app: a, run: run, dest: dest, cancel: cancel}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		_, _ = finish(1, "launch_failed")
		return 1, err
	}
	owner, err := childowner.New(cmd)
	if err != nil {
		_ = gate.Close()
		_ = reader.Close()
		// Only the fixed unapproved gate shell exists; no user process was launched.
		waitErr := cmd.Wait()
		return 1, fmt.Errorf("run %s original child binding unavailable: %w", run.ID, errors.Join(err, waitErr))
	}
	_ = reader.Close()
	if ctx.Err() == nil {
		if _, err = gate.Write([]byte("go\n")); err != nil {
			cancel()
		}
	}
	_ = gate.Close()
	heartbeatDone := make(chan struct{})
	go output.heartbeat(ctx, heartbeatDone)
	result := owner.Wait(ctx)
	cancel()
	<-heartbeatDone
	output.mu.Lock()
	reportErr := output.err
	output.mu.Unlock()
	var exitErr *exec.ExitError
	if result.WaitError != nil && !errors.As(result.WaitError, &exitErr) {
		reportErr = errors.Join(reportErr, result.WaitError)
	}
	if !result.Reaped || result.CleanupError != nil {
		return 1, fmt.Errorf("run %s cleanup is uncertain: %w", run.ID, result.CleanupError)
	}
	code := cmd.ProcessState.ExitCode()
	if code < 0 {
		code = 128 + int(cmd.ProcessState.Sys().(syscall.WaitStatus).Signal())
	}
	outcome := "completed"
	if code != 0 {
		outcome = "failed"
	}
	if a.ctx.Err() != nil {
		outcome = "cancelled"
	}
	if reportErr != nil {
		outcome = "log_failed"
	}
	code, err = finish(code, outcome)
	if reportErr != nil {
		return 1, fmt.Errorf("run %s log reporting failed: %w", run.ID, reportErr)
	}
	return code, err
}
