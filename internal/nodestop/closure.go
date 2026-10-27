package nodestop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/storage"
	"golang.org/x/sys/unix"
)

var closureStages = [...]string{"http", "endpoint", "monitor", "lifecycle", "operator", "workspace", "mesh", "manager"}

// Closure records explicit stage completion, not a transport/PID inference. It
// is lifetime-local and called serially by Serve's teardown. A failure is sticky.
type Closure struct {
	record  Record
	next    int
	failure error
}

type Closed struct {
	Schema string             `json:"schema"`
	Lock   model.FileIdentity `json:"daemon_lock"`
	Record Record             `json:"record"`
	Stages []string           `json:"joined_stages"`
	At     time.Time          `json:"closed_at"`
}

func NewClosure(record Record) (*Closure, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	if record.Phase != "drain-ready" {
		return nil, fmt.Errorf("a joined close requires the original drain-ready operation")
	}
	record.Plan = clonePlan(record.Plan)
	return &Closure{record: record}, nil
}

func (c *Closure) Stage(name string, err error) {
	if c == nil {
		return
	}
	if c.next >= len(closureStages) || closureStages[c.next] != name {
		c.failure = errors.Join(c.failure, fmt.Errorf("unexpected/duplicate close stage %q", name))
		return
	}
	c.next++
	if err != nil {
		c.failure = errors.Join(c.failure, fmt.Errorf("%s: %w", name, err))
	}
}

func (c *Closure) Error() error {
	if c == nil {
		return fmt.Errorf("no original node-stop closure")
	}
	if c.next != len(closureStages) {
		return errors.Join(c.failure, fmt.Errorf("unjoined close stages"))
	}
	return c.failure
}

// WriteClosed is called after all stages (including SQLite Close) while the
// ORIGINAL directory and daemon-lock descriptors are still retained. It uses
// O_EXCL/openat and never replaces an earlier marker. A partial/write/fsync
// failure is retained as incomplete evidence and never a stopped verdict.
func (c *Closure) WriteClosed(directory, lock *os.File) (resultErr error) {
	var completed *Closed
	defer func() { traceClosure(c, completed, resultErr) }()
	if err := c.Error(); err != nil {
		return err
	}
	if directory == nil || lock == nil {
		return fmt.Errorf("original directory and lock required")
	}
	target := c.record.Plan.Target
	id, err := storage.DirectoryIdentity(int(directory.Fd()))
	if err != nil || id != target.Directory {
		return fmt.Errorf("original home directory changed: %w", err)
	}
	var held, named unix.Stat_t
	if err = unix.Fstat(int(lock.Fd()), &held); err != nil {
		return err
	}
	if err = unix.Fstatat(int(directory.Fd()), "daemon.lock", &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if held.Dev != named.Dev || held.Ino != named.Ino || held.Uid != named.Uid || held.Mode != named.Mode || held.Uid != target.Directory.Owner || held.Mode&unix.S_IFMT != unix.S_IFREG || held.Mode&0077 != 0 {
		return fmt.Errorf("retained daemon lock identity changed")
	}
	lockID, err := storage.DirectoryIdentity(int(lock.Fd()))
	if err != nil {
		return err
	}
	closed := Closed{Lock: lockID, Schema: "arm.node-stop.closed/v1", Record: c.record, Stages: append([]string{}, closureStages[:]...), At: time.Now().UTC()}
	b, err := json.Marshal(closed)
	if err != nil || len(b) > 64<<10 {
		return fmt.Errorf("invalid bounded closure marker: %w", err)
	}
	fd, err := unix.Openat(int(directory.Fd()), "node-stop-closed-"+target.Generation+".json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "node-stop-generation-marker")
	n, writeErr := f.Write(b)
	if writeErr == nil && n != len(b) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	err = errors.Join(writeErr, f.Close())
	if err == nil {
		err = directory.Sync()
	}
	if err == nil {
		completed = &closed
	}
	return err
}

func (c Closed) Validate(record Record, lockID model.FileIdentity) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if c.Schema != "arm.node-stop.closed/v1" || c.At.IsZero() || c.At.Before(record.Updated) || c.Lock != lockID || !lockID.BirthKnown || c.Record.Phase != "drain-ready" || c.Record.Digest != record.Digest || c.Record.Shutdown != record.Shutdown || c.Record.DesiredRevision != record.DesiredRevision || c.Record.Plan.Target != record.Plan.Target || len(c.Stages) != len(closureStages) {
		return fmt.Errorf("closed marker lacks exact original generation/lock/join evidence")
	}
	if err := c.Record.Validate(); err != nil {
		return err
	}
	left, _ := json.Marshal(c.Record)
	right, _ := json.Marshal(record)
	if !bytes.Equal(left, right) {
		return fmt.Errorf("closed marker differs from exact durable drain-ready record")
	}
	for i, stage := range c.Stages {
		if stage != closureStages[i] {
			return fmt.Errorf("incomplete/reordered joined stages")
		}
	}
	return nil
}
