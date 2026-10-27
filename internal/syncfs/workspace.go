package syncfs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"getarm.org/internal/syncreconcile"
	"golang.org/x/sys/unix"
)

// Reservation is the caller's durable admission reference. syncfs verifies
// its shape and enforces DiskBytes during preflight, but only the controller
// can prove the reservation was actually charged to both endpoint budgets.
type Reservation struct {
	Key, PlanDigest string
	Generation      uint64
	Side            syncreconcile.Side
	DiskBytes       int64
}

func (v Reservation) validate() error {
	if len(v.Key) < 16 || len(v.Key) > 128 || v.Generation == 0 || v.DiskBytes <= 0 || v.DiskBytes > 1<<40 || (v.Side != syncreconcile.Local && v.Side != syncreconcile.Remote) {
		return fmt.Errorf("invalid sync reservation")
	}
	for _, r := range v.Key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return fmt.Errorf("invalid sync reservation key")
		}
	}
	if len(v.PlanDigest) != 64 {
		return fmt.Errorf("invalid plan digest")
	}
	_, err := hex.DecodeString(v.PlanDigest)
	return err
}

// Workspace is operation-owned staging and quarantine on the receiver's
// filesystem, outside selected contents. Close only closes handles: recovery
// evidence is retained until a separate confirmed cleanup procedure.
type Workspace struct {
	Reservation                     Reservation
	Parent, Base, Stage, Quarantine *Root
	path                            string
}

// openCreatedChild binds a freshly observed mkdir result through both the
// held parent descriptor and its named absolute route. A replaced route is
// never adopted as this operation's workspace.
func openCreatedChild(parent *Root, name string, created unix.Stat_t) (*Root, error) {
	if e := parent.Check(); e != nil {
		return nil, e
	}
	fd, e := unix.Openat(parent.FD(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	defer unix.Close(fd)
	held, e := statFD(fd)
	if e != nil || !stableStat(created, held) {
		return nil, ErrChanged
	}
	child, e := OpenRoot(filepath.Join(parent.Path(), name))
	if e != nil {
		return nil, e
	}
	closeOnError := func(e error) (*Root, error) { _ = child.Close(); return nil, e }
	if len(child.chain) != len(parent.chain)+1 {
		return closeOnError(ErrChanged)
	}
	viaName, e := statFD(child.FD())
	if e != nil || !stableStat(held, viaName) {
		return closeOnError(ErrChanged)
	}
	viaParent, e := statFD(int(child.chain[len(parent.chain)-1].Fd()))
	if e != nil {
		return closeOnError(e)
	}
	actualParent, e := statFD(parent.FD())
	if e != nil || !sameObject(actualParent, viaParent) {
		return closeOnError(ErrChanged)
	}
	if e := parent.Check(); e != nil {
		return closeOnError(e)
	}
	return child, nil
}

func mkdirBound(parent *Root, name string) (*Root, error) {
	if e := parent.Check(); e != nil {
		return nil, e
	}
	if e := unix.Mkdirat(parent.FD(), name, 0700); e != nil {
		return nil, e
	}
	created, e := statNamed(parent.FD(), name)
	if e != nil {
		return nil, e
	}
	if created.Mode&unix.S_IFMT != unix.S_IFDIR || created.Mode&0777 != 0700 || created.Uid != uint32(os.Geteuid()) {
		return nil, ErrChanged
	}
	return openCreatedChild(parent, name, created)
}

func (w *Workspace) Path() string           { return w.path }
func (w *Workspace) StagePath() string      { return w.Stage.Path() }
func (w *Workspace) QuarantinePath() string { return w.Quarantine.Path() }
func (w *Workspace) Close() error {
	var first error
	for _, r := range []*Root{w.Quarantine, w.Stage, w.Base} {
		if r != nil {
			if e := r.Close(); first == nil {
				first = e
			}
		}
	}
	return first
}

func CreateWorkspace(receiver, parent *Root, v Reservation) (*Workspace, error) {
	if receiver == nil || parent == nil {
		return nil, fmt.Errorf("receiver and workspace parent roots required")
	}
	if e := v.validate(); e != nil {
		return nil, e
	}
	if e := receiver.Check(); e != nil {
		return nil, e
	}
	if e := parent.Check(); e != nil {
		return nil, e
	}
	if receiver.Path() == parent.Path() || strings.HasPrefix(parent.Path(), receiver.Path()+string(os.PathSeparator)) {
		return nil, fmt.Errorf("workspace must be outside selected root")
	}
	a, e := statFD(receiver.FD())
	if e != nil {
		return nil, e
	}
	// A second named route (for example a bind mount) can identify the
	// receiver inside the workspace parent's ancestor chain even when the
	// cleaned path strings differ.
	for _, ancestor := range parent.chain {
		candidate, statErr := statFD(int(ancestor.Fd()))
		if statErr != nil {
			return nil, statErr
		}
		if sameObject(a, candidate) {
			return nil, fmt.Errorf("workspace parent aliases selected root")
		}
	}
	b, e := statFD(parent.FD())
	if e != nil {
		return nil, e
	}
	if a.Dev != b.Dev {
		return nil, fmt.Errorf("workspace must share receiver filesystem")
	}
	if b.Uid != uint32(os.Geteuid()) || b.Mode&0077 != 0 {
		return nil, fmt.Errorf("workspace parent must be private and owned by receiver account")
	}
	var nonce [12]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return nil, e
	}
	name := ".arm-sync-" + hex.EncodeToString(nonce[:])
	path := filepath.Join(parent.Path(), name)
	w := &Workspace{Reservation: v, Parent: parent, path: path}
	if w.Base, e = mkdirBound(parent, name); e != nil {
		return w, e
	}
	if w.Stage, e = mkdirBound(w.Base, "stage"); e != nil {
		return w, e
	}
	if w.Quarantine, e = mkdirBound(w.Base, "quarantine"); e != nil {
		return w, e
	}
	for _, fd := range []int{w.Stage.FD(), w.Quarantine.FD(), w.Base.FD(), parent.FD()} {
		if e = unix.Fsync(fd); e != nil {
			return w, e
		}
	}
	if e = writeWorkspaceMarker(receiver, w); e != nil {
		return w, e
	}
	return w, nil
}
