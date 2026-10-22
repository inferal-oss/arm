package historypack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"golang.org/x/sys/unix"
)

// Prepare creates only a new private stage under an existing private physical
// root. Any born stage returned with an error is retained, including on context
// cancellation; callers must preserve it for explicit exact reconciliation.
func Prepare(ctx context.Context, rootPath, id, authority string, records []Record) (Prepared, error) {
	return prepare(ctx, rootPath, id, authority, records, (*os.File).Sync)
}

// The scoped sync argument exercises real publication failures without global
// mutable hooks. Public entry points always use native File.Sync.
func prepare(ctx context.Context, rootPath, id, authority string, records []Record, syncFile func(*os.File) error) (p Prepared, err error) {
	if ctx == nil {
		return p, fmt.Errorf("context required")
	}
	if !hexValue(id, 16) || !hexValue(authority, 16) {
		return p, fmt.Errorf("canonical32hex pack and authority IDs required")
	}
	ordered, e := inputOrder(records)
	if e != nil {
		return p, e
	}
	if e = ctx.Err(); e != nil {
		return p, e
	}
	root, rootID, e := openRoot(rootPath)
	if e != nil {
		return p, e
	}
	defer root.Close()
	lock, e := acquire(ctx, root, true, nil)
	if e != nil {
		return p, e
	}
	defer lock.close()
	p.ID, p.Authority, p.Root, p.Lock = id, authority, rootID, lock.identity
	if e = sameRoot(rootPath, p.Root); e != nil {
		return p, e
	}
	// An already-published name must never acquire a second stage/identity.
	var st unix.Stat_t
	if e = unix.Fstatat(int(root.Fd()), finalName(id), &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(e, unix.ENOENT) {
		if e == nil {
			e = fmt.Errorf("pack ID already exists")
		}
		return p, e
	}
	if e = unix.Mkdirat(int(root.Fd()), stageName(id), 0700); e != nil {
		return p, e
	}
	dir, dirID, e := openDir(root, stageName(id), nil)
	if e != nil {
		return p, e
	}
	defer dir.Close()
	p.Directory = dirID
	if e = syncFile(root); e != nil {
		return p, e
	}
	recordsFile, recordID, e := openFile(dir, "records.bin", true)
	if e != nil {
		return p, e
	}
	p.Records = recordID
	m, e := makeRecords(ctx, recordsFile, &p, ordered)
	if e == nil {
		e = syncFile(recordsFile)
	}
	closeErr := recordsFile.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return p, e
	}
	p.RecordsBytes, p.RecordsSHA256 = m.RecordsBytes, m.RecordsSHA256
	raw, e := json.Marshal(m)
	if e != nil {
		return p, e
	}
	if len(raw) > MaxManifestBytes || uint64(len(raw)) > MaxPackBytes-p.RecordsBytes {
		return p, fmt.Errorf("combined manifest/records byte bound exceeded")
	}
	manifest, manifestID, e := openFile(dir, "manifest.json", true)
	if e != nil {
		return p, e
	}
	p.Manifest = manifestID
	e = writeAll(ctx, manifest, raw)
	if e == nil {
		e = syncFile(manifest)
	}
	closeErr = manifest.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return p, e
	}
	p.ManifestBytes, p.ManifestSHA256 = uint64(len(raw)), digest(raw)
	if e = syncFile(dir); e != nil {
		return p, e
	}
	if _, e = verifyDirectory(ctx, root, stageName(id), p); e != nil {
		return p, e
	}
	if e = lock.check(root); e != nil {
		return p, e
	}
	if e = sameRoot(rootPath, p.Root); e != nil {
		return p, e
	}
	return p, nil
}

