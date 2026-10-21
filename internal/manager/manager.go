package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
	"github.com/yrashk/arm/internal/storage"
	_ "modernc.org/sqlite"
)

type RunRequest struct {
	model.Request
	Command []string `json:"command"`
	CWD     string   `json:"cwd"`
}
type job struct {
	cmd  *exec.Cmd
	done chan struct{}
}
type Manager struct {
	mu                sync.Mutex
	db                *sql.DB
	home, host, owner string
	capacity          model.Resources
	jobs              map[string]*job
	stop              chan struct{}
	wg                sync.WaitGroup
	closing           bool
	observationMu     sync.RWMutex
	observations      map[string]model.ResourceMeasurement
	storage           *storage.Provider
	storageErr        error
	storageJobs       map[string]bool
}

var capabilities = []string{"cooperative-reservation", "process-group-supervision", "durable-events", "host-observation"}

func Open(home string, capacity model.Resources) (*Manager, error) {
	if e := local.Prepare(home); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS leases (id TEXT PRIMARY KEY, request_key TEXT UNIQUE, data TEXT NOT NULL, run TEXT)`,
		`CREATE TABLE IF NOT EXISTS events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, entity TEXT NOT NULL, at TEXT NOT NULL, data TEXT)`,
		`CREATE TABLE IF NOT EXISTS agents (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS cancelled_requests (key TEXT PRIMARY KEY)`} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	if _, e = db.Exec(`INSERT OR IGNORE INTO metadata VALUES ('host',?)`, local.ID()); e != nil {
		db.Close()
		return nil, e
	}
	m := &Manager{db: db, home: home, capacity: capacity, jobs: map[string]*job{}, stop: make(chan struct{}), storageJobs: map[string]bool{}}
	if e = db.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&m.host); e != nil {
		db.Close()
		return nil, e
	}
	u, e := user.Current()
	if e != nil {
		db.Close()
		return nil, e
	}
	m.owner = u.Uid
	m.loadStorage()
	opened := false
	defer func() {
		if !opened && m.storage != nil {
			m.storage.Close()
		}
	}()
	// A recovered PID alone never authorizes signalling. Keep uncertain jobs charged.
	leaves, e := m.list()
	if e != nil {
		db.Close()
		return nil, e
	}
	for _, l := range leaves {
		var run sql.NullString
		if e = db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&run); e != nil {
			db.Close()
			return nil, e
		}
		if run.Valid && consuming(l.State) {
			l.State = "needs_attention"
			l.Outcome = "supervision_lost"
			if l.PID == 0 || errors.Is(syscall.Kill(-l.PID, 0), syscall.ESRCH) {
				now := time.Now().UTC()
				l.State = "terminal"
				l.Outcome = "interrupted"
				l.Ended = &now
			}
			l.Revision++
			if e = m.put(l, "lease.recovery_required"); e != nil {
				db.Close()
				return nil, e
			}
		}
	}
	if e = m.recoverDeliveries(); e != nil {
		db.Close()
		return nil, e
	}
	m.sample()
	opened = true
	return m, nil
}

func (m *Manager) Start() {
	m.wg.Go(func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.sample()
			}
		}
	})
	m.wg.Go(func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.tick()
			}
		}
	})
}

func (m *Manager) sample() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	observations := observe.Snapshot(ctx, m.home)
	m.observationMu.Lock()
	m.observations = observations
	m.observationMu.Unlock()
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closing = true
	for id := range m.jobs {
		_, _ = m.end(id, "cancelled")
	}
	m.mu.Unlock()
	close(m.stop)
	m.wg.Wait()
	if m.storage != nil {
		m.storage.Close()
	}
	return m.db.Close()
}
func consuming(s string) bool {
	return s == "active" || s == "provisioning" || s == "ending" || s == "needs_attention"
}
func (m *Manager) list() ([]model.Lease, error) {
	rows, e := m.db.Query(`SELECT data FROM leases ORDER BY rowid`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Lease{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var l model.Lease
		if e = json.Unmarshal(b, &l); e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (m *Manager) get(id string) (model.Lease, error) {
	var l model.Lease
	var b []byte
	e := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return l, model.Err("not_found", "lease does not exist")
	}
	if e != nil {
		return l, e
	}
	e = json.Unmarshal(b, &l)
	return l, e
}
func event(tx *sql.Tx, kind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, kind, id, time.Now().UTC().Format(time.RFC3339Nano), string(b))
	return e
}
func (m *Manager) put(l model.Lease, kind string) error {
	b, e := json.Marshal(l)
	if e != nil {
		return e
	}
	tx, e := m.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE leases SET data=? WHERE id=?`, string(b), l.ID); e != nil {
		return e
	}
	if e = event(tx, kind, l.ID, l); e != nil {
		return e
	}
	return tx.Commit()
}

