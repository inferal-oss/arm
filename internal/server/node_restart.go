package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/nodestop"
	"github.com/inferal-oss/arm/internal/storage"
	"golang.org/x/sys/unix"
)

// NodeRestartReview owns the actual existing authority lock across a concrete
// local review. Neither serialized timestamps nor an arbitrary Restart value
// can manufacture this object. No backend/native child is launched by it.
type NodeRestartReview struct {
	mu                        sync.Mutex
	directory                 *database.Directory
	lock                      *os.File
	home                      string
	expected                  nodestop.State
	review                    nodestop.Restart
	elapsed                   *nodestop.ElapsedReview
	attempted, closed, locked bool
	priorPhase, ownerUsername string
	closeError                error
}

type NodeRestartPlan struct {
	Operation     string           `json:"operation"`
	Review        nodestop.Restart `json:"review"`
	OwnerUsername string           `json:"owner_username"`
	PriorPhase    string           `json:"prior_phase"`
	Warning       string           `json:"warning"`
}

// PrepareNodeRestart creates no missing home/lock/database/schema. An absent or
// running intent needs ordinary startup, not a fabricated restart approval.
func PrepareNodeRestart(ctx context.Context, home, reason string) (review *NodeRestartReview, needed bool, resultErr error) {
	info, err := os.Lstat(home)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("restart authority must be a real private directory, not a symlink")
	}
	physical, err := filepath.EvalSymlinks(home)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		return nil, false, err
	}
	dir, err := database.OpenDirectory(physical)
	if err != nil {
		return nil, false, err
	}
	r := &NodeRestartReview{home: physical, directory: dir}
	defer func() {
		if review == nil {
			resultErr = errors.Join(resultErr, r.Close())
		}
	}()
	lock, _, err := dir.Open("daemon.lock", unix.O_RDONLY)
	if err != nil {
		return nil, false, err
	}
	if lock == nil {
		main, err := dir.Observe("state.db", false)
		if err != nil {
			return nil, false, err
		}
		if main.Present {
			return nil, false, fmt.Errorf("existing database has no retained authority lock; explicit inspection required")
		}
		return nil, false, nil
	}
	r.lock = lock
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, false, fmt.Errorf("authority is busy; no restart review prepared: %w", err)
	}
	r.locked = true
	source, present, err := database.OpenStartupRead(ctx, physical, lock)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, source.Close())
		if resultErr != nil && review != nil {
			resultErr = errors.Join(resultErr, r.Close())
			review = nil
		}
	}()
	if !present {
		return nil, false, nil
	}
	state, stopped, err := nodestop.ReadState(source)
	if err != nil {
		return nil, false, err
	}
	if !stopped || state.Running {
		return nil, false, nil
	}
	id, err := storage.DirectoryIdentity(int(dir.FD.Fd()))
	if err != nil {
		return nil, false, err
	}
	var host string
	if e := source.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&host); e != nil || host != state.Target.Host || state.Target.Directory != id || state.Target.Home != physical || state.Target.Owner != strconv.Itoa(os.Geteuid()) {
		return nil, false, fmt.Errorf("stopped intent differs from exact native authority")
	}
	record, err := nodestop.Lookup(source, state.Key)
	if err != nil {
		return nil, false, err
	}
	r.elapsed, err = nodestop.NewElapsedReview()
	if err != nil {
		return nil, false, err
	}
	r.review, err = nodestop.ReviewRestart(state, record, reason, time.Now())
	if err != nil {
		return nil, true, err
	}
	r.expected = state
	r.priorPhase = record.Phase
	identity := local.CurrentIdentity()
	if identity.UID == state.Target.Owner {
		r.ownerUsername = identity.Username
	}
	if err = source.Validate(ctx); err != nil {
		return nil, true, err
	}
	traceRestartReview(r, "prepared", nil)
	return r, true, nil
}

func (r *NodeRestartReview) Plan() (out NodeRestartPlan, resultErr error) {
	if r == nil {
		return NodeRestartPlan{}, fmt.Errorf("retained local restart review required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() { traceRestartReview(r, "plan", resultErr) }()
	if r.closed || r.attempted {
		return NodeRestartPlan{}, fmt.Errorf("restart review is closed or already attempted")
	}
	if err := r.elapsed.Check(); err != nil {
		return NodeRestartPlan{}, err
	}
	return NodeRestartPlan{Operation: "restart-arm-authority", Review: r.review, OwnerUsername: r.ownerUsername, PriorPhase: r.priorPhase, Warning: "Starts a fresh backend generation. Earlier unresolved stop duties, resource obligations and audit history remain. This changes desired state before backend launch; a launch failure does not roll it back."}, nil
}

// Apply performs the one exact reviewed CAS under the retained original lock.
// Any attempted write, including unknown Commit, consumes the review locally.
func (r *NodeRestartReview) Apply(ctx context.Context) (resultErr error) {
	if r == nil {
		return fmt.Errorf("retained local restart review required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() { traceRestartReview(r, "apply", resultErr) }()
	if r.closed || r.attempted {
		return fmt.Errorf("restart was already attempted or closed; inspect current intent")
	}
	if err := r.elapsed.Check(); err != nil {
		return err
	}
	if err := r.directory.Validate(); err != nil {
		return err
	}
	source, present, err := database.OpenStartupRead(ctx, r.home, r.lock)
	if err != nil {
		return err
	}
	current, exists, readErr := nodestop.ReadState(source)
	closeErr := source.Close()
	if !present || !exists || current != r.expected || readErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("stopped intent changed before reviewed restart"), readErr, closeErr)
	}
	if err = r.elapsed.Check(); err != nil {
		return err
	}
	r.attempted = true
	return applyNodeStopRestartWithin(ctx, r.home, r.expected, r.review, r.elapsed)
}
func (r *NodeRestartReview) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.closeError
	}
	r.closed = true
	if r.lock != nil {
		if r.locked {
			r.closeError = errors.Join(r.closeError, unix.Flock(int(r.lock.Fd()), unix.LOCK_UN))
		}
		r.closeError = errors.Join(r.closeError, r.lock.Close())
		r.lock = nil
	}
	if r.directory != nil {
		r.closeError = errors.Join(r.closeError, r.directory.Close())
		r.directory = nil
	}
	return r.closeError
}
