package resident

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type receipt struct {
	Resident  Identity        `json:"resident"`
	Principal Principal       `json:"principal"`
	Key       string          `json:"key"`
	Operation string          `json:"operation"`
	SHA256    string          `json:"request_sha256"`
	State     string          `json:"state"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Failure        `json:"error,omitempty"`
}

// The separate private fixture journal changes no Manager schema. Pending is
// persisted before dispatch; failure after an effect never authorizes replay.
type journal struct {
	dir      *os.File
	lock     *os.File
	path     string
	identity Identity
	receipts map[string]receipt
}

func openJournal(path string, identity Identity) (*journal, error) {
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil || physical != abs {
		return nil, failure("journal_directory_invalid")
	}
	fd, err := unix.Open(abs, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	j := &journal{dir: os.NewFile(uintptr(fd), abs), path: abs, identity: identity, receipts: map[string]receipt{}}
	fail := func(err error) (*journal, error) { _ = j.close(); return nil, err }
	info, err := j.dir.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fail(failure("journal_directory_invalid"))
	}
	lock, err := unix.Openat(fd, "writer.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fail(err)
	}
	j.lock = os.NewFile(uintptr(lock), "writer.lock")
	lockInfo, err := j.lock.Stat()
	if err != nil || !privateRegular(lockInfo) {
		return fail(failure("journal_lock_invalid"))
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(failure("journal_busy"))
	}
	raw, err := j.read("resident.json", 4096)
	if errors.Is(err, os.ErrNotExist) {
		if err = j.write("resident.json", identity, true); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	} else {
		var saved Identity
		if decode(raw, &saved) != nil || saved != identity {
			return fail(failure("resident_identity_changed"))
		}
	}
	entries, err := j.dir.ReadDir(MaxReceipts + MaxGenerations + 4)
	if err != nil {
		return fail(err)
	}
	if more, nextErr := j.dir.ReadDir(1); nextErr != io.EOF || len(more) != 0 {
		return fail(failure("journal_limit"))
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "writer.lock" || name == "resident.json" {
			continue
		}
		// Native worker continuation is intentionally not implemented. Retain
		// every sealed/active generation rather than resurrecting an old epoch.
		if strings.HasPrefix(name, "worker-") {
			return fail(failure("worker_continuation_unsupported"))
		}
		if !strings.HasPrefix(name, "receipt-") || !strings.HasSuffix(name, ".json") {
			return fail(failure("journal_unknown_entry"))
		}
		raw, err := j.read(name, MaxResponse+MaxRequest)
		if err != nil {
			return fail(err)
		}
		var r receipt
		if decode(raw, &r) != nil || r.Resident != identity || !validPrincipal(r.Principal, identity) || r.Key == "" || len(r.Key) > 128 || !digestID.MatchString(r.SHA256) || !validReceiptOutcome(r) || name != receiptName(r.Principal, r.Key) {
			return fail(failure("journal_invalid"))
		}
		j.receipts[name] = r
		if len(j.receipts) > MaxReceipts {
			return fail(failure("journal_limit"))
		}
	}
	return j, nil
}

func validReceiptOutcome(r receipt) bool {
	if r.Operation != "run" && r.Operation != "renew" && r.Operation != "end" {
		return false
	}
	switch r.State {
	case "pending":
		return len(r.Result) == 0 && r.Error == nil
	case "complete":
		return r.Error == nil && len(r.Result) <= MaxResponse/2 && json.Valid(r.Result)
	case "unknown":
		return len(r.Result) == 0 && r.Error != nil && r.Error.Code == "effect_unknown"
	default:
		return false
	}
}

func privateRegular(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && info.Sys().(*syscall.Stat_t).Uid == uint32(os.Geteuid())
}

func (j *journal) validate() error {
	held, err := j.dir.Stat()
	named, namedErr := os.Lstat(j.path)
	if err != nil || namedErr != nil || !named.IsDir() || !os.SameFile(held, named) || named.Mode().Perm()&0077 != 0 {
		return failure("journal_directory_changed")
	}
	return nil
}

func (j *journal) read(name string, maximum int) ([]byte, error) {
	if err := j.validate(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(j.dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !privateRegular(info) || info.Size() < 1 || info.Size() > int64(maximum) {
		return nil, failure("journal_file_invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(maximum)+1))
	if len(raw) > maximum {
		return nil, failure("journal_limit")
	}
	return raw, err
}

func (j *journal) write(name string, value any, exclusive bool) error {
	if err := j.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > MaxResponse+MaxRequest {
		return failure("journal_limit")
	}
	staged := name
	if !exclusive {
		staged = ".pending-" + secret()
	}
	fd, err := unix.Openat(int(j.dir.Fd()), staged, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), staged)
	if !exclusive {
		defer unix.Unlinkat(int(j.dir.Fd()), staged, 0)
	}
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if !exclusive {
		if err = unix.Renameat(int(j.dir.Fd()), staged, int(j.dir.Fd()), name); err != nil {
			return err
		}
	}
	return j.dir.Sync()
}

func (j *journal) close() error {
	var err error
	if j.lock != nil {
		err = errors.Join(unix.Flock(int(j.lock.Fd()), unix.LOCK_UN), j.lock.Close())
	}
	if j.dir != nil {
		err = errors.Join(err, j.dir.Close())
	}
	return err
}

func (j *journal) worker(worker WorkerIdentity, state string, exclusive bool) error {
	return j.write("worker-"+worker.Generation+".json", struct {
		Resident Identity       `json:"resident"`
		Worker   WorkerIdentity `json:"worker"`
		State    string         `json:"state"`
	}{j.identity, worker, state}, exclusive)
}

func (j *journal) put(r receipt, exclusive bool) error {
	name := receiptName(r.Principal, r.Key)
	if _, ok := j.receipts[name]; !ok && len(j.receipts) >= MaxReceipts {
		return failure("journal_limit")
	}
	if err := j.write(name, r, exclusive); err != nil {
		return fmt.Errorf("receipt persistence uncertain: %w", err)
	}
	j.receipts[name] = r
	return nil
}
