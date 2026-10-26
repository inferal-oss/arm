package database_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/usage"
	"golang.org/x/crypto/ssh"
)

func sshProofState(t *testing.T) (string, sshservice.Binding, sshservice.Pin) {
	t.Helper()
	return sshProofStateInHome(t, proofFixture(t, false))
}

func sshProofStateInHome(t *testing.T, home string) (string, sshservice.Binding, sshservice.Pin) {
	t.Helper()
	p, e := prove(t, home)
	if e != nil {
		t.Fatal(e)
	}
	native := local.CurrentIdentity()
	b := sshservice.Binding{Host: p.Host, Owner: native.UID, Account: native.Username}
	store, e := mesh.Init(home, b.Host, "ssh-proof-source", "127.0.0.1:23123", "127.0.0.1:23123")
	if e != nil {
		t.Fatal(e)
	}
	store.Close()
	r, e := sshservice.ReviewService(context.Background(), home, b, true, "explicit private SSH fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.ApplyService(context.Background(), home, b, r); e != nil {
		t.Fatal(e)
	}
	s, e := sshservice.ReadState(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	pin := sshservice.Pin{Schema: 1, Source: b.Host, Target: strings.Repeat("b", 32), Pair: strings.Repeat("c", 64), Revision: 2, TLSFingerprint: strings.Repeat("d", 64), TLSSPKI: strings.Repeat("e", 64), Account: "destination", PublicKey: s.PublicKey, At: time.Now().UTC()}
	pr, e := sshservice.ReviewPin(filepath.Join(home, "mesh"), pin)
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.CommitStatePin(context.Background(), home, b, pr); e != nil {
		t.Fatal(e)
	}
	return home, b, pin
}
func proofSSHHash(v []byte) string { s := sha256.Sum256(v); return hex.EncodeToString(s[:]) }
func sshProofIntent(t *testing.T, home string, b sshservice.Binding, pin sshservice.Pin) sshservice.SourceIntent {
	t.Helper()
	dir, e := sshservice.CreateInvocationDirectory(context.Background(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Remove(dir.Path) })
	worker, e := usage.Capture(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	key, e := sshservice.LoadHostKey(filepath.Join(home, "mesh"), b.Host)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(home, "mesh", "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	var config mesh.Config
	if e = json.Unmarshal(raw, &config); e != nil {
		t.Fatal(e)
	}
	r := sshservice.SourceIntent{Schema: 1, Source: b.Host, Owner: b.Owner, Request: sshservice.SourceRequest{Key: "proof-private-ssh", Pin: pin, TargetOwner: "1000", SourceCertificate: config.Identity.Fingerprint, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey()))), Reason: "private native intent", TTL: time.Minute, Resources: model.Resources{CPU: 1, Memory: 9223372036854775807}, CWD: "/tmp", Kind: "exec", CommandSHA256: proofSSHHash([]byte("private unpersisted command"))}, Worker: worker, Directory: dir, Created: time.Now().UTC()}
	if e = sshservice.ReserveSource(context.Background(), home, b, r); e != nil {
		t.Fatal(e)
	}
	return r
}
func proofSSHReceipt(r sshservice.SourceIntent) model.SSHLookup {
	now := time.Now().UTC()
	q := r.Request
	k, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(q.PublicKey))
	request := model.SSHExecutionRequest{Source: r.Source, Target: q.Pin.Target, Pair: q.Pin.Pair, Revision: q.Pin.Revision, Certificate: q.SourceCertificate, Account: q.Pin.Account, Key: q.Key, PublicKeySHA256: proofSSHHash(k.Marshal()), Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, CWD: q.CWD, Kind: q.Kind, CommandSHA256: q.CommandSHA256}
	l := model.Lease{ID: strings.Repeat("9", 32), Host: q.Pin.Target, Owner: q.TargetOwner, OwnerUsername: q.Pin.Account, State: "terminal", Outcome: "completed", Created: r.Created, Ended: &now, Request: model.Request{Host: request.Target, Peer: r.Source, OriginKey: q.Key, Key: mesh.Key(r.Source, "ssh-exec\x00"+q.Key), Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, PeerCertificate: q.SourceCertificate, PeerGrantRevision: q.Pin.Revision}}
	return model.SSHLookup{Found: true, Operation: &model.SSHOperation{Schema: 1, Request: request, Lease: l.ID, AdmittedAt: r.Created}, Lease: &l}
}
func TestSSHNamespaceRetirementAndPendingProofAreReadOnly(t *testing.T) {
	home, b, pin := sshProofState(t)
	before := snapshotTree(t, home)
	r, e := review(t, home)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Proof.Settled || r.Mesh == nil || !r.Mesh.Required {
		t.Fatal("SSH identities not included in retirement")
	}
	r.Close()
	if !reflect.DeepEqual(before, snapshotTree(t, home)) {
		t.Fatal("SSH namespace proof wrote state")
	}
	intent := sshProofIntent(t, home, b, pin)
	r, e = review(t, home)
	if e != nil {
		t.Fatal(e)
	}
	if r.Proof.Settled {
		t.Fatal("SSH pending operation declared safe")
	}
	r.Close()
	if e = os.Remove(intent.Directory.Path); e != nil {
		t.Fatal(e)
	}
	receipt := proofSSHReceipt(intent)
	if e = sshservice.SettleSource(context.Background(), home, b, intent, receipt); e != nil {
		t.Fatal(e)
	}
	r, e = review(t, home)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Proof.Settled || r.Mesh == nil || !r.Mesh.Required {
		t.Fatal("verified terminal source history cannot be retired")
	}
	r.Close()
	before = snapshotTree(t, home)
	reset, e := database.ReviewReset(context.Background(), home, "preserve SSH identity retirement", false)
	if e != nil {
		t.Fatal(e)
	}
	defer reset.Close()
	if _, e = reset.Apply(context.Background()); e == nil {
		t.Fatal("SSH mesh reset bypassed explicit retirement")
	}
	if !reflect.DeepEqual(before, snapshotTree(t, home)) {
		t.Fatal("declined retirement changed namespace")
	}
}
func TestSSHNamespaceStrictMirrorCannotHideUnknown(t *testing.T) {
	for _, kind := range []string{"case-boolean", "null-boolean", "nested-alias", "wrong-owner", "wrong-authority", "unknown-file", "bad-lock", "key-mismatch", "executable-file", "special-file", "special-directory"} {
		t.Run(kind, func(t *testing.T) {
			home, b, pin := sshProofState(t)
			intent := sshProofIntent(t, home, b, pin)
			dir := filepath.Join(home, "mesh")
			path := filepath.Join(dir, sshservice.ServiceFile)
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "case-boolean":
				raw = bytes.Replace(raw, []byte(`"enabled":true`), []byte(`"Enabled":false,"enabled":true`), 1)
			case "null-boolean":
				raw = bytes.Replace(raw, []byte(`"enabled":true`), []byte(`"enabled":null`), 1)
			case "nested-alias":
				raw = bytes.Replace(raw, []byte(`"account":`), []byte(`"Account":`), 1)
			case "wrong-owner":
				raw = bytes.Replace(raw, []byte(`"owner":"`+b.Owner+`"`), []byte(`"owner":"999999"`), 1)
			case "wrong-authority":
				raw = bytes.Replace(raw, []byte(b.Host), []byte(strings.Repeat("f", 32)), 1)
			case "unknown-file":
				os.WriteFile(filepath.Join(dir, ".ssh-write-interrupted"), []byte("preserve"), 0600)
			case "bad-lock":
				os.WriteFile(filepath.Join(dir, sshservice.StateLockName), []byte("unsafe"), 0600)
			case "key-mismatch":
				raw = bytes.Replace(raw, []byte(`"public_key":"`), []byte(`"public_key":"changed`), 1)
			case "executable-file":
				os.Chmod(path, 0700)
			case "special-file":
				// Set after final write: Linux clears setuid on file writes.
			case "special-directory":
				os.Chmod(dir, 0700|os.ModeSticky)
			}
			if e = os.WriteFile(path, raw, 0600); e != nil {
				t.Fatal(e)
			}
			if kind == "special-file" {
				if e = os.Chmod(path, 0600|os.ModeSetuid); e != nil {
					t.Fatal(e)
				}
			}
			r, e := review(t, home)
			if e == nil {
				r.Close()
				t.Fatal("unsupported/corrupt SSH record accepted", kind, intent.Request.Key)
			}
		})
	}
}
func TestSSHCompletionProofRejectsForgedTerminalAliases(t *testing.T) {
	home, b, pin := sshProofState(t)
	r := sshProofIntent(t, home, b, pin)
	if e := os.Remove(r.Directory.Path); e != nil {
		t.Fatal(e)
	}
	receipt := proofSSHReceipt(r)
	if e := sshservice.SettleSource(context.Background(), home, b, r, receipt); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(filepath.Join(home, "mesh"))
	if e != nil {
		t.Fatal(e)
	}
	var path string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "ssh-complete-") {
			path = filepath.Join(home, "mesh", entry.Name())
		}
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	bad := bytes.Replace(raw, []byte(`"state":"terminal"`), []byte(`"state":"active","State":"terminal"`), 1)
	if bytes.Equal(bad, raw) {
		t.Fatal("receipt fixture missing state")
	}
	if e = os.WriteFile(path, bad, 0600); e != nil {
		t.Fatal(e)
	}
	proof, e := review(t, home)
	if e == nil {
		proof.Close()
		t.Fatal("aliased active completion counted settled")
	}
}

