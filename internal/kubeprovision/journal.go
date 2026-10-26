package kubeprovision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

var ErrJournalConflict = errors.New("Kubernetes operation revision changed")

// FileJournal is a private, fsynced source journal. It contains no kubeconfig
// credentials. One process-wide advisory lock serializes all keys in this
// private directory; other writers must use this implementation as well.
type FileJournal struct{ Root string }

func validRecord(r Record, key string) bool {
	if r.Plan.Review.Key != key || r.Plan.Validate() != nil || r.Revision == 0 {
		return false
	}
	if r.Phase != PhaseCreated {
		return r.Pod == nil && r.BinaryPhase == "" && r.StartPhase == "" && (r.Phase == PhasePreflight && r.Revision == 1 || r.Phase == PhaseCreateAttempted || r.Phase == PhaseUnknown || r.Phase == PhaseConflict)
	}
	if r.Pod == nil || checkPod(r.Plan, *r.Pod) != nil {
		return false
	}
	if r.BinaryPhase != "" && r.BinaryPhase != "ship_attempted" && r.BinaryPhase != "binary_verified" {
		return false
	}
	if r.StartPhase != "" && r.StartPhase != "start_attempted" && r.StartPhase != "marker_verified" {
		return false
	}
	return r.StartPhase == "" || r.BinaryPhase == "binary_verified"
}

func (j FileJournal) openDir(create bool) (int, error) {
	if create {
		if err := os.Mkdir(j.Root, 0700); err != nil && !os.IsExist(err) {
			return -1, err
		}
	}
	fd, err := unix.Open(j.Root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0077 != 0 || st.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return -1, errors.New("Kubernetes journal directory must be private and owned by caller")
	}
	return fd, nil
}

func (j FileJournal) withLock(ctx context.Context, fn func(int) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dirfd, err := j.openDir(true)
	if err != nil {
		return err
	}
	defer unix.Close(dirfd)
	fd, err := unix.Openat(dirfd, ".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), ".lock")
	defer lock.Close()
	var lockStat unix.Stat_t
	if err := unix.Fstat(fd, &lockStat); err != nil {
		return err
	}
	if lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Uid != uint32(os.Geteuid()) || lockStat.Nlink != 1 || lockStat.Mode&0077 != 0 {
		return errors.New("unsafe Kubernetes journal lock")
	}
	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			break
		} else if err != unix.EWOULDBLOCK {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	return fn(dirfd)
}

func (j FileJournal) path(key string) (string, error) {
	if !digestPattern.MatchString(key) {
		return "", errors.New("invalid Kubernetes operation key")
	}
	return filepath.Join(j.Root, key+".json"), nil
}

func (j FileJournal) readLocked(dirfd int, key string) (Record, bool, error) {
	var r Record
	path, err := j.path(key)
	if err != nil {
		return r, false, err
	}
	fd, err := unix.Openat(dirfd, filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err == unix.ENOENT {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return r, false, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || st.Size > 65536 || st.Size < 1 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return r, false, errors.New("unsafe Kubernetes journal record")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return r, false, err
	}
	if len(raw) > 65536 {
		return r, false, errors.New("Kubernetes journal record exceeds bound")
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, false, err
	}
	if !validRecord(r, key) {
		return r, false, errors.New("invalid Kubernetes journal intent")
	}
	return r, true, nil
}

func (j FileJournal) writeLocked(dirfd int, r Record) error {
	path, err := j.path(r.Plan.Review.Key)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 65536 {
		return errors.New("Kubernetes journal record exceeds bound")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := ".record-" + hex.EncodeToString(nonce[:])
	fd, err := unix.Openat(dirfd, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	temp := os.NewFile(uintptr(fd), name)
	defer unix.Unlinkat(dirfd, name, 0)
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(dirfd, name, dirfd, filepath.Base(path)); err != nil {
		return err
	}
	return unix.Fsync(dirfd)
}

func (j FileJournal) Read(ctx context.Context, key string) (Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, false, err
	}
	dirfd, err := j.openDir(false)
	if errors.Is(err, unix.ENOENT) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	defer unix.Close(dirfd)
	return j.readLocked(dirfd, key)
}

func (j FileJournal) Insert(ctx context.Context, r Record) error {
	if !validRecord(r, r.Plan.Review.Key) || r.Phase != PhasePreflight {
		return errors.New("invalid initial Kubernetes intent")
	}
	return j.withLock(ctx, func(dirfd int) error {
		_, ok, err := j.readLocked(dirfd, r.Plan.Review.Key)
		if err != nil {
			return err
		}
		if ok {
			return ErrJournalConflict
		}
		return j.writeLocked(dirfd, r)
	})
}

func (j FileJournal) Update(ctx context.Context, key string, revision uint64, change func(*Record) error) (Record, error) {
	var result Record
	err := j.withLock(ctx, func(dirfd int) error {
		r, ok, err := j.readLocked(dirfd, key)
		if err != nil {
			return err
		}
		if !ok || r.Revision != revision {
			return ErrJournalConflict
		}
		before := r.Plan.SHA256
		if err := change(&r); err != nil {
			return err
		}
		if r.Plan.SHA256 != before || !validRecord(r, key) || r.Revision == ^uint64(0) {
			return errors.New("Kubernetes journal intent changed or full")
		}
		r.Revision++
		r.Updated = time.Now().UTC()
		if err := j.writeLocked(dirfd, r); err != nil {
			return fmt.Errorf("Kubernetes journal commit: %w", err)
		}
		result = r
		return nil
	})
	return result, err
}
