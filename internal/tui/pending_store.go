package tui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

const pendingDirectory = "dashboard-pending"
const pendingRecordLimit = 64 << 10
const pendingRequestLimit = 60 << 10
const pendingCountLimit = 64

type pendingRecord struct {
	Attempts  uint64          `json:"attempts"`
	Version   int             `json:"version"`
	Kind      string          `json:"kind"`
	Authority string          `json:"authority"`
	Created   time.Time       `json:"created_at"`
	Request   json.RawMessage `json:"request"`
}

func newPendingRecord(kind, authority string, request any) (pendingRecord, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return pendingRecord{}, err
	}
	r := pendingRecord{Attempts: 1, Version: 1, Kind: kind, Authority: authority, Created: time.Now().UTC(), Request: raw}
	return r, r.validate()
}
func decodePending(raw []byte) (pendingRecord, error) {
	var r pendingRecord
	if len(raw) > pendingRecordLimit {
		return r, fmt.Errorf("pending record exceeds 64 KiB")
	}
	if err := strictPendingJSON(raw, &r); err != nil {
		return r, err
	}
	return r, r.validate()
}
func strictPendingJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing pending record data")
	}
	return nil
}
func (r pendingRecord) validate() error {
	if r.Version != 1 || r.Attempts == 0 || r.Attempts > 1000000 || r.Authority == "" || len(r.Authority) > 256 || !poolPrintable(r.Authority) || r.Created.IsZero() {
		return fmt.Errorf("unsupported or invalid pending record schema/authority")
	}
	if len(r.Request) > pendingRequestLimit {
		return fmt.Errorf("exact request exceeds 60 KiB; backend frames are limited to 64 KiB, nothing was sent or truncated")
	}
	key := ""
	switch r.Kind {
	case "pool":
		var q model.PoolMutation
		if err := strictPendingJSON(r.Request, &q); err != nil {
			return err
		}
		key = q.Key
		if q.Reviewed == nil || q.Reviewed.Authority != r.Authority || q.Reviewed.Consumers == nil || len(q.Reviewed.Consumers) > 8192 || strings.TrimSpace(q.Reason) == "" || len(q.Reason) > 4096 {
			return fmt.Errorf("invalid exact reviewed pool request")
		}
		switch q.Operation {
		case "create", "set", "adjust", "drain", "resume", "delete":
		default:
			return fmt.Errorf("unsupported pending pool operation")
		}
	case "message":
		var q manager.MessageRequest
		if err := strictPendingJSON(r.Request, &q); err != nil {
			return err
		}
		key = q.Key
		if q.Audience == "" || q.Audience == "operator" || strings.TrimSpace(q.Body) == "" || len(q.Body) > 16384 || len(q.Targets) == 0 || len(q.Targets) > 128 {
			return fmt.Errorf("invalid exact reviewed operator message")
		}
	case "mesh":
		var q meshIntent
		if err := strictPendingJSON(r.Request, &q); err != nil {
			return err
		}
		if err := q.validate(); err != nil {
			return err
		}
		key = q.Key
	default:
		return fmt.Errorf("unknown pending operation kind")
	}
	if key == "" || len(key) > 128 {
		return fmt.Errorf("pending request key must contain 1..128 bytes")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(raw) > pendingRecordLimit {
		return fmt.Errorf("pending envelope exceeds 64 KiB")
	}
	return nil
}
func (r pendingRecord) key() string {
	if r.Kind == "mesh" {
		var q meshIntent
		json.Unmarshal(r.Request, &q)
		return q.Key
	}
	var q struct {
		Key string `json:"idempotency_key"`
	}
	json.Unmarshal(r.Request, &q)
	return q.Key
}
func (r pendingRecord) name() string {
	sum := sha256.Sum256([]byte(r.Kind + "\x00" + r.Authority + "\x00" + r.key()))
	return "op-" + hex.EncodeToString(sum[:]) + ".json"
}
func samePending(a, b pendingRecord) bool {
	if a.Kind != b.Kind || a.Authority != b.Authority || a.Version != b.Version {
		return false
	}
	switch a.Kind {
	case "pool":
		var x, y model.PoolMutation
		if json.Unmarshal(a.Request, &x) != nil || json.Unmarshal(b.Request, &y) != nil {
			return false
		}
		return samePoolRequest(x, y)
	case "message":
		var x, y manager.MessageRequest
		if json.Unmarshal(a.Request, &x) != nil || json.Unmarshal(b.Request, &y) != nil {
			return false
		}
		return reflect.DeepEqual(x, y)
	case "mesh":
		var x, y meshIntent
		if json.Unmarshal(a.Request, &x) != nil || json.Unmarshal(b.Request, &y) != nil {
			return false
		}
		return reflect.DeepEqual(x, y)
	}
	return false
}

type pendingFiles struct {
	parent, root *os.Root
	lock         *os.File
	identity     os.FileInfo
}

func privatePending(info os.FileInfo, limit int64) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid()) && s.Nlink == 1 && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && info.Size() <= limit
}
func syncPendingDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func openPending(home string) (*pendingFiles, error) {
	if err := local.CheckStateFence(home); err != nil {
		return nil, err
	}
	parent, err := trustedClientRoot(home)
	if err != nil {
		return nil, err
	}
	h := &pendingFiles{parent: parent}
	fail := func(e error) (*pendingFiles, error) { h.close(); return nil, e }
	if err = parent.Mkdir(pendingDirectory, 0700); err == nil {
		if err = syncPendingDirectory(parent); err != nil {
			return fail(err)
		}
	} else if !os.IsExist(err) {
		return fail(err)
	}
	info, err := parent.Lstat(pendingDirectory)
	if err != nil {
		return fail(err)
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fail(fmt.Errorf("pending directory must be private, same-user and real"))
	}
	h.root, err = parent.OpenRoot(pendingDirectory)
	if err != nil {
		return fail(err)
	}
	opened, err := h.root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return fail(fmt.Errorf("pending directory changed while opening"))
	}
	h.identity = opened
	// An earlier mkdir may have succeeded while its parent fsync failed.
	// Reconfirm the directory entry is durable even when it already exists.
	if err = syncPendingDirectory(parent); err != nil {
		return fail(err)
	}
	// Separate first creation from opening an existing lock. Concurrent
	// O_CREATE opens can report ENOENT on the native filesystem even though
	// another process has just created the entry. Exclusive creation gives
	// the losing processes an explicit existing-file path; all ownership,
	// no-follow, flock and named-inode checks still apply below.
	flags := os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	h.lock, err = h.root.OpenFile(".lock", flags|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		h.lock, err = h.root.OpenFile(".lock", flags, 0)
	}
	if err != nil {
		return fail(err)
	}
	info, err = h.lock.Stat()
	if err != nil || !privatePending(info, 0) {
		return fail(fmt.Errorf("pending lock must be a private same-user regular file without links"))
	}
	if err = syscall.Flock(int(h.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("pending operations are busy in another dashboard; retry: %w", err))
	}
	now, err := parent.Lstat(pendingDirectory)
	if err != nil || !os.SameFile(now, h.identity) {
		return fail(fmt.Errorf("pending directory was replaced"))
	}
	named, err := h.root.Lstat(".lock")
	if err != nil || !os.SameFile(named, info) {
		return fail(fmt.Errorf("pending lock was replaced"))
	}
	// A reset holds this same lock through its final empty-roster proof.
	// Recheck after acquiring it so a raced durable fence cannot authorize a
	// new intent based on the earlier observation.
	if err := local.CheckStateFence(home); err != nil {
		return fail(err)
	}
	return h, nil
}
func (h *pendingFiles) close() {
	if h.lock != nil {
		syscall.Flock(int(h.lock.Fd()), syscall.LOCK_UN)
		h.lock.Close()
	}
	if h.root != nil {
		h.root.Close()
	}
	if h.parent != nil {
		h.parent.Close()
	}
}
func (h *pendingFiles) read(name string) (pendingRecord, error) {
	f, err := h.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return pendingRecord{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return pendingRecord{}, err
	}
	if !privatePending(info, pendingRecordLimit) {
		return pendingRecord{}, fmt.Errorf("unsafe pending file %s; inspect its type, owner, permissions and size", name)
	}
	raw, err := io.ReadAll(io.LimitReader(f, pendingRecordLimit+1))
	if err != nil {
		return pendingRecord{}, err
	}
	r, err := decodePending(raw)
	if err != nil {
		return r, fmt.Errorf("pending file %s requires inspection: %w", name, err)
	}
	if r.name() != name {
		return r, fmt.Errorf("pending file identity differs from its name: %s", name)
	}
	return r, nil
}
func (h *pendingFiles) list() ([]pendingRecord, error) {
	f, err := h.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(pendingCountLimit + 2)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(names) > pendingCountLimit+1 {
		return nil, fmt.Errorf("pending directory exceeds 64 operations; nothing was truncated or purged")
	}
	sort.Strings(names)
	var out []pendingRecord
	for _, name := range names {
		if name == ".lock" {
			continue
		}
		if len(name) != 72 || !strings.HasPrefix(name, "op-") || !strings.HasSuffix(name, ".json") {
			return nil, fmt.Errorf("unrecognized pending entry %s; inspect it before another send", name)
		}
		r, err := h.read(name)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func loadPending(home string) ([]pendingRecord, error) {
	h, err := openPending(home)
	if err != nil {
		return nil, err
	}
	defer h.close()
	return h.list()
}

// Intent is immutable. Only explicit retries may reuse an existing record.
func persistPending(home string, r pendingRecord, retry bool) error {
	_, err := reservePending(home, r, retry)
	return err
}

type pendingDurability struct {
	fileSync func(*os.File) error
	dirSync  func(*os.Root) error
}

func reservePending(home string, r pendingRecord, retry bool) (pendingRecord, error) {
	return reservePendingWith(home, r, retry, pendingDurability{})
}
func reservePendingWith(home string, r pendingRecord, retry bool, durability pendingDurability) (pendingRecord, error) {
	if err := r.validate(); err != nil {
		return r, err
	}
	h, err := openPending(home)
	if err != nil {
		return r, err
	}
	defer h.close()
	records, err := h.list()
	if err != nil {
		return r, err
	}
	found := false
	for _, old := range records {
		if old.name() == r.name() {
			if !samePending(old, r) {
				return r, fmt.Errorf("pending key belongs to a different exact request")
			}
			if !retry {
				return r, fmt.Errorf("request is already pending; open recovery and explicitly inspect/retry it")
			}
			if old.Attempts >= 1000000 {
				return r, fmt.Errorf("pending attempt counter exhausted; inspect the durable journal")
			}
			r = old
			r.Attempts++
			found = true
			break
		}
	}
	if retry && !found {
		return r, fmt.Errorf("durable pending record is missing; inspect the journal before another action")
	}
	if !found {
		if len(records) >= pendingCountLimit {
			return r, fmt.Errorf("64 pending operations retained; resolve one before another send")
		}
		for _, old := range records {
			if old.Kind == r.Kind && old.Authority == r.Authority {
				return r, fmt.Errorf("another %s request is pending on this authority; open recovery before replacing it", r.Kind)
			}
		}
	}
	raw, _ := json.Marshal(r)
	temp := ".intent-" + local.ID()
	f, err := h.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return r, err
	}
	defer h.root.Remove(temp)
	_, err = f.Write(raw)
	if err == nil {
		if durability.fileSync != nil {
			err = durability.fileSync(f)
		} else {
			err = f.Sync()
		}
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return r, err
	}
	if err = h.root.Rename(temp, r.name()); err != nil {
		return r, err
	}
	if durability.dirSync != nil {
		return r, durability.dirSync(h.root)
	}
	return r, syncPendingDirectory(h.root)
}

func settlePending(home string, r pendingRecord) error { return settlePendingOutcome(home, r, false) }
func settlePendingOutcome(home string, r pendingRecord, initialRejection bool) error {
	h, err := openPending(home)
	if err != nil {
		return err
	}
	defer h.close()
	stored, err := h.read(r.name())
	if os.IsNotExist(err) {
		return syncPendingDirectory(h.root)
	}
	if err != nil {
		return err
	}
	if initialRejection && (r.Attempts != 1 || stored.Attempts != r.Attempts) {
		return fmt.Errorf("another dispatch attempt is retained; an initial refusal cannot settle its uncertainty")
	}
	if r.Kind == "mesh" && stored.Attempts != r.Attempts {
		return fmt.Errorf("a newer Mesh dispatch remains retained; an older receipt cannot settle its outcome")
	}
	if !samePending(stored, r) {
		return fmt.Errorf("retained request changed; refusing to clear another operation")
	}
	if err = h.root.Remove(r.name()); err != nil {
		return err
	}
	return syncPendingDirectory(h.root)
}