func TestSSHSettledProofSeparatesAuthorityClocks(t *testing.T) {
	for _, shift := range []time.Duration{-5 * time.Minute, 5 * time.Minute} {
		t.Run(shift.String(), func(t *testing.T) {
			home, b, pin := sshProofState(t)
			intent := sshProofIntent(t, home, b, pin)
			receipt := proofSSHReceipt(intent)
			receipt.Lease.Created = receipt.Lease.Created.Add(shift)
			receipt.Operation.AdmittedAt = receipt.Operation.AdmittedAt.Add(shift)
			end := receipt.Lease.Ended.Add(shift)
			receipt.Lease.Ended = &end
			wrong := receipt
			op := *receipt.Operation
			wrong.Operation = &op
			op.Request.Key = "mismatched-original-key"
			if e := sshservice.MatchTerminalReceipt(intent, wrong); e == nil {
				t.Fatal("mismatched request accepted under skew")
			}
			if e := os.Remove(intent.Directory.Path); e != nil {
				t.Fatal(e)
			}
			if e := sshservice.SettleSource(context.Background(), home, b, intent, receipt); e != nil {
				t.Fatal(e)
			}
			r, e := review(t, home)
			if e != nil {
				t.Fatal("strict proof rejected valid skewed completion", e)
			}
			defer r.Close()
			if !r.Proof.Settled || r.Mesh == nil || !r.Mesh.Required {
				t.Fatal("skewed terminal receipt not eligible for explicit retirement")
			}
		})
	}
}

