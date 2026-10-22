package sshservice

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

type stateFile struct {
	Dev  uint64
	Ino  uint64
	Mode uint32
	UID  uint32
	Size int64
	SHA  string
}
type State struct {
	mutationContext context.Context
	syncFile        func(*os.File) error
	syncDirectory   func(*os.File) error
	Binding         Binding
	Service         *ServiceRecord
	Pins            []Pin
	Completions     []SourceCompletion
	Seals           []SourceSealCompletion
	Intents         []SourceIntent
	History         *SourceHistorySummary
	historyMarker   *SourceHistoryMarker
	historyRecovery *SourceHistoryRecovery
	PublicKey       string
	home, dir       string
	files           map[string]stateFile
	contents        map[string][]byte
	directory       stateFile
	config          mesh.Config
	guard           func() error
}

func ReadState(ctx context.Context, home string, b Binding) (*State, error) {
	return readSourceState(ctx, home, b, true)
}
func readSourceState(ctx context.Context, home string, b Binding, checkHistory bool) (*State, error) {
	if ctx == nil || b.Validate() != nil || b.Owner != strconv.Itoa(os.Geteuid()) {
		return nil, fmt.Errorf("current SSH owner binding required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := local.CheckStateFence(home); err != nil {
		return nil, err
	}
	h, err := privateDirectory(home)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	d, err := privateDirectory(filepath.Join(home, "mesh"))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	s := &State{Binding: b, home: home, dir: filepath.Join(home, "mesh"), Pins: []Pin{}, Intents: []SourceIntent{}, files: map[string]stateFile{}, contents: map[string][]byte{}}
	s.directory, err = statState(d)
	if err != nil {
		return nil, err
	}
	scanFD, err := unix.Openat(int(d.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	scan := os.NewFile(uintptr(scanFD), s.dir)
	names, err := scan.Readdirnames(MaxStateMembers + 1)
	scan.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > MaxStateMembers {
		return nil, fmt.Errorf("SSH namespace exceeds complete256-member bound")
	}
	sort.Strings(names)
	var total int64
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		limit := int64(MaxIntentBytes)
		if name == SourceHistoryRecoveryName {
			limit = SourceHistoryRecoveryMaxBytes
		}
		if name == "config.json" {
			limit = 1 << 20
		}
		raw, id, e := readStateFile(ctx, d, name, limit)
		if e != nil {
			return nil, e
		}
		total += id.Size
		if total > 4<<20 {
			return nil, fmt.Errorf("SSH namespace exceeds4MiB bound")
		}
		s.files[name] = id
		s.contents[name] = raw
		switch {
		case name == "config.json", name == "node-key.pem":
		case name == StateLockName:
			if id.Size != 0 {
				return nil, fmt.Errorf("SSH coordination lock must be empty")
			}
		case name == hostKeyFile:
			var r hostRecord
			if e = DecodeStateRecord(raw, &r); e != nil || r.Schema != 1 || r.Host != b.Host {
				return nil, fmt.Errorf("SSH host key authority/schema differs")
			}
			key, e := x509.ParsePKCS8PrivateKey(r.Private)
			if e != nil {
				return nil, fmt.Errorf("invalid SSH host key")
			}
			ed, ok := key.(ed25519.PrivateKey)
			if !ok || len(ed) != ed25519.PrivateKeySize {
				return nil, fmt.Errorf("invalid Ed25519 SSH host key")
			}
			signer, e := ssh.NewSignerFromKey(ed)
			if e != nil {
				return nil, e
			}
			s.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
		case name == SourceHistoryRecoveryName:
			var v SourceHistoryRecovery
			if e = decodeSourceRecovery(raw, &v); e != nil || v.Validate() != nil || v.Marker.Binding != b {
				return nil, fmt.Errorf("invalid source history recovery fence")
			}
			s.historyRecovery = &v
		case name == SourceHistoryMarkerName:
			var m SourceHistoryMarker
			if e = DecodeStateRecord(raw, &m); e != nil || m.Validate() != nil || m.Binding != b {
				return nil, fmt.Errorf("invalid source history marker")
			}
			s.historyMarker = &m
		case name == ServiceFile:
			var r ServiceRecord
			if e = DecodeStateRecord(raw, &r); e != nil || r.Validate() != nil || r.Binding != b {
				return nil, fmt.Errorf("invalid SSH service authority/evidence")
			}
			s.Service = &r
		case strings.HasPrefix(name, "ssh-pin-") && strings.HasSuffix(name, ".json"):
			var p Pin
			if e = DecodeStateRecord(raw, &p); e != nil || p.Validate() != nil || p.Source != b.Host || name != "ssh-pin-"+p.Target+".json" {
				return nil, fmt.Errorf("invalid SSH pin evidence/name")
			}
			s.Pins = append(s.Pins, p)
		case strings.HasPrefix(name, "ssh-complete-") && strings.HasSuffix(name, ".json"):
			var r SourceCompletion
			if e = DecodeStateRecord(raw, &r); e != nil || r.Validate() != nil || r.Intent.Source != b.Host || r.Intent.Owner != b.Owner || name != completionName(r.Intent) {
				return nil, fmt.Errorf("invalid SSH terminal completion evidence")
			}
			s.Completions = append(s.Completions, r)
		case strings.HasPrefix(name, "ssh-sealed-") && strings.HasSuffix(name, ".json"):
			var r SourceSealCompletion
			if e = DecodeStateRecord(raw, &r); e != nil || r.Validate() != nil || r.Intent.Source != b.Host || r.Intent.Owner != b.Owner || name != sealCompletionName(r.Intent) {
				return nil, fmt.Errorf("invalid exact SSH nonexecution completion evidence")
			}
			s.Seals = append(s.Seals, r)
		case strings.HasPrefix(name, "ssh-client-") && strings.HasSuffix(name, ".json"):
			var r SourceIntent
			if e = DecodeStateRecord(raw, &r); e != nil || r.Validate() != nil || r.Source != b.Host || r.Owner != b.Owner || name != intentName(r) {
				return nil, fmt.Errorf("invalid exact SSH client intention")
			}
			s.Intents = append(s.Intents, r)
		default:
			return nil, fmt.Errorf("unfamiliar/partial mesh SSH namespace member %q; preserve evidence", name)
		}
	}
	if len(s.Pins) > MaxPins || len(s.Intents) > MaxSourceIntents || len(s.Completions)+len(s.Seals) > MaxCompletions {
		return nil, fmt.Errorf("SSH complete pin/intent roster exceeds bounds")
	}
	for _, c := range s.Seals {
		for _, r := range s.Intents {
			if intentName(c.Intent) == intentName(r) && !exactIntent(c.Intent, r) {
				return nil, fmt.Errorf("paired SSH seal differs from pending intention")
			}
		}
		for _, r := range s.Completions {
			if intentName(c.Intent) == intentName(r.Intent) {
				return nil, fmt.Errorf("SSH key records both admitted work and nonexecution")
			}
		}
	}
	for _, c := range s.Completions {
		for _, r := range s.Intents {
			if intentName(c.Intent) == intentName(r) && !exactIntent(c.Intent, r) {
				return nil, fmt.Errorf("paired SSH settlement differs from pending intention")
			}
		}
	}
	if s.Service != nil && (s.PublicKey == "" || s.PublicKey != s.Service.PublicKey) {
		return nil, fmt.Errorf("SSH service and host key differ")
	}
	if s.contents["config.json"] == nil || s.contents["node-key.pem"] == nil {
		return nil, fmt.Errorf("complete mesh config/key pair required")
	}
	if err = mesh.Decode(s.contents["config.json"], &s.config); err != nil || mesh.ValidateRegistry(s.config) != nil || s.config.Identity.Host != b.Host || s.config.Identity.Owner != b.Owner {
		return nil, fmt.Errorf("SSH namespace mesh authority is invalid")
	}
	cert, e := tls.X509KeyPair([]byte(s.config.Identity.Certificate), s.contents["node-key.pem"])
	if e != nil || len(cert.Certificate) != 1 {
		return nil, fmt.Errorf("SSH namespace mesh private key/certificate differ")
	}
	if s.historyRecovery != nil && (s.historyMarker == nil || s.historyRecovery.Marker != *s.historyMarker) {
		return nil, fmt.Errorf("source recovery lost its exact catalog marker")
	}
	if checkHistory {
		if err = loadSourceHistorySummary(ctx, s); err != nil {
			return nil, err
		}
	}
	scanFD, err = unix.Openat(int(d.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	scan = os.NewFile(uintptr(scanFD), s.dir)
	afterNames, e := scan.Readdirnames(MaxStateMembers + 1)
	scan.Close()
	if e != nil && !errors.Is(e, io.EOF) {
		return nil, e
	}
	sort.Strings(afterNames)
	if !reflect.DeepEqual(names, afterNames) {
		return nil, fmt.Errorf("SSH namespace membership changed while reading")
	}
	for _, name := range names {
		_, v, e := readStateFile(ctx, d, name, 1<<20)
		if e != nil || v != s.files[name] {
			return nil, fmt.Errorf("SSH namespace evidence changed while reading")
		}
	}

	if err = validateDirectory(h, home); err != nil {
		return nil, err
	}
	if err = validateDirectory(d, s.dir); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return s, nil
}
func statState(f *os.File) (stateFile, error) {
	var st unix.Stat_t
	e := unix.Fstat(int(f.Fd()), &st)
	return stateFile{Dev: uint64(st.Dev), Ino: st.Ino, Mode: uint32(st.Mode), UID: st.Uid, Size: st.Size}, e
}
func readStateFile(ctx context.Context, d *os.File, name string, limit int64) ([]byte, stateFile, error) {
	var before unix.Stat_t
	if e := ctx.Err(); e != nil {
		return nil, stateFile{}, e
	}
	if e := unix.Fstatat(int(d.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		return nil, stateFile{}, e
	}
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, stateFile{}, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	v, e := statState(f)
	if e != nil || v.Dev != uint64(before.Dev) || v.Ino != before.Ino || v.UID != uint32(os.Geteuid()) || v.Mode&unix.S_IFMT != unix.S_IFREG || v.Mode&07777 != 0600 || before.Nlink != 1 || v.Size < 0 || v.Size > limit {
		return nil, v, fmt.Errorf("unsafe/oversized SSH namespace file")
	}
	raw, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, v, e
	}
	var after unix.Stat_t
	if e = unix.Fstatat(int(d.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW); e != nil || uint64(after.Dev) != v.Dev || after.Ino != v.Ino || after.Size != v.Size || uint32(after.Mode) != v.Mode || after.Uid != v.UID || after.Nlink != 1 || int64(len(raw)) != v.Size {
		return nil, v, fmt.Errorf("SSH evidence changed while reading")
	}
	if e = ctx.Err(); e != nil {
		return nil, v, e
	}
	v.SHA = stateHash(raw)
	return raw, v, nil
}

type stateLock struct {
	file       *os.File
	directory  *os.File
	path, name string
	identity   stateFile
}

func (l *stateLock) Close() error {
	return errors.Join(unix.Flock(int(l.file.Fd()), unix.LOCK_UN), l.file.Close())
}
func (l *stateLock) Check() error {
	var st unix.Stat_t
	if unix.Fstatat(int(l.directory.Fd()), l.name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || uint64(st.Dev) != l.identity.Dev || st.Ino != l.identity.Ino || uint32(st.Mode) != l.identity.Mode || st.Uid != l.identity.UID || st.Size != 0 || st.Nlink != 1 || validateDirectory(l.directory, l.path) != nil {
		return fmt.Errorf("SSH namespace lock identity changed")
	}
	return nil
}
func lockStateAt(ctx context.Context, d *os.File, name, path string) (*stateLock, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	fail := func(e error) (*stateLock, error) { f.Close(); return nil, e }
	v, e := statState(f)
	var st unix.Stat_t
	if e != nil || unix.Fstat(fd, &st) != nil || v.Mode&unix.S_IFMT != unix.S_IFREG || v.Mode&07777 != 0600 || v.UID != uint32(os.Geteuid()) || v.Size != 0 || st.Nlink != 1 {
		return fail(fmt.Errorf("unsafe SSH namespace lock"))
	}
	for {
		e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if e != unix.EAGAIN && e != unix.EWOULDBLOCK {
			return fail(e)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	l := &stateLock{f, d, path, name, v}
	if e = l.Check(); e != nil {
		unix.Flock(fd, unix.LOCK_UN)
		return fail(e)
	}
	if e = d.Sync(); e != nil {
		unix.Flock(fd, unix.LOCK_UN)
		return fail(e)
	}
	return l, nil
}
func withStateMutation(ctx context.Context, home string, b Binding, sshLock bool, fn func(*State, *os.File) error) error {
	return withSourceMutation(ctx, home, b, sshLock, true, fn)
}
func withSourceMutation(ctx context.Context, home string, b Binding, sshLock, checkHistory bool, fn func(*State, *os.File) error) error {
	if ctx == nil {
		return fmt.Errorf("context required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return e
	}
	if b.Validate() != nil || b.Owner != strconv.Itoa(os.Geteuid()) {
		return fmt.Errorf("current SSH authority binding required")
	}
	if e := local.CheckStateFence(home); e != nil {
		return e
	}
	native := local.CurrentIdentity()
	if native.UID != b.Owner || native.Username != b.Account {
		return fmt.Errorf("current native SSH account differs from reviewed binding")
	}
	h, e := privateDirectory(home)
	if e != nil {
		return e
	}
	defer h.Close()
	unlock, e := lockStateAt(ctx, h, "mesh-lifecycle.lock", home)
	if e != nil {
		return e
	}
	defer unlock.Close()
	if e = local.CheckStateFence(home); e != nil {
		return e
	}
	d, e := privateDirectory(filepath.Join(home, "mesh"))
	if e != nil {
		return e
	}
	defer d.Close()
	var pinLock *stateLock
	if sshLock {
		pinLock, e = lockStateAt(ctx, d, StateLockName, filepath.Join(home, "mesh"))
		if e != nil {
			return e
		}
		defer pinLock.Close()
	}
	if e = local.CheckStateFence(home); e != nil {
		return e
	}
	s, e := readSourceState(ctx, home, b, checkHistory)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	s.mutationContext = ctx
	s.guard = func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := local.CheckStateFence(home); e != nil {
			return e
		}
		if e := unlock.Check(); e != nil {
			return e
		}
		if pinLock != nil {
			return pinLock.Check()
		}
		return nil
	}
	if e = s.guard(); e != nil {
		return e
	}
	return fn(s, d)
}
func currentStateFile(ctx context.Context, d *os.File, name string) (*stateFile, error) {
	_, v, e := readStateFile(ctx, d, name, 1<<20)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	return &v, e
}
func atomicStateFile(ctx context.Context, s *State, d *os.File, name string, raw []byte, expected *stateFile) error {
	return atomicStateRecord(ctx, s, d, name, raw, expected, MaxIntentBytes)
}
func atomicStateRecord(ctx context.Context, s *State, d *os.File, name string, raw []byte, expected *stateFile, limit int) error {
	check := func() error {
		if s.guard != nil {
			if e := s.guard(); e != nil {
				return e
			}
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := local.CheckStateFence(s.home); e != nil {
			return e
		}
		if e := validateDirectory(d, s.dir); e != nil {
			return e
		}
		v, e := currentStateFile(ctx, d, name)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(v, expected) {
			return fmt.Errorf("reviewed SSH file changed; no overwrite")
		}
		return nil
	}
	if len(raw) > limit {
		return fmt.Errorf("SSH record exceeds its declared bound")
	}
	if e := check(); e != nil {
		return e
	}
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return e
	}
	temp := ".ssh-write-" + hex.EncodeToString(nonce[:])
	fd, e := unix.Openat(int(d.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), temp)
	created, e := statState(f)
	if e != nil {
		f.Close()
		return e
	}
	defer func() {
		var st unix.Stat_t
		if unix.Fstatat(int(d.Fd()), temp, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && uint64(st.Dev) == created.Dev && st.Ino == created.Ino {
			_ = unix.Unlinkat(int(d.Fd()), temp, 0)
		}
	}()
	_, e = f.Write(raw)
	if e == nil {
		if s.syncFile != nil {
			e = s.syncFile(f)
		} else {
			e = f.Sync()
		}
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = check(); e != nil {
		return e
	}
	if expected == nil {
		e = unix.Linkat(int(d.Fd()), temp, int(d.Fd()), name, 0)
		if e == nil {
			e = unix.Unlinkat(int(d.Fd()), temp, 0)
		}
	} else {
		e = unix.Renameat(int(d.Fd()), temp, int(d.Fd()), name)
	}
	if e != nil {
		return e
	}
	if s.syncDirectory != nil {
		return s.syncDirectory(d)
	}
	return d.Sync()
}
func stateEqual(a, b *State) bool {
	if a.directory.Dev != b.directory.Dev || a.directory.Ino != b.directory.Ino || a.directory.Mode != b.directory.Mode || a.directory.UID != b.directory.UID {
		return false
	}
	af := map[string]stateFile{}
	bf := map[string]stateFile{}
	for k, v := range a.files {
		if k != StateLockName {
			af[k] = v
		}
	}
	for k, v := range b.files {
		if k != StateLockName {
			bf[k] = v
		}
	}
	return reflect.DeepEqual(af, bf)
}

// CommitStatePin is the only production pin writer. It shares the original
// ssh-pins.lock, ordered after lifecycle, and cannot cross a reset fence.
func CommitStatePin(ctx context.Context, home string, b Binding, review PinReview) error {
	return withStateMutation(ctx, home, b, false, func(s *State, _ *os.File) error {
		if review.directory != s.dir || review.After.Source != b.Host {
			return fmt.Errorf("SSH pin review belongs to another authority")
		}
		if len(s.Pins) >= MaxPins && review.Before == nil {
			return fmt.Errorf("SSH pin roster full; no evidence removed")
		}
		return commitPin(s.mutationContext, review, s.guard)
	})
}

// ServiceReview holds a newly generated key in memory only. Declining it writes
// nothing. ApplyService never repairs a partial host key or changes authority.
type ServiceReview struct {
	Before   *ServiceRecord
	After    ServiceRecord
	snapshot *State
	private  ed25519.PrivateKey
	digest   string
}

func ReviewService(ctx context.Context, home string, b Binding, enabled bool, reason string) (ServiceReview, error) {
	s, e := ReadState(ctx, home, b)
	if e != nil {
		return ServiceReview{}, e
	}
	if !stateText(reason, 4096) {
		return ServiceReview{}, fmt.Errorf("required bounded service purpose")
	}
	r := ServiceReview{Before: s.Service, snapshot: s}
	now := time.Now().UTC()
	r.After = ServiceRecord{Schema: 1, Binding: b, PublicKey: s.PublicKey, Enabled: enabled, Revision: 1, Reason: reason, Created: now, Updated: now}
	if s.Service != nil {
		r.After.Revision = s.Service.Revision + 1
		r.After.Created = s.Service.Created
	}
	if s.PublicKey == "" {
		if !enabled {
			return ServiceReview{}, fmt.Errorf("SSH service was never set up")
		}
		_, r.private, e = ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return ServiceReview{}, e
		}
		key, e := ssh.NewPublicKey(r.private.Public())
		if e != nil {
			return ServiceReview{}, e
		}
		r.After.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	}
	raw, _ := json.Marshal(r.After)
	r.digest = stateHash(raw)
	return r, r.After.Validate()
}
func ApplyService(ctx context.Context, home string, b Binding, review ServiceReview) error {
	rawReview, _ := json.Marshal(review.After)
	if review.digest == "" || stateHash(rawReview) != review.digest || review.snapshot == nil || review.After.Validate() != nil || review.After.Binding != b {
		return fmt.Errorf("exact reviewed SSH service required")
	}
	return withStateMutation(ctx, home, b, true, func(s *State, d *os.File) error {
		if !stateEqual(s, review.snapshot) || !reflect.DeepEqual(s.Service, review.Before) {
			return fmt.Errorf("SSH service review is stale")
		}
		if s.PublicKey == "" {
			if len(review.private) != ed25519.PrivateKeySize {
				return fmt.Errorf("reviewed host key unavailable")
			}
			key, e := ssh.NewPublicKey(review.private.Public())
			if e != nil || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) != review.After.PublicKey {
				return fmt.Errorf("reviewed SSH host key changed")
			}
			private, e := x509.MarshalPKCS8PrivateKey(review.private)
			if e != nil {
				return e
			}
			raw, _ := json.Marshal(hostRecord{1, b.Host, private})
			if e = atomicStateFile(ctx, s, d, hostKeyFile, raw, nil); e != nil {
				return e
			}
		} else if s.PublicKey != review.After.PublicKey {
			return fmt.Errorf("reviewed host key differs")
		}
		raw, _ := json.Marshal(review.After)
		var expected *stateFile
		if v, ok := s.files[ServiceFile]; ok {
			expected = &v
		}
		return atomicStateFile(ctx, s, d, ServiceFile, raw, expected)
	})
}

func intentName(r SourceIntent) string {
	return "ssh-client-" + stateHash([]byte(r.Source+"\x00"+r.Request.Pin.Target+"\x00"+r.Request.Key)) + ".json"
}
func exactIntent(a, b SourceIntent) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func completionName(r SourceIntent) string {
	return strings.Replace(intentName(r), "ssh-client-", "ssh-complete-", 1)
}
