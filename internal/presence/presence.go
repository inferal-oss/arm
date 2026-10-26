// Package presence publishes bounded, untrusted local ARM endpoint hints.
// A hint grants no native/backend authority; callers must authenticate the
// ephemeral signing key over the connected WebSocket before using a summary.
package presence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const prefix = "arm-presence-v1-"
const maxRecord = 2048
const MaxCandidates = 128
const maxDirectoryEntries = 4096
const MaxAge = 15 * time.Second

type Record struct {
	Version    int       `json:"version"`
	URL        string    `json:"url"`
	Host       string    `json:"host"`
	Generation string    `json:"generation"`
	PublicKey  []byte    `json:"public_key"`
	Expires    time.Time `json:"expires_at"`
}

type Candidate struct {
	Record Record `json:"record"`
	UID    uint32 `json:"uid"`
	Name   string `json:"-"`
}
type Scan struct {
	Candidates  []Candidate
	Truncated   bool
	Unavailable string
}

type Directory struct {
	mu     sync.RWMutex
	closed bool
	path   string
	fd     int
	dev    uint64
	ino    uint64
}

func Open() (*Directory, error) {
	root, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&07777 != 01777 {
		unix.Close(fd)
		return nil, fmt.Errorf("shared temp directory requires root-owned mode 1777")
	}
	return &Directory{path: root, fd: fd, dev: uint64(st.Dev), ino: st.Ino}, nil
}
func (d *Directory) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	err := unix.Close(d.fd)
	d.fd = -1
	return err
}
func (d *Directory) validLocked() error {
	if d.closed || d.fd < 0 {
		return errors.New("presence directory is closed")
	}
	var st unix.Stat_t
	if err := unix.Fstat(d.fd, &st); err != nil {
		return err
	}
	if uint64(st.Dev) != d.dev || st.Ino != d.ino || st.Uid != 0 || st.Mode&07777 != 01777 {
		return errors.New("shared temp directory changed")
	}
	return nil
}
func ValidURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "ws" || u.User != nil || u.Path != "/v1/peers" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || u.ForceQuery || u.String() != raw {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || strconv.Itoa(portNumber) != port {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && ip.String() == host && u.Hostname() == host
}
func validRecord(r Record, now time.Time) bool {
	return r.Version == 1 && ValidURL(r.URL) && validID(r.Host) && validID(r.Generation) && len(r.PublicKey) == ed25519.PublicKeySize &&
		!r.Expires.IsZero() && !now.After(r.Expires) && r.Expires.Sub(now) <= MaxAge
}
func validID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func randomName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]) + ".json", nil
}
func (d *Directory) write(name string, r Record, replace bool) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.writeLocked(name, r, replace)
}
func (d *Directory) writeLocked(name string, r Record, replace bool) error {
	if err := d.validLocked(); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(raw) > maxRecord {
		return errors.New("presence record too large")
	}
	temp, err := randomName()
	if err != nil {
		return err
	}
	temp += ".tmp"
	fd, err := unix.Openat(d.fd, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(d.fd, temp, 0)
	f := os.NewFile(uintptr(fd), temp)
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Chmod(0644)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if replace {
		var st unix.Stat_t
		if err = unix.Fstatat(d.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
			return errors.New("presence record owner or type changed")
		}
		err = unix.Renameat(d.fd, temp, d.fd, name)
	} else {
		err = unix.Linkat(d.fd, temp, d.fd, name, 0)
	}
	if err != nil {
		return err
	}
	return unix.Fsync(d.fd)
}
func (d *Directory) read(name string, now time.Time, requireFresh bool) (Candidate, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.readLocked(name, now, requireFresh)
}
func (d *Directory) readLocked(name string, now time.Time, requireFresh bool) (Candidate, error) {
	if err := d.validLocked(); err != nil {
		return Candidate{}, err
	}
	if len(name) != len(prefix)+32+len(".json") || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") || !validID(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")) {
		return Candidate{}, errors.New("invalid presence name")
	}
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return Candidate{}, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return Candidate{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Mode&0022 != 0 || st.Size < 1 || st.Size > maxRecord {
		return Candidate{}, errors.New("invalid presence file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxRecord+1))
	if err != nil || len(raw) > maxRecord {
		return Candidate{}, errors.New("presence read failed")
	}
	var r Record
	if err = json.Unmarshal(raw, &r); err != nil || (requireFresh && !validRecord(r, now)) {
		return Candidate{}, errors.New("invalid or stale presence record")
	}
	return Candidate{Record: r, UID: st.Uid, Name: name}, nil
}
func (d *Directory) Read(name string) (Candidate, error) { return d.read(name, time.Now().UTC(), true) }
func (d *Directory) Scan() Scan {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := Scan{Candidates: []Candidate{}}
	if err := d.validLocked(); err != nil {
		out.Unavailable = err.Error()
		return out
	}
	f, err := os.Open(d.path)
	if err != nil {
		out.Unavailable = err.Error()
		return out
	}
	defer f.Close()
	var opened unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &opened); err != nil || uint64(opened.Dev) != d.dev || opened.Ino != d.ino {
		out.Unavailable = "shared temp directory changed"
		return out
	}
	names, err := f.Readdirnames(maxDirectoryEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		out.Unavailable = err.Error()
		return out
	}
	if len(names) > maxDirectoryEntries {
		out.Truncated = true
		names = names[:maxDirectoryEntries]
	}
	now := time.Now().UTC()
	count := 0
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		count++
		if count > MaxCandidates {
			out.Truncated = true
			break
		}
		if c, e := d.readLocked(name, now, true); e == nil {
			out.Candidates = append(out.Candidates, c)
		}
	}
	return out
}

