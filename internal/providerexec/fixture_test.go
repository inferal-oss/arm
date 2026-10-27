package providerexec

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/providerproto"
	"github.com/inferal-oss/arm/internal/usage"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "_fixture-group-anchor" {
		_, _ = os.Stdout.WriteString("ready\n")
		for {
			time.Sleep(time.Hour)
		}
	}
	if len(os.Args) == 3 && os.Args[1] == "_fixture-reserve" {
		os.Exit(reserveFixtureChild(os.Args[2]))
	}
	if len(os.Args) == 2 && os.Args[1] == HelperArgument {
		os.Exit(HelperMain(nil, os.Stdout, os.Stderr))
	}
	if len(os.Args) == 2 && os.Args[1] == "--arm-provider-v1" {
		os.Exit(fixtureProvider())
	}
	os.Exit(m.Run())
}

// Actual fresh diagnostic process. Its only configuration source is the request.
func fixtureProvider() int {
	d, err := providerproto.ReadRequest(os.Stdin)
	if err != nil {
		return 125
	}
	q := d.Request()
	var config struct {
		Mode     string `json:"mode"`
		Sentinel string `json:"sentinel"`
		Marker   string `json:"marker"`
	}
	if json.Unmarshal(q.Configuration.Canonical(), &config) != nil {
		return 125
	}
	if config.Sentinel != "" {
		b, e := os.ReadFile(config.Sentinel)
		if e != nil || string(b) != "owned fixture sentinel" {
			return 125
		}
	}
	if config.Marker != "" {
		if os.WriteFile(config.Marker, []byte("entered"), 0600) != nil {
			return 125
		}
	}
	if os.Getenv("ARM_PROVIDER_SECRET") != "" || os.Getenv("ARM_HOME") != "" || os.Getenv("ARM_JSON") != "" {
		return 125
	}
	switch config.Mode {
	case "slow":
		time.Sleep(time.Hour)
	case "stderr":
		_, _ = os.Stderr.Write([]byte(strings.Repeat("x", MaxStderrBytes+8192)))
	case "extra":
		_, _ = os.Stdout.Write([]byte("invalid extra bytes"))
		return 0
	case "nonzero":
		return 7
	}
	r := providerproto.Response{Protocol: providerproto.Protocol, Version: 1, Kind: "response", RequestID: q.RequestID, Registration: q.Registration, Method: q.Method, OperationKey: q.Context.OperationKey, IntentSHA256: q.Context.IntentSHA256}
	switch q.Method {
	case providerproto.Describe:
		r.Result = providerproto.Descriptor{Kind: "descriptor", ImplementationID: "fixture", ImplementationVersion: "fixture-1", ProtocolVersions: []int{1}, ResourceTypes: []providerproto.Name{"object"}, Methods: []providerproto.Method{providerproto.Describe, providerproto.Validate, providerproto.Plan}, Metadata: providerproto.NullData()}
	case providerproto.Validate:
		r.Result = providerproto.Validated{Kind: "validated", ConfigurationSHA256: q.Configuration.SHA256()}
	case providerproto.Plan:
		p := q.Params.(providerproto.PlanParams)
		r.Result = providerproto.Planned{Kind: "planned", SpecificationSHA256: p.Specification.SHA256(), EstimatedBudgets: []providerproto.Budget{}, Readiness: "delayed", Metadata: providerproto.NullData()}
	default:
		return 125
	}
	if providerproto.WriteResponse(os.Stdout, r) != nil {
		return 125
	}
	return 0
}

