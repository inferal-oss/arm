// Package gitmeta reads bounded local repository metadata for display grouping
// and explicitly reviewed onboarding bindings. It never reads an index, runs
// project code, queries a remote or starts an agent/client service.
package gitmeta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const OperationTimeout = 15 * time.Second
const ProbeTimeout = 3 * time.Second
const MaxProbes = 64
const MaxOutputBytes = 1 << 20
const MaxGitBindings = 32

var ErrBudget = errors.New("Git metadata operation budget exhausted")
var ErrUnsupported = errors.New("Git metadata is unavailable or unsupported")
var ErrOutput = errors.New("Git metadata output limit reached")
var ErrChanged = errors.New("Git metadata directory changed")

type Usage struct {
	Probes int
	Bytes  int64
}

// Budget is shared by every normalization pass in one operation. Failed probes
// and all observed bytes are charged too; a refusal never resets its allowance.
// Limits are refusal thresholds, not kernel bounds or filesystem snapshots.
type Budget struct {
	started time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	usage   Usage
	err     error
}

func NewBudget(parent context.Context) *Budget {
	started := time.Now()
	ctx, cancel := context.WithDeadline(parent, started.Add(OperationTimeout))
	return &Budget{started: started, ctx: ctx, cancel: cancel}
}

func (b *Budget) Close()                   { b.cancel() }
func (b *Budget) Context() context.Context { return b.ctx }

func (b *Budget) Check() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return errors.Join(b.err, b.ctx.Err())
}

func (b *Budget) Usage() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}

func (b *Budget) probe() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := errors.Join(b.err, b.ctx.Err()); err != nil {
		return err
	}
	if b.usage.Probes >= MaxProbes {
		b.err = ErrBudget
		b.cancel()
		return b.err
	}
	b.usage.Probes++
	return nil
}

func (b *Budget) charge(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usage.Bytes += int64(n)
	if b.usage.Bytes > MaxOutputBytes {
		b.err = ErrBudget
		b.cancel()
	}
	return errors.Join(b.err, b.ctx.Err())
}

type boundedOutput struct {
	budget *Budget
	limit  int
	cancel context.CancelFunc
	data   bytes.Buffer
	err    error
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	charged := w.budget.charge(len(p))
	remaining := w.limit - w.data.Len()
	kept := min(len(p), max(0, remaining))
	_, _ = w.data.Write(p[:kept])
	if len(p) > remaining {
		w.err = ErrOutput
	}
	w.err = errors.Join(w.err, charged)
	if w.err != nil {
		if w.cancel != nil {
			w.cancel()
		}
		return kept, w.err
	}
	return len(p), nil
}

type Directory struct {
	Path     string
	Identity string
}

type Info struct {
	Root   Directory
	Git    Directory
	Common Directory
}

// Primary is established by the probe's actual top-level/git/common outputs,
// including separate-Git-dir repositories. It never trims a /.git path.
func (i Info) Primary() bool {
	return i.Git.Path == i.Common.Path && i.Git.Identity == i.Common.Identity
}

type Resolver struct {
	budget *Budget
	binary string
}

func NewResolver(budget *Budget) (*Resolver, error) {
	if err := budget.Check(); err != nil {
		return nil, err
	}
	binary, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrUnsupported
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, ErrUnsupported
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return nil, ErrUnsupported
	}
	return &Resolver{budget: budget, binary: binary}, nil
}

func safePath(path string) bool {
	return filepath.IsAbs(path) && len(path) <= 4096 && !strings.ContainsFunc(path, func(r rune) bool { return r < 32 || r == 127 })
}

func inspect(path string) (Directory, os.FileInfo, error) {
	if !safePath(path) {
		return Directory{}, nil, ErrUnsupported
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !safePath(resolved) {
		return Directory{}, nil, ErrUnsupported
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return Directory{}, nil, ErrUnsupported
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Directory{}, nil, ErrUnsupported
	}
	return Directory{Path: resolved, Identity: fmt.Sprintf("%d:%d", identity.Dev, identity.Ino)}, info, nil
}

func environment() []string {
	var out []string
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_") {
			out = append(out, item)
		}
	}
	return append(out, "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (r *Resolver) Resolve(path string) (Info, error) {
	if err := r.budget.Check(); err != nil {
		return Info{}, err
	}
	selected, before, err := inspect(path)
	if err != nil {
		return Info{}, err
	}
	if err = r.budget.probe(); err != nil {
		return Info{}, err
	}
	ctx, cancel := context.WithTimeout(r.budget.Context(), ProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.binary, "-c", "core.fsmonitor=false", "-c", "core.hooksPath="+os.DevNull, "-C", selected.Path, "rev-parse", "--path-format=absolute", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	cmd.Dir = selected.Path
	cmd.Env = environment()
	stdout := &boundedOutput{budget: r.budget, limit: 16 << 10, cancel: cancel}
	stderr := &boundedOutput{budget: r.budget, limit: 4 << 10, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 100 * time.Millisecond
	// Run owns and joins the original process and os/exec's two copy goroutines.
	// WaitDelay/stream/cancellation errors refuse the result; none prove EOF.
	runErr := cmd.Run()
	if runErr != nil || stdout.err != nil || stderr.err != nil {
		return Info{}, errors.Join(ErrUnsupported, stdout.err, stderr.err, ctx.Err(), r.budget.Check())
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		return Info{}, ErrUnsupported
	}
	if err = r.budget.Check(); err != nil {
		return Info{}, err
	}
	lines := strings.Split(strings.TrimSuffix(stdout.data.String(), "\n"), "\n")
	if len(lines) != 3 {
		return Info{}, ErrUnsupported
	}
	root, _, err := inspect(lines[0])
	if err != nil {
		return Info{}, err
	}
	git, _, err := inspect(lines[1])
	if err != nil {
		return Info{}, err
	}
	common, _, err := inspect(lines[2])
	if err != nil {
		return Info{}, err
	}
	after, err := os.Stat(selected.Path)
	if err != nil || !os.SameFile(before, after) || !within(root.Path, selected.Path) {
		return Info{}, ErrChanged
	}
	if err = r.budget.Check(); err != nil {
		return Info{}, err
	}
	return Info{Root: root, Git: git, Common: common}, nil
}
