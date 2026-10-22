package sshservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const ArtifactReceiptName = "artifacts.json"

var artifactNames = []string{"identity.pub", "known_hosts", "agent.sock", "proxy.sock"}

type artifactFile struct {
	Name   string `json:"name"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Mode   uint32 `json:"mode"`
	Owner  uint32 `json:"owner"`
	SHA256 string `json:"sha256"`
}
type artifactReceipt struct {
	Schema        int                 `json:"schema"`
	Directory     InvocationDirectory `json:"directory"`
	ControlSHA256 string              `json:"control_sha256"`
	Files         []artifactFile      `json:"files"`
}

// Artifacts owns exact born private handles, never a persisted PID/process group.
// A partial setup remains preserved, not recursively removed or guessed safe.
type Artifacts struct {
	mu          sync.Mutex
	directory   *os.File
	intent      SourceIntent
	receipt     artifactReceipt
	receiptID   stateFile
	listeners   map[string]*net.UnixListener
	connections []*net.UnixConn
	closed      bool
}

func artifactStat(d *os.File, name string) (artifactFile, error) {
	var st unix.Stat_t
	if e := unix.Fstatat(int(d.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		return artifactFile{}, e
	}
	v := artifactFile{Name: name, Device: uint64(st.Dev), Inode: st.Ino, Mode: uint32(st.Mode), Owner: st.Uid}
	if st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || uint32(st.Mode)&07777 != 0600 {
		return v, fmt.Errorf("unsafe private SSH artifact %s", name)
	}
	switch uint32(st.Mode) & unix.S_IFMT {
	case unix.S_IFREG:
		raw, id, e := readStateFile(context.Background(), d, name, 8<<10)
		if e != nil {
			return v, e
		}
		if id.Dev != v.Device || id.Ino != v.Inode {
			return v, fmt.Errorf("artifact changed")
		}
		v.SHA256 = stateHash(raw)
	case unix.S_IFSOCK:
	default:
		return v, fmt.Errorf("unfamiliar artifact type")
	}
	return v, nil
}
func openInvocationArtifacts(intent SourceIntent) (*os.File, error) {
	actual, e := invocationIdentity(intent.Directory.Path)
	if e != nil || actual != intent.Directory {
		return nil, fmt.Errorf("exact born invocation directory required")
	}
	return privateDirectory(intent.Directory.Path)
}
func createArtifactFile(d *os.File, name string, raw []byte) error {
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_WRONLY|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return e
	}
	return f.Sync()
}

// PrepareArtifacts must follow durable ReserveSource. It publishes only public
// identity and known-host data, two private sockets and a fixed-roster receipt.
// No command/private key or network credential is written. Nothing is sent.
func PrepareArtifacts(ctx context.Context, home string, b Binding, intent SourceIntent) (*Artifacts, error) {
	return prepareArtifacts(ctx, home, b, intent, nil)
}
func prepareArtifacts(ctx context.Context, home string, b Binding, intent SourceIntent, checkpoint func(string)) (*Artifacts, error) {
	s, e := ReadState(ctx, home, b)
	if e != nil {
		return nil, e
	}
	found := false
	for _, r := range s.Intents {
		if exactIntent(r, intent) {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("durable exact source intention required before artifacts")
	}
	d, e := openInvocationArtifacts(intent)
	if e != nil {
		return nil, e
	}
	a := &Artifacts{directory: d, intent: intent, listeners: map[string]*net.UnixListener{}}
	fail := func(e error) (*Artifacts, error) {
		a.Close()
		return nil, fmt.Errorf("partial SSH artifacts preserved at %s: %w", intent.Directory.Path, e)
	}
	scan, e := os.ReadDir(intent.Directory.Path)
	if e != nil || len(scan) != 0 {
		return fail(fmt.Errorf("new invocation directory is not empty"))
	}
	key, _, _, _, e := ssh.ParseAuthorizedKey([]byte(intent.Request.Pin.PublicKey))
	if e != nil {
		return fail(e)
	}
	known, e := KnownHostsLine(intent.Request.Pin.Target, key)
	if e != nil {
		return fail(e)
	}
	for _, v := range []struct {
		name string
		raw  []byte
	}{{"identity.pub", []byte(intent.Request.PublicKey + "\n")}, {"known_hosts", []byte(known)}} {
		if e = ctx.Err(); e != nil {
			return fail(e)
		}
		if e = createArtifactFile(d, v.name, v.raw); e != nil {
			return fail(e)
		}
		if checkpoint != nil {
			checkpoint(v.name)
		}
	}
	for _, name := range []string{"agent.sock", "proxy.sock"} {
		if e = ctx.Err(); e != nil {
			return fail(e)
		}
		path := filepath.Join(intent.Directory.Path, name)
		l, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if e != nil {
			return fail(e)
		}
		l.SetUnlinkOnClose(false)
		a.listeners[name] = l
		if checkpoint != nil {
			checkpoint("bound:" + name)
		}
		// Directory is verified0700, and only this exact just-bound socket is chmodded.
		var before unix.Stat_t
		if e = unix.Fstatat(int(d.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); e != nil {
			return fail(e)
		}
		if uint32(before.Mode)&unix.S_IFMT != unix.S_IFSOCK || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 {
			return fail(fmt.Errorf("new socket identity invalid"))
		}
		if e = unix.Fchmodat(int(d.Fd()), name, 0600, unix.AT_SYMLINK_NOFOLLOW); e != nil {
			return fail(e)
		}
		var after unix.Stat_t
		if unix.Fstatat(int(d.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || before.Dev != after.Dev || before.Ino != after.Ino {
			return fail(fmt.Errorf("new socket replaced"))
		}
	}
	c, e := intent.ControlCommitment()
	if e != nil {
		return fail(e)
	}
	digest, e := c.Digest()
	if e != nil {
		return fail(e)
	}
	a.receipt = artifactReceipt{Schema: 1, Directory: intent.Directory, ControlSHA256: digest, Files: []artifactFile{}}
	for _, name := range artifactNames {
		v, e := artifactStat(d, name)
		if e != nil {
			return fail(e)
		}
		a.receipt.Files = append(a.receipt.Files, v)
	}
	raw, e := json.Marshal(a.receipt)
	if e != nil {
		return fail(e)
	}
	if e = createArtifactFile(d, ArtifactReceiptName, raw); e != nil {
		return fail(e)
	}
	_, a.receiptID, e = readStateFile(ctx, d, ArtifactReceiptName, 8<<10)
	if e != nil {
		return fail(e)
	}
	if e = d.Sync(); e != nil {
		return fail(e)
	}
	if checkpoint != nil {
		checkpoint("published")
	}
	return a, nil
}
func (a *Artifacts) Path(name string) string { return filepath.Join(a.intent.Directory.Path, name) }

// Accept grants one same-user local connection on one exact live listener.
// Recovery manifests do not grant accept authority or recreate sockets.
func (a *Artifacts) Accept(ctx context.Context, name string) (*net.UnixConn, error) {
	a.mu.Lock()
	l := a.listeners[name]
	if a.closed || l == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("owned SSH listener unavailable")
	}
	delete(a.listeners, name)
	a.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	defer l.Close()
	c, e := l.AcceptUnix()
	if e != nil {
		return nil, e
	}
	uid, e := peerUID(c)
	if e != nil || uid != uint32(os.Geteuid()) {
		c.Close()
		return nil, fmt.Errorf("SSH invocation peer account differs")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		c.Close()
		return nil, fmt.Errorf("SSH artifacts closed")
	}
	a.connections = append(a.connections, c)
	return c, nil
}
func (a *Artifacts) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	for _, l := range a.listeners {
		l.Close()
	}
	for _, c := range a.connections {
		c.Close()
	}
	if a.directory != nil {
		a.directory.Close()
		a.directory = nil
	}
}

func validateArtifactRoster(ctx context.Context, d *os.File, intent SourceIntent) (artifactReceipt, stateFile, error) {
	var r artifactReceipt
	raw, id, e := readStateFile(ctx, d, ArtifactReceiptName, 8<<10)
	if e != nil {
		return r, id, e
	}
	if e = DecodeStateRecord(raw, &r); e != nil {
		return r, id, e
	}
	c, e := intent.ControlCommitment()
	if e != nil {
		return r, id, e
	}
	digest, _ := c.Digest()
	if r.Schema != 1 || r.Directory != intent.Directory || r.ControlSHA256 != digest || len(r.Files) != len(artifactNames) {
		return r, id, fmt.Errorf("artifact receipt differs from exact source intention")
	}
	scanFD, e := unix.Openat(int(d.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return r, id, e
	}
	scan := os.NewFile(uintptr(scanFD), "artifacts")
	names, e := scan.Readdirnames(6)
	scan.Close()
	if e != nil && e != io.EOF {
		return r, id, e
	}
	if len(names) != 5 {
		return r, id, fmt.Errorf("artifact roster changed")
	}
	for i, name := range artifactNames {
		if r.Files[i].Name != name {
			return r, id, fmt.Errorf("artifact roster is not fixed")
		}
		actual, e := artifactStat(d, name)
		if e != nil || actual != r.Files[i] {
			return r, id, fmt.Errorf("artifact %s changed; preserve complete directory", name)
		}
	}
	return r, id, nil
}

// Cleanup validates the COMPLETE exact receipt before deleting a single child.
// Call only after closing this invocation's live handles. Failure preserves any
// remaining evidence; no recursive removal or names recovered from arbitrary JSON.
func (a *Artifacts) Cleanup(ctx context.Context) error {
	a.Close()
	return cleanupArtifactDirectory(ctx, a.intent, &a.receiptID)
}
func cleanupArtifactDirectory(ctx context.Context, intent SourceIntent, expected *stateFile) error {
	d, e := openInvocationArtifacts(intent)
	if e != nil {
		return e
	}
	defer d.Close()
	r, id, e := validateArtifactRoster(ctx, d, intent)
	if e != nil {
		return e
	}
	if expected != nil && id != *expected {
		return fmt.Errorf("artifact receipt replaced")
	}
	for i, name := range artifactNames {
		actual, checkErr := artifactStat(d, name)
		if checkErr != nil || actual != r.Files[i] {
			return fmt.Errorf("artifact identity changed before cleanup")
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = unix.Unlinkat(int(d.Fd()), name, 0); e != nil {
			return e
		}
	}
	raw, now, e := readStateFile(ctx, d, ArtifactReceiptName, 8<<10)
	if e != nil || now != id {
		return fmt.Errorf("artifact receipt changed during cleanup")
	}
	var check artifactReceipt
	if DecodeStateRecord(raw, &check) != nil {
		return fmt.Errorf("artifact receipt changed")
	}
	if e = unix.Unlinkat(int(d.Fd()), ArtifactReceiptName, 0); e != nil {
		return e
	}
	if e = d.Sync(); e != nil {
		return e
	}
	if validateDirectory(d, intent.Directory.Path) != nil {
		return fmt.Errorf("invocation directory changed")
	}
	return os.Remove(intent.Directory.Path)
}

// ArtifactReceipt exposes a bounded public cleanup preview without network or
// mutation. Explicit recovery must independently prove the original worker dead.
func ArtifactReceipt(ctx context.Context, intent SourceIntent) (any, error) {
	d, e := openInvocationArtifacts(intent)
	if e != nil {
		return nil, e
	}
	defer d.Close()
	r, _, e := validateArtifactRoster(ctx, d, intent)
	return r, e
}
