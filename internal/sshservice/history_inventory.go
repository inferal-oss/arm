package sshservice

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"getarm.org/internal/historypack"
	"golang.org/x/sys/unix"
)

func historyNames(dir *os.File, limit int) ([]string, error) {
	fd, e := unix.Openat(int(dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	scan := os.NewFile(uintptr(fd), ".")
	defer scan.Close()
	names, e := scan.Readdirnames(limit + 1)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(names) > limit {
		return nil, fmt.Errorf("source history complete directory bound exceeded")
	}
	sort.Strings(names)
	return names, nil
}
func (c *historyCatalog) checkPackIdentity(r historypack.Receipt) error {
	if r.Root != c.marker.Packs {
		return fmt.Errorf("source pack canonical root changed")
	}
	root, dir, e := openSourcePackIdentity(filepath.Join(c.path, "packs"), r)
	if e != nil {
		return e
	}
	defer root.Close()
	defer dir.Close()
	return nil
}

// Hold the exact root and pack while checking their named bindings. Relative
// opens avoid repeatedly walking the same absolute ancestor chain for every
// file/version observation. No held descriptor alone authorizes a renamed
// namespace: checkSourcePackIdentityAt rechecks the canonical root and named
// child as well as the complete lock/roster/file evidence.
func openSourcePackIdentity(path string, r historypack.Receipt) (*os.File, *os.File, error) {
	if !validID(r.ID) {
		return nil, nil, fmt.Errorf("invalid source pack ID")
	}
	root, e := privateDirectory(path)
	if e != nil {
		return nil, nil, e
	}
	fd, e := unix.Openat(int(root.Fd()), "pack-"+r.ID, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		root.Close()
		return nil, nil, e
	}
	dir := os.NewFile(uintptr(fd), "pack-"+r.ID)
	if e = checkSourcePackIdentityAt(root, dir, path, r); e != nil {
		dir.Close()
		root.Close()
		return nil, nil, e
	}
	return root, dir, nil
}

func checkSourcePackIdentityAt(root, dir *os.File, path string, r historypack.Receipt) error {
	if e := validateDirectory(root, path); e != nil {
		return e
	}
	id, e := historyDirIdentity(root)
	if e != nil || id != r.Root {
		return fmt.Errorf("source pack canonical root changed")
	}
	lockID, size, e := sourceFileIdentity(root, ".historypack.lock", 0)
	if e != nil || size != 0 || lockID != r.Lock {
		return fmt.Errorf("source pack publication lock changed")
	}
	id, e = historyDirIdentity(dir)
	if e != nil || id != r.Directory {
		return fmt.Errorf("published source pack identity changed")
	}
	var named unix.Stat_t
	if e = unix.Fstatat(int(root.Fd()), "pack-"+r.ID, &named, unix.AT_SYMLINK_NOFOLLOW); e != nil ||
		(historypack.Identity{Device: uint64(named.Dev), Inode: named.Ino, Owner: named.Uid, Mode: uint32(named.Mode)}) != id {
		return fmt.Errorf("published source pack named binding changed")
	}
	names, e := historyNames(dir, 2)
	if e != nil || len(names) != 2 || names[0] != "manifest.json" || names[1] != "records.bin" {
		return fmt.Errorf("source pack file roster changed")
	}
	for _, item := range []struct {
		name     string
		identity historypack.Identity
		size     uint64
	}{{"manifest.json", r.Manifest, r.ManifestBytes}, {"records.bin", r.Records, r.RecordsBytes}} {
		current, size, e := sourceFileIdentity(dir, item.name, SourceHistoryMaxPackBytes)
		if e != nil || current != item.identity || size < 0 || uint64(size) != item.size {
			return fmt.Errorf("source pack file identity/size changed")
		}
	}
	// Close the relative traversal with a fresh canonical root check and exact
	// named pack identity; a moved/replaced parent must not lend its old files.
	if e = unix.Fstatat(int(root.Fd()), "pack-"+r.ID, &named, unix.AT_SYMLINK_NOFOLLOW); e != nil ||
		(historypack.Identity{Device: uint64(named.Dev), Inode: named.Ino, Owner: named.Uid, Mode: uint32(named.Mode)}) != id {
		return fmt.Errorf("published source pack named binding changed")
	}
	return validateDirectory(root, path)
}
func (c *historyCatalog) checkInventory(ctx context.Context, summary SourceHistorySummary) error {
	names, e := historyNames(c.directory, 4)
	if e != nil {
		return e
	}
	want := []string{sourceHistoryWorkerLockName, SourceHistoryCatalogName, "packs"}
	sort.Strings(want)
	if len(names) != len(want) {
		return fmt.Errorf("unknown/partial source history namespace")
	}
	for i, name := range names {
		if name != want[i] {
			return fmt.Errorf("unknown source history namespace member")
		}
	}
	rows, e := c.db.QueryContext(ctx, `SELECT substr(id,1,33),substr(data,1,262145) FROM source_history_packs ORDER BY id LIMIT 257`)
	if e != nil {
		return e
	}
	packs := map[string]SourceHistoryPack{}
	totalMembers := 0
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return e
		}
		if len(packs) >= SourceHistoryMaxPacks {
			rows.Close()
			return fmt.Errorf("source pack roster exceeds bound")
		}
		var row SourceHistoryPack
		if e = decodeHistory([]byte(raw), &row); e != nil {
			rows.Close()
			return e
		}
		if row.Schema != 1 || !validID(id) || row.Receipt.ID != id || row.Receipt.Root != c.marker.Packs || row.Receipt.Authority != c.marker.Binding.Host || row.Members < 1 || row.Members > MaxCompletions || row.Receipt.RecordsBytes > SourceHistoryMaxPackBytes || row.Receipt.ManifestBytes > SourceHistoryMaxPackBytes-row.Receipt.RecordsBytes {
			rows.Close()
			return fmt.Errorf("invalid source pack roster metadata")
		}
		packs[id] = row
		totalMembers += row.Members
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	if len(packs) != summary.Packs || totalMembers != summary.Archived {
		return fmt.Errorf("permanent source key inventory lost a pack/ref dependency")
	}
	for id, row := range packs {
		var count int
		if e = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_history_refs WHERE pack=? AND source=?`, id, c.marker.Binding.Host).Scan(&count); e != nil {
			return e
		}
		if count != row.Members {
			return fmt.Errorf("source pack key fence count differs")
		}
		if e = c.checkPackIdentity(row.Receipt); e != nil {
			return e
		}
	}
	stage := ""
	if len(summary.Busy) == 1 {
		op, e := c.lookupOp(ctx, summary.Busy[0])
		if e != nil {
			return e
		}
		stage = op.PackID
	}
	root, e := privateDirectory(filepath.Join(c.path, "packs"))
	if e != nil {
		return e
	}
	defer root.Close()
	names, e = historyNames(root, SourceHistoryMaxPacks+2)
	if e != nil {
		return e
	}
	seen := 0
	for _, name := range names {
		if name == ".historypack.lock" {
			_, size, e := sourceFileIdentity(root, name, 0)
			if e != nil || size != 0 {
				return fmt.Errorf("invalid source pack lock")
			}
			continue
		}
		if strings.HasPrefix(name, "pack-") {
			id := strings.TrimPrefix(name, "pack-")
			if _, ok := packs[id]; ok {
				seen++
				continue
			}
			if id == stage && validID(id) {
				continue
			}
			return fmt.Errorf("published source pack lacks known operation/ref inventory")
		}
		if strings.HasPrefix(name, ".stage-") && strings.TrimPrefix(name, ".stage-") == stage && validID(stage) {
			continue
		}
		return fmt.Errorf("unfamiliar/partial source pack evidence retained")
	}
	if seen != len(packs) {
		return fmt.Errorf("known source pack is missing")
	}
	return c.guard()
}

// SourceHistoryReadMetadata is a bounded diagnostic copy, not a destructive
// settlement proof. Reset keeps independent shapes and semantic validation.
func SourceHistoryReadMetadata(ctx context.Context, home string, b Binding) (*SourceHistoryMarker, *SourceHistorySummary, error) {
	s, e := ReadState(ctx, home, b)
	if e != nil {
		return nil, nil, e
	}
	if s.historyMarker == nil {
		return nil, nil, nil
	}
	m := *s.historyMarker
	summary := *s.History
	summary.Busy = append([]string{}, summary.Busy...)
	return &m, &summary, nil
}