type fixture struct {
	m                 *manager.Manager
	root              string
	runtime, provider Image
	owner             *Owner
	journal           *fixtureJournal
	mu                sync.Mutex
	gates             []chan struct{}
	// Test-only source observation after actual native/temp/worker positives,
	// before the separate SQL finalization; no manager/SQL lock is held here.
	beforeSettlement func(*testing.T, *Handle)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := manager.Open(filepath.Join(root, "arm"), model.Resources{CPU: 8, Memory: 2 << 30, Disk: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := PinImage(exe, true)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	target := filepath.Join(root, "provider")
	in, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	in.Close()
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := PinImage(target, false)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := openFixtureJournal(filepath.Join(root, "worker-fixture.db"), status.Host)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewOwner(status.Host)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{m: m, root: root, runtime: runtime, provider: provider, owner: owner, journal: journal}
	t.Cleanup(func() {
		f.mu.Lock()
		for _, g := range f.gates {
			select {
			case <-g:
			default:
				close(g)
			}
		}
		f.mu.Unlock()
		f.owner.mu.Lock()
		handles := []*Handle{}
		for _, h := range f.owner.handles {
			handles = append(handles, h)
		}
		f.owner.mu.Unlock()
		for _, h := range handles {
			h.Cancel()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			_, err := h.WaitObservation(ctx)
			cancel()
			if err != nil {
				t.Error("retained native fixture cleanup", err)
			}
		}
		if err := journal.db.Close(); err != nil {
			t.Error(err)
		}
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *fixture) gate() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := make(chan struct{})
	f.gates = append(f.gates, g)
	return g
}
func (f *fixture) prepared(t *testing.T, attempt, reg string, method providerproto.Method, mode string) Prepared {
	t.Helper()
	worker, err := f.m.Acquire(model.Request{Host: "localhost", Key: attempt + "-worker", Reason: "owned provider diagnostic fixture CPU and memory", Resources: model.Resources{CPU: 1, Memory: 128 << 20}, TTL: time.Hour}, nil)
	if err != nil {
		t.Fatal(err)
	}
	temp, err := f.m.Acquire(model.Request{Host: "localhost", Key: attempt + "-temp", Kind: "storage", Reason: "owned provider diagnostic fixture temp", Resources: model.Resources{Disk: 64 << 20}, TTL: time.Hour}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rawWorker, _ := json.Marshal(worker)
	rawTemp, _ := json.Marshal(temp)
	f.journal.trace.raw("ordinary_worker_granted", attempt, rawWorker)
	f.journal.trace.raw("managed_temp_granted", attempt, rawTemp)
	held, err := HeldFromTrusted(worker, temp)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(f.root, "sentinel")
	if err = os.WriteFile(sentinel, []byte("owned fixture sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := ""
	if mode == "witness" {
		marker = filepath.Join(f.root, attempt+"-provider-entered")
	} // owned fixture instrumentation, not managed scratch
	if mode == "temp-write" {
		marker = filepath.Join(temp.Artifact.Path, "provider-entered")
	}
	cfgraw, _ := json.Marshal(map[string]string{"mode": mode, "sentinel": sentinel, "marker": marker})
	config, err := providerproto.ParseData(cfgraw)
	if err != nil {
		t.Fatal(err)
	}
	q := providerproto.Request{Protocol: providerproto.Protocol, Version: 1, Kind: "request", RequestID: "wire", Registration: providerproto.Registration{ID: providerproto.ID(reg), Generation: "1", CodeSHA256: providerproto.Digest(f.provider.SHA256()), ImplementationVersion: "fixture-1", ResourceType: "object", ConfigSHA256: config.SHA256(), ExternalScope: providerproto.ExternalScope{Account: "account", Project: "project", Namespace: "namespace"}}, Configuration: config, CredentialRefs: []string{}, Method: method, Context: providerproto.Context{Authority: providerproto.ID(held.authority), Scope: providerproto.Scope{Kind: "host", HostID: providerproto.ID(held.authority)}, OwnerUID: providerproto.Uint(held.owner), OwnerName: nil, Reason: "actual diagnostic worker admission", OperationKey: providerproto.Key(attempt)}, Params: providerproto.ValidateParams{}}
	switch method {
	case providerproto.Describe:
		q.Params = providerproto.DescribeParams{}
	case providerproto.Plan:
		spec, _ := providerproto.ParseData([]byte(`{"payload":"owned"}`))
		q.Params = providerproto.PlanParams{Specification: spec, Budgets: []providerproto.Budget{}, ExpiresAt: "2027-01-01T00:00:00Z"}
	}
	q.Context.IntentSHA256, err = providerproto.IntentDigest(q)
	if err != nil {
		t.Fatal(err)
	}
	original, err := providerproto.OriginalFromTrusted(q)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := SelectFromTrusted(f.runtime, f.provider)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(attempt, reg, selection, held, q, original, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Fixture journal is real durable SQL, not the future P1b catalogue/manager
// adapter. No product path opens it. It enforces authority slots across handles
// and processes, records start ownership and prevents restart redispatch.
type fixtureJournal struct {
	db        *sql.DB
	authority string
	trace     observer
}

func openFixtureJournal(path, authority string) (*fixtureJournal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS worker_attempts(authority TEXT NOT NULL,id TEXT PRIMARY KEY,registration TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,owner TEXT NOT NULL,initial BLOB NOT NULL,terminal BLOB NOT NULL,remaining INTEGER NOT NULL)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &fixtureJournal{db: db, authority: authority}, nil
}
func initialFixtureBytes(p Prepared) ([]byte, error) {
	return encodeInitial(Initial{Attempt: ID(p.attempt), Generation: ID(p.registration), Authority: ID(p.held.authority), Request: p.request, RequestSHA256: Digest(p.requestDigest), WorkerLease: ID(p.held.worker), TempReservation: ID(p.held.temp), CPU: Uint("1000"), Memory: Uint(fmt.Sprint(p.held.memory)), TempDisk: Uint(fmt.Sprint(p.held.disk))})
}
func (j *fixtureJournal) reserve(p Prepared) error {
	raw, err := initialFixtureBytes(p)
	if err != nil {
		return err
	}
	return j.reserveRaw(p.attempt, p.registration, p.requestDigest, raw)
}
func (j *fixtureJournal) reserveRaw(id, registration, digest string, raw []byte) error {
	if len(raw) > InitialCeiling {
		return fmt.Errorf("initial durable bytes exceed bound")
	}
	t := Terminal{Phase: "prepared", WorkerLease: "", BudgetState: "held", Result: Selected(`null`)}
	t.Transitions[0] = Transition{"prepared", Stamp(time.Now().UTC().Format(time.RFC3339Nano))}
	terminal, err := encodeTerminal(t)
	if err != nil {
		return err
	}
	remaining := TerminalCeiling - len(terminal)
	result, err := j.db.Exec(`INSERT INTO worker_attempts(authority,id,registration,digest,phase,owner,initial,terminal,remaining) SELECT ?,?,?,?,'prepared','',?,?,? WHERE (SELECT COUNT(*) FROM worker_attempts WHERE authority=? AND phase<>'settled')<4 AND NOT EXISTS(SELECT 1 FROM worker_attempts WHERE authority=? AND registration=? AND phase<>'settled') AND (SELECT COUNT(*) FROM worker_attempts WHERE authority=?)<128 AND (SELECT COALESCE(SUM(length(initial)+length(terminal)+remaining+1024),0) FROM worker_attempts WHERE authority=?)+?<=4194304 AND (SELECT COALESCE(SUM(length(initial)+length(terminal)+remaining+1024),0) FROM worker_attempts WHERE authority=? AND registration=?)+?<=131072`, j.authority, id, registration, digest, raw, terminal, remaining, j.authority, j.authority, registration, j.authority, j.authority, len(raw)+TerminalCeiling+1024, j.authority, registration, len(raw)+TerminalCeiling+1024)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("durable scope/count/byte reservations unavailable")
	}
	j.trace.sql("durable_reserve_ack", id, raw, terminal, remaining)
	return nil
}
func (j *fixtureJournal) terminal(id string) (Terminal, []byte, error) {
	var raw []byte
	err := j.db.QueryRow(`SELECT terminal FROM worker_attempts WHERE authority=? AND id=?`, j.authority, id).Scan(&raw)
	if err != nil {
		return Terminal{}, nil, err
	}
	var t Terminal
	if err = json.Unmarshal(raw, &t); err != nil {
		return t, nil, err
	}
	return t, raw, nil
}

// All native/manager work precedes this bounded pure serializer and exact CAS.
// Each named slot is write-once, independent of chronological array order.
func (j *fixtureJournal) publish(id string, next Terminal, old []byte) error {
	prior, _, err := j.terminal(id)
	if err != nil {
		return err
	}
	for i, v := range prior.Transitions {
		if v.Phase != "" && v != next.Transitions[i] {
			return fmt.Errorf("write-once milestone changed")
		}
	}
	raw, err := encodeTerminal(next)
	if err != nil {
		return err
	}
	remaining := TerminalCeiling - len(raw)
	if next.Phase == "settled" {
		remaining = 0
	}
	result, err := j.db.Exec(`UPDATE worker_attempts SET phase=?,terminal=?,remaining=? WHERE authority=? AND id=? AND terminal=?`, string(next.Phase), raw, remaining, j.authority, id, old)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("fixture exact terminal CAS conflict")
	}
	j.trace.sql("durable_"+string(next.Phase)+"_ack", id, nil, raw, remaining)
	return nil
}
func milestone(t *Terminal, index int, label Phase) {
	if t.Transitions[index].Phase == "" {
		t.Transitions[index] = Transition{label, Stamp(time.Now().UTC().Format(time.RFC3339Nano))}
	}
	t.Phase = label
}

func (j *fixtureJournal) startMarker(p Prepared, ownerToken string) (StartMarker, error) {
	native, err := usage.Capture(os.Getpid())
	if err != nil {
		return StartMarker{}, err
	}
	rawNative, _ := json.Marshal(native)
	owner := hash(rawNative)
	if !strings.HasPrefix(ownerToken, owner+":") || len(ownerToken) != 97 {
		return StartMarker{}, fmt.Errorf("actual native owner epoch required")
	}
	owner = ownerToken
	terminal, old, err := j.terminal(p.attempt)
	if err != nil {
		return StartMarker{}, err
	}
	if terminal.Phase != "prepared" {
		return StartMarker{}, fmt.Errorf("original attempt already started/unknown; no replay")
	}
	milestone(&terminal, 1, "helper_starting")
	terminal.WorkerLease = ID(p.held.worker)
	raw, err := encodeTerminal(terminal)
	if err != nil {
		return StartMarker{}, err
	}
	result, err := j.db.Exec(`UPDATE worker_attempts SET phase='helper_starting',owner=?,terminal=?,remaining=? WHERE authority=? AND id=? AND digest=? AND phase='prepared' AND terminal=?`, owner, raw, TerminalCeiling-len(raw), j.authority, p.attempt, p.requestDigest, old)
	if err != nil {
		return StartMarker{}, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return StartMarker{}, fmt.Errorf("original launch marker conflict; no replay")
	}
	j.trace.sql("durable_start_marker_ack", p.attempt, nil, raw, TerminalCeiling-len(raw))
	return StartMarker{p.attempt, p.requestDigest, j.authority, p.held.worker, p.held.temp, owner}, nil
}

func (f *fixture) begin(t *testing.T, p Prepared) *Handle {
	t.Helper()
	if err := f.journal.reserve(p); err != nil {
		t.Fatal(err)
	}
	marker, err := f.journal.startMarker(p, f.owner.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	h, err := f.owner.BeginStart(context.Background(), p, marker)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Caller-side observed uncertainty is committed in its fixed write-once slot
// before waiting for a late native outcome; it never grants another start.
func (f *fixture) observedUnknown(t *testing.T, h *Handle) {
	t.Helper()
	terminal, old, err := f.journal.terminal(h.prepared.attempt)
	if err != nil {
		t.Fatal(err)
	}
	milestone(&terminal, 5, "unknown")
	if err = f.journal.publish(h.prepared.attempt, terminal, old); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) settle(t *testing.T, h *Handle) {
	t.Helper()
	if !f.compensate(t, h, true) {
		t.Fatal("expected positive compensation")
	}
}
func (f *fixture) compensate(t *testing.T, h *Handle, requireComplete bool) bool {
	t.Helper()
	o := h.Snapshot()
	if !o.ScopedNativeClosed && !o.KnownNotStarted {
		t.Fatal("native ownership unresolved")
	}
	terminal, old, e := f.journal.terminal(h.prepared.attempt)
	if e != nil {
		t.Fatal(e)
	}
	milestone(&terminal, 4, "observed")
	terminal.ObservedAt = Stamp(time.Now().UTC().Format(time.RFC3339Nano))
	terminal.MonitorClosed = o.PumpsJoined
	terminal.Group = "observed_absent"
	if o.KnownNotStarted {
		terminal.Group = "not_started"
	}
	terminal.ResponseSHA256 = nil
	if o.ResponseSHA256 != "" {
		d := Digest(o.ResponseSHA256)
		terminal.ResponseSHA256 = &d
	}
	terminal.StdoutBytes = Uint(fmt.Sprint(o.StdoutBytes))
	terminal.StderrBytes = Uint(fmt.Sprint(o.StderrBytes))
	if len(o.Selected) > 0 {
		terminal.Result = Selected(o.Selected)
	}
	if o.Failure != "" {
		milestone(&terminal, 5, "unknown")
		if terminal.Failure == nil {
			terminal.Failure = &Failure{Code: Code(o.Failure), Message: Message("Owned diagnostic did not produce a confirmed success")}
		}
	}
	if e = f.journal.publish(h.prepared.attempt, terminal, old); e != nil {
		t.Fatal(e)
	}
	terminal, old, e = f.journal.terminal(h.prepared.attempt)
	if e != nil {
		t.Fatal(e)
	}
	milestone(&terminal, 6, "compensating")
	terminal.BudgetState = "cleanup_pending"
	if e = f.journal.publish(h.prepared.attempt, terminal, old); e != nil {
		t.Fatal(e)
	}
	temp, err := f.m.End(h.prepared.held.temp, "released")
	if err != nil {
		t.Fatal(err)
	}
	// Actual cleanup owns a30s context with two independent bounded native
	// activity checks. A5s fixture deadline cannot assert its outcome.
	until := time.Now().Add(35 * time.Second)
	for temp.State != "terminal" && temp.State != "needs_attention" && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
		temp, err = f.m.Inspect(temp.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if temp.State != "terminal" || temp.Outcome != "released" || temp.Artifact == nil || temp.Artifact.Phase != "removed" {
		if !requireComplete && temp.State == "needs_attention" {
			terminal, old, e = f.journal.terminal(h.prepared.attempt)
			if e != nil {
				t.Fatal(e)
			}
			milestone(&terminal, 5, "unknown")
			terminal.BudgetState = "unknown"
			if terminal.Failure == nil {
				terminal.Failure = &Failure{Code: "temp_reclamation_unverified", Message: "Managed temp reclamation is unverified; native closure does not free commitments"}
			}
			if e = f.journal.publish(h.prepared.attempt, terminal, old); e != nil {
				t.Fatal(e)
			}
			return false
		}
		t.Fatalf("positive exact managed-temp cleanup missing: %+v", temp)
	}
	terminal, old, e = f.journal.terminal(h.prepared.attempt)
	if e != nil {
		t.Fatal(e)
	}
	terminal.BudgetState = "release_pending"
	if e = f.journal.publish(h.prepared.attempt, terminal, old); e != nil {
		t.Fatal(e)
	}
	rawT, _ := json.Marshal(temp)
	f.journal.trace.raw("positive_temp_cleanup", h.prepared.attempt, rawT)
	worker, err := f.m.End(h.prepared.held.worker, "released")
	if err != nil || worker.State != "terminal" || worker.Outcome != "released" {
		t.Fatal("positive ordinary release", worker, err)
	}
	rawW, _ := json.Marshal(worker)
	f.journal.trace.raw("positive_worker_release", h.prepared.attempt, rawW)
	if f.beforeSettlement != nil {
		f.beforeSettlement(t, h)
	}
	terminal, old, e = f.journal.terminal(h.prepared.attempt)
	if e != nil {
		t.Fatal(e)
	}
	terminal.BudgetState = "released"
	milestone(&terminal, 7, "settled")
	if e = f.journal.publish(h.prepared.attempt, terminal, old); e != nil {
		t.Fatal(e)
	}
	if err = f.owner.RetireFromTrusted(h, modelLeaseReceipt{worker.ID, worker.Host, worker.Outcome, hash(rawW)}, modelLeaseReceipt{temp.ID, temp.Host, temp.Outcome, hash(rawT)}); err != nil {
		t.Fatal(err)
	}
	return true
}

func (f *fixture) grant(t *testing.T, h *Handle) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	native := h.helloProcess
	h.mu.Unlock()
	raw, _ := json.Marshal(native)
	terminal, old, err := f.journal.terminal(h.prepared.attempt)
	if err != nil {
		t.Fatal(err)
	}
	milestone(&terminal, 2, "helper_ready")
	if err = f.journal.publish(h.prepared.attempt, terminal, old); err != nil {
		t.Fatal(err)
	}
	terminal, old, err = f.journal.terminal(h.prepared.attempt)
	if err != nil {
		t.Fatal(err)
	}
	milestone(&terminal, 3, "dispatching")
	if err = f.journal.publish(h.prepared.attempt, terminal, old); err != nil {
		t.Fatal(err)
	}
	var id, digest, phase, owner string
	if err = f.journal.db.QueryRow(`SELECT id,digest,phase,owner FROM worker_attempts WHERE id=?`, h.prepared.attempt).Scan(&id, &digest, &phase, &owner); err != nil {
		t.Fatal(err)
	}
	receipt, _ := json.Marshal([]string{id, digest, phase, owner})
	if err = h.Execute(ctx, DispatchMarker{h.marker, hash(raw), hash(receipt)}); err != nil {
		t.Fatal(err)
	}
}

type reserveInput struct {
	Path, Authority, ID, Registration, Digest string
	Initial                                   json.RawMessage
}

func reserveFixtureChild(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 2
	}
	raw, err := io.ReadAll(io.LimitReader(file, InitialCeiling+4097))
	file.Close()
	if err != nil || len(raw) > InitialCeiling+4096 {
		return 2
	}
	var input reserveInput
	if json.Unmarshal(raw, &input) != nil {
		return 2
	}
	j, err := openFixtureJournal(input.Path, input.Authority)
	if err != nil {
		return 2
	}
	defer j.db.Close()
	if j.reserveRaw(input.ID, input.Registration, input.Digest, input.Initial) != nil {
		return 1
	}
	return 0
}
func runReserveChildren(t *testing.T, f *fixture, prepared []Prepared) int {
	t.Helper()
	exe, _ := os.Executable()
	commands := []*exec.Cmd{}
	for i, p := range prepared {
		raw, err := initialFixtureBytes(p)
		if err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(reserveInput{filepath.Join(f.root, "worker-fixture.db"), f.journal.authority, p.attempt, p.registration, p.requestDigest, raw})
		path := filepath.Join(f.root, fmt.Sprintf("reserve-%d.json", i))
		if err = os.WriteFile(path, input, 0600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(exe, "_fixture-reserve", path)
		command.Env = []string{"PATH=/usr/bin:/bin"}
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	successes := 0
	for _, command := range commands {
		if err := command.Wait(); err == nil {
			successes++
		} else if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
			t.Fatal("unexpected reserve child failure", err)
		}
	}
	return successes
}
