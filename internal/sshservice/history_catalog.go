package sshservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"getarm.org/internal/historypack"
	"getarm.org/internal/local"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

var sourceHistoryDDL = []string{
	`CREATE TABLE source_history_metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
	`CREATE TABLE source_history_operations(key TEXT PRIMARY KEY,request TEXT NOT NULL,phase TEXT NOT NULL,data TEXT NOT NULL)`,
	`CREATE TABLE source_history_packs(id TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE source_history_refs(source TEXT NOT NULL,target TEXT NOT NULL,key TEXT NOT NULL,kind TEXT NOT NULL,pack TEXT NOT NULL,record TEXT NOT NULL,sha256 TEXT NOT NULL,intent_sha256 TEXT NOT NULL,PRIMARY KEY(source,target,key))`,
}
var sourceHistoryTables = []string{"source_history_metadata", "source_history_operations", "source_history_packs", "source_history_refs"}

type historyCatalog struct {
	ctx           context.Context
	home, path    string
	marker        SourceHistoryMarker
	directory     *os.File
	db            *sql.DB
	write         bool
	guardPolicy   func() error
	allowRecovery bool
}

func (c *historyCatalog) close() error { return errors.Join(c.db.Close(), c.directory.Close()) }
func sourceFileIdentity(d *os.File, name string, limit int64) (historypack.Identity, int64, error) {
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return historypack.Identity{}, 0, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return historypack.Identity{}, 0, e
	}
	id := historypack.Identity{Device: uint64(st.Dev), Inode: st.Ino, Owner: st.Uid, Mode: uint32(st.Mode)}
	if !historyIdentityValid(id, false) || id.Owner != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size < 0 || st.Size > limit {
		return id, st.Size, fmt.Errorf("unsafe/oversized source history file")
	}
	return id, st.Size, nil
}
func historyDirIdentity(f *os.File) (historypack.Identity, error) {
	v, e := statState(f)
	if e != nil {
		return historypack.Identity{}, e
	}
	id := historyID(v)
	if !historyIdentityValid(id, true) || id.Owner != uint32(os.Geteuid()) {
		return id, fmt.Errorf("invalid private history directory")
	}
	return id, nil
}
func (c *historyCatalog) guard() error {
	if e := local.CheckStateFence(c.home); e != nil {
		return e
	}
	if e := checkSourceRecoveryFence(c.ctx, c.home, c.marker, c.allowRecovery); e != nil {
		return e
	}
	if c.guardPolicy != nil {
		if e := c.guardPolicy(); e != nil {
			return e
		}
	}
	if e := validateDirectory(c.directory, c.path); e != nil {
		return e
	}
	id, e := historyDirIdentity(c.directory)
	if e != nil || id != c.marker.Directory {
		return fmt.Errorf("history directory born identity changed")
	}
	id, _, e = sourceFileIdentity(c.directory, SourceHistoryCatalogName, SourceHistoryMaxCatalogBytes)
	if e != nil || id != c.marker.Catalog {
		return fmt.Errorf("history catalog born identity changed: %v", e)
	}
	packs, e := privateDirectory(filepath.Join(c.path, "packs"))
	if e != nil {
		return e
	}
	packsID, e := historyDirIdentity(packs)
	packs.Close()
	if e != nil || packsID != c.marker.Packs {
		return fmt.Errorf("history packs-root born identity changed")
	}
	workerID, size, e := sourceFileIdentity(c.directory, sourceHistoryWorkerLockName, 0)
	if e != nil || size != 0 || workerID != c.marker.WorkerLock {
		return fmt.Errorf("history worker lock identity changed")
	}
	return nil
}
func checkHistorySidecars(dir *os.File, recover bool) error {
	for _, name := range []string{SourceHistoryCatalogName + "-wal", SourceHistoryCatalogName + "-shm", SourceHistoryCatalogName + "-journal"} {
		_, size, e := sourceFileIdentity(dir, name, SourceHistoryMaxCatalogBytes+4096)
		if errors.Is(e, unix.ENOENT) {
			continue
		}
		if e != nil {
			return e
		}
		if name != SourceHistoryCatalogName+"-journal" || !recover || size == 0 {
			return fmt.Errorf("source catalog sidecar requires explicit locked recovery")
		}
	}
	return nil
}
func openHistoryCatalog(ctx context.Context, home string, b Binding, m SourceHistoryMarker, write, recover bool, policy func() error) (*historyCatalog, error) {
	if ctx == nil || m.Validate() != nil || m.Binding != b || write && policy == nil || recover && !write {
		return nil, fmt.Errorf("exact history authority and owner lock required")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := local.CheckStateFence(home); e != nil {
		return nil, e
	}
	dir, e := privateDirectory(filepath.Join(home, SourceHistoryName))
	if e != nil {
		return nil, e
	}
	c := &historyCatalog{home: home, ctx: ctx, path: filepath.Join(home, SourceHistoryName), marker: m, directory: dir, write: write, guardPolicy: policy, allowRecovery: recover}
	fail := func(e error) (*historyCatalog, error) {
		if c.db != nil {
			c.db.Close()
		}
		dir.Close()
		return nil, e
	}
	if e = c.guard(); e != nil {
		return fail(e)
	}
	if e = checkHistorySidecars(dir, recover); e != nil {
		return fail(e)
	}
	mode := "ro"
	pragma := "&_pragma=query_only(1)"
	if write {
		mode = "rw"
		pragma = ""
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.Join(c.path, SourceHistoryCatalogName), RawQuery: "mode=" + mode + "&_pragma=busy_timeout(50)" + pragma}).String()
	c.db, e = sql.Open("sqlite", uri)
	if e != nil {
		return fail(e)
	}
	c.db.SetMaxOpenConns(1)
	if write {
		for _, q := range []string{`PRAGMA synchronous=FULL`, `PRAGMA foreign_keys=ON`, `PRAGMA trusted_schema=OFF`} {
			if _, e = c.db.ExecContext(ctx, q); e != nil {
				return fail(e)
			}
		}
	}
	var boundedPages int
	if e = c.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", SourceHistoryMaxPages)).Scan(&boundedPages); e != nil || boundedPages != SourceHistoryMaxPages {
		return fail(fmt.Errorf("source catalog page bound could not be established: %v", e))
	}
	if e = c.schema(ctx); e != nil {
		return fail(e)
	}
	if e = c.guard(); e != nil {
		return fail(e)
	}
	if e = checkHistorySidecars(dir, false); e != nil {
		return fail(e)
	}
	// Recovered SQLite bytes are not by themselves family proof. The caller of
	// explicit recovery must validate every op/ref/pack before continuation.
	return c, nil
}
func (c *historyCatalog) schema(ctx context.Context) error {
	rows, e := c.db.QueryContext(ctx, `SELECT type,name,tbl_name,substr(sql,1,4097) FROM sqlite_schema ORDER BY name LIMIT 13`)
	if e != nil {
		return e
	}
	defer rows.Close()
	tables := map[string]string{}
	indexes := map[string]bool{}
	count := 0
	for rows.Next() {
		count++
		if count > 8 {
			return fmt.Errorf("unknown source catalog schema objects")
		}
		var kind, name, table string
		var ddl sql.NullString
		if e = rows.Scan(&kind, &name, &table, &ddl); e != nil {
			return e
		}
		if kind == "table" && ddl.Valid {
			tables[name] = ddl.String
		} else if kind == "index" && !ddl.Valid && name == "sqlite_autoindex_"+table+"_1" {
			indexes[table] = true
		} else {
			return fmt.Errorf("unknown source history schema object")
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	rows.Close()
	normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	if len(tables) != len(sourceHistoryTables) || len(indexes) != len(sourceHistoryTables) {
		return fmt.Errorf("partial source history schema")
	}
	for i, name := range sourceHistoryTables {
		if normalize(tables[name]) != normalize(sourceHistoryDDL[i]) || !indexes[name] {
			return fmt.Errorf("source history DDL differs")
		}
	}
	var pageSize, pages, maxPages int
	var mode string
	for _, v := range []struct {
		query  string
		target any
	}{{`PRAGMA page_size`, &pageSize}, {`PRAGMA page_count`, &pages}, {`PRAGMA max_page_count`, &maxPages}, {`PRAGMA journal_mode`, &mode}} {
		if e = c.db.QueryRowContext(ctx, v.query).Scan(v.target); e != nil {
			return e
		}
	}
	if pageSize != SourceHistoryPageSize || pages > SourceHistoryMaxPages || pages < 1 || maxPages > SourceHistoryMaxPages || maxPages < pages || mode != "delete" {
		return fmt.Errorf("source history SQLite bounds/journal policy differs")
	}
	meta, e := json.Marshal(c.marker)
	if e != nil {
		return e
	}
	rows, e = c.db.QueryContext(ctx, `SELECT substr(key,1,65),substr(value,1,16385) FROM source_history_metadata LIMIT 3`)
	if e != nil {
		return e
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var k, v string
		if e = rows.Scan(&k, &v); e != nil {
			return e
		}
		seen[k] = v
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if len(seen) != 2 || seen["schema"] != "1" || seen["marker"] != string(meta) {
		return fmt.Errorf("source history marker/catalog metadata differs")
	}
	return nil
}
func historyCanonicalMarker(ctx context.Context, home string, b Binding) (*SourceHistoryMarker, error) {
	dir, e := privateDirectory(filepath.Join(home, "mesh"))
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	raw, _, markerErr := readStateFile(ctx, dir, SourceHistoryMarkerName, MaxIntentBytes)
	h, e := privateDirectory(filepath.Join(home, SourceHistoryName))
	present := e == nil
	if h != nil {
		h.Close()
	}
	if e != nil && !errors.Is(e, unix.ENOENT) {
		return nil, e
	}
	if errors.Is(markerErr, unix.ENOENT) && !present {
		return nil, nil
	}
	if markerErr != nil || !present {
		return nil, fmt.Errorf("source history marker/directory incomplete: %w", ErrHistoryUnavailable)
	}
	var m SourceHistoryMarker
	if e = DecodeStateRecord(raw, &m); e != nil || m.Validate() != nil || m.Binding != b {
		return nil, fmt.Errorf("source history marker invalid")
	}
	return &m, nil
}
func (c *historyCatalog) summary(ctx context.Context) (SourceHistorySummary, error) {
	out := SourceHistorySummary{CatalogID: c.marker.CatalogID, Busy: []string{}}
	if e := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_history_refs`).Scan(&out.Archived); e != nil {
		return out, e
	}
	if e := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_history_packs`).Scan(&out.Packs); e != nil {
		return out, e
	}
	if out.Archived > SourceHistoryMaxRefs || out.Packs > SourceHistoryMaxPacks {
		return out, fmt.Errorf("history permanent roster exceeds bounds")
	}
	rows, e := c.db.QueryContext(ctx, `SELECT substr(key,1,129),substr(phase,1,65) FROM source_history_operations ORDER BY key LIMIT 257`)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		var key, phase string
		if e = rows.Scan(&key, &phase); e != nil {
			return out, e
		}
		if n > SourceHistoryMaxPacks || !stateText(key, 128) || !historyPhase(phase) {
			return out, fmt.Errorf("invalid history operation roster")
		}
		if phase != "completed" {
			out.Busy = append(out.Busy, key)
		}
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Busy) > 1 {
		return out, fmt.Errorf("multiple unfinished history operations")
	}
	return out, c.guard()
}
func historyPhase(phase string) bool {
	switch phase {
	case "prepared", "pack_prepared", "published", "committed", "completed", "needs_attention":
		return true
	}
	return false
}
func (c *historyCatalog) lookupOp(ctx context.Context, key string) (SourceHistoryOperation, error) {
	var raw, request, phase string
	e := c.db.QueryRowContext(ctx, `SELECT substr(request,1,262145),substr(data,1,262145),substr(phase,1,65) FROM source_history_operations WHERE key=?`, key).Scan(&request, &raw, &phase)
	if e != nil {
		return SourceHistoryOperation{}, e
	}
	var op SourceHistoryOperation
	if e = decodeHistory([]byte(raw), &op); e != nil {
		return op, e
	}
	canonical, e := json.Marshal(op.Request)
	if e != nil || string(canonical) != request || op.Request.Key != key || op.Phase != phase || op.validate(c.marker.Binding) != nil || op.Request.Plan.CatalogID != "" && op.Request.Plan.CatalogID != c.marker.CatalogID {
		return op, fmt.Errorf("archive operation exact request/phase differs")
	}
	return op, c.guard()
}
func (op SourceHistoryOperation) validate(b Binding) error {
	if op.Schema != 1 || op.Request.Validate() != nil || op.Request.Plan.Binding != b || op.RequestSHA256 != historyRequestSHA(op.Request) || op.Actor.UID != b.Owner || op.Actor.Username != b.Account || op.Created.IsZero() || op.Updated.Before(op.Created) || op.Revision < 1 || !historyPhase(op.Phase) || !validID(op.PackID) || op.Error != "" && !stateText(op.Error, 4096) || op.Cleaned == nil || len(op.Cleaned) > len(op.Request.Plan.Members) {
		return fmt.Errorf("invalid exact archive operation")
	}
	if op.Receipt != nil && (op.Prepared == nil || op.Receipt.Prepared != *op.Prepared) {
		return fmt.Errorf("archive publication identity differs")
	}
	if op.Prepared != nil && (op.Prepared.ID != op.PackID || op.Prepared.Authority != b.Host) {
		return fmt.Errorf("archive born pack differs")
	}
	if (op.Phase == "pack_prepared" || op.Phase == "published" || op.Phase == "committed" || op.Phase == "completed") && op.Prepared == nil {
		return fmt.Errorf("archive phase lacks born evidence")
	}
	if (op.Phase == "published" || op.Phase == "committed" || op.Phase == "completed") && op.Receipt == nil {
		return fmt.Errorf("archive phase lacks published receipt")
	}
	seen := map[string]bool{}
	for _, name := range op.Cleaned {
		if seen[name] {
			return fmt.Errorf("duplicate archive cleanup")
		}
		seen[name] = true
		found := false
		for _, m := range op.Request.Plan.Members {
			if name == m.File {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown cleaned member")
		}
	}
	if op.Phase == "completed" && len(op.Cleaned) != len(op.Request.Plan.Members) {
		return fmt.Errorf("completed archive lost cleanup evidence")
	}
	return nil
}
func (c *historyCatalog) saveOp(ctx context.Context, op SourceHistoryOperation, insert bool) error {
	if !c.write || op.validate(c.marker.Binding) != nil {
		return fmt.Errorf("valid owner archive operation required")
	}
	raw, e := json.Marshal(op)
	if e != nil || len(raw) > SourceHistoryMaxRecordBytes {
		return fmt.Errorf("archive op exceeds bound")
	}
	request, _ := json.Marshal(op.Request)
	if e = c.guard(); e != nil {
		return e
	}
	if insert {
		_, e = c.db.ExecContext(ctx, `INSERT INTO source_history_operations(key,request,phase,data) VALUES(?,?,?,?)`, op.Request.Key, string(request), op.Phase, string(raw))
	} else {
		var result sql.Result
		result, e = c.db.ExecContext(ctx, `UPDATE source_history_operations SET phase=?,data=? WHERE key=? AND request=?`, op.Phase, string(raw), op.Request.Key, string(request))
		if e == nil {
			var n int64
			n, e = result.RowsAffected()
			if e == nil && n != 1 {
				e = fmt.Errorf("exact archive operation disappeared")
			}
		}
	}
	if e != nil {
		return fmt.Errorf("archive catalog write outcome uncertain; inspect exact key: %w", e)
	}
	return c.guard()
}
func (c *historyCatalog) ref(ctx context.Context, target, key string) (SourceHistoryRef, error) {
	var r SourceHistoryRef
	var record string
	e := c.db.QueryRowContext(ctx, `SELECT substr(source,1,33),substr(target,1,33),substr(key,1,129),substr(kind,1,33),substr(pack,1,33),substr(record,1,4097),substr(sha256,1,65),substr(intent_sha256,1,65) FROM source_history_refs WHERE source=? AND target=? AND key=?`, c.marker.Binding.Host, target, key).Scan(&r.Source, &r.Target, &r.Key, &r.Kind, &r.Pack, &record, &r.SHA256, &r.IntentSHA256)
	if e != nil {
		return r, e
	}
	if e = decodeHistory([]byte(record), &r.Record); e != nil {
		return r, e
	}
	m := SourceHistoryMember{Source: r.Source, Target: r.Target, Key: r.Key, Kind: r.Kind}
	if r.Source != c.marker.Binding.Host || r.Target != target || r.Key != key || !validID(r.Pack) || !hexString(r.SHA256, 64) || !hexString(r.IntentSHA256, 64) || (r.Kind != "completion" && r.Kind != "seal") || r.Record != sourceHistoryKey(m) {
		return r, fmt.Errorf("invalid exact cold key reference")
	}
	return r, c.guard()
}
func (c *historyCatalog) pack(ctx context.Context, id string) (SourceHistoryPack, error) {
	var raw string
	e := c.db.QueryRowContext(ctx, `SELECT substr(data,1,262145) FROM source_history_packs WHERE id=?`, id).Scan(&raw)
	if e != nil {
		return SourceHistoryPack{}, e
	}
	var p SourceHistoryPack
	if e = decodeHistory([]byte(raw), &p); e != nil {
		return p, e
	}
	if p.Schema != 1 || p.Receipt.ID != id || p.Receipt.Authority != c.marker.Binding.Host || p.Receipt.Root != c.marker.Packs || p.Members < 1 || p.Members > MaxCompletions || !stateText(p.Operation, 128) || p.Receipt.RecordsBytes > SourceHistoryMaxPackBytes || p.Receipt.ManifestBytes > SourceHistoryMaxPackBytes-p.Receipt.RecordsBytes {
		return p, fmt.Errorf("invalid source pack identity/bound")
	}
	return p, c.guard()
}
func (c *historyCatalog) ensureCapacity(ctx context.Context, members int) error {
	summary, e := c.summary(ctx)
	if e != nil {
		return e
	}
	if len(summary.Busy) > 0 {
		return ErrHistoryBusy
	}
	if summary.Packs >= SourceHistoryMaxPacks || members > SourceHistoryMaxRefs-summary.Archived {
		return fmt.Errorf("source history bounded capacity exhausted; permanent keys retained")
	}
	var pages, free int
	if e = c.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); e != nil {
		return e
	}
	if e = c.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); e != nil {
		return e
	}
	// Reserve2MiB for the final op/ref/pack transaction and every cleanup
	// receipt update; largest admitted request/result stays<=256KiB each.
	if pages-free+sourceHistoryPageReserve > SourceHistoryMaxPages {
		return fmt.Errorf("source catalog completion growth reserve exhausted")
	}
	return nil
}
func initializeHistory(ctx context.Context, s *State, meshDir *os.File) (SourceHistoryMarker, error) {
	if s.guard == nil {
		return SourceHistoryMarker{}, fmt.Errorf("source owner locks required")
	}
	if e := s.guard(); e != nil {
		return SourceHistoryMarker{}, e
	}
	if _, e := os.Lstat(filepath.Join(s.home, SourceHistoryName)); !os.IsNotExist(e) {
		return SourceHistoryMarker{}, fmt.Errorf("history initialization namespace already exists")
	}
	root, e := privateDirectory(s.home)
	if e != nil {
		return SourceHistoryMarker{}, e
	}
	defer root.Close()
	if e = unix.Mkdirat(int(root.Fd()), SourceHistoryName, 0700); e != nil {
		return SourceHistoryMarker{}, e
	}
	dir, e := privateDirectory(filepath.Join(s.home, SourceHistoryName))
	if e != nil {
		return SourceHistoryMarker{}, e
	}
	defer dir.Close()
	if e = root.Sync(); e != nil {
		return SourceHistoryMarker{}, e
	}
	if e = unix.Mkdirat(int(dir.Fd()), "packs", 0700); e != nil {
		return SourceHistoryMarker{}, e
	}
	packs, e := privateDirectory(filepath.Join(s.home, SourceHistoryName, "packs"))
	if e != nil {
		return SourceHistoryMarker{}, e
	}
	defer packs.Close()
	fd, e := unix.Openat(int(dir.Fd()), SourceHistoryCatalogName, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return SourceHistoryMarker{}, e
	}
	file := os.NewFile(uintptr(fd), SourceHistoryCatalogName)
	if e = file.Sync(); e != nil {
		file.Close()
		return SourceHistoryMarker{}, e
	}
	file.Close()
	workerFD, e := unix.Openat(int(dir.Fd()), sourceHistoryWorkerLockName, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return SourceHistoryMarker{}, e
	}
	workerFile := os.NewFile(uintptr(workerFD), sourceHistoryWorkerLockName)
	if e = workerFile.Sync(); e != nil {
		workerFile.Close()
		return SourceHistoryMarker{}, e
	}
	workerFile.Close()
	m := SourceHistoryMarker{Schema: 1, Binding: s.Binding, CatalogID: local.ID()}
	m.Directory, e = historyDirIdentity(dir)
	if e != nil {
		return m, e
	}
	m.Packs, e = historyDirIdentity(packs)
	if e != nil {
		return m, e
	}
	m.Catalog, _, e = sourceFileIdentity(dir, SourceHistoryCatalogName, SourceHistoryMaxCatalogBytes)
	if e != nil {
		return m, e
	}
	m.WorkerLock, _, e = sourceFileIdentity(dir, sourceHistoryWorkerLockName, 0)
	if e != nil {
		return m, e
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.Join(s.home, SourceHistoryName, SourceHistoryCatalogName), RawQuery: "mode=rw"}).String()
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		return m, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{fmt.Sprintf(`PRAGMA page_size=%d`, SourceHistoryPageSize), `PRAGMA journal_mode=DELETE`, `PRAGMA synchronous=FULL`, fmt.Sprintf(`PRAGMA max_page_count=%d`, SourceHistoryMaxPages)} {
		if _, e = db.ExecContext(ctx, query); e != nil {
			return m, e
		}
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return m, e
	}
	defer tx.Rollback()
	for _, ddl := range sourceHistoryDDL {
		if _, e = tx.ExecContext(ctx, ddl); e != nil {
			return m, e
		}
	}
	raw, _ := json.Marshal(m)
	if _, e = tx.ExecContext(ctx, `INSERT INTO source_history_metadata(key,value) VALUES('schema','1'),('marker',?)`, string(raw)); e != nil {
		return m, e
	}
	if e = tx.Commit(); e != nil {
		return m, fmt.Errorf("history initialization outcome uncertain: %w", e)
	}
	if e = db.Close(); e != nil {
		return m, e
	}
	if e = dir.Sync(); e != nil {
		return m, e
	}
	if e = s.guard(); e != nil {
		return m, e
	}
	raw, _ = json.Marshal(m)
	if e = atomicStateFile(ctx, s, meshDir, SourceHistoryMarkerName, raw, nil); e != nil {
		return m, e
	}
	return m, nil
}

// HistorySchemaStatements returns a copy for non-destructive diagnostics only.
// Destructive maintenance must keep its own independently reviewed DDL mirror.
func HistorySchemaStatements() []string { return append([]string{}, sourceHistoryDDL...) }

func loadSourceHistorySummary(ctx context.Context, s *State) error {
	m, e := historyCanonicalMarker(ctx, s.home, s.Binding)
	if e != nil {
		return e
	}
	if m == nil {
		if s.historyMarker != nil {
			return ErrHistoryUnavailable
		}
		return nil
	}
	if s.historyMarker == nil || *m != *s.historyMarker {
		return fmt.Errorf("source history marker changed")
	}
	c, e := openHistoryCatalog(ctx, s.home, s.Binding, *m, false, false, nil)
	if e != nil {
		return fmt.Errorf("%w: %v", ErrHistoryUnavailable, e)
	}
	defer c.close()
	summary, e := c.summary(ctx)
	if e != nil {
		return e
	}
	if e = c.checkInventory(ctx, summary); e != nil {
		return e
	}
	s.History = &summary
	return nil
}
