package syncfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/inferal-oss/arm/internal/syncreconcile"
	"golang.org/x/sys/unix"
)

var ErrUnknown = errors.New("sync native outcome unknown; retained evidence requires inspection")

type Receipt struct {
	Key, PlanDigest  string
	Generation       uint64
	Side             syncreconcile.Side
	Index            int
	Action           syncreconcile.Action
	State            string // prepared, applied, conflict, or unknown
	Preimage, Staged string
	Error            string
}

// Journal must durably record Prepare before a native effect and Outcome
// after it. A failed Outcome leaves the same-key operation unknown; the
// caller must never infer success from a returned nil OS error alone.
type Journal interface {
	Prepare(context.Context, Receipt) error
	Outcome(context.Context, Receipt) error
}

type Publisher struct {
	Root        *Root
	Work        *Workspace
	Plan        syncreconcile.Plan
	Metadata    Metadata
	Names       NamePolicy
	Exclusions  []string
	Journal     Journal
	actions     []syncreconcile.Action
	dirExpected map[string]unix.Stat_t
	stagedDirs  map[string]unix.Stat_t
	used        atomic.Bool
}

func NewPublisher(root *Root, work *Workspace, plan syncreconcile.Plan, names NamePolicy, metadata Metadata, exclusions []string, j Journal) (*Publisher, error) {
	if root == nil || work == nil || work.Base == nil || work.Stage == nil || work.Quarantine == nil || j == nil {
		return nil, fmt.Errorf("root, owned workspace and durable journal required")
	}
	if plan.Digest() == "" || plan.Digest() != work.Reservation.PlanDigest || plan.Generation() != work.Reservation.Generation || len(plan.Conflicts()) != 0 {
		return nil, fmt.Errorf("reservation does not bind conflict-free plan")
	}
	var actions []syncreconcile.Action
	for _, a := range plan.Actions() {
		if a.Side == work.Reservation.Side {
			actions = append(actions, a)
		}
	}
	if len(actions) > syncreconcile.MaxActions {
		return nil, fmt.Errorf("too many receiver actions")
	}
	p := &Publisher{Root: root, Work: work, Plan: plan, Metadata: metadata, Names: names, Exclusions: exclusions, Journal: j, actions: actions, dirExpected: map[string]unix.Stat_t{}, stagedDirs: map[string]unix.Stat_t{}}
	return p, nil
}
func (p *Publisher) receipt(i int, a syncreconcile.Action) Receipt {
	return Receipt{Key: p.Work.Reservation.Key, PlanDigest: p.Plan.Digest(), Generation: p.Plan.Generation(), Side: p.Work.Reservation.Side, Index: i, Action: a, State: "prepared"}
}
func (p *Publisher) exclusionGuard(a syncreconcile.Action) error {
	key, e := p.Names.Key(a.Path)
	if e != nil {
		return e
	}
	for _, raw := range p.Exclusions {
		x, e := p.Names.Key(raw)
		if e != nil {
			return e
		}
		if key == x || strings.HasPrefix(key, x+"/") || strings.HasPrefix(x, key+"/") && (a.Kind == syncreconcile.Remove || a.After != nil && a.After.Kind != syncreconcile.Directory) {
			return fmt.Errorf("action %q intersects excluded subtree %q", a.Path, raw)
		}
	}
	return nil
}
func entryAt(ctx context.Context, r *Root, name string, m Metadata, budget int64) (*syncreconcile.Entry, error) {
	h, base, e := r.parent(name)
	if e != nil {
		return nil, e
	}
	defer h.Close()
	entry, e := fingerprintAt(ctx, h.fd, base, m, budget)
	if errors.Is(e, unix.ENOENT) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if e = h.Check(); e != nil {
		return nil, e
	}
	return &entry, nil
}
func nativeAt(r *Root, name string) (unix.Stat_t, error) {
	h, base, e := r.parent(name)
	if e != nil {
		return unix.Stat_t{}, e
	}
	defer h.Close()
	st, e := statNamed(h.fd, base)
	if e != nil {
		return st, e
	}
	if e = h.Check(); e != nil {
		return st, e
	}
	return st, nil
}
func equalEntry(a, b *syncreconcile.Entry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func (p *Publisher) before(ctx context.Context, a syncreconcile.Action) error {
	current, e := entryAt(ctx, p.Root, a.Path, p.Metadata, 64<<30)
	if e != nil {
		return e
	}
	if a.Before != nil && a.Before.Kind == syncreconcile.Directory {
		if current == nil {
			return ErrChanged
		}
		st, e := nativeAt(p.Root, a.Path)
		if e != nil {
			return e
		}
		if expected, ok := p.dirExpected[a.Path]; ok && sameObject(expected, st) && stableStat(expected, st) {
			return nil
		}
		return ErrChanged
	}
	if !equalEntry(current, a.Before) {
		return ErrChanged
	}
	return nil
}
func (p *Publisher) verifyStaged(ctx context.Context, a syncreconcile.Action) error {
	if a.After == nil {
		return nil
	}
	entry, e := entryAt(ctx, p.Work.Stage, a.Path, p.Metadata, p.Work.Reservation.DiskBytes)
	if e != nil {
		return e
	}
	if !equalEntry(entry, a.After) {
		return fmt.Errorf("staged SHA-256/metadata differs from reviewed after fingerprint: %w", ErrChanged)
	}
	if a.After.Kind == syncreconcile.File {
		h, base, e := p.Work.Stage.parent(a.Path)
		if e != nil {
			return e
		}
		defer h.Close()
		fd, e := unix.Openat(h.fd, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		defer unix.Close(fd)
		st, e := statFD(fd)
		if e != nil {
			return e
		}
		if st.Nlink != 1 {
			return fmt.Errorf("staged file has other hard links")
		}
		if e = unix.Fsync(fd); e != nil {
			return e
		}
		if e = unix.Fsync(h.fd); e != nil {
			return e
		}
	}
	return nil
}
func (p *Publisher) preflight(ctx context.Context) error {
	if e := p.Root.Check(); e != nil {
		return e
	}
	for _, r := range []*Root{p.Work.Base, p.Work.Stage, p.Work.Quarantine} {
		if e := r.Check(); e != nil {
			return e
		}
	}
	staging, e := p.Work.Stage.Scan(ctx, nil, nil, p.Names, p.Metadata, ScanLimits{Entries: syncreconcile.MaxEntries, Bytes: p.Work.Reservation.DiskBytes})
	if e != nil {
		return fmt.Errorf("whole staging tree: %w", e)
	}
	var charged int64
	for _, entry := range staging.Entries {
		if entry.Size > p.Work.Reservation.DiskBytes-charged {
			return fmt.Errorf("staging reservation exceeded")
		}
		charged += entry.Size
	}
	for _, a := range p.actions {
		if e := validateRelative(a.Path); e != nil {
			return e
		}
		if e := p.exclusionGuard(a); e != nil {
			return e
		}
		if a.Kind == syncreconcile.Remove {
			if a.Before == nil || a.After != nil {
				return fmt.Errorf("invalid remove action")
			}
		} else if a.After == nil {
			return fmt.Errorf("action lacks after fingerprint")
		}
		if a.Before != nil && a.After != nil && (a.Before.Kind == syncreconcile.Directory) != (a.After.Kind == syncreconcile.Directory) {
			return fmt.Errorf("directory type replacement requires structural adapter")
		}
		if a.After != nil && a.After.Kind == syncreconcile.Directory && a.Kind != syncreconcile.Create && a.Kind != syncreconcile.Metadata {
			return fmt.Errorf("directory replacement requires structural adapter")
		}
		if a.Before != nil && a.Before.Kind == syncreconcile.Directory {
			current, e := entryAt(ctx, p.Root, a.Path, p.Metadata, 64<<30)
			if e != nil {
				return e
			}
			if !equalEntry(current, a.Before) {
				return ErrChanged
			}
			st, e := nativeAt(p.Root, a.Path)
			if e != nil {
				return e
			}
			p.dirExpected[a.Path] = st
		}
		if a.Before != nil && a.Before.Kind != syncreconcile.Directory {
			if a.Before.Size > p.Work.Reservation.DiskBytes-charged {
				return fmt.Errorf("preimage retention reservation exceeded")
			}
			charged += a.Before.Size
		}
		if a.Kind != syncreconcile.Remove {
			if e := p.verifyStaged(ctx, a); e != nil {
				return fmt.Errorf("staging %q: %w", a.Path, e)
			}
			if a.After.Kind == syncreconcile.Directory {
				st, e := nativeAt(p.Work.Stage, a.Path)
				if e != nil {
					return e
				}
				p.stagedDirs[a.Path] = st
			}
		}
	}
	return nil
}
func (p *Publisher) refreshParent(name string) {
	parent := path.Dir(name)
	if parent == "." {
		return
	}
	if _, ok := p.dirExpected[parent]; !ok {
		return
	}
	expected := p.dirExpected[parent]
	if st, e := nativeAt(p.Root, parent); e == nil && sameObject(expected, st) && expected.Mode == st.Mode && expected.Uid == st.Uid && expected.Gid == st.Gid {
		p.dirExpected[parent] = st
	}
}
func (p *Publisher) assertRoots() error {
	for _, r := range []*Root{p.Root, p.Work.Base, p.Work.Stage, p.Work.Quarantine} {
		if e := r.Check(); e != nil {
			return e
		}
	}
	return nil
}
func (p *Publisher) applyNative(ctx context.Context, i int, a syncreconcile.Action, receipt *Receipt) error {
	if e := p.assertRoots(); e != nil {
		return e
	}
	if e := p.before(ctx, a); e != nil {
		return e
	}
	if a.Kind != syncreconcile.Remove {
		if e := p.verifyStaged(ctx, a); e != nil {
			return e
		}
	}
	dst, name, e := p.Root.parent(a.Path)
	if e != nil {
		return e
	}
	defer dst.Close()
	qname := fmt.Sprintf("%06d", i)
	if a.Kind == syncreconcile.Remove {
		if a.Before.Kind == syncreconcile.Directory {
			if e = emptyDirectory(dst.fd, name); e != nil {
				return e
			}
		}
		if _, e = statNamed(p.Work.Quarantine.FD(), qname); e == nil {
			return fmt.Errorf("quarantine slot occupied")
		}
		if !errors.Is(e, unix.ENOENT) {
			return e
		}
		if e = dst.Check(); e != nil {
			return e
		}
		if e = renameNoReplace(dst.fd, name, p.Work.Quarantine.FD(), qname); e != nil {
			return e
		}
		receipt.Preimage = filepath.Join(p.Work.Quarantine.Path(), qname)
		if _, present := statNamed(dst.fd, name); present == nil {
			return fmt.Errorf("receiver name reappeared after removal: %w", ErrUnknown)
		} else if !errors.Is(present, unix.ENOENT) {
			return fmt.Errorf("receiver name unreadable after removal: %w", ErrUnknown)
		}
		if a.Before.Kind == syncreconcile.Directory {
			if e = emptyDirectory(p.Work.Quarantine.FD(), qname); e != nil {
				return fmt.Errorf("captured directory gained entries: %w", ErrUnknown)
			}
		}
		if e = p.afterCapture(ctx, receipt, a.Before, p.Work.Quarantine, qname, dst.fd); e != nil {
			return e
		}
		p.refreshParent(a.Path)
		return nil
	}
	if a.After.Kind == syncreconcile.Directory {
		if a.Kind == syncreconcile.Metadata {
			return p.finalizeDirectory(ctx, a)
		}
		if a.Kind != syncreconcile.Create {
			return fmt.Errorf("directory replacement/metadata requires reviewed structural adapter")
		}
		if e = unix.Mkdirat(dst.fd, name, 0700); e != nil {
			if errors.Is(e, unix.EEXIST) {
				return ErrChanged
			}
			return e
		}
		if e = unix.Fsync(dst.fd); e != nil {
			return fmt.Errorf("new directory sync: %w", ErrUnknown)
		}
		if st, statErr := nativeAt(p.Root, a.Path); statErr == nil {
			p.dirExpected[a.Path] = st
		}
		if e = p.assertRoots(); e != nil {
			return fmt.Errorf("root after directory create: %w", ErrUnknown)
		}
		p.refreshParent(a.Path)
		return nil // metadata finalized after descendants
	}
	src, srcName, e := p.Work.Stage.parent(a.Path)
	if e != nil {
		return e
	}
	defer src.Close()
	if e = dst.Check(); e != nil {
		return e
	}
	if e = src.Check(); e != nil {
		return e
	}
	if a.Kind == syncreconcile.Create {
		if e = renameNoReplace(src.fd, srcName, dst.fd, name); e != nil {
			if errors.Is(e, unix.EEXIST) {
				return ErrChanged
			}
			return e
		}
	} else {
		if a.Before == nil {
			return fmt.Errorf("replacement lacks reviewed before state")
		}
		if _, e = statNamed(p.Work.Quarantine.FD(), qname); e == nil {
			return fmt.Errorf("quarantine slot occupied")
		}
		if !errors.Is(e, unix.ENOENT) {
			return e
		}
		if e = renameExchange(src.fd, srcName, dst.fd, name); e != nil {
			return e
		}
		receipt.Preimage = filepath.Join(p.Work.Stage.Path(), a.Path)
		// The displaced exact variant is now in the original stage slot.
		captured, captureErr := entryAt(ctx, p.Work.Stage, a.Path, p.Metadata, 64<<30)
		if captureErr != nil || !equalEntry(captured, a.Before) {
			return fmt.Errorf("captured preimage differs: %w", ErrUnknown)
		}
		if e = renameNoReplace(src.fd, srcName, p.Work.Quarantine.FD(), qname); e != nil {
			return fmt.Errorf("preimage retained in stage: %w", ErrUnknown)
		}
		receipt.Preimage = filepath.Join(p.Work.Quarantine.Path(), qname)
	}
	receipt.Staged = ""
	if e = p.afterCapture(ctx, receipt, a.After, p.Root, a.Path, dst.fd); e != nil {
		return e
	}
	if e = unix.Fsync(src.fd); e != nil {
		return fmt.Errorf("stage directory sync after effect: %w", ErrUnknown)
	}
	p.refreshParent(a.Path)
	return nil
}
func emptyDirectory(parent int, name string) error {
	fd, e := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	d := os.NewFile(uintptr(fd), name)
	defer d.Close()
	items, e := d.ReadDir(1)
	if e != nil && e != io.EOF {
		return e
	}
	if len(items) != 0 {
		return fmt.Errorf("reviewed directory gained entries: %w", ErrChanged)
	}
	return nil
}
func (p *Publisher) finalizeDirectory(ctx context.Context, a syncreconcile.Action) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := p.assertRoots(); e != nil {
		return e
	}
	// Child publications change the staging directory's mtime. Its reviewed
	// metadata was verified and captured before the first native effect.
	stageStat, ok := p.stagedDirs[a.Path]
	if !ok {
		return ErrChanged
	}
	dst, name, e := p.Root.parent(a.Path)
	if e != nil {
		return e
	}
	defer dst.Close()
	src, srcName, e := p.Work.Stage.parent(a.Path)
	if e != nil {
		return e
	}
	defer src.Close()
	stagedNow, e := statNamed(src.fd, srcName)
	if e != nil || !sameObject(stageStat, stagedNow) {
		return ErrChanged
	}
	fd, e := unix.Openat(dst.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	current, e := statFD(fd)
	if e != nil {
		return e
	}
	if expected, ok := p.dirExpected[a.Path]; !ok || !sameObject(expected, current) || !stableStat(expected, current) {
		return ErrChanged
	}
	if p.Metadata.Permissions {
		if e = unix.Fchmod(fd, uint32(stageStat.Mode&0777)); e != nil {
			return e
		}
	}
	if p.Metadata.ModTimes {
		sec, _ := statMtime(stageStat)
		times := []unix.Timeval{{Sec: sec}, {Sec: sec}}
		if e = unix.Futimes(fd, times); e != nil {
			return e
		}
	}
	if e = unix.Fsync(fd); e != nil {
		return fmt.Errorf("directory metadata sync: %w", ErrUnknown)
	}
	if e = unix.Fsync(dst.fd); e != nil {
		return fmt.Errorf("directory parent sync: %w", ErrUnknown)
	}
	got, e := entryAt(ctx, p.Root, a.Path, p.Metadata, p.Work.Reservation.DiskBytes)
	if e != nil || !equalEntry(got, a.After) {
		return fmt.Errorf("directory metadata differs: %w", ErrUnknown)
	}
	if e = p.assertRoots(); e != nil {
		return fmt.Errorf("directory binding after metadata: %w", ErrUnknown)
	}
	if st, e := nativeAt(p.Root, a.Path); e == nil {
		p.dirExpected[a.Path] = st
	}
	return nil
}
func (p *Publisher) afterCapture(ctx context.Context, r *Receipt, expected *syncreconcile.Entry, root *Root, name string, parentFD int) error {
	if e := unix.Fsync(parentFD); e != nil {
		return fmt.Errorf("directory sync after native effect: %w", ErrUnknown)
	}
	if e := unix.Fsync(p.Work.Quarantine.FD()); e != nil {
		return fmt.Errorf("quarantine sync after native effect: %w", ErrUnknown)
	}
	observed, e := entryAt(ctx, root, name, p.Metadata, p.Work.Reservation.DiskBytes)
	if e != nil || !equalEntry(observed, expected) {
		return fmt.Errorf("post-effect fingerprint differs: %w", ErrUnknown)
	}
	if e := p.assertRoots(); e != nil {
		return fmt.Errorf("root binding after effect: %w", ErrUnknown)
	}
	return nil
}

// ApplyAll is single-use. It verifies every staged After before any effect,
// then journals and applies exact actions in plan order. On the first fault it
// stops; returned receipts and retained stage/quarantine are recovery evidence.
func (p *Publisher) ApplyAll(ctx context.Context) ([]Receipt, error) {
	if !p.used.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("original sync publisher cannot be replayed")
	}
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	if e := p.preflight(ctx); e != nil {
		return nil, e
	}
	var receipts []Receipt
	var pendingDirs []int
	for i, a := range p.actions {
		if e := ctx.Err(); e != nil {
			return receipts, e
		}
		r := p.receipt(i, a)
		if e := p.Journal.Prepare(ctx, r); e != nil {
			return receipts, e
		}
		e := p.applyNative(ctx, i, a, &r)
		if e == nil && a.Kind == syncreconcile.Create && a.After.Kind == syncreconcile.Directory {
			r.State = "pending-directory-metadata"
			pendingDirs = append(pendingDirs, len(receipts))
			receipts = append(receipts, r)
			continue
		}
		if e != nil {
			r.State = "unknown"
			if errors.Is(e, ErrChanged) {
				r.State = "conflict"
			}
			r.Error = e.Error()
		} else {
			r.State = "applied"
		}
		// An outcome write failure never means the native effect was absent.
		if outcomeErr := p.Journal.Outcome(ctx, r); outcomeErr != nil {
			return append(receipts, r), fmt.Errorf("outcome receipt unavailable: %w", ErrUnknown)
		}
		receipts = append(receipts, r)
		if e != nil {
			return receipts, e
		}
	}
	for i := len(pendingDirs) - 1; i >= 0; i-- {
		idx := pendingDirs[i]
		r := receipts[idx]
		e := p.finalizeDirectory(ctx, r.Action)
		if e != nil {
			r.State = "unknown"
			r.Error = e.Error()
		} else {
			r.State = "applied"
		}
		if outcomeErr := p.Journal.Outcome(ctx, r); outcomeErr != nil {
			return receipts, fmt.Errorf("directory outcome receipt unavailable: %w", ErrUnknown)
		}
		receipts[idx] = r
		if e != nil {
			return receipts, e
		}
	}
	return receipts, nil
}
