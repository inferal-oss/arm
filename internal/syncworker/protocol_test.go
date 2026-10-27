package syncworker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
	_ "modernc.org/sqlite"
)

func hash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

type testHold struct {
	intent  syncservice.Intent
	root    string
	disk    int64
	charged bool
	reject  bool
}

func (h *testHold) CheckActive(context.Context) error {
	if h.reject {
		return errors.New("hold revoked")
	}
	return nil
}
func (h *testHold) MemoryBytes() int64 { return 64 << 20 }
func (h *testHold) DiskBytes() int64   { return h.disk }
func (h *testHold) CheckIntent(_ context.Context, i syncservice.Intent) error {
	if !reflect.DeepEqual(i, h.intent) {
		return errors.New("intent differs from admission")
	}
	return nil
}
func (h *testHold) ChargeVolume(_ context.Context, root string, n int64) error {
	if root != h.root || n > h.disk {
		return errors.New("wrong volume or disk charge")
	}
	h.charged = true
	return nil
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.Exec(`PRAGMA synchronous=FULL`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = syncservice.InstallSchemaTx(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}
func reserveTest(t *testing.T, db *sql.DB, i syncservice.Intent, rows []syncservice.ManifestRow) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = syncservice.ReserveTx(t.Context(), tx, i, rows, expectedSnapshot(rows, i.Side)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type testPair struct {
	local, remote         *syncfs.Root
	localWork, remoteWork *syncfs.Root
	localDB, remoteDB     *sql.DB
	names                 syncfs.NamePolicy
	metadata              syncfs.Metadata
}

func setupPair(t *testing.T) testPair {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	open := func(name string) *syncfs.Root {
		p := filepath.Join(base, name)
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
		r, err := syncfs.OpenRoot(p)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		return r
	}
	p := testPair{local: open("local"), remote: open("remote"), localWork: open("local-work"), remoteWork: open("remote-work"), localDB: openTestDB(t), remoteDB: openTestDB(t)}
	p.names, err = syncfs.DetectNamePolicy(p.local, p.localWork)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func write(t *testing.T, r *syncfs.Root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Path(), name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func contents(t *testing.T, r *syncfs.Root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Path(), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func requestPair(t *testing.T, p testPair, direction syncreconcile.Direction, policy syncreconcile.Policy, prior *syncreconcile.ConfirmedBaseline, resolutions []syncreconcile.Resolution) syncreconcile.Request {
	t.Helper()
	local, err := p.local.Scan(t.Context(), nil, nil, p.names, p.metadata, syncfs.ScanLimits{Entries: 100, Bytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := p.remote.Scan(t.Context(), nil, nil, p.names, p.metadata, syncfs.ScanLimits{Entries: 100, Bytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for name := range local.NameKeys {
		if _, ok := remote.NameKeys[name]; !ok {
			remote.NameKeys[name], err = p.names.Key(name)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for name := range remote.NameKeys {
		if _, ok := local.NameKeys[name]; !ok {
			local.NameKeys[name], err = p.names.Key(name)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	lid, err := p.local.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	rid, err := p.remote.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	req := syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: "local/account", RootID: lid}, Remote: syncreconcile.Endpoint{ID: "remote/account", RootID: rid}, SelectionID: "all", MetadataID: "plain"}, Direction: direction, LocalPolicy: policy, RemotePolicy: policy, Local: local, Remote: remote, FirstRun: prior == nil, Baseline: prior, Resolutions: resolutions}
	if prior != nil {
		req.ExpectedGeneration = prior.Generation
	}
	return req
}
func runPair(t *testing.T, p testPair, req syncreconcile.Request, key string, joinError error) (*syncreconcile.ConfirmedBaseline, error, error, *endpoint, *endpoint) {
	t.Helper()
	plan, err := syncreconcile.Reconcile(req)
	if err != nil || len(plan.Conflicts()) != 0 {
		t.Fatalf("plan: %v conflicts=%+v", err, plan.Conflicts())
	}
	res := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 64 << 20}
	local, rows, err := syncservice.NewIntent(key, FixedCommandSHA256(), hash("reviewed SSH control"), "sync test", syncreconcile.Local, req, plan, res, res, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	remote, other, err := syncservice.NewIntent(key, FixedCommandSHA256(), local.SSHControlSHA256, local.Reason, syncreconcile.Remote, req, plan, res, res, local.Created)
	if err != nil || !reflect.DeepEqual(rows, other) {
		t.Fatalf("remote intent: %v", err)
	}
	reserveTest(t, p.localDB, local, rows)
	reserveTest(t, p.remoteDB, remote, rows)
	a, b := net.Pipe()
	ls, err := newSingleStreamSession(a, func() error { return joinError })
	if err != nil {
		t.Fatal(err)
	}
	rs, err := newSingleStreamSession(b, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	lh := &testHold{intent: local, root: req.Binding.Local.RootID, disk: 64 << 20}
	rh := &testHold{intent: remote, root: req.Binding.Remote.RootID, disk: 64 << 20}
	le := &endpoint{Side: syncreconcile.Local, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.localDB, Key: key, Side: syncreconcile.Local}, Hold: lh, Session: ls}
	re := &endpoint{Side: syncreconcile.Remote, Root: p.remote, WorkParent: p.remoteWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.remoteDB, Key: key, Side: syncreconcile.Remote}, Hold: rh, Session: rs}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	localResult := make(chan error, 1)
	remoteResult := make(chan error, 1)
	go func() { localResult <- runEndpoint(ctx, le) }()
	go func() { remoteResult <- runEndpoint(ctx, re) }()
	var lerr, rerr error
	select {
	case lerr = <-localResult:
	case <-ctx.Done():
		t.Fatal("local worker did not join")
	}
	select {
	case rerr = <-remoteResult:
	case <-ctx.Done():
		t.Fatal("remote worker did not join")
	}
	if !lh.charged || !rh.charged || !ls.joined || !rs.joined {
		t.Fatalf("missing holds or joins: holds=%v/%v joins=%v/%v", lh.charged, rh.charged, ls.joined, rs.joined)
	}
	if lerr != nil || rerr != nil {
		return nil, lerr, rerr, le, re
	}
	for _, endpoint := range []*endpoint{le, re} {
		records, err := endpoint.Ledger.Workspaces(ctx)
		if err != nil || len(records) == 0 {
			t.Fatalf("successful worker omitted durable workspace locators: %+v %v", records, err)
		}
		for _, record := range records {
			if !strings.HasPrefix(record.Path, endpoint.WorkParent.Path()+string(os.PathSeparator)) {
				t.Fatalf("workspace locator escaped private parent: %+v", record)
			}
		}
	}
	lb, err := syncservice.LoadBaseline(t.Context(), p.localDB, req.Binding, syncreconcile.Local)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := syncservice.LoadBaseline(t.Context(), p.remoteDB, req.Binding, syncreconcile.Remote)
	if err != nil || !reflect.DeepEqual(lb, rb) {
		t.Fatalf("baseline mismatch: %v", err)
	}
	return lb, nil, nil, le, re
}

func TestFixedWorkerThroughDuplexTransfer(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		direction             syncreconcile.Direction
		policy                syncreconcile.Policy
		local, remote         map[string]string
		wantLocal, wantRemote map[string]string
	}{
		{"push-additive", syncreconcile.Push, syncreconcile.Additive, map[string]string{"a": "from local"}, map[string]string{"r": "keep remote"}, map[string]string{"a": "from local"}, map[string]string{"a": "from local", "r": "keep remote"}},
		{"push-full", syncreconcile.Push, syncreconcile.Full, map[string]string{"a": "from local"}, map[string]string{"r": "delete remote"}, map[string]string{"a": "from local"}, map[string]string{"a": "from local"}},
		{"pull-additive", syncreconcile.Pull, syncreconcile.Additive, map[string]string{"l": "keep local"}, map[string]string{"a": "from remote"}, map[string]string{"a": "from remote", "l": "keep local"}, map[string]string{"a": "from remote"}},
		{"both-additive", syncreconcile.Both, syncreconcile.Additive, map[string]string{"a": "from local"}, map[string]string{"b": "from remote"}, map[string]string{"a": "from local", "b": "from remote"}, map[string]string{"a": "from local", "b": "from remote"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := setupPair(t)
			for n, v := range tc.local {
				write(t, p.local, n, v)
			}
			for n, v := range tc.remote {
				write(t, p.remote, n, v)
			}
			req := requestPair(t, p, tc.direction, tc.policy, nil, nil)
			_, lerr, rerr, _, _ := runPair(t, p, req, "sync-key-"+strings.Repeat("x", 16), nil)
			if lerr != nil || rerr != nil {
				t.Fatalf("workers: local=%v remote=%v", lerr, rerr)
			}
			for n, v := range tc.wantLocal {
				if got := contents(t, p.local, n); got != v {
					t.Fatalf("local %s=%q", n, got)
				}
			}
			for n, v := range tc.wantRemote {
				if got := contents(t, p.remote, n); got != v {
					t.Fatalf("remote %s=%q", n, got)
				}
			}
			if tc.name == "push-full" {
				if _, err := os.Lstat(filepath.Join(p.remote.Path(), "r")); !os.IsNotExist(err) {
					t.Fatalf("full removal: %v", err)
				}
			}
		})
	}
}

func TestFailedTransportJoinDoesNotConfirmBaseline(t *testing.T) {
	p := setupPair(t)
	write(t, p.local, "a", "bytes")
	req := requestPair(t, p, syncreconcile.Push, syncreconcile.Additive, nil, nil)
	_, lerr, _, le, _ := runPair(t, p, req, "sync-key-"+strings.Repeat("j", 16), errors.New("transport join failed"))
	if lerr == nil || !strings.Contains(lerr.Error(), "transport join failed") {
		t.Fatalf("join failure lost: %v", lerr)
	}
	if _, err := syncservice.LoadBaseline(t.Context(), p.localDB, req.Binding, syncreconcile.Local); err == nil {
		t.Fatal("baseline available after failed local join")
	}
	lookup, err := le.Ledger.Lookup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if lookup.State == "confirmed" {
		t.Fatal("baseline confirmed before transport join")
	}
}

func TestConflictKeepBothOverSingleSSHStream(t *testing.T) {
	p := setupPair(t)
	write(t, p.local, "shared", "original")
	first := requestPair(t, p, syncreconcile.Both, syncreconcile.Additive, nil, nil)
	baseline, lerr, rerr, _, _ := runPair(t, p, first, "sync-key-"+strings.Repeat("a", 16), nil)
	if lerr != nil || rerr != nil {
		t.Fatalf("first generation: %v / %v", lerr, rerr)
	}
	write(t, p.local, "shared", "local edit")
	write(t, p.remote, "shared", "remote edit")
	req := requestPair(t, p, syncreconcile.Both, syncreconcile.Additive, baseline, nil)
	localEntry := req.Local.Entries["shared"]
	remoteEntry := req.Remote.Entries["shared"]
	resolution := syncreconcile.Resolution{Path: "shared", Choice: syncreconcile.KeepBoth, ExpectedLocal: &localEntry, ExpectedRemote: &remoteEntry, LocalName: "shared.local", RemoteName: "shared.remote"}
	for _, name := range []string{resolution.LocalName, resolution.RemoteName} {
		key, err := p.names.Key(name)
		if err != nil {
			t.Fatal(err)
		}
		req.Local.NameKeys[name] = key
		req.Remote.NameKeys[name] = key
	}
	req.Resolutions = []syncreconcile.Resolution{resolution}
	second, lerr, rerr, _, _ := runPair(t, p, req, "sync-key-"+strings.Repeat("b", 16), nil)
	if lerr != nil || rerr != nil {
		t.Fatalf("resolved generation: %v / %v", lerr, rerr)
	}
	if second.Generation != 2 {
		t.Fatalf("generation %d", second.Generation)
	}
	for _, root := range []*syncfs.Root{p.local, p.remote} {
		if contents(t, root, "shared.local") != "local edit" || contents(t, root, "shared.remote") != "remote edit" {
			t.Fatal("keep-both contents changed")
		}
		if _, err := os.Lstat(filepath.Join(root.Path(), "shared")); !os.IsNotExist(err) {
			t.Fatalf("original conflict path remains: %v", err)
		}
	}
}

func TestCancellationClosesBlockedSSHControl(t *testing.T) {
	p := setupPair(t)
	write(t, p.local, "selected", "data")
	req := requestPair(t, p, syncreconcile.Push, syncreconcile.Additive, nil, nil)
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	resources := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 64 << 20}
	intent, rows, err := syncservice.NewIntent("sync-key-"+strings.Repeat("c", 16), FixedCommandSHA256(), hash("control"), "cancel test", syncreconcile.Local, req, plan, resources, resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	reserveTest(t, p.localDB, intent, rows)
	a, b := net.Pipe()
	defer b.Close()
	session, err := newSingleStreamSession(a, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	hold := &testHold{intent: intent, root: req.Binding.Local.RootID, disk: 64 << 20}
	e := &endpoint{Side: syncreconcile.Local, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.localDB, Key: intent.Key, Side: syncreconcile.Local}, Hold: hold, Session: session}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runEndpoint(ctx, e) }()
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = io.ReadFull(b, make([]byte, 8)); err != nil {
		t.Fatalf("preparation never reached SSH stream: %v", err)
	}
	cancel()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled SSH worker succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled SSH worker did not join")
	}
	if !session.joined || hold.charged {
		t.Fatal("cancelled control was not joined or reached native stage")
	}
	if got := contents(t, p.local, "selected"); got != "data" {
		t.Fatalf("selected content changed: %q", got)
	}
}

func TestUnreviewedWorkerCommandCannotDispatch(t *testing.T) {
	p := setupPair(t)
	write(t, p.local, "selected", "data")
	req := requestPair(t, p, syncreconcile.Push, syncreconcile.Additive, nil, nil)
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	resources := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 64 << 20}
	intent, rows, err := syncservice.NewIntent("sync-key-"+strings.Repeat("u", 16), hash("unreviewed command"), hash("control"), "reject test", syncreconcile.Local, req, plan, resources, resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	reserveTest(t, p.localDB, intent, rows)
	a, b := net.Pipe()
	defer b.Close()
	session, err := newSingleStreamSession(a, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	hold := &testHold{intent: intent, root: req.Binding.Local.RootID, disk: 64 << 20}
	e := &endpoint{Side: syncreconcile.Local, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.localDB, Key: intent.Key, Side: syncreconcile.Local}, Hold: hold, Session: session}
	if err = runEndpoint(t.Context(), e); err == nil || !strings.Contains(err.Error(), "unreviewed SSH worker command") {
		t.Fatalf("unreviewed command: %v", err)
	}
	if hold.charged || !session.joined {
		t.Fatal("unreviewed command charged disk or skipped SSH join")
	}
	if got := contents(t, p.local, "selected"); got != "data" {
		t.Fatalf("selected content changed: %q", got)
	}
}