func TestSSHCompletionProofNestedUnknownFieldsStayClosed(t *testing.T) {
	for _, field := range []string{"receipt", "operation", "execution-request", "lease", "lease-request", "worker", "source-resources", "destination-resources", "lease-resources"} {
		t.Run(field, func(t *testing.T) {
			home, b, pin := sshProofState(t)
			intent := sshProofIntent(t, home, b, pin)
			if e := os.Remove(intent.Directory.Path); e != nil {
				t.Fatal(e)
			}
			if e := sshservice.SettleSource(context.Background(), home, b, intent, proofSSHReceipt(intent)); e != nil {
				t.Fatal(e)
			}
			entries, e := os.ReadDir(filepath.Join(home, "mesh"))
			if e != nil {
				t.Fatal(e)
			}
			path := ""
			for _, v := range entries {
				if strings.HasPrefix(v.Name(), "ssh-complete-") {
					path = filepath.Join(home, "mesh", v.Name())
				}
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			var data map[string]any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if e = decoder.Decode(&data); e != nil {
				t.Fatal(e)
			}
			src := data["intent"].(map[string]any)
			srcq := src["request"].(map[string]any)
			receipt := data["receipt"].(map[string]any)
			op := receipt["operation"].(map[string]any)
			q := op["request"].(map[string]any)
			lease := receipt["lease"].(map[string]any)
			lq := lease["request"].(map[string]any)
			objects := map[string]map[string]any{"receipt": receipt, "operation": op, "execution-request": q, "lease": lease, "lease-request": lq, "worker": src["worker"].(map[string]any), "source-resources": srcq["resources"].(map[string]any), "destination-resources": q["resources"].(map[string]any), "lease-resources": lq["resources"].(map[string]any)}
			objects[field]["future_obligation"] = true
			altered, e := json.Marshal(data)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(path, altered, 0600); e != nil {
				t.Fatal(e)
			}
			review, e := review(t, home)
			if e == nil {
				review.Close()
				t.Fatal("unknown nested producer field accepted", field)
			}
		})
	}
}
