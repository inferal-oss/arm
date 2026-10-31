package agentsessions

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const storedIndexBytes = 1 << 20
const storedExtentBytes = 32 << 20

// CodexStored reads explicit saved names, not prompt-derived titles. It uses a
// read-only main database and normal SQLite locking; ordinary WAL/SHM reader
// housekeeping may occur. It never starts a client or an ARM backend.
type CodexStored struct{ Home string }

func (c CodexStored) DiscoverTitles(ctx context.Context, roots []string) TitleResult {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	titles, warnings, err := c.read(ctx, roots)
	if err != nil {
		return TitleResult{Warnings: append(warnings, "Codex saved session names are unavailable or unsupported; directory suggestions remain selectable.")}
	}
	return TitleResult{Titles: titles, Warnings: warnings}
}

type storedFile struct {
	name string
	file *os.File
	info os.FileInfo
}

func openStored(root *os.Root, name string, optional bool) (*storedFile, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if optional && os.IsNotExist(err) {
		return &storedFile{name: name}, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > storedExtentBytes {
		return nil, errors.Join(err, f.Close(), fmt.Errorf("unsupported metadata file"))
	}
	return &storedFile{name: name, file: f, info: info}, nil
}
func (f *storedFile) stable(root *os.Root) bool {
	after, err := root.Lstat(f.name)
	if f.file == nil {
		if os.IsNotExist(err) {
			return true
		}
		// A read-only connection to a checkpointed WAL-mode database may
		// create an empty WAL. No newly committed frames are accepted.
		return f.name == "state_5.sqlite-wal" && err == nil && after.Mode().IsRegular() && after.Size() == 0
	}
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(f.info, after) || f.info.Size() != after.Size() || !f.info.ModTime().Equal(after.ModTime()) {
		return false
	}
	held, err := f.file.Stat()
	return err == nil && os.SameFile(f.info, held) && f.info.Size() == held.Size() && f.info.ModTime().Equal(held.ModTime())
}

func storedExtents(root *os.Root) error {
	// Extents bound supported input, not total SQLite I/O or heap. SHM is
	// synchronization state: normal reader bookkeeping is not claimed unchanged.
	var extent int64
	for _, name := range []string{"session_index.jsonl", "state_5.sqlite", "state_5.sqlite-wal", "state_5.sqlite-shm", "state_5.sqlite-journal"} {
		info, e := root.Lstat(name)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported metadata extent")
		}
		if info.Size() < 0 || info.Size() > storedExtentBytes-extent {
			return fmt.Errorf("metadata extent limit")
		}
		extent += info.Size()
	}
	return nil
}

type savedName struct {
	name    string
	updated time.Time
}

