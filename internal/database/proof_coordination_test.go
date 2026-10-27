package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
)

func coordJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func coordPreview(i mesh.Identity) proofIdentityPreview {
	return proofIdentityPreview{Host: i.Host, Name: i.Name, Owner: i.Owner, Username: i.OwnerUsername, Address: i.Address, Fingerprint: i.Fingerprint, SPKI: i.SPKI, Expires: i.Expires}
}
func coordName(t *testing.T, key string) string {
	t.Helper()
	path, err := mesh.LifecycleOperationPath("", key)
	if err != nil {
		t.Fatal(err)
	}
	return "coordinator-" + filepath.Base(path)
}
func coordHistoryFixture(t *testing.T, kind string) (mesh.Coordination, map[string]mesh.LifecycleOperation) {
	t.Helper()
	actor := local.CurrentIdentity()
	now := time.Now().UTC()
	dirs := []string{t.TempDir(), t.TempDir()}
	ids := []mesh.Identity{}
	for i, home := range dirs {
		canonical, err := filepath.EvalSymlinks(home)
		if err != nil {
			t.Fatal(err)
		}
		dirs[i] = canonical
		if err = os.Chmod(canonical, 0700); err != nil {
			t.Fatal(err)
		}
		host := local.ID()
		name, address := "source", "127.0.0.1:32101"
		if i == 1 {
			name, address = "target", "127.0.0.1:32102"
		}
		store, err := mesh.Init(canonical, host, name, address, address)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, store.Config().Identity)
		store.Close()
	}
	pair := strings.Repeat("c", 64)
	node := func(index int) proofNodePreview {
		preview := coordPreview(ids[index])
		return proofNodePreview{Running: true, Host: ids[index].Host, Home: dirs[index], OS: runtime.GOOS, Arch: runtime.GOARCH, Version: "test", Owner: actor, Startup: mesh.DefaultStartup(), StartupSource: "saved", Identity: &preview, PrivateIPs: []string{"127.0.0.1"}}
	}
	q := meshops.StartRequest{Key: "original", Kind: kind}
	var review, result any
	life := mesh.LifecycleOperation{Actor: actor, Key: q.Key, Kind: kind, At: now, Updated: now, LocalHost: ids[0].Host, RemoteHost: ids[1].Host, SSHLocator: "fixture", SSHAccount: "fixture", SSHHostKey: "SHA256:fixture", RemoteHome: dirs[1], Binary: "arm", Pair: pair, LocalIdentity: &ids[0], RemoteIdentity: &ids[1], LocalRevision: 3, RemoteRevision: 3, Reason: "private archive test"}
	digest := ""
	if kind == "connect" {
		q.Connect = &meshops.ConnectRequest{Destination: "fixture", RemoteHome: dirs[1], Binary: "arm", Reason: life.Reason, Forward: mesh.Grant{Permissions: []string{"observe-own"}, MaxTTL: time.Hour}, Reverse: mesh.Grant{Permissions: []string{}, MaxTTL: time.Hour}}
		digest, _ = mesh.ReviewDigest(*q.Connect)
		review = proofCoordConnectReview{Operation: "connect-direct-peers", Key: q.Key, Request: *q.Connect, SSH: meshssh.Identity{Account: life.SSHAccount, HostKey: life.SSHHostKey}, Local: node(0), Remote: node(1), LocalPrepared: &proofPreparedPreview{Identity: coordPreview(ids[0]), Listen: ids[0].Address, Expires: now.Add(time.Minute)}, RemotePrepared: &proofPreparedPreview{Identity: coordPreview(ids[1]), Listen: ids[1].Address, Expires: now.Add(time.Minute)}, Warning: "New trust requires exact approval"}
		result = proofCoordConnectResult{Host: ids[1].Host, Name: ids[1].Name, Key: q.Key, Pair: pair, Local: ids[0], Remote: ids[1], Forward: q.Connect.Forward, Reverse: q.Connect.Reverse}
		life.Phase = "connected"
	} else {
		q.Disconnect = &meshops.DisconnectDraft{Host: ids[1].Host, Pair: pair, Reason: life.Reason}
		digest, _ = mesh.ReviewDigest(struct{ Host, Pair, Reason string }{ids[1].Host, pair, life.Reason})
		review = proofCoordDisconnectReview{Operation: "disconnect-direct-peer", Key: q.Key, Host: ids[1], Pair: pair, LocalRevision: life.LocalRevision, RemoteRevision: life.RemoteRevision, SSHAccount: life.SSHAccount, SSHHostKey: life.SSHHostKey, Effect: "revoke exact enrollment; leases retain expiry", Reason: life.Reason}
		result = proofCoordDisconnectResult{Host: ids[1].Host, Pending: false, Key: q.Key}
		life.Phase = "disconnected"
	}
	life.Digest = digest
	life.Transitions = []mesh.LifecycleTransition{{Phase: life.Phase, At: now}}
	rawReview := coordJSON(t, review)
	hash, _ := mesh.ReviewDigest(review)
	op := mesh.Coordination{Schema: 1, Key: q.Key, Kind: kind, Digest: digest, Request: coordJSON(t, q), Actor: actor, Targets: []string{"host:" + ids[1].Host}, Revision: 9, Phase: "completed", Updated: now, Review: rawReview, ReviewRevision: 5, ReviewHash: hash, Approved: true, Result: coordJSON(t, result)}
	return op, map[string]mesh.LifecycleOperation{q.Key: life}
}
func proveCoordFixture(t *testing.T, ops []mesh.Coordination, lifecycle map[string]mesh.LifecycleOperation) ([]ProofBlocker, error) {
	t.Helper()
	raws := map[string][]byte{}
	for _, op := range ops {
		raws[coordName(t, op.Key)] = coordJSON(t, op)
	}
	host := ""
	for _, life := range lifecycle {
		host = life.LocalHost
		break
	}
	return proveResetCoordinations(raws, local.CurrentIdentity().UID, host, lifecycle)
}
func TestStrictCoordinatorArchivalAcceptsExactCompletedShapes(t *testing.T) {
	for _, kind := range []string{"connect", "disconnect"} {
		t.Run(kind, func(t *testing.T) {
			op, life := coordHistoryFixture(t, kind)
			blockers, err := proveCoordFixture(t, []mesh.Coordination{op}, life)
			if err != nil || len(blockers) != 0 {
				t.Fatalf("%+v %v", blockers, err)
			}
		})
	}
}
func editCoordRaw(t *testing.T, raw json.RawMessage, change func(map[string]any)) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	change(value)
	return coordJSON(t, value)
}
func TestStrictCoordinatorArchivalRejectsAliasesNullAndMissingInnerFields(t *testing.T) {
	for _, kind := range []string{"connect", "disconnect"} {
		for _, field := range []string{"request-alias", "request-null", "review-alias", "review-null", "result-alias", "result-null", "result-missing", "nested-alias"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				op, life := coordHistoryFixture(t, kind)
				switch field {
				case "request-alias":
					op.Request = editCoordRaw(t, op.Request, func(v map[string]any) { v["Key"] = "hidden" })
				case "request-null":
					op.Request = editCoordRaw(t, op.Request, func(v map[string]any) { v["kind"] = nil })
				case "review-alias":
					op.Review = editCoordRaw(t, op.Review, func(v map[string]any) { v["Operation"] = "hidden" })
				case "review-null":
					op.Review = editCoordRaw(t, op.Review, func(v map[string]any) { v["operation"] = nil })
				case "result-alias":
					op.Result = editCoordRaw(t, op.Result, func(v map[string]any) { v["Key"] = "hidden" })
				case "result-null":
					op.Result = editCoordRaw(t, op.Result, func(v map[string]any) {
						if kind == "connect" {
							v["reused"] = nil
						} else {
							v["remote_pending"] = nil
						}
					})
				case "result-missing":
					op.Result = editCoordRaw(t, op.Result, func(v map[string]any) {
						if kind == "connect" {
							delete(v, "pair")
						} else {
							delete(v, "remote_pending")
						}
					})
				case "nested-alias":
					op.Review = editCoordRaw(t, op.Review, func(v map[string]any) {
						if kind == "connect" {
							v["request"].(map[string]any)["source_permissions_on_target"].(map[string]any)["Operator_inbox"] = false
						} else {
							v["host"].(map[string]any)["Host"] = local.ID()
						}
					})
				}
				// Re-hash changed reviews deliberately: self-consistent hashes must NOT
				// substitute for strict known canonical evidence validation.
				compact, err := compactProofJSON(op.Review)
				if err != nil {
					t.Fatal(err)
				}
				var value any
				if err = json.Unmarshal([]byte(compact), &value); err != nil {
					t.Fatal(err)
				}
				// Marshal map ordering is exactly this saved altered raw ordering.
				op.ReviewHash = hashCoordBytes([]byte(compact))
				if blockers, err := proveCoordFixture(t, []mesh.Coordination{op}, life); err == nil && len(blockers) == 0 {
					t.Fatal("malformed inner evidence provided settlement")
				}
			})
		}
	}
}
func hashCoordBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func TestStrictCoordinatorArchivalKeepsWorkersAndHandoffsBlocked(t *testing.T) {
	for _, phase := range []string{"preparing", "awaiting_review", "review_approved", "applying", "incomplete", "handed_off", "future"} {
		t.Run(phase, func(t *testing.T) {
			op, life := coordHistoryFixture(t, "connect")
			op.Phase = phase
			op.Result = nil
			blockers, err := proveCoordFixture(t, []mesh.Coordination{op}, life)
			if err != nil || len(blockers) != 1 {
				t.Fatalf("%+v %v", blockers, err)
			}
		})
	}
	op, life := coordHistoryFixture(t, "disconnect")
	op.Worker = strings.Repeat("a", 64)
	op.WorkerExpires = time.Now().Add(-time.Hour)
	blockers, err := proveCoordFixture(t, []mesh.Coordination{op}, life)
	if err != nil || len(blockers) != 1 {
		t.Fatalf("expired worker %+v %v", blockers, err)
	}
}
func TestStrictCoordinatorArchivalCompleteFamilyResolution(t *testing.T) {
	root, life := coordHistoryFixture(t, "connect")
	root.Phase = "incomplete"
	root.Result = nil
	root.Error = &model.Error{Code: "unknown", Message: "retained original uncertainty"}
	var original proofCoordConnectReview
	if err := strictEvidence(string(root.Review), &original); err != nil {
		t.Fatal(err)
	}
	result := proofCoordConnectResult{Host: life[root.Key].RemoteHost, Name: life[root.Key].RemoteIdentity.Name, Key: root.Key, Pair: life[root.Key].Pair, Local: *life[root.Key].LocalIdentity, Remote: *life[root.Key].RemoteIdentity, Forward: original.Request.Forward, Reverse: original.Request.Reverse, Reused: true}
	review := proofCoordVerifyReview{Operation: "verify-existing-connection", Original: root.Key, Pair: result.Pair, LocalListen: original.LocalPrepared.Listen, RemoteListen: original.RemotePrepared.Listen, LocalRevision: 3, RemoteRevision: 3, Forward: result.Forward, Reverse: result.Reverse, Local: original.Local, Remote: original.Remote, Effect: "prove already enabled exact enrollment only"}
	review.Local.Prior = &proofPriorTrust{Identity: coordPreview(result.Remote), Pair: result.Pair, Revision: 3, State: "active", Grant: result.Reverse}
	review.Remote.Prior = &proofPriorTrust{Identity: coordPreview(result.Local), Pair: result.Pair, Revision: 3, State: "active", Grant: result.Forward}
	child := root
	child.Key = "attempt"
	child.ParentKey = root.Key
	child.ResumeFrom = root.Key
	child.ResolvedBy = ""
	child.Phase = "completed"
	child.Error = nil
	child.Review = coordJSON(t, review)
	child.ReviewHash, _ = mesh.ReviewDigest(review)
	child.Result = coordJSON(t, result)
	root.ResolvedBy = child.Key
	if blockers, err := proveCoordFixture(t, []mesh.Coordination{root, child}, life); err != nil || len(blockers) != 0 {
		t.Fatalf("valid family %+v %v", blockers, err)
	}
	for _, kind := range []string{"missing-child", "child-unapproved", "cycle", "unresolved-sibling", "different-request"} {
		t.Run(kind, func(t *testing.T) {
			parent, attempt := root, child
			ops := []mesh.Coordination{parent, attempt}
			switch kind {
			case "missing-child":
				ops = ops[:1]
			case "child-unapproved":
				ops[1].Approved = false
			case "cycle":
				ops[1].ResumeFrom = ops[1].Key
			case "unresolved-sibling":
				sibling := root
				sibling.Key = "unresolved"
				sibling.ParentKey = root.Key
				sibling.ResumeFrom = root.Key
				sibling.ResolvedBy = ""
				ops = append(ops, sibling)
			case "different-request":
				ops[1].Request = editCoordRaw(t, ops[1].Request, func(v map[string]any) { v["connect"].(map[string]any)["reason"] = "different" })
			}
			if blockers, err := proveCoordFixture(t, ops, life); err == nil && len(blockers) == 0 {
				t.Fatal("incomplete/corrupt family settled")
			}
		})
	}
}