func (m *Manager) Status() (model.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, e := m.list()
	s := model.Status{Host: m.host, Owner: m.owner, Capacity: m.capacity, Leases: ls, Capabilities: capabilities}
	if e != nil {
		return s, e
	}
	for _, l := range ls {
		if consuming(l.State) {
			s.Reserved = s.Reserved.Add(l.Request.Resources)
		}
	}
	for _, kind := range []string{"cpu", "memory", "disk"} {
		total, reserved, unit := s.Capacity.CPU, s.Reserved.CPU, "logical-core-equivalents"
		if kind == "memory" {
			total, reserved, unit = float64(s.Capacity.Memory), float64(s.Reserved.Memory), "bytes"
		}
		if kind == "disk" {
			total, reserved, unit = float64(s.Capacity.Disk), float64(s.Reserved.Disk), "bytes"
		}
		v := model.ResourceView{ID: m.host + "/" + kind, Host: m.host, Pool: m.host + "/" + kind + "-budget", Kind: kind, Unit: unit, BudgetTotal: total, Reserved: reserved, Available: max(0, total-reserved), Accounting: "cooperative-reservation-budget", Users: []model.ResourceUse{}}
		m.observationMu.RLock()
		measurement, exists := m.observations[kind]
		m.observationMu.RUnlock()
		if exists {
			v.Measurement = &measurement
			v.Observed = measurement.Used.Value
		}
		for _, l := range ls {
			if !consuming(l.State) {
				continue
			}
			q := l.Request.Resources.CPU
			if kind == "memory" {
				q = float64(l.Request.Resources.Memory)
			}
			if kind == "disk" {
				q = float64(l.Request.Resources.Disk)
			}
			if q > 0 {
				v.Users = append(v.Users, model.ResourceUse{Lease: l.ID, Owner: l.Owner, Agent: l.Request.Agent, Project: l.Request.Project, Reason: l.Request.Reason, Quantity: q, State: l.State, Expires: l.Expires})
			}
		}
		s.Resources = append(s.Resources, v)
	}
	e = m.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM events`).Scan(&s.Cursor)
	return s, e
}
func (m *Manager) Inspect(id string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.get(id)
}

func (m *Manager) Acquire(r model.Request, run *RunRequest) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var empty model.Lease
	if m.closing {
		return empty, model.Err("backend_stopping", "backend is shutting down")
	}
	if len(r.Key) > 128 {
		return empty, model.Err("invalid_request", "idempotency key must be at most 128 bytes")
	}
	if r.Host != "" && r.Host != "localhost" && r.Host != m.host {
		return empty, model.Err("host_unavailable", "only the local host is registered")
	}
	if strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 4096 {
		return empty, model.Err("invalid_request", "reason is required and must be at most 4096 bytes")
	}
	if r.TTL <= 0 || r.TTL > 7*24*time.Hour {
		return empty, model.Err("invalid_request", "ttl must be positive and no greater than 7 days")
	}
	if math.IsNaN(r.Resources.CPU) || math.IsInf(r.Resources.CPU, 0) || r.Resources.CPU < 0 || r.Resources.Memory < 0 || r.Resources.Disk < 0 || r.Resources == (model.Resources{}) {
		return empty, model.Err("invalid_request", "request at least one positive resource quantity")
	}
	if r.Kind != "" && r.Kind != "execution" && r.Kind != "storage" {
		return empty, model.Err("invalid_request", "kind must be execution or storage")
	}
	if r.Kind == "storage" && (run != nil || r.Resources.CPU != 0 || r.Resources.Memory != 0 || r.Resources.Disk <= 0 || r.Profile != "" || r.StorageLease != "") {
		return empty, model.Err("invalid_request", "a storage lease requires only a disk claim and no command, profile or parent storage lease")
	}
	if r.StorageLease != "" && run == nil {
		return empty, model.Err("invalid_request", "only supervised commands can attach storage")
	}
	if r.Profile != "" && r.Profile != "rust-build" {
		return empty, model.Err("unsupported_profile", "supported profile: rust-build")
	}
	if r.Agent != "" {
		var count int
		if e := m.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id=?`, r.Agent).Scan(&count); e != nil {
			return empty, e
		}
		if count == 0 {
			return empty, model.Err("invalid_contact", "register the agent contact before using it for attribution")
		}
	}
	if !r.Resources.Fits(m.capacity) {
		return empty, model.Err("capacity_exceeded", "request exceeds the configured host budget")
	}
	if r.Key != "" {
		var old []byte
		var savedRun sql.NullString
		e := m.db.QueryRow(`SELECT data,run FROM leases WHERE request_key=?`, r.Key).Scan(&old, &savedRun)
		if e == nil {
			var l model.Lease
			if e = json.Unmarshal(old, &l); e != nil {
				return empty, e
			}
			a, b := r, l.Request
			a.Host = ""
			b.Host = ""
			if a != b {
				return empty, model.Err("idempotency_conflict", "key was already used for a different request")
			}
			var rb string
			if run != nil {
				raw, _ := json.Marshal(run)
				rb = string(raw)
			}
			if savedRun.String != rb {
				return empty, model.Err("idempotency_conflict", "key was already used for a different command")
			}
			return l, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return empty, e
		}
		var cancelled int
		if e = m.db.QueryRow(`SELECT COUNT(*) FROM cancelled_requests WHERE key=?`, r.Key).Scan(&cancelled); e != nil {
			return empty, e
		}
		if cancelled > 0 {
			return empty, model.Err("request_cancelled", "this request was cancelled before admission")
		}
	}
	if r.StorageLease != "" {
		if _, e := m.storageForRun(r); e != nil {
			return empty, e
		}
	}
	ls, e := m.list()
	if e != nil {
		return empty, e
	}
	var used model.Resources
	queued := false
	for _, l := range ls {
		if consuming(l.State) {
			used = used.Add(l.Request.Resources)
		}
		if l.State == "queued" {
			queued = true
		}
	}
	l := model.Lease{ID: local.ID(), Host: m.host, Owner: m.owner, Request: r, Created: time.Now().UTC(), State: "queued", Revision: 1, Capabilities: capabilities}
	if !queued && used.Add(r.Resources).Fits(m.capacity) {
		m.activate(&l, run != nil)
	} else if !r.Queue {
		return empty, model.Err("insufficient_capacity", "capacity is reserved; use --wait to queue")
	}
	var rb any
	if run != nil {
		if len(run.Command) == 0 || !filepath.IsAbs(run.CWD) {
			return empty, model.Err("invalid_request", "run needs a command and absolute cwd")
		}
		if r.Profile == "rust-build" {
			if e = validateCargo(run.Command, r.Resources.CPU); e != nil {
				return empty, e
			}
			if r.StorageLease != "" {
				for _, arg := range run.Command[1:] {
					if arg == "--target-dir" || strings.HasPrefix(arg, "--target-dir=") || arg == "--build-dir" || strings.HasPrefix(arg, "--build-dir=") {
						return empty, model.Err("profile_conflict", "attached storage supplies CARGO_TARGET_DIR; remove target-dir overrides")
					}
				}
			}
		}
		b, _ := json.Marshal(run)
		rb = string(b)
	}
	b, _ := json.Marshal(l)
	tx, e := m.db.Begin()
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	var key any
	if r.Key != "" {
		key = r.Key
	}
	if _, e = tx.Exec(`INSERT INTO leases VALUES(?,?,?,?)`, l.ID, key, string(b), rb); e != nil {
		return empty, e
	}
	if e = event(tx, "lease.created", l.ID, l); e != nil {
		return empty, e
	}
	if e = tx.Commit(); e != nil {
		return empty, e
	}
	if l.State == "provisioning" {
		if r.Kind == "storage" {
			l, e = m.provisionStorage(l)
		} else {
			l, e = m.launch(l, *run)
		}
	}
	return l, e
}

