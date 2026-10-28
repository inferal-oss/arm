package manager

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"golang.org/x/sys/unix"
)

const runPageBytes = 64 << 10
const proxyLogLimit int64 = 64 << 20
const proxyTotalLogLimit int64 = 256 << 20
const runInventoryLimit = 4096

// Chunks are bounded TEXT so the stopped-ledger proof can inspect them without
// unbounded BLOB allocation. Appends and their acknowledgement commit together.
const proxyRunsSchema = `CREATE TABLE IF NOT EXISTS proxy_runs (id TEXT PRIMARY KEY, request_key TEXT UNIQUE NOT NULL, request TEXT NOT NULL, data TEXT NOT NULL)`
const proxyChunksSchema = `CREATE TABLE IF NOT EXISTS proxy_run_chunks (run TEXT NOT NULL, offset INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(run,offset))`

type proxyRunRecord struct {
	model.Run
	LastOffset int64  `json:"last_offset"`
	LastSize   int    `json:"last_size"`
	LastSHA256 string `json:"last_sha256"`
}

func (m *Manager) initRunRecords() error {
	m.proxyReported = map[string]time.Time{}
	for _, q := range []string{proxyRunsSchema, proxyChunksSchema} {
		if _, err := m.db.Exec(q); err != nil {
			return err
		}
	}
	rows, err := m.db.Query(`SELECT id,substr(data,1,65537) FROM proxy_runs WHERE json_extract(data,'$.state')='active' LIMIT 4097`)
	if err != nil {
		return err
	}
	var lost []proxyRunRecord
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			break
		}
		var rec proxyRunRecord
		if len(raw) > 65536 || json.Unmarshal([]byte(raw), &rec) != nil || rec.ID != id || !runID(id) {
			err = fmt.Errorf("invalid proxy run record")
			break
		}
		rec.State, rec.Outcome = "interrupted", "reporter_lost"
		// Losing the reporter is no observation of child exit.
		rec.Ended, rec.ExitCode = nil, nil
		lost = append(lost, rec)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(lost) > runInventoryLimit {
		return model.Err("run_limit", "proxy inventory exceeds bound")
	}
	for _, rec := range lost {
		raw, _ := json.Marshal(rec)
		if _, err = m.db.Exec(`UPDATE proxy_runs SET data=? WHERE id=?`, string(raw), rec.ID); err != nil {
			return err
		}
	}
	return nil
}

