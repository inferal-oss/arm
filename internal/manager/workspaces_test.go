package manager

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
)

func workspaceManagerFixture(t *testing.T, home string) (*Manager, workspace.Operation) {
	t.Helper()
	m, e := Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	store, e := mesh.InitPrepared(home, m.host, "workspace-fixture", "127.0.0.1:23456", "127.0.0.1:23456")
	if e != nil {
		m.Close()
		t.Fatal(e)
	}
	store.Close()
	op, e := m.WorkspacePrepareCreate(context.Background(), workspace.CreateRequest{Key: "workspace-create", Name: "private lifecycle fixture", Policy: workspace.DefaultPolicy(), Reason: "reviewed private workspace lifecycle"})
	if e != nil {
		m.Close()
		t.Fatal(e)
	}
	op, e = m.WorkspaceDecide(context.Background(), op.Workspace, op.Key, op.Revision, op.ReviewHash, true)
	if e != nil {
		m.Close()
		t.Fatal(e)
	}
	return m, op
}
func TestWorkspaceReadDoesNotInitializeAndStatusUsesBoundedProjection(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, e := Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		s, e := m.Status()
		if e != nil || s.Workspaces == nil || s.Workspaces.Ledger != "unconfigured" {
			t.Fatal(s.Workspaces, e)
		}
	}
	if _, e = m.WorkspaceRead(context.Background()); e != nil {
		t.Fatal(e)
	}
	var count int
	m.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name LIKE 'workspace_%'`).Scan(&count)
	if count != 0 {
		t.Fatal("read created workspace schema")
	}
	if _, e = os.Stat(filepath.Join(home, "mesh")); !os.IsNotExist(e) {
		t.Fatal("read initialized mesh identity/listener", e)
	}
	if _, e = m.WorkspacePrepareCreate(context.Background(), workspace.CreateRequest{Key: "missing-identity", Name: "no identity", Policy: workspace.DefaultPolicy(), Reason: "must not auto init"}); errorCode(e) != "workspace_identity_unavailable" {
		t.Fatal(e)
	}
	m.Close()
	m, op := workspaceManagerFixture(t, home)
	defer m.Close()
	if _, e = m.db.Exec(`UPDATE workspace_instances SET data='{}' WHERE id=?`, op.Workspace); e != nil {
		t.Fatal(e)
	}
	// Status uses last validated bounded projection, not the complete ledger on
	// each dashboard refresh. Fresh authority calls detect the corruption.
	if s, e := m.Status(); e != nil || s.Workspaces.Ledger != "available" {
		t.Fatal("Status scanned workspace evidence", s.Workspaces, e)
	}
	if _, e = m.WorkspacePrepareCreate(context.Background(), workspace.CreateRequest{Key: "must-revalidate", Name: "new", Policy: workspace.DefaultPolicy(), Reason: "must scan fresh authority evidence"}); errorCode(e) != "workspace_state_invalid" {
		t.Fatal("fresh authority used cached ledger", e)
	}
	s, e := m.Status()
	if e != nil || s.Workspaces.Ledger != "invalid" || s.Workspaces.Error == nil {
		t.Fatal(s.Workspaces, e)
	}
}
func TestWorkspaceMissingCurrentIdentityPreservesNativeSettlement(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, op := workspaceManagerFixture(t, home)
	if e := os.Rename(filepath.Join(home, "mesh", "config.json"), filepath.Join(home, "mesh", "config.saved")); e != nil {
		t.Fatal(e)
	}
	if _, e := m.WorkspacePrepareCreate(context.Background(), workspace.CreateRequest{Key: "missing", Name: "unavailable", Policy: workspace.DefaultPolicy(), Reason: "no auto identity"}); errorCode(e) != "workspace_identity_unavailable" {
		t.Fatal(e)
	}
	if _, found, e := m.WorkspaceLookup(context.Background(), op.Workspace, op.Key); e != nil || !found {
		t.Fatal(found, e)
	}
	v, e := m.WorkspaceRead(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.WorkspaceRetire(context.Background(), op.Workspace, workspace.MutationRequest{Key: "native-retire", Revision: v.Instances[0].Revision, Reason: "local retirement despite missing current identity"}); e != nil {
		t.Fatal(e)
	}
	m.Close()
	m, e = Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	v, e = m.WorkspaceRead(context.Background())
	if e != nil || !workspace.Settled(v) {
		t.Fatal(v, e)
	}
}
func TestWorkspaceCrashFixtureChild(t *testing.T) {
	home := os.Getenv("ARM_WORKSPACE_MANAGER_CRASH_HOME")
	if home == "" {
		return
	}
	m, op := workspaceManagerFixture(t, home)
	storage, e := m.Acquire(storageRequest("workspace-cleanup"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, "unrelated.txt"), []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(storage)
	if e = os.WriteFile(filepath.Join(home, "crash-fixture.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	switch os.Getenv("ARM_WORKSPACE_MANAGER_DAMAGE") {
	case "missing-marker":
		_, e = m.db.Exec(`DELETE FROM metadata WHERE key='workspace_schema'`)
	case "missing-clock":
		_, e = m.db.Exec(`DELETE FROM workspace_clocks WHERE workspace=?`, op.Workspace)
	case "null-policy":
		_, e = m.db.Exec(`UPDATE workspace_instances SET data=json_set(data,'$.policy',NULL) WHERE id=?`, op.Workspace)
	case "empty-group":
		_, e = m.db.Exec(`DELETE FROM workspace_changes;DELETE FROM workspace_operations;DELETE FROM workspace_directory_members;DELETE FROM workspace_trusted_members;DELETE FROM workspace_clocks;DELETE FROM workspace_instances`)
	}
	if e != nil {
		t.Fatal(e)
	}
	os.Exit(71)
}
func TestWorkspaceDegradedActualCrashRestartPreservesProviderCleanup(t *testing.T) {
	for _, damage := range []string{"missing-marker", "missing-clock", "null-policy", "empty-group"} {
		t.Run(damage, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkspaceCrashFixtureChild$")
			cmd.Env = append(os.Environ(), "ARM_WORKSPACE_MANAGER_CRASH_HOME="+home, "ARM_WORKSPACE_MANAGER_DAMAGE="+damage)
			out, e := cmd.CombinedOutput()
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ExitCode() != 71 {
				t.Fatalf("fixture did not crash after durable damage: %v %s", e, out)
			}
			m, e := Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
			if e != nil {
				t.Fatal("workspace corruption disabled ordinary authority", e)
			}
			defer m.Close()
			m.Start()
			status, e := m.Status()
			if e != nil || status.Workspaces == nil || status.Workspaces.Ledger != "invalid" {
				t.Fatal(status.Workspaces, e)
			}
			if _, e = m.WorkspaceRead(context.Background()); errorCode(e) != "workspace_state_invalid" {
				t.Fatal("corrupt workspace was reinterpreted", e)
			}
			if _, e = m.WorkspacePrepareCreate(context.Background(), workspace.CreateRequest{Key: "forbidden", Name: "new", Policy: workspace.DefaultPolicy(), Reason: "no recreation"}); errorCode(e) != "workspace_state_invalid" {
				t.Fatal(e)
			}
			q := request("ordinary-work-still-allowed")
			q.TTL = 30 * time.Millisecond
			ordinary, e := m.Acquire(q, nil)
			if e != nil {
				t.Fatal("workspace error blocked unrelated resource admission", e)
			}
			storageEventually(t, func() bool {
				lease, _ := m.Inspect(ordinary.ID)
				return lease.State == "terminal" && lease.Outcome == "expired"
			})
			raw, e := os.ReadFile(filepath.Join(home, "crash-fixture.json"))
			if e != nil {
				t.Fatal(e)
			}
			var storage model.Lease
			if e = json.Unmarshal(raw, &storage); e != nil {
				t.Fatal(e)
			}
			if _, e = m.End(storage.ID, "released"); e != nil {
				t.Fatal("provider-safe storage cleanup blocked", e)
			}
			storageEventually(t, func() bool { lease, _ := m.Inspect(storage.ID); return lease.State == "terminal" })
			if _, e = os.Stat(storage.ManagedDir); !os.IsNotExist(e) {
				t.Fatal("owned empty directory not reclaimed", e)
			}
			if raw, e = os.ReadFile(filepath.Join(home, "unrelated.txt")); e != nil || string(raw) != "keep" {
				t.Fatal("unrelated data changed", e)
			}
			var marker string
			e = m.db.QueryRow(`SELECT value FROM metadata WHERE key='workspace_schema'`).Scan(&marker)
			if damage == "missing-marker" && e == nil {
				t.Fatal("corrupt group silently repaired")
			}
		})
	}
}

type workspaceProofFunc func(context.Context, workspace.Challenge) (workspace.Proof, error)

func (f workspaceProofFunc) Prove(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
	return f(ctx, c)
}
func TestWorkspaceIdentityRecheckedAfterProofAndInvalidatesCurrentRuntime(t *testing.T) {
	ctx := context.Background()
	d, created := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "directory"))
	defer d.Close()
	v, e := d.WorkspaceRead(ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	home := filepath.Join(t.TempDir(), "member")
	m, e := Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	identity, e := mesh.Init(home, m.host, "joining-node", "127.0.0.1:23458", "127.0.0.1:23458")
	if e != nil {
		t.Fatal(e)
	}
	identity.Close()
	join, e := m.WorkspacePrepareJoin(ctx, workspace.JoinRequest{Key: "join", Workspace: root.ID, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: d.host, Reason: "exact separately reviewed candidate"})
	if e != nil {
		t.Fatal(e)
	}
	join, e = m.WorkspaceDecide(ctx, join.Workspace, join.Key, join.Revision, join.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	p := workspace.Principal{Workspace: root.ID, Generation: root.Generation, Host: d.host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
	candidate := m.workspaces.identity
	admission, e := d.WorkspacePrepareAdmission(ctx, p, workspace.AdmissionRequest{Key: "candidate", Candidate: candidate, ConsentHash: join.ReviewHash, HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: "candidate consents"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = d.WorkspaceActivate(ctx, p, admission.Key, workspaceProofFunc(func(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
		if _, e := d.Status(); e != nil {
			t.Fatal("proof held manager mutex", e)
		}
		proof, e := m.WorkspaceProveJoin(ctx, root.Directory, c)
		if e == nil {
			e = os.Rename(filepath.Join(d.home, "mesh", "config.json"), filepath.Join(d.home, "mesh", "saved-config.json"))
		}
		return proof, e
	}))
	if errorCode(e) != "workspace_identity_unavailable" {
		t.Fatal("post-I/O identity replacement was not fenced", e)
	}

	// Admission keys have a workspace principal; inspect complete LOCAL native
	// evidence rather than pretending native lookup is the remote actor.
	v, e = d.WorkspaceRead(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, member := range v.Members {
		if member.Identity.Host == candidate.Host && member.State != "pending" {
			t.Fatal("old identity promoted candidate", member)
		}
	}
	if e = os.Rename(filepath.Join(d.home, "mesh", "saved-config.json"), filepath.Join(d.home, "mesh", "config.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = d.WorkspacePrepareCreate(ctx, workspace.CreateRequest{Key: "no-revive", Name: "restored", Policy: workspace.DefaultPolicy(), Reason: "manual config restore cannot revive invalidated runtime"}); errorCode(e) != "workspace_identity_unavailable" {
		t.Fatal(e)
	}
	if _, _, e = d.WorkspaceLookup(ctx, created.Workspace, created.Key); e != nil {
		t.Fatal(e)
	}
}

func TestWorkspaceManagerCloseCancelsAndJoinsAdmittedCallsBeforeDBClose(t *testing.T) {
	m, _ := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := workspaceCall(context.Background(), m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (bool, error) {
			if _, e := w.operator.Read(ctx); e != nil {
				return false, e
			}
			close(started)
			<-ctx.Done()
			var count int
			if e := m.db.QueryRow(`SELECT count(*) FROM metadata`).Scan(&count); e != nil {
				return false, e
			}
			<-release
			return false, ctx.Err()
		})
		done <- e
	}()
	select {
	case <-started:
	case e := <-done:
		t.Fatal(e)
	case <-time.After(time.Second):
		t.Fatal("call not admitted")
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case e := <-closed:
		t.Fatal("Close returned before admitted call settled", e)
	case <-time.After(30 * time.Millisecond):
	}
	if _, e := m.WorkspaceRead(context.Background()); errorCode(e) != "backend_stopping" {
		t.Fatal("closing manager admitted new workspace call", e)
	}
	close(release)
	if e := <-done; errorCode(e) != "backend_stopping" {
		t.Fatal(e)
	}
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join admitted call")
	}
	if e := m.db.Ping(); e == nil {
		t.Fatal("database still open after completed Close")
	}
}

func TestWorkspaceCloseCancelsHeldTransactionBeforeEndingOwnedJob(t *testing.T) {
	m, _ := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	q := request("owned-job-for-close-order")
	lease, e := m.Acquire(q, &RunRequest{Request: q, Command: []string{"/bin/sleep", "30"}, CWD: m.home})
	if e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	owned := m.jobs[lease.ID]
	m.mu.Unlock()
	if owned == nil {
		t.Fatal("owned process not supervised")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := make(chan struct{})
	callDone := make(chan error, 1)
	go func() {
		_, e := workspaceCall(ctx, m, false, func(ctx context.Context, _ *workspaceBoundary, _ *workspace.Store) (bool, error) {
			tx, e := m.db.BeginTx(ctx, nil)
			if e != nil {
				return false, e
			}
			defer tx.Rollback()
			var host string
			if e = tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='host'`).Scan(&host); e != nil {
				return false, e
			}
			close(started)
			<-ctx.Done()
			return false, ctx.Err()
		})
		callDone <- e
	}()
	select {
	case <-started:
	case e := <-callDone:
		t.Fatal(e)
	case <-time.After(time.Second):
		t.Fatal("transaction not held")
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		cancel()
		<-closed
		<-callDone
		t.Fatal("Close could not cancel workspace transaction before ordinary job-ending SQL")
	}
	if e := <-callDone; errorCode(e) != "backend_stopping" {
		t.Fatal(e)
	}
	select {
	case <-owned.done:
	default:
		t.Fatal("owned process did not finish normal supervision")
	}
	if e = m.db.Ping(); e == nil {
		t.Fatal("DB did not close after workspace and owned job settled")
	}
}