func (m *Manager) activate(l *model.Lease, isRun bool) {
	now := time.Now().UTC()
	end := now.Add(l.Request.TTL)
	l.Started = &now
	l.Expires = &end
	l.State = "active"
	if isRun || l.Request.Kind == "storage" {
		l.State = "provisioning"
	}
}

func (m *Manager) End(id, outcome string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.end(id, outcome)
}

// Retain cancellation even when it races ahead of an unacknowledged request.
func (m *Manager) CancelRequest(key string) error {
	if key == "" || len(key) > 128 {
		return model.Err("invalid_request", "a valid request key is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT OR IGNORE INTO cancelled_requests VALUES(?)`, key); e != nil {
		return e
	}
	if e = event(tx, "request.cancelled", key, nil); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	var id string
	e = m.db.QueryRow(`SELECT id FROM leases WHERE request_key=?`, key).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = m.end(id, "cancelled")
	return e
}
func (m *Manager) end(id, outcome string) (model.Lease, error) {
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if l.State == "terminal" || l.State == "ending" {
		return l, nil
	}
	if l.State == "needs_attention" {
		return l, model.Err("needs_attention", "allocation remains charged pending reconciliation; storage leases support explicit cleanup retry or handoff")
	}
	if l.Request.Kind == "storage" {
		return m.endStorage(l, outcome)
	}
	l.Revision++
	l.Outcome = outcome
	if j := m.jobs[id]; j != nil {
		l.State = "ending"
		if e = m.put(l, "lease.ending"); e != nil {
			return l, e
		}
		_ = syscall.Kill(-j.cmd.Process.Pid, syscall.SIGTERM)
		pid := j.cmd.Process.Pid
		m.wg.Go(func() {
			select {
			case <-j.done:
				return
			case <-time.After(2 * time.Second):
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.jobs[id] == j {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		})
		return l, nil
	}
	now := time.Now().UTC()
	l.State = "terminal"
	l.Ended = &now
	return l, m.put(l, "lease."+outcome)
}

func (m *Manager) Renew(id string, ttl time.Duration) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return l, model.Err("invalid_request", "invalid renewal ttl")
	}
	if l.State != "active" || l.Expires == nil || !time.Now().Before(*l.Expires) {
		return l, model.Err("lease_inactive", "only an unexpired active lease can be renewed")
	}
	if l.Request.StorageLease != "" {
		r := l.Request
		r.TTL = ttl
		if _, err := m.storageForRun(r); err != nil {
			return l, err
		}
	}
	ls, e := m.list()
	if e != nil {
		return l, e
	}
	for _, o := range ls {
		if o.State == "queued" {
			return l, model.Err("renewal_denied", "queued work has priority over renewal")
		}
	}
	end := time.Now().UTC().Add(ttl)
	if end.Before(*l.Expires) {
		return l, model.Err("invalid_request", "renewal must extend the current expiry")
	}
	l.Expires = &end
	l.Revision++
	return l, m.put(l, "lease.renewed")
}

func (m *Manager) Events(after int64) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, e := m.db.Query(`SELECT sequence,type,entity,at,data FROM events WHERE sequence>? ORDER BY sequence LIMIT 256`, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var ev model.Event
		var at string
		var data string
		if e = rows.Scan(&ev.Sequence, &ev.Type, &ev.Entity, &at, &data); e != nil {
			return nil, e
		}
		ev.Data = json.RawMessage(data)
		ev.At, e = time.Parse(time.RFC3339Nano, at)
		if e != nil {
			return nil, e
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (m *Manager) tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return
	}
	ls, e := m.list()
	if e != nil {
		return
	}
	now := time.Now()
	for _, l := range ls {
		if (l.State == "active" || l.State == "provisioning") && l.Expires != nil && !now.Before(*l.Expires) {
			_, _ = m.end(l.ID, "expired")
		}
	}
	ls, e = m.list()
	if e != nil {
		return
	}
	var used model.Resources
	for _, l := range ls {
		if consuming(l.State) {
			used = used.Add(l.Request.Resources)
		}
	}
	for _, l := range ls {
		if l.Request.Kind == "storage" && l.State == "ending" {
			m.scheduleStorage(l.ID)
		}
	}
	for _, l := range ls {
		if l.State != "queued" {
			continue
		}
		if l.Request.StorageLease != "" {
			if _, err := m.storageForRun(l.Request); err != nil {
				_, _ = m.end(l.ID, "storage_unavailable")
				continue
			}
		}
		if !used.Add(l.Request.Resources).Fits(m.capacity) {
			break
		}
		var rb sql.NullString
		if e = m.db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&rb); e != nil {
			return
		}
		m.activate(&l, rb.Valid)
		l.Revision++
		if e = m.put(l, "lease.granted"); e != nil {
			return
		}
		used = used.Add(l.Request.Resources)
		if rb.Valid {
			var r RunRequest
			if json.Unmarshal([]byte(rb.String), &r) == nil {
				_, _ = m.launch(l, r)
			}
		} else if l.Request.Kind == "storage" {
			_, _ = m.provisionStorage(l)
		}
	}
}