// Publish performs a directory rename with kernel no-replace semantics. No
// fallback may overwrite an existing name. Persist Prepared before calling.
// Retrying exact Prepared after a lost acknowledgement is read-only with respect
// to pack contents; it checks the same born identity, then syncs its parent.
func Publish(ctx context.Context, rootPath string, p Prepared) (Receipt, error) {
	return publish(ctx, rootPath, p, (*os.File).Sync, renameNoReplace)
}
func publish(ctx context.Context, rootPath string, p Prepared, syncFile func(*os.File) error, rename func(int, string, string) error) (Receipt, error) {
	receipt := Receipt{p}
	if ctx == nil || !p.valid() {
		return receipt, fmt.Errorf("complete prepared evidence required")
	}
	if e := ctx.Err(); e != nil {
		return receipt, e
	}
	root, id, e := openRoot(rootPath)
	if e != nil {
		return receipt, e
	}
	defer root.Close()
	if id != p.Root {
		return receipt, fmt.Errorf("publication root differs")
	}
	lock, e := acquire(ctx, root, false, &p.Lock)
	if e != nil {
		return receipt, e
	}
	defer lock.close()
	if e = sameRoot(rootPath, p.Root); e != nil {
		return receipt, e
	}
	final, _, finalErr := openDir(root, finalName(p.ID), &p.Directory)
	if finalErr == nil {
		final.Close()
		// A second name, even if byte-identical, is not an exact publication retry.
		var st unix.Stat_t
		if e = unix.Fstatat(int(root.Fd()), stageName(p.ID), &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(e, unix.ENOENT) {
			return receipt, fmt.Errorf("stage and final names both present or uncertain")
		}
		if _, e = verifyDirectory(ctx, root, finalName(p.ID), p); e != nil {
			return receipt, e
		}
		if e = syncFile(root); e != nil {
			return receipt, &PublicationError{receipt, e}
		}
		if e = lock.check(root); e == nil {
			e = sameRoot(rootPath, p.Root)
		}
		if e != nil {
			return receipt, &PublicationError{receipt, e}
		}
		return receipt, nil
	}
	// Any final name is a collision, including an unsafe file/directory/symlink.
	var st unix.Stat_t
	if e = unix.Fstatat(int(root.Fd()), finalName(p.ID), &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(e, unix.ENOENT) {
		return receipt, fmt.Errorf("final pack name exists or cannot be inspected")
	}
	if _, e = verifyDirectory(ctx, root, stageName(p.ID), p); e != nil {
		return receipt, e
	}
	if e = ctx.Err(); e != nil {
		return receipt, e
	}
	if e = lock.check(root); e != nil {
		return receipt, e
	}
	if e = sameRoot(rootPath, p.Root); e != nil {
		return receipt, e
	}
	if e = rename(int(root.Fd()), stageName(p.ID), finalName(p.ID)); e != nil {
		if errors.Is(e, unix.EEXIST) || errors.Is(e, unix.ENOTEMPTY) {
			return receipt, e
		}
		return receipt, &PublicationError{receipt, e}
	}
	uncertain := func(e error) (Receipt, error) { return receipt, &PublicationError{receipt, e} }
	if e = syncFile(root); e != nil {
		return uncertain(e)
	}
	if _, e = verifyDirectory(ctx, root, finalName(p.ID), p); e != nil {
		return uncertain(e)
	}
	if e = lock.check(root); e != nil {
		return uncertain(e)
	}
	// Hashing can take time. Reopen the canonical parent AGAIN afterwards.
	if e = sameRoot(rootPath, p.Root); e != nil {
		return uncertain(e)
	}
	return receipt, nil
}

func verifyDirectory(ctx context.Context, root *os.File, name string, p Prepared) (Manifest, error) {
	var m Manifest
	if !p.valid() {
		return m, fmt.Errorf("incomplete pack evidence")
	}
	dir, _, e := openDir(root, name, &p.Directory)
	if e != nil {
		return m, e
	}
	defer dir.Close()
	if e = directoryNames(dir); e != nil {
		return m, e
	}
	manifest, id, e := openFile(dir, "manifest.json", false)
	if e != nil {
		return m, e
	}
	defer manifest.Close()
	if id != p.Manifest {
		return m, fmt.Errorf("manifest born identity differs")
	}
	raw, e := boundedRead(ctx, manifest, MaxManifestBytes)
	if e != nil {
		return m, e
	}
	if uint64(len(raw)) != p.ManifestBytes || digest(raw) != p.ManifestSHA256 {
		return m, fmt.Errorf("manifest digest/size differs")
	}
	if e = preflightManifest(raw); e != nil {
		return m, e
	}
	if e = canonical(raw, &m); e != nil {
		return m, e
	}
	if e = manifestValid(m); e != nil {
		return m, e
	}
	if m.ID != p.ID || m.Authority != p.Authority || m.RecordsBytes != p.RecordsBytes || m.RecordsSHA256 != p.RecordsSHA256 {
		return m, fmt.Errorf("manifest prepared authority differs")
	}
	records, id, e := openFile(dir, "records.bin", false)
	if e != nil {
		return m, e
	}
	defer records.Close()
	if id != p.Records {
		return m, fmt.Errorf("records born identity differs")
	}
	if e = verifyRecords(ctx, records, m); e != nil {
		return m, e
	}
	if e = verifyManifestBytes(ctx, dir, p); e != nil {
		return m, e
	}
	if e = checkName(dir, "records.bin", p.Records); e != nil {
		return m, e
	}
	if e = directoryNames(dir); e != nil {
		return m, e
	}
	check, _, e := openDir(root, name, &p.Directory)
	if e != nil {
		return m, e
	}
	check.Close()
	return m, nil
}

func verifyManifestBytes(ctx context.Context, dir *os.File, p Prepared) error {
	f, id, e := openFile(dir, "manifest.json", false)
	if e != nil {
		return e
	}
	defer f.Close()
	if id != p.Manifest {
		return fmt.Errorf("manifest identity changed")
	}
	raw, e := boundedRead(ctx, f, MaxManifestBytes)
	if e != nil {
		return e
	}
	if uint64(len(raw)) != p.ManifestBytes || digest(raw) != p.ManifestSHA256 {
		return fmt.Errorf("manifest bytes changed")
	}
	return nil
}

// Pack holds read-only verified file descriptors. Returned payloads/manifests
// are copies. Close joins in-progress Reads through the same mutex; no cached
// bytes authorize a read after canonical root/file identities become unsafe.
type Pack struct {
	mu                 sync.Mutex
	closed             bool
	rootPath           string
	root, dir, records *os.File
	receipt            Receipt
	manifest           Manifest
}

func Open(ctx context.Context, rootPath string, receipt Receipt) (*Pack, error) {
	if ctx == nil || !receipt.Prepared.valid() {
		return nil, fmt.Errorf("complete receipt/context required")
	}
	root, id, e := openRoot(rootPath)
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*Pack, error) { root.Close(); return nil, e }
	if id != receipt.Root {
		return fail(fmt.Errorf("root identity differs"))
	}
	if e = checkLock(root, receipt.Lock); e != nil {
		return fail(e)
	}
	m, e := verifyDirectory(ctx, root, finalName(receipt.ID), receipt.Prepared)
	if e != nil {
		return fail(e)
	}
	dir, _, e := openDir(root, finalName(receipt.ID), &receipt.Directory)
	if e != nil {
		return fail(e)
	}
	records, id, e := openFile(dir, "records.bin", false)
	if e != nil {
		dir.Close()
		return fail(e)
	}
	if id != receipt.Records {
		records.Close()
		dir.Close()
		return fail(fmt.Errorf("record identity changed after verification"))
	}
	if e = sameRoot(rootPath, receipt.Root); e != nil {
		records.Close()
		dir.Close()
		return fail(e)
	}
	pack := &Pack{rootPath: rootPath, root: root, dir: dir, records: records, receipt: receipt, manifest: m}
	if e = pack.guard(ctx); e != nil {
		pack.Close()
		return nil, e
	}
	return pack, nil
}
func (p *Pack) guard(ctx context.Context) error {
	if p.closed {
		return fmt.Errorf("pack closed")
	}
	if e := checkLock(p.root, p.receipt.Lock); e != nil {
		return e
	}
	if e := sameRoot(p.rootPath, p.receipt.Root); e != nil {
		return e
	}
	dir, _, e := openDir(p.root, finalName(p.receipt.ID), &p.receipt.Directory)
	if e != nil {
		return e
	}
	dir.Close()
	if e = checkName(p.dir, "records.bin", p.receipt.Records); e != nil {
		return e
	}
	if e = checkName(p.dir, "manifest.json", p.receipt.Manifest); e != nil {
		return e
	}
	if e = verifyManifestBytes(ctx, p.dir, p.receipt.Prepared); e != nil {
		return e
	}
	info, e := p.records.Stat()
	if e != nil {
		return e
	}
	if info.Size() < 0 || uint64(info.Size()) != p.receipt.RecordsBytes {
		return fmt.Errorf("records EOF changed")
	}
	// The manifest read/hash above can take time. Revalidate every canonical
	// identity after it, including the absolute parent, before returning bytes.
	if e = checkLock(p.root, p.receipt.Lock); e != nil {
		return e
	}
	late, _, e := openDir(p.root, finalName(p.receipt.ID), &p.receipt.Directory)
	if e != nil {
		return e
	}
	late.Close()
	if e = checkName(p.dir, "records.bin", p.receipt.Records); e != nil {
		return e
	}
	if e = checkName(p.dir, "manifest.json", p.receipt.Manifest); e != nil {
		return e
	}
	if e = directoryNames(p.dir); e != nil {
		return e
	}
	return sameRoot(p.rootPath, p.receipt.Root)
}
func (p *Pack) Read(ctx context.Context, key Key) ([]byte, error) {
	if ctx == nil || !key.valid() {
		return nil, fmt.Errorf("exact key/context required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := p.guard(ctx); e != nil {
		return nil, e
	}
	i := sort.Search(len(p.manifest.Entries), func(i int) bool { return compare(p.manifest.Entries[i].Key, key) >= 0 })
	if i == len(p.manifest.Entries) || compare(p.manifest.Entries[i].Key, key) != 0 {
		return nil, os.ErrNotExist
	}
	raw, e := readEntry(p.records, p.manifest.Entries[i])
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if e = p.guard(ctx); e != nil {
		return nil, e
	}
	return raw, nil
}
func (p *Pack) Manifest() (Manifest, error) {
	ctx := context.Background()
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.guard(ctx); e != nil {
		return Manifest{}, e
	}
	m := p.manifest
	m.Entries = append([]Entry{}, m.Entries...)
	return m, nil
}
func (p *Pack) Verify(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.guard(ctx); e != nil {
		return e
	}
	if _, e := verifyDirectory(ctx, p.root, finalName(p.receipt.ID), p.receipt.Prepared); e != nil {
		return e
	}
	return p.guard(ctx)
}
func (p *Pack) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return errors.Join(p.records.Close(), p.dir.Close(), p.root.Close())
}
