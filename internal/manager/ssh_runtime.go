package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"github.com/yrashk/arm/internal/usage"
	"golang.org/x/crypto/ssh"
)

var sshAccountSlot = make(chan struct{}, 1)

func resolveSSHAccount(ctx context.Context) (*user.User, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case sshAccountSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, model.Err("account_unavailable", "destination account lookup queue timed out")
	}
	type result struct {
		u   *user.User
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-sshAccountSlot }()
		u, err := user.LookupId(strconv.Itoa(os.Geteuid()))
		done <- result{u, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, model.Err("account_unavailable", "destination account lookup timed out")
	case r := <-done:
		if r.err != nil || r.u == nil || r.u.HomeDir == "" {
			return nil, model.Err("account_unavailable", "destination account home unknown")
		}
		return r.u, nil
	}
}

// This type and RunRequest.sshRuntime are intentionally unexported and have no
// JSON representation. Persisted descriptors cannot reconstruct this capability.
type sshRuntime struct {
	ctx            context.Context
	cancel         context.CancelFunc
	request        model.SSHExecutionRequest
	commitment     *model.SSHControlCommitment
	execution      sshservice.Execution
	channel        ssh.Channel
	account        *user.User
	mu             sync.Mutex
	all            []*os.File
	child          []*os.File
	input          *os.File
	stdout, stderr *os.File
	master         *os.File
	outputDone     chan struct{}
	stopClose      func() bool
	closeOnce      sync.Once
}

func newSSHRuntime(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel, account *user.User, until time.Time) (*sshRuntime, error) {
	if e.Terminal != nil {
		copyTerminal := *e.Terminal
		copyTerminal.Modes = ssh.TerminalModes{}
		for op, value := range e.Terminal.Modes {
			copyTerminal.Modes[op] = value
		}
		e.Terminal = &copyTerminal
	}
	expiry := time.Now().Add(q.TTL)
	if until.Before(expiry) {
		expiry = until
	}
	run, cancel := context.WithDeadline(ctx, expiry)
	r := &sshRuntime{ctx: run, cancel: cancel, request: q, execution: e, channel: ch, account: account, outputDone: make(chan struct{})}
	r.stopClose = context.AfterFunc(run, r.closeFiles)
	return r, nil
}
func (r *sshRuntime) closeFiles() {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, f := range r.all {
			_ = f.Close()
		}
	})
}
func (r *sshRuntime) closeAll() {
	r.cancel()
	r.closeFiles()
	if r.stopClose != nil {
		r.stopClose()
	}
}
func (r *sshRuntime) check() error { return r.ctx.Err() }

type sshChildFiles struct{ r *sshRuntime }

