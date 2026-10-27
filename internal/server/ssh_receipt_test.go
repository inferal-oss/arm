package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

func TestPublicSSHReceiptOmitsUnboundedDiagnosticsPreservesExactAdmission(t *testing.T) {
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	pub, e := ssh.NewPublicKey(private.Public())
	if e != nil {
		t.Fatal(e)
	}
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	source, target := strings.Repeat("a", 32), strings.Repeat("b", 32)
	now := time.Now().UTC()
	owner := fmt.Sprint(os.Geteuid())
	dir := "/tmp/arm-ssh-" + strings.Repeat("e", 32)
	if runtime.GOOS == "darwin" {
		dir = "/private" + dir
	}
	q := model.SSHExecutionRequest{Source: source, Target: target, Pair: strings.Repeat("c", 64), Revision: 1, Certificate: strings.Repeat("d", 64), Account: "account", Key: "maximum-receipt", PublicKeySHA256: sshPublicHash(pub), Reason: strings.Repeat("r", 4096), TTL: time.Minute, Resources: model.Resources{CPU: 1, Memory: 9223372036854775807}, CWD: "/" + strings.Repeat("x", 4095), Kind: "exec", CommandSHA256: strings.Repeat("f", 64)}
	intent := sshservice.SourceIntent{Schema: 1, Source: source, Owner: owner, Request: sshservice.SourceRequest{Key: q.Key, Pin: sshservice.Pin{Schema: 1, Source: source, Target: target, Pair: q.Pair, Revision: 1, TLSFingerprint: strings.Repeat("1", 64), TLSSPKI: strings.Repeat("2", 64), Account: q.Account, PublicKey: key, At: now}, TargetOwner: owner, SourceCertificate: q.Certificate, PublicKey: key, Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, CWD: q.CWD, Kind: q.Kind, CommandSHA256: q.CommandSHA256}, Worker: model.ProcessIdentity{PID: 1, PGID: 1, UID: owner, Start: "test:start", Namespace: "test:namespace"}, Directory: sshservice.InvocationDirectory{Path: dir, Device: 1, Inode: 1, Owner: owner}, Created: now}
	ended := now.Add(time.Second)
	l := model.Lease{ID: strings.Repeat("3", 32), Host: target, Owner: owner, OwnerUsername: q.Account, State: "terminal", Outcome: "completed", Created: now, Ended: &ended, Request: model.Request{Host: target, Peer: source, OriginKey: q.Key, Key: mesh.Key(source, "ssh-exec\x00"+q.Key), Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, PeerCertificate: q.Certificate, PeerGrantRevision: 1}, Usage: &model.WorkloadUsage{Members: make([]model.ProcessUsage, 4096), MemberCount: 4096, Detail: strings.Repeat("large diagnostic ", 4096)}}
	op := model.SSHOperation{Schema: 1, Request: q, Lease: l.ID, AdmittedAt: now}
	input := model.SSHKeyStatus{Schema: 1, Source: source, Target: target, Key: q.Key, State: "admitted", Execution: &model.SSHLookup{Found: true, Operation: &op, Lease: &l}}
	output, e := compactSSHKeyStatus(input)
	if e != nil {
		t.Fatal(e)
	}
	if output.Execution.Lease.Usage != nil || input.Execution.Lease.Usage == nil {
		t.Fatal("projection retained diagnostics or changed original")
	}
	completion := sshservice.SourceCompletion{Schema: 1, Intent: intent, Receipt: *output.Execution, At: ended}
	if e = completion.Validate(); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(completion)
	if e != nil || len(raw) > sshservice.MaxIntentBytes {
		t.Fatalf("maximum admission cannot settle within source bound: %d %v", len(raw), e)
	}
	for _, change := range []string{"cargo", "storage", "pool", "custom-budget"} {
		copy := l
		switch change {
		case "cargo":
			copy.Cargo = &model.CargoBinding{}
		case "storage":
			copy.Request.StorageLease = "attached"
		case "pool":
			copy.Request.Pools = []model.PoolClaim{{Pool: "attached", Quantity: 1}}
		case "custom-budget":
			copy.Request.Budget = "unreviewed"
		}
		lookup := *input.Execution
		lookup.Lease = &copy
		bad := input
		bad.Execution = &lookup
		if _, e = compactSSHKeyStatus(bad); e == nil {
			t.Fatal("unsupported attached obligation projected away", change)
		}
	}
}