func runID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func (m *Manager) runsLocked() ([]model.Run, error) {
	out := []model.Run{}
	rows, err := m.db.Query(`SELECT substr(data,1,262145),substr(run,1,65537) FROM leases WHERE run IS NOT NULL ORDER BY rowid DESC LIMIT 4097`)
	if err != nil {
		return nil, err
	}
	scanned := 0
	for rows.Next() {
		scanned++
		if scanned > runInventoryLimit {
			err = model.Err("run_limit", "complete run inventory exceeds 4096 entries")
			break
		}
		var raw, saved string
		if err = rows.Scan(&raw, &saved); err != nil {
			break
		}
		var l model.Lease
		var q RunRequest
		if json.Unmarshal([]byte(raw), &l) != nil || json.Unmarshal([]byte(saved), &q) != nil || !runID(l.ID) {
			err = fmt.Errorf("invalid recorded run")
			break
		}
		// Native SSH owns a transport, not the ordinary merged log file.
		if q.SSH != nil {
			continue
		}
		r, e := m.projectLeasedRun(l, q)
		if e != nil {
			err = e
			break
		}
		out = append(out, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = m.db.Query(`SELECT substr(data,1,65537) FROM proxy_runs ORDER BY rowid DESC LIMIT 4097`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var rec proxyRunRecord
		if len(raw) > 65536 || json.Unmarshal([]byte(raw), &rec) != nil || !runID(rec.ID) {
			err = fmt.Errorf("invalid proxy run record")
			break
		}
		out = append(out, rec.Run)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out) > runInventoryLimit {
		return nil, model.Err("run_limit", "complete run inventory exceeds 4096 entries")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	if raw, _ := json.Marshal(out); len(raw) > 2<<20 {
		return nil, model.Err("run_limit", "complete run inventory exceeds response bound")
	}
	return out, nil
}

func (m *Manager) Runs() ([]model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runsLocked()
}
func (m *Manager) projectLeasedRun(l model.Lease, q RunRequest) (model.Run, error) {
	r := model.Run{ID: l.ID, Name: readablename.FromID("run", l.ID), LeaseID: l.ID, Command: q.Command, CWD: q.CWD, Agent: l.Request.Agent, Reason: l.Request.Reason, Host: l.Host, State: l.State, Outcome: l.Outcome, Created: l.Created, Started: l.Started, Ended: l.Ended, ExitCode: l.ExitCode}
	if f, e := m.openRunLog(l.ID); e == nil {
		info, e := f.Stat()
		f.Close()
		if e != nil {
			return r, e
		}
		r.LogBytes = info.Size()
		r.AcceptedBytes = r.LogBytes
	} else if !os.IsNotExist(e) {
		return r, e
	}
	return r, nil
}

func (m *Manager) runLocked(selector string) (model.Run, error) {
	// Canonical IDs remain readable even after the bounded alias inventory fills.
	if runID(selector) {
		var raw string
		var saved sql.NullString
		err := m.db.QueryRow(`SELECT substr(data,1,262145),substr(run,1,65537) FROM leases WHERE id=?`, selector).Scan(&raw, &saved)
		if err == nil && saved.Valid {
			var l model.Lease
			var q RunRequest
			if json.Unmarshal([]byte(raw), &l) != nil || json.Unmarshal([]byte(saved.String), &q) != nil || l.ID != selector {
				return model.Run{}, fmt.Errorf("invalid recorded run")
			}
			if q.SSH == nil {
				return m.projectLeasedRun(l, q)
			}
		} else if err != nil && err != sql.ErrNoRows {
			return model.Run{}, err
		}
		rec, err := proxyRecord(m.db, selector)
		if err != nil {
			return model.Run{}, err
		}
		return rec.Run, nil
	}

	runs, err := m.runsLocked()
	if err != nil {
		return model.Run{}, err
	}
	candidates := make([]readablename.Candidate, 0, len(runs))
	for _, r := range runs {
		candidates = append(candidates, readablename.Candidate{ID: r.ID, Name: r.Name})
	}
	id, err := readablename.Resolve("run", selector, candidates, true)
	if err != nil {
		return model.Run{}, model.Err("run_not_found", err.Error())
	}
	for _, r := range runs {
		if r.ID == id {
			return r, nil
		}
	}
	return model.Run{}, model.Err("run_not_found", "run is unavailable")
}
func (m *Manager) InspectRun(selector string) (model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runLocked(selector)
}

func (m *Manager) openRunLog(id string) (*os.File, error) {
	if !runID(id) {
		return nil, model.Err("invalid_request", "invalid run ID")
	}
	// Refuse a replaced log-directory symlink and a final-path symlink.
	dir, err := unix.Open(filepath.Join(m.home, "logs"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, id+".log", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), id+".log")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = model.Err("invalid_log", "run log is not a regular file")
		}
		return nil, err
	}
	return f, nil
}

func (m *Manager) RunOutput(selector string, offset int64) (model.RunLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if offset < 0 {
		return model.RunLog{}, model.Err("invalid_request", "negative log offset")
	}
	r, err := m.runLocked(selector)
	if err != nil {
		return model.RunLog{}, err
	}
	result := model.RunLog{Run: r, Data: []byte{}, Offset: offset, NextOffset: offset}
	if offset > r.LogBytes {
		return result, model.Err("invalid_request", "offset exceeds retained log")
	}
	if r.LeaseID != "" {
		f, e := m.openRunLog(r.ID)
		if os.IsNotExist(e) {
			e = nil
		} else if e == nil {
			defer f.Close()
			result.Data = make([]byte, min(int64(runPageBytes), r.LogBytes-offset))
			var n int
			n, e = f.ReadAt(result.Data, offset)
			result.Data = result.Data[:n]
			if e == io.EOF {
				e = nil
			}
		}
		if e != nil {
			return result, e
		}
	} else {
		// Retained chunks are contiguous; after truncation no later bytes are stored.
		rows, e := m.db.Query(`SELECT offset,substr(data,1,87385) FROM proxy_run_chunks WHERE run=? AND offset+((length(data)/4)*3)>=? AND offset<? ORDER BY offset LIMIT 4097`, r.ID, offset, offset+runPageBytes)
		if e != nil {
			return result, e
		}
		for rows.Next() {
			var start int64
			var raw string
			if e = rows.Scan(&start, &raw); e != nil {
				break
			}
			var data []byte
			if len(raw) > 87384 || start < 0 {
				e = model.Err("invalid_log", "invalid proxy log chunk")
				break
			}
			data, e = base64.StdEncoding.Strict().DecodeString(raw)
			if e != nil {
				break
			}
			from := max(offset-start, int64(0))
			to := min(int64(len(data)), offset+runPageBytes-start)
			if to > from {
				if start+from != offset+int64(len(result.Data)) {
					e = model.Err("invalid_log", "proxy log is not contiguous")
					break
				}
				result.Data = append(result.Data, data[from:to]...)
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return result, e
		}
	}
	if int64(len(result.Data)) != min(int64(runPageBytes), r.LogBytes-offset) {
		return result, model.Err("invalid_log", "retained run log changed or has missing bytes")
	}
	result.NextOffset = offset + int64(len(result.Data))
	result.Done = r.State == "terminal" || r.State == "interrupted"
	return result, nil
}

func validateRunCreate(q model.RunCreateRequest) error {
	if q.Key == "" || len(q.Key) > 128 || !utf8.ValidString(q.Key) || len(q.Command) == 0 || len(q.Command) > 256 || q.Command[0] == "" || !filepath.IsAbs(q.CWD) || len(q.CWD) > 4096 || len(q.Agent) > 256 || strings.TrimSpace(q.Reason) == "" || len(q.Reason) > 4096 {
		return model.Err("invalid_request", "run requires a bounded key, command, absolute cwd and reason")
	}
	raw, _ := json.Marshal(q)
	if len(raw) > 32768 {
		return model.Err("invalid_request", "run metadata exceeds 32KiB")
	}
	for _, v := range append(append([]string{}, q.Command...), q.CWD, q.Agent, q.Reason) {
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return model.Err("invalid_request", "run metadata contains invalid text")
		}
	}
	return nil
}

func (m *Manager) CreateRun(q model.RunCreateRequest) (model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validateRunCreate(q); err != nil {
		return model.Run{}, err
	}
	if m.closing || m.draining {
		return model.Run{}, model.Err("backend_stopping", "backend no longer accepts runs")
	}
	canonical, _ := json.Marshal(q)
	var prior, raw string
	err := m.db.QueryRow(`SELECT request,data FROM proxy_runs WHERE request_key=?`, q.Key).Scan(&prior, &raw)
	if err == nil {
		if prior != string(canonical) {
			return model.Run{}, model.Err("idempotency_conflict", "run key was used for different metadata")
		}
		var rec proxyRunRecord
		if json.Unmarshal([]byte(raw), &rec) != nil {
			return model.Run{}, fmt.Errorf("invalid proxy record")
		}
		return rec.Run, nil
	}
	if err != sql.ErrNoRows {
		return model.Run{}, err
	}
	var count int
	if err = m.db.QueryRow(`SELECT COUNT(*) FROM proxy_runs`).Scan(&count); err != nil {
		return model.Run{}, err
	}
	if count >= runInventoryLimit {
		return model.Run{}, model.Err("run_limit", "proxy run inventory is full")
	}
	if q.Agent != "" {
		if err = m.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id=?`, q.Agent).Scan(&count); err != nil {
			return model.Run{}, err
		}
		if count != 1 {
			return model.Run{}, model.Err("unknown_agent", "agent must be an existing local contact ID")
		}
	}
	now := time.Now().UTC()
	id := local.ID()
	rec := proxyRunRecord{Run: model.Run{ID: id, Name: readablename.FromID("run", id), Command: append([]string{}, q.Command...), CWD: q.CWD, Agent: q.Agent, Reason: q.Reason, Host: m.host, State: "active", Created: now, Started: &now, LastReported: now}, LastOffset: -1}
	data, _ := json.Marshal(rec)
	tx, err := m.db.Begin()
	if err != nil {
		return model.Run{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO proxy_runs(id,request_key,request,data) VALUES(?,?,?,?)`, id, q.Key, string(canonical), string(data)); err == nil {
		err = event(tx, "run.created", id, rec.Run)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		m.proxyReported[id] = time.Now()
	}
	return rec.Run, err
}
func proxyRecord(db *sql.DB, id string) (proxyRunRecord, error) {
	var raw string
	var rec proxyRunRecord
	err := db.QueryRow(`SELECT substr(data,1,65537) FROM proxy_runs WHERE id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return rec, model.Err("run_not_found", "proxy run is unavailable")
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &rec)
		if err == nil && (len(raw) > 65536 || rec.ID != id || !runID(rec.ID) || rec.LogBytes < 0 || rec.LogBytes > proxyLogLimit || rec.AcceptedBytes < rec.LogBytes) {
			err = fmt.Errorf("invalid proxy run record")
		}
	}
	return rec, err
}

func (m *Manager) AppendRun(q model.RunAppendRequest) (model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !runID(q.ID) || q.Offset < 0 || len(q.Data) > runPageBytes || q.Offset > math.MaxInt64-int64(len(q.Data)) {
		return model.Run{}, model.Err("invalid_request", "invalid append offset or chunk size")
	}
	rec, err := proxyRecord(m.db, q.ID)
	if err != nil {
		return model.Run{}, err
	}
	digest := sha256.Sum256(q.Data)
	hash := hex.EncodeToString(digest[:])
	retry := q.Offset == rec.LastOffset && len(q.Data) == rec.LastSize && hash == rec.LastSHA256
	if retry && rec.State != "active" {
		return rec.Run, nil
	}
	if rec.State != "active" {
		return rec.Run, model.Err("run_inactive", "run no longer accepts output")
	}
	if !retry && q.Offset != rec.AcceptedBytes {
		return rec.Run, model.Err("run_offset", "append offset does not match acknowledged bytes")
	}
	rec.LastReported = time.Now().UTC()
	if retry || len(q.Data) == 0 {
		raw, _ := json.Marshal(rec)
		_, err = m.db.Exec(`UPDATE proxy_runs SET data=? WHERE id=?`, string(raw), q.ID)
		if err == nil {
			m.proxyReported[q.ID] = time.Now()
		}
		return rec.Run, err
	}
	var total int64
	var chunks, runChunks int
	if err = m.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN run=? THEN 1 ELSE 0 END),0) FROM proxy_run_chunks`, q.ID).Scan(&chunks, &runChunks); err != nil {
		return rec.Run, err
	}
	if err = m.db.QueryRow(`SELECT COALESCE(SUM(json_extract(data,'$.log_bytes')),0) FROM proxy_runs`).Scan(&total); err != nil {
		return rec.Run, err
	}
	keep := min(int64(len(q.Data)), max(int64(0), proxyLogLimit-rec.LogBytes), max(int64(0), proxyTotalLogLimit-total))
	// A retained prefix only: once a run drops bytes, never splice later bytes in.
	if rec.Truncated {
		keep = 0
	}
	var tailOffset int64
	var tail []byte
	var tailRoom int64
	if keep > 0 {
		var encoded string
		err = m.db.QueryRow(`SELECT offset,substr(data,1,87385) FROM proxy_run_chunks WHERE run=? ORDER BY offset DESC LIMIT 1`, q.ID).Scan(&tailOffset, &encoded)
		if err == nil {
			if len(encoded) > 87384 {
				return rec.Run, model.Err("invalid_log", "proxy log tail exceeds chunk bound")
			}
			tail, err = base64.StdEncoding.Strict().DecodeString(encoded)
			if err != nil || len(tail) == 0 || len(tail) > runPageBytes || tailOffset < 0 || tailOffset != rec.LogBytes-int64(len(tail)) {
				return rec.Run, model.Err("invalid_log", "proxy log tail does not match retained bytes")
			}
			tailRoom = int64(runPageBytes - len(tail))
		} else if err != sql.ErrNoRows {
			return rec.Run, err
		} else if rec.LogBytes != 0 {
			return rec.Run, model.Err("invalid_log", "proxy log tail is missing")
		}
		// At a row-count bound the existing tail still has usable space.
		if chunks >= 16384 || runChunks >= 4096 {
			keep = min(keep, tailRoom)
		}
	}
	rec.Truncated = rec.Truncated || keep < int64(len(q.Data))
	start := rec.LogBytes
	rec.LogBytes += keep
	rec.AcceptedBytes += int64(len(q.Data))
	rec.LastOffset = q.Offset
	rec.LastSize = len(q.Data)
	rec.LastSHA256 = hash
	raw, _ := json.Marshal(rec)
	tx, err := m.db.Begin()
	if err != nil {
		return rec.Run, err
	}
	defer tx.Rollback()
	if keep > 0 {
		// The append receipt describes the original input independently of this
		// storage packing. Update the tail and insert any remainder atomically.
		fill := min(keep, tailRoom)
		if fill > 0 {
			tail = append(tail, q.Data[:fill]...)
			_, err = tx.Exec(`UPDATE proxy_run_chunks SET data=? WHERE run=? AND offset=?`, base64.StdEncoding.EncodeToString(tail), q.ID, tailOffset)
		}
		if err == nil && fill < keep {
			_, err = tx.Exec(`INSERT INTO proxy_run_chunks(run,offset,data) VALUES(?,?,?)`, q.ID, start+fill, base64.StdEncoding.EncodeToString(q.Data[fill:keep]))
		}
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE proxy_runs SET data=? WHERE id=?`, string(raw), q.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		m.proxyReported[q.ID] = time.Now()
	}
	return rec.Run, err
}

func (m *Manager) FinishRun(q model.RunFinishRequest) (model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !runID(q.ID) || q.ExitCode < 0 || q.ExitCode > 255 || (q.Outcome != "completed" && q.Outcome != "failed" && q.Outcome != "cancelled" && q.Outcome != "launch_failed" && q.Outcome != "log_failed") {
		return model.Run{}, model.Err("invalid_request", "invalid reported run result")
	}
	rec, err := proxyRecord(m.db, q.ID)
	if err != nil {
		return rec.Run, err
	}
	if rec.State == "terminal" {
		if rec.ExitCode != nil && *rec.ExitCode == q.ExitCode && rec.Outcome == q.Outcome {
			return rec.Run, nil
		}
		return rec.Run, model.Err("idempotency_conflict", "run already has another result")
	}
	if rec.State != "active" {
		return rec.Run, model.Err("run_inactive", "lost reporter cannot supply a later terminal result")
	}
	now := time.Now().UTC()
	rec.State, rec.Outcome, rec.Ended, rec.ExitCode = "terminal", q.Outcome, &now, &q.ExitCode
	raw, _ := json.Marshal(rec)
	tx, err := m.db.Begin()
	if err != nil {
		return rec.Run, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE proxy_runs SET data=? WHERE id=?`, string(raw), q.ID); err == nil {
		err = event(tx, "run.finished", q.ID, rec.Run)
	}
	if err == nil {
		err = tx.Commit()
	}
	return rec.Run, err
}

// expireProxyRuns observes loss of the CLI reporter using this process's
// monotonic clock. It does not signal a PID or supply an exit/cleanup witness.
func (m *Manager) expireProxyRuns(now time.Time) error {
	for id, at := range m.proxyReported {
		if now.Sub(at) <= 30*time.Second {
			continue
		}
		rec, err := proxyRecord(m.db, id)
		if err != nil {
			return err
		}
		if rec.State == "active" {
			rec.State, rec.Outcome = "interrupted", "reporter_lost"
			rec.Ended, rec.ExitCode = nil, nil
			raw, _ := json.Marshal(rec)
			if _, err = m.db.Exec(`UPDATE proxy_runs SET data=? WHERE id=?`, string(raw), id); err != nil {
				return err
			}
		}
		delete(m.proxyReported, id)
	}
	return nil
}