func (m *Manager) launch(l model.Lease, r RunRequest) (model.Lease, error) {
	if e := os.MkdirAll(filepath.Join(m.home, "logs"), 0700); e != nil {
		return m.launchFailed(l, e)
	}
	f, e := os.OpenFile(filepath.Join(m.home, "logs", l.ID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return m.launchFailed(l, e)
	}
	// Do not execute user work until its PID and lease have been persisted.
	// Parent death closes the gate, making the waiting child exit.
	reader, writer, e := os.Pipe()
	if e != nil {
		f.Close()
		return m.launchFailed(l, e)
	}
	defer reader.Close()
	defer writer.Close()
	args := append([]string{"-c", `IFS= read -r arm_gate <&3 || exit 125; [ "$arm_gate" = go ] || exit 125; exec 3<&-; exec "$@"`, "arm-launch"}, r.Command...)
	cmd := exec.Command("/bin/sh", args...)
	cmd.ExtraFiles = []*os.File{reader}
	cmd.Dir = r.CWD
	cmd.Env = os.Environ()
	if l.Request.StorageLease != "" {
		store, e := m.storageForRun(l.Request)
		if e != nil {
			f.Close()
			return m.launchFailed(l, e)
		}
		cmd.Env = append(cmd.Env, "ARM_STORAGE_DIR="+store.ManagedDir, "ARM_STORAGE_LEASE="+store.ID)
		if l.Request.Profile == "rust-build" {
			target := filepath.Join(store.ManagedDir, "target")
			cmd.Env = append(cmd.Env, "CARGO_TARGET_DIR="+target, "CARGO_BUILD_TARGET_DIR="+target, "CARGO_BUILD_BUILD_DIR="+target)
		}
	}
	if l.Request.Profile == "rust-build" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("CARGO_BUILD_JOBS=%d", max(1, int(l.Request.Resources.CPU))))
	}
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = cmd.Start(); e != nil {
		f.Close()
		return m.launchFailed(l, e)
	}
	j := &job{cmd: cmd, done: make(chan struct{})}
	m.jobs[l.ID] = j
	l.PID = cmd.Process.Pid
	l.State = "active"
	l.Revision++
	if e = m.put(l, "lease.started"); e != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	} else {
		_, e = writer.Write([]byte("go\n"))
		if e != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	m.wg.Go(func() {
		err := cmd.Wait()
		f.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH) {
			time.Sleep(10 * time.Millisecond)
		}
		groupGone := errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH)
		m.mu.Lock()
		defer m.mu.Unlock()
		defer close(j.done)
		delete(m.jobs, l.ID)
		current, ge := m.get(l.ID)
		if ge != nil {
			return
		}
		code := 0
		if err != nil {
			code = 1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		current.ExitCode = &code
		now := time.Now().UTC()
		current.State = "terminal"
		if current.Outcome == "" {
			current.Outcome = "released"
		}
		current.Ended = &now
		if !groupGone {
			current.State = "needs_attention"
			current.Outcome = "descendants_unreconciled"
			current.Ended = nil
		}
		current.Revision++
		_ = m.put(current, "lease."+current.Outcome)
	})
	return l, e
}

