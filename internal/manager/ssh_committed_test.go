package manager

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

func committedControl(m *Manager, q model.SSHExecutionRequest) model.SSHControlCommitment {
	c := fenceControl(m, q.Key)
	c.Source = q.Source
	c.Target = q.Target
	c.Pair = q.Pair
	c.Revision = q.Revision
	c.Certificate = q.Certificate
	c.Account = q.Account
	c.PublicKeySHA256 = q.PublicKeySHA256
	c.Reason = q.Reason
	c.TTL = q.TTL
	c.Resources = q.Resources
	c.CWD = q.CWD
	c.Kind = q.Kind
	c.CommandSHA256 = q.CommandSHA256
	c.PTY = q.TerminalSHA256 != ""
	return c
}
func committedFixture(t *testing.T, m *Manager) *sshManagerFixture {
	t.Helper()
	return newSSHManagerFixtureWithAdmission(t, m, time.Minute, func(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel) (model.Lease, sshservice.Process, error) {
		c := committedControl(m, q)
		return m.AcquireSSHCommitted(ctx, q, e, ch, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute), c)
	})
}
func sshTestQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func TestSSHCommittedChildWitness(t *testing.T) {
	split := -1
	for i, v := range os.Args {
		if v == "--" {
			split = i
			break
		}
	}
	if split < 0 {
		return
	}
	args := os.Args[split+1:]
	if len(args) != 3 {
		os.Exit(4)
	}
	uri := (&url.URL{Scheme: "file", Path: args[0]}).String() + "?mode=ro&_pragma=query_only(1)"
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		os.Exit(5)
	}
	var n int
	e = db.QueryRow(`SELECT COUNT(*) FROM ssh_key_fences f JOIN ssh_operations o ON o.principal=f.principal AND o.key=f.key AND o.lease=f.lease JOIN leases l ON l.id=o.lease WHERE f.principal=? AND f.key=? AND f.state='admitted'`, args[1], args[2]).Scan(&n)
	db.Close()
	if e != nil || n != 1 {
		fmt.Fprintf(os.Stderr, "durable admission witness failed: %v count%d", e, n)
		os.Exit(6)
	}
	fmt.Print("fence-visible-before-requested-process-output")
	os.Exit(0)
}
func TestSSHCommittedProcessWitnessesTransactionBeforeExec(t *testing.T) {
	m := newManager(t)
	f := committedFixture(t, m)
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	command := sshTestQuote(binary) + " -test.run '^TestSSHCommittedChildWitness$' -- " + sshTestQuote(filepath.Join(m.home, "state.db")) + " " + sshTestQuote(f.q.Source) + " " + sshTestQuote(f.q.Key)
	session, e := f.client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	output, e := session.CombinedOutput(command)
	if e != nil || string(output) != "fence-visible-before-requested-process-output" {
		t.Fatal("requested process could not see committed fence+operation+lease", string(output), e)
	}
	q, ch, l := f.snapshot()
	status, e := m.LookupSSHKey(context.Background(), q.Source, q.Key)
	if e != nil || status.State != "admitted" || status.Execution == nil || status.Execution.Lease.ID != l.ID || status.Fence == nil || status.Fence.Control == nil {
		t.Fatal("missing exact admitted outcome", status, e)
	}
	c := committedControl(m, q)
	repeated, p, e := m.AcquireSSHCommitted(context.Background(), q, sshservice.Execution{Kind: q.Kind, Command: command}, ch, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute), c)
	if e != nil || p != nil || repeated.ID != l.ID {
		t.Fatal("duplicate key replayed process", p, e)
	}
	if _, p, e = m.AcquireSSH(context.Background(), q, sshservice.Execution{Kind: q.Kind, Command: command}, ch, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute)); e != nil || p != nil {
		t.Fatal("legacy inspection changed committed outcome", e)
	}
}
func TestSSHCommittedSealBlocksActualDelayedExec(t *testing.T) {
	m := newManager(t)
	f := committedFixture(t, m)
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	command := "printf forbidden > " + sshTestQuote(marker)
	q := f.q
	q.Kind = "exec"
	q.CommandSHA256 = sshDigest([]byte(command))
	c := committedControl(m, q)
	if _, e := m.SealSSHKey(context.Background(), c, "Prevent this delayed exec before admission"); e != nil {
		t.Fatal(e)
	}
	session, e := f.client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if out, e := session.CombinedOutput(command); e == nil {
		t.Fatal("sealed key executed", string(out))
	}
	if _, e = os.Lstat(marker); !os.IsNotExist(e) {
		t.Fatal("sealed execution produced a side effect", e)
	}
	if leases, ops := fenceRows(t, m, c); leases != 0 || ops != 0 {
		t.Fatal("sealed exec created a lease", leases, ops)
	}
	status, e := m.LookupSSHKey(context.Background(), c.Source, c.Key)
	if e != nil || status.State != "sealed" {
		t.Fatal("delayed exec changed seal", status, e)
	}
}
func TestSSHUncommittedAdmissionCannotBypassEstablishedFences(t *testing.T) {
	m := newManager(t)
	c := fenceControl(m, "unrelated-seal")
	if _, e := m.SealSSHKey(context.Background(), c, "establish fenced key namespace"); e != nil {
		t.Fatal(e)
	}
	f := newSSHManagerFixture(t, m, time.Minute)
	marker := filepath.Join(t.TempDir(), "bypass")
	session, e := f.client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = session.CombinedOutput("printf bypass > " + sshTestQuote(marker)); e == nil {
		t.Fatal("uncommitted adapter admitted into fenced namespace")
	}
	if _, e = os.Lstat(marker); !os.IsNotExist(e) {
		t.Fatal("uncommitted path launched process", e)
	}
}