type Publication struct {
	d      *Directory
	Name   string
	Record Record
	key    ed25519.PrivateKey
}

func Publish(d *Directory, url, host, generation string) (*Publication, error) {
	if d == nil {
		return nil, errors.New("no presence directory")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	name, err := randomName()
	if err != nil {
		return nil, err
	}
	p := &Publication{d: d, Name: name, Record: Record{Version: 1, URL: url, Host: host, Generation: generation, PublicKey: pub}, key: key}
	if err = p.Refresh(); err != nil {
		return nil, err
	}
	return p, nil
}
func (p *Publication) Refresh() error {
	p.d.mu.RLock()
	defer p.d.mu.RUnlock()
	if err := p.d.validLocked(); err != nil {
		return err
	}
	p.Record.Expires = time.Now().UTC().Add(MaxAge)
	err := unix.Faccessat(p.d.fd, p.Name, unix.F_OK, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		current, e := p.d.readLocked(p.Name, time.Now().UTC(), false)
		if e != nil || !EqualIdentity(current.Identity(), p.Identity()) {
			return errors.New("presence publication identity changed")
		}
		return p.d.writeLocked(p.Name, p.Record, true)
	}
	if err != syscall.ENOENT {
		return err
	}
	return p.d.writeLocked(p.Name, p.Record, false)
}
func (p *Publication) Sign(message []byte) []byte { return ed25519.Sign(p.key, message) }
func (p *Publication) Close() error {
	p.d.mu.RLock()
	defer p.d.mu.RUnlock()
	c, err := p.d.readLocked(p.Name, time.Now().UTC(), false)
	if err != nil {
		return nil
	}
	if !EqualIdentity(c.Identity(), p.Identity()) {
		return errors.New("presence identity changed")
	}
	err = unix.Unlinkat(p.d.fd, p.Name, 0)
	if err != nil {
		return err
	}
	return p.d.validLocked()
}
func stringEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	v := byte(0)
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// Identity binds an announcement's stable locator and OS file owner to its
// ephemeral signing key. It is not a claim that TCP authenticates process UID
// or that this instance's reported quantities are native measurements.
type Identity struct {
	UID        uint32 `json:"uid"`
	Name       string `json:"record"`
	Version    int    `json:"version"`
	URL        string `json:"url"`
	Host       string `json:"host"`
	Generation string `json:"generation"`
	PublicKey  []byte `json:"public_key"`
}

func (c Candidate) Identity() Identity {
	r := c.Record
	return Identity{c.UID, c.Name, r.Version, r.URL, r.Host, r.Generation, r.PublicKey}
}
func (p *Publication) Identity() Identity {
	return Candidate{Record: Record{Version: p.Record.Version, URL: p.Record.URL, Host: p.Record.Host, Generation: p.Record.Generation, PublicKey: p.Record.PublicKey}, UID: uint32(os.Geteuid()), Name: p.Name}.Identity()
}
func EqualIdentity(a, b Identity) bool {
	return a.UID == b.UID && a.Name == b.Name && a.Version == b.Version && a.URL == b.URL && a.Host == b.Host && a.Generation == b.Generation && stringEqual(a.PublicKey, b.PublicKey)
}