func (c sshChildFiles) Close() error {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	for _, f := range c.r.child {
		_ = f.Close()
	}
	if c.r.input != nil {
		_ = c.r.input.Close()
	}
	return nil
}
func (r *sshRuntime) prepare(cmd *exec.Cmd) (io.Closer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	cmd.Env = []string{"HOME=" + r.account.HomeDir, "USER=" + r.account.Username, "LOGNAME=" + r.account.Username, "SHELL=/bin/sh", "PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"}
	if t := r.execution.Terminal; t != nil {
		if t.Name == "" || len(t.Name) > 128 || t.Window.Columns < 1 || t.Window.Columns > 1000 || t.Window.Rows < 1 || t.Window.Rows > 1000 || t.Window.WidthPixels > 16384 || t.Window.HeightPixels > 16384 {
			return nil, model.Err("invalid_request", "invalid SSH terminal dimensions")
		}
		master, slave, err := pty.Open()
		if err != nil {
			return nil, err
		}
		r.all = append(r.all, master, slave)
		r.child = append(r.child, slave)
		r.master = master
		if err = pty.Setsize(master, &pty.Winsize{Cols: uint16(t.Window.Columns), Rows: uint16(t.Window.Rows), X: uint16(t.Window.WidthPixels), Y: uint16(t.Window.HeightPixels)}); err != nil {
			return nil, err
		}
		if err = applySSHTerminalModes(slave, t.Modes); err != nil {
			return nil, err
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.Env = append(cmd.Env, "TERM="+t.Name)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	} else {
		inR, inW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		r.all = append(r.all, inR, inW)
		outR, outW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		r.all = append(r.all, outR, outW)
		errR, errW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		r.all = append(r.all, errR, errW)
		r.child = append(r.child, inR, outW, errW)
		r.input = inW
		r.stdout = outR
		r.stderr = errR
		cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	return sshChildFiles{r}, nil
}
func (r *sshRuntime) startCopies() {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Only retained child descriptors close here; stdin's parent writer stays
	// open until client EOF or command completion.
	for _, f := range r.child {
		_ = f.Close()
	}
	if r.master != nil {
		go func() { _, _ = io.Copy(r.master, r.channel) }()
		go func() { defer close(r.outputDone); _, _ = io.Copy(r.channel, r.master) }()
	} else {
		go func() { _, _ = io.Copy(r.input, r.channel); _ = r.input.Close() }()
		go func() {
			defer close(r.outputDone)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _, _ = io.Copy(r.channel, r.stdout) }()
			go func() { defer wg.Done(); _, _ = io.Copy(r.channel.Stderr(), r.stderr) }()
			wg.Wait()
		}()
	}
}

type sshProcess struct {
	manager *Manager
	lease   string
	runtime *sshRuntime
	job     *job
}

func (p *sshProcess) Wait(ctx context.Context) (sshservice.Exit, error) {
	defer p.runtime.closeAll()
	select {
	case <-ctx.Done():
		return sshservice.Exit{}, ctx.Err()
	case <-p.job.done:
	}
	select {
	case <-ctx.Done():
		return sshservice.Exit{}, ctx.Err()
	case <-p.runtime.ctx.Done():
		return sshservice.Exit{}, p.runtime.ctx.Err()
	case <-p.runtime.outputDone:
	}
	state := p.job.cmd.ProcessState
	if state == nil {
		return sshservice.Exit{}, fmt.Errorf("SSH process state unknown")
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		for name, sig := range sshSignals {
			if sig == status.Signal() {
				return sshservice.Exit{Signal: name}, nil
			}
		}
	}
	code := state.ExitCode()
	if code < 0 {
		code = 255
	}
	return sshservice.Exit{Code: uint32(code)}, nil
}

var sshSignals = map[string]syscall.Signal{"HUP": syscall.SIGHUP, "INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT, "TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL, "USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2, "WINCH": syscall.SIGWINCH}

func (p *sshProcess) owned() error {
	if p.manager.jobs[p.lease] != p.job || p.job.identity == nil {
		return model.Err("lease_inactive", "SSH process is no longer owned")
	}
	actual, err := usage.Capture(p.job.cmd.Process.Pid)
	if err != nil || actual != *p.job.identity {
		return model.Err("observation_unavailable", "SSH native process identity changed or is unavailable")
	}
	return nil
}
func (p *sshProcess) Signal(ctx context.Context, name string) error {
	sig, ok := sshSignals[name]
	if !ok {
		return model.Err("invalid_request", "unsupported SSH signal")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.manager.mu.Lock()
	defer p.manager.mu.Unlock()
	if err := p.owned(); err != nil {
		return err
	}
	return syscall.Kill(-p.job.cmd.Process.Pid, sig)
}
func (p *sshProcess) Resize(ctx context.Context, w sshservice.Window) error {
	if w.Columns < 1 || w.Columns > 1000 || w.Rows < 1 || w.Rows > 1000 || w.WidthPixels > 16384 || w.HeightPixels > 16384 {
		return model.Err("invalid_request", "invalid SSH terminal dimensions")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.manager.mu.Lock()
	defer p.manager.mu.Unlock()
	if err := p.owned(); err != nil {
		return err
	}
	p.runtime.mu.Lock()
	defer p.runtime.mu.Unlock()
	if p.runtime.master == nil {
		return errors.New("SSH execution has no terminal")
	}
	return pty.Setsize(p.runtime.master, &pty.Winsize{Cols: uint16(w.Columns), Rows: uint16(w.Rows), X: uint16(w.WidthPixels), Y: uint16(w.HeightPixels)})
}

type sshNoopCloser struct{}

func (sshNoopCloser) Close() error { return nil }
