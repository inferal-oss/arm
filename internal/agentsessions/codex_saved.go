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
	"time"

	"github.com/google/uuid"
)

// DiscoverSavedSessions is an additive display catalog. Its explicit scope and
// canonical native references carry no native generation or ARM authority.
// The legacy title parser/query and ordinary native discovery are unchanged.
func (c CodexStored) DiscoverSavedSessions(ctx context.Context, request SavedSessionRequest) SavedSessionResult {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return c.discoverSaved(ctx, request, savedCatalogHooks{})
}

// Hooks let owned fixtures observe actual close results and the final stability
// boundary. Production uses zero hooks; a hook cannot prevent a resource close.
type savedCatalogHooks struct {
	closed             func(string, error) error
	beforeStable       func()
	afterDirectory     func(string)
	beforeMetadataOpen func(string)
}

func (h savedCatalogHooks) close(kind string, closer io.Closer) error {
	err := closer.Close()
	if h.closed != nil {
		err = errors.Join(err, h.closed(kind, err))
	}
	return err
}

func savedWarning(out *SavedSessionResult, warning string) {
	if len(out.Warnings) < 16 && !slices.Contains(out.Warnings, warning) {
		out.Warnings = append(out.Warnings, warning)
	}
}

func canonicalSavedDirectory(path string) (string, error) {
	if !savedPath(path) {
		return "", fmt.Errorf("unsupported saved directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", fmt.Errorf("saved directory is not canonical")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("saved directory unavailable")
	}
	return resolved, nil
}

func (c CodexStored) discoverSaved(ctx context.Context, request SavedSessionRequest, hooks savedCatalogHooks) SavedSessionResult {
	out, err := c.readSaved(ctx, request, hooks)
	if err != nil {
		out.Sessions = nil
		savedWarning(&out, "Codex saved chats are unavailable or unsupported; directory projects remain selectable.")
	}
	return out
}

func (c CodexStored) readSaved(ctx context.Context, request SavedSessionRequest, hooks savedCatalogHooks) (out SavedSessionResult, err error) {
	if !request.valid() || !savedPath(c.Home) {
		return out, fmt.Errorf("unsupported saved catalog scope")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	for _, path := range request.Directories {
		if _, err = canonicalSavedDirectory(path); err != nil {
			return out, err
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
	}
	home, err := filepath.EvalSymlinks(c.Home)
	if err != nil || !savedPath(home) {
		return out, errors.Join(err, fmt.Errorf("unsupported saved catalog home"))
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, hooks.close("home", root), ctx.Err()) }()
	homeInfo, err := root.Stat(".")
	if err != nil {
		return out, err
	}
	var held []*storedFile
	defer func() {
		for _, file := range held {
			if file.file != nil {
				err = errors.Join(err, hooks.close(file.name, file.file))
			}
		}
	}()
	for _, item := range []struct {
		name     string
		optional bool
	}{{"session_index.jsonl", true}, {"state_5.sqlite", false}, {"state_5.sqlite-wal", true}} {
		file, openErr := openStored(root, item.name, item.optional)
		if openErr != nil {
			return out, openErr
		}
		held = append(held, file)
	}
	if err = storedExtents(root); err != nil {
		return out, err
	}
	names := map[string]savedName{}
	index := held[0]
	if index.file == nil {
		savedWarning(&out, "Codex saved names are unavailable; unnamed chats use their saved IDs.")
	} else {
		if index.info.Size() > storedIndexBytes {
			return out, fmt.Errorf("saved index limit")
		}
		data, readErr := io.ReadAll(io.LimitReader(index.file, storedIndexBytes+1))
		if readErr != nil || len(data) > storedIndexBytes || !index.stable(root) {
			return out, errors.Join(readErr, fmt.Errorf("saved index changed or exceeded limit"))
		}
		names, err = parseSavedCatalogNames(ctx, data, &out)
		if err != nil {
			return out, err
		}
	}
	rows, err := readSavedCatalogRows(ctx, filepath.Join(home, "state_5.sqlite"), request, names, hooks)
	if err != nil {
		return out, err
	}
	out.Truncated = rows.truncated
	if rows.unsupported {
		savedWarning(&out, "Some Codex saved IDs or directory associations were unsupported or ambiguous and omitted.")
	}
	if rows.truncated {
		savedWarning(&out, "Codex saved-chat catalog reached its bounded window; other chats may be omitted.")
	}
	if hooks.beforeStable != nil {
		hooks.beforeStable()
	}
	for _, file := range held {
		if !file.stable(root) {
			return out, fmt.Errorf("saved catalog changed")
		}
	}
	if err = storedExtents(root); err != nil {
		return out, err
	}
	current, statErr := os.Stat(home)
	if statErr != nil || !os.SameFile(homeInfo, current) {
		return out, errors.Join(statErr, fmt.Errorf("saved home changed"))
	}
	for _, id := range rows.order {
		if directory, ok := rows.matches[id]; ok {
			name := names[id]
			out.Sessions = append(out.Sessions, SavedSession{Ref: SavedSessionRef{Adapter: "codex", Home: home, ID: id}, Directory: directory, Name: name.name, Updated: name.updated})
		}
	}
	return out, ctx.Err()
}

func parseSavedCatalogNames(ctx context.Context, data []byte, out *SavedSessionResult) (map[string]savedName, error) {
	names := map[string]savedName{}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > Limit {
		lines = lines[len(lines)-Limit:]
		savedWarning(out, "Codex saved-name index is truncated to its last 512 records.")
	}
	for _, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record struct {
			ID      string `json:"id"`
			Name    string `json:"thread_name"`
			Updated string `json:"updated_at"`
		}
		if len(line) > 64<<10 || json.Unmarshal(line, &record) != nil {
			savedWarning(out, "Some Codex saved-name records use an unsupported format.")
			continue
		}
		id, parseErr := uuid.Parse(record.ID)
		at, dateErr := time.Parse(time.RFC3339Nano, record.Updated)
		if parseErr != nil || dateErr != nil || at.IsZero() || at.After(time.Now().Add(time.Minute)) {
			savedWarning(out, "Some Codex saved-name records use an unsupported format.")
			continue
		}
		if strings.TrimSpace(record.Name) != "" {
			names[id.String()] = savedName{record.Name, at.UTC()}
		}
	}
	if len(names) == 0 {
		savedWarning(out, "Codex saved names are unavailable; unnamed chats use their saved IDs.")
	}
	return names, nil
}

type savedCatalogRows struct {
	matches     map[string]string
	order       []string
	truncated   bool
	unsupported bool
	rawRows     int
	queries     int
}

func readSavedCatalogRows(ctx context.Context, path string, request SavedSessionRequest, names map[string]savedName, hooks savedCatalogHooks) (out savedCatalogRows, err error) {
	if !request.valid() || len(names) > Limit {
		return out, fmt.Errorf("unsupported saved row-query scope")
	}
	for id := range names {
		parsed, parseErr := uuid.Parse(id)
		if parseErr != nil || parsed.String() != id {
			return out, fmt.Errorf("unsupported saved named-query ID")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	uri := url.URL{Scheme: "file", Path: path}
	uri.RawQuery = (url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(250)"}}).Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return out, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	defer func() { err = errors.Join(err, hooks.close("database", db)) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, hooks.close("connection", conn)) }()
	if err = conn.Raw(func(raw any) error {
		probe, ok := raw.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			return fmt.Errorf("saved catalog readonly verification unsupported")
		}
		readonly, e := probe.IsReadOnly("main")
		if e != nil || !readonly {
			return errors.Join(e, fmt.Errorf("saved catalog main not readonly"))
		}
		return nil
	}); err != nil {
		return out, err
	}
	for _, guard := range []struct {
		set, read string
		want      int
	}{{"PRAGMA query_only=ON", "PRAGMA query_only", 1}, {"PRAGMA trusted_schema=OFF", "PRAGMA trusted_schema", 0}} {
		if _, err = conn.ExecContext(ctx, guard.set); err != nil {
			return out, err
		}
		var value int
		if err = conn.QueryRowContext(ctx, guard.read).Scan(&value); err != nil || value != guard.want {
			return out, errors.Join(err, fmt.Errorf("saved catalog connection guard unsupported"))
		}
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() {
		if tx != nil {
			rollback := tx.Rollback()
			if !(errors.Is(rollback, sql.ErrTxDone) && ctx.Err() != nil) {
				err = errors.Join(err, rollback)
			}
		}
	}()
	if err = storedSchema(ctx, tx); err != nil {
		return out, err
	}
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out.matches = map[string]string{}
	seen := map[string]string{}
	ambiguous := map[string]bool{}
	placeholders := func(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
	query := func(named bool, capacity int) (e error) {
		statement := "SELECT id,cwd FROM main.threads WHERE typeof(id)='text' AND length(id)=36 AND typeof(cwd)='text' AND length(cwd)<=4096"
		var args []any
		if len(ids) > 0 {
			operator := " NOT IN ("
			if named {
				operator = " IN ("
			}
			statement += " AND id" + operator + placeholders(len(ids)) + ")"
			for _, id := range ids {
				args = append(args, id)
			}
		}
		if request.Scope == SavedExactDirectories {
			statement += " AND cwd IN (" + placeholders(len(request.Directories)) + ")"
			for _, directory := range request.Directories {
				args = append(args, directory)
			}
		}
		statement += " ORDER BY id ASC,cwd ASC LIMIT ?"
		args = append(args, capacity+1)
		rows, e := tx.QueryContext(ctx, statement, args...)
		if e != nil {
			return e
		}
		out.queries++
		defer func() { e = errors.Join(e, hooks.close("rows", rows)) }()
		observed := 0
		for rows.Next() {
			observed++
			out.rawRows++
			if observed > capacity && named {
				return fmt.Errorf("saved named catalog row limit")
			}
			var id, directory string
			if e = rows.Scan(&id, &directory); e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			parsed, parseErr := uuid.Parse(id)
			if parseErr != nil || parsed.String() != id {
				out.unsupported = true
				if observed > capacity {
					out.truncated = true
				}
				continue
			}
			previous, exists := seen[id]
			if observed > capacity {
				out.truncated = true
				// A terminal repeated ID may hide another cwd beyond the
				// bounded window. Omit its entire observed association.
				if exists {
					ambiguous[id] = true
					delete(out.matches, id)
					out.unsupported = true
				}
				continue
			}
			seen[id] = directory
			if exists && previous != directory {
				ambiguous[id] = true
				delete(out.matches, id)
				out.unsupported = true
			}
			if ambiguous[id] {
				continue
			}
			canonical, canonicalErr := canonicalSavedDirectory(directory)
			if canonicalErr != nil {
				out.unsupported = true
				continue
			}
			if _, exists = out.matches[id]; !exists {
				out.order = append(out.order, id)
			}
			out.matches[id] = canonical
		}
		return errors.Join(rows.Err(), ctx.Err())
	}
	if len(ids) > 0 {
		if err = query(true, Limit); err != nil {
			return out, err
		}
	}
	if len(out.matches) < Limit {
		if err = query(false, Limit-len(out.matches)); err != nil {
			return out, err
		}
	} else {
		// No remaining-row query is allowed at capacity; completeness is
		// unknown, so expose the bound rather than imply a complete catalog.
		out.truncated = true
	}
	if err = tx.Rollback(); err != nil {
		return out, err
	}
	tx = nil
	return out, ctx.Err()
}