func (c CodexStored) read(ctx context.Context, roots []string) (titles []Title, warnings []string, err error) {
	if len(roots) == 0 || len(roots) > Limit || !filepath.IsAbs(c.Home) || len(c.Home) > 4096 {
		return nil, nil, fmt.Errorf("unsupported metadata scope")
	}
	requested := map[string]bool{}
	for _, root := range roots {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if !filepath.IsAbs(root) || len(root) > 4096 {
			return nil, nil, fmt.Errorf("unsupported metadata scope")
		}
		resolved, e := filepath.EvalSymlinks(root)
		if e != nil {
			continue
		}
		info, e := os.Stat(resolved)
		if e == nil && info.IsDir() && len(resolved) <= 4096 {
			requested[resolved] = true
		}
	}
	if len(requested) == 0 {
		return nil, nil, nil
	}
	home, e := filepath.EvalSymlinks(c.Home)
	if e != nil {
		return nil, nil, e
	}
	if len(home) > 4096 {
		return nil, nil, fmt.Errorf("unsupported metadata home")
	}
	root, e := os.OpenRoot(home)
	if e != nil {
		return nil, nil, e
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	homeInfo, e := root.Stat(".")
	if e != nil {
		return nil, nil, e
	}
	var held []*storedFile
	defer func() {
		for _, f := range held {
			if f.file != nil {
				err = errors.Join(err, f.file.Close())
			}
		}
	}()
	for _, item := range []struct {
		name     string
		optional bool
	}{{"session_index.jsonl", false}, {"state_5.sqlite", false}, {"state_5.sqlite-wal", true}} {
		f, e := openStored(root, item.name, item.optional)
		if e != nil {
			return nil, nil, e
		}
		held = append(held, f)
	}
	if e := storedExtents(root); e != nil {
		return nil, nil, e
	}
	index := held[0]
	if index.info.Size() > storedIndexBytes {
		return nil, nil, fmt.Errorf("metadata index limit")
	}
	data, e := io.ReadAll(io.LimitReader(index.file, storedIndexBytes))
	if e != nil || !index.stable(root) {
		return nil, nil, fmt.Errorf("metadata index changed")
	}
	names := map[string]savedName{}
	unsupported := false
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > Limit {
		lines = lines[len(lines)-Limit:]
		warnings = append(warnings, "Codex saved-name index is truncated to its last 512 records.")
	}
	for _, line := range lines {
		if ctx.Err() != nil {
			return nil, warnings, ctx.Err()
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(line) > 64<<10 {
			unsupported = true
			continue
		}
		var record struct {
			ID      string `json:"id"`
			Name    string `json:"thread_name"`
			Updated string `json:"updated_at"`
		}
		if json.Unmarshal(line, &record) != nil {
			unsupported = true
			continue
		}
		id, e := uuid.Parse(record.ID)
		at, dateErr := time.Parse(time.RFC3339Nano, record.Updated)
		if e != nil || dateErr != nil {
			unsupported = true
			continue
		}
		if strings.TrimSpace(record.Name) != "" {
			names[id.String()] = savedName{record.Name, at.UTC()}
		}
	}
	if unsupported {
		warnings = append(warnings, "Some Codex saved-name records use an unsupported format.")
	}
	if len(names) == 0 {
		return nil, warnings, nil
	}
	directories := make([]string, 0, len(requested))
	for directory := range requested {
		directories = append(directories, directory)
	}
	slices.Sort(directories)
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	matches, e := readStoredDirectories(ctx, filepath.Join(home, "state_5.sqlite"), directories, ids)
	if e != nil {
		return nil, warnings, e
	}
	for _, f := range held {
		if !f.stable(root) {
			return nil, warnings, fmt.Errorf("metadata changed")
		}
	}
	if e := storedExtents(root); e != nil {
		return nil, warnings, e
	}
	current, e := os.Stat(home)
	if e != nil || !os.SameFile(homeInfo, current) {
		return nil, warnings, fmt.Errorf("metadata home changed")
	}
	for _, id := range ids {
		if directory := matches[id]; directory != "" {
			titles = append(titles, Title{Directory: directory, Name: names[id].name, Updated: names[id].updated})
		}
	}
	return titles, warnings, ctx.Err()
}

func readStoredDirectories(ctx context.Context, path string, roots, ids []string) (matches map[string]string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	uri := url.URL{Scheme: "file", Path: path}
	parameters := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(250)"}}
	uri.RawQuery = parameters.Encode()
	db, e := sql.Open("sqlite", uri.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	defer func() { err = errors.Join(err, db.Close()) }()
	conn, e := db.Conn(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	// The public read-only probe verifies main-file enforcement independently
	// of the transaction option (which only chooses deferred BEGIN).
	if e = conn.Raw(func(raw any) error {
		probe, ok := raw.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			return fmt.Errorf("read-only verification unsupported")
		}
		readonly, e := probe.IsReadOnly("main")
		if e != nil {
			return e
		}
		if !readonly {
			return fmt.Errorf("metadata main is not read-only")
		}
		return nil
	}); e != nil {
		return nil, e
	}
	for _, statement := range []string{"PRAGMA query_only=ON", "PRAGMA trusted_schema=OFF"} {
		if _, e = conn.ExecContext(ctx, statement); e != nil {
			return nil, e
		}
	}
	for _, guard := range []struct {
		sql  string
		want int
	}{{"PRAGMA query_only", 1}, {"PRAGMA trusted_schema", 0}} {
		var value int
		if e = conn.QueryRowContext(ctx, guard.sql).Scan(&value); e != nil {
			return nil, e
		}
		if value != guard.want {
			return nil, fmt.Errorf("metadata connection guard unsupported")
		}
	}
	tx, e := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer func() {
		if tx != nil {
			rollback := tx.Rollback()
			if !(errors.Is(rollback, sql.ErrTxDone) && ctx.Err() != nil) {
				err = errors.Join(err, rollback)
			}
		}
	}()
	if e = storedSchema(ctx, tx); e != nil {
		return nil, e
	}
	placeholders := func(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
	query := "SELECT id,cwd FROM main.threads WHERE id IN (" + placeholders(len(ids)) + ") AND cwd IN (" + placeholders(len(roots)) + ") AND typeof(id)='text' AND length(id)=36 AND typeof(cwd)='text' AND length(cwd)<=4096 LIMIT 513"
	args := make([]any, 0, len(ids)+len(roots))
	for _, id := range ids {
		args = append(args, id)
	}
	for _, root := range roots {
		args = append(args, root)
	}
	rows, e := tx.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer func() {
		if rows != nil {
			err = errors.Join(err, rows.Close())
		}
	}()
	matches = map[string]string{}
	ambiguous := map[string]bool{}
	wanted := map[string]bool{}
	for _, root := range roots {
		wanted[root] = true
	}
	count := 0
	for rows.Next() {
		count++
		if count > Limit {
			return nil, fmt.Errorf("metadata row limit")
		}
		var id, directory string
		if e = rows.Scan(&id, &directory); e != nil {
			return nil, e
		}
		parsed, e := uuid.Parse(id)
		if e != nil || !wanted[directory] {
			return nil, fmt.Errorf("unsupported metadata association")
		}
		key := parsed.String()
		if old := matches[key]; old != "" && old != directory {
			ambiguous[key] = true
			delete(matches, key)
		}
		if !ambiguous[key] {
			matches[key] = directory
		}
	}
	if e = errors.Join(rows.Err(), ctx.Err(), rows.Close()); e != nil {
		return nil, e
	}
	rows = nil
	if e = tx.Rollback(); e != nil {
		return nil, e
	}
	tx = nil
	return matches, nil
}

func storedSchema(ctx context.Context, tx *sql.Tx) (err error) {
	rows, e := tx.QueryContext(ctx, "PRAGMA main.table_list('threads')")
	if e != nil {
		return e
	}
	count := 0
	for rows.Next() {
		var schema, name, kind string
		var columns, withoutRowid, strict int
		if e = rows.Scan(&schema, &name, &kind, &columns, &withoutRowid, &strict); e != nil {
			break
		}
		count++
		if count > 1 || schema != "main" || name != "threads" || kind != "table" {
			e = fmt.Errorf("unsupported metadata table")
			break
		}
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return e
	}
	if count != 1 {
		return fmt.Errorf("missing metadata table")
	}
	rows, e = tx.QueryContext(ctx, "PRAGMA main.table_xinfo('threads')")
	if e != nil {
		return e
	}
	found := map[string]bool{}
	columns := 0
	for rows.Next() {
		columns++
		if columns > 64 {
			e = fmt.Errorf("metadata column limit")
			break
		}
		var name, kind string
		var cid, notNull, primaryKey, hidden int
		var defaultValue any
		if e = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey, &hidden); e != nil {
			break
		}
		if name != "id" && name != "cwd" {
			continue
		}
		if found[name] || !strings.EqualFold(strings.TrimSpace(kind), "TEXT") || hidden != 0 {
			e = fmt.Errorf("unsupported metadata columns")
			break
		}
		found[name] = true
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return e
	}
	if len(found) != 2 {
		return fmt.Errorf("missing metadata columns")
	}
	return nil
}