func (m *Manager) launchFailed(l model.Lease, err error) (model.Lease, error) {
	now := time.Now().UTC()
	l.State = "terminal"
	l.Outcome = "launch_failed"
	l.Failure = err.Error()
	l.Ended = &now
	l.Revision++
	return l, m.put(l, "lease.launch_failed")
}

// Reconcile confirms that an uncertain process group is absent. It never
// sends a signal to a recovered PID, which may have been recycled.
func (m *Manager) Reconcile(id string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if l.State == "terminal" {
		return l, nil
	}
	if l.Request.Kind == "storage" {
		return l, model.Err("needs_attention", "use storage cleanup to retry or storage handoff to transfer responsibility")
	}
	if l.State != "needs_attention" || l.PID <= 0 {
		return l, model.Err("needs_attention", "lease has no verifiable recovered process group")
	}
	if !errors.Is(syscall.Kill(-l.PID, 0), syscall.ESRCH) {
		return l, model.Err("needs_attention", "process group still exists or cannot be inspected; no signal was sent")
	}
	now := time.Now().UTC()
	l.State = "terminal"
	l.Outcome = "reconciled"
	l.Ended = &now
	l.Revision++
	return l, m.put(l, "lease.reconciled")
}

func validateCargo(args []string, cpu float64) error {
	if cpu < 1 {
		return model.Err("invalid_request", "rust-build requires at least one CPU")
	}
	for _, arg := range args[1:] {
		if arg == "-j" || strings.HasPrefix(arg, "-j") || arg == "--jobs" || strings.HasPrefix(arg, "--jobs=") || arg == "--config" || strings.HasPrefix(arg, "--config=") {
			return model.Err("profile_conflict", "rust-build supplies the jobs budget; remove explicit jobs or config overrides")
		}
	}
	return nil
}

func (m *Manager) Output(id string, offset int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.get(id); e != nil {
		return nil, e
	}
	if offset < 0 {
		return nil, model.Err("invalid_request", "negative log offset")
	}
	f, e := os.Open(filepath.Join(m.home, "logs", id+".log"))
	if os.IsNotExist(e) {
		return []byte{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if _, e = f.Seek(offset, 0); e != nil {
		return nil, e
	}
	b := make([]byte, 32*1024)
	n, e := f.Read(b)
	if n > 0 {
		return b[:n], nil
	}
	return []byte{}, nil
}

func (m *Manager) Active() bool {
	s, e := m.Status()
	if e != nil {
		return true
	}
	for _, l := range s.Leases {
		if consuming(l.State) || l.State == "queued" {
			return true
		}
	}
	return false
}
