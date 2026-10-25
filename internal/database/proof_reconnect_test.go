package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
)

func reconnectHistoryFixture(t *testing.T) (mesh.Coordination, map[string]mesh.LifecycleOperation) {
	t.Helper()
	op, history := coordHistoryFixture(t, "connect")
	life := history[op.Key]
	var oldRequest meshops.StartRequest
	if e := json.Unmarshal(op.Request, &oldRequest); e != nil {
		t.Fatal(e)
	}
	var review proofCoordConnectReview
	var result proofCoordConnectResult
	if e := json.Unmarshal(op.Review, &review); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(op.Result, &result); e != nil {
		t.Fatal(e)
	}
	oldLife := life
	oldLife.Key = "previous-connection"
	oldLife.Pair = strings.Repeat("b", 64)
	prior := mesh.Peer{Identity: *oldLife.RemoteIdentity, Grant: mesh.Grant{Permissions: []string{"observe-host", "message"}}, Pair: oldLife.Pair, State: "revoked", Revision: 4, Reason: "prior revocation"}
	r := &meshops.ReconnectRequest{Draft: meshops.ReconnectDraft{Host: prior.Identity.Host, Reason: oldRequest.Connect.Reason, Forward: oldRequest.Connect.Forward, Reverse: oldRequest.Connect.Reverse}, Binding: meshops.ReconnectBinding{Local: *life.LocalIdentity, Prior: prior, Connection: &oldLife, Selected: mesh.Connector{ID: "reconnect", Destination: oldLife.SSHLocator, Binary: oldLife.Binary, Home: oldLife.RemoteHome, Reason: oldRequest.Connect.Reason}}}
	request := meshops.StartRequest{Key: op.Key, Kind: "reconnect", Reconnect: r}
	current := proofConnectRequest(request)
	op.Kind = "reconnect"
	op.Request = coordJSON(t, request)
	op.Digest, _ = mesh.ReviewDigest(*r)
	review.Operation = "reconnect-direct-peers"
	review.Request = *current
	review.Local.Prior = &proofPriorTrust{Identity: coordPreview(*oldLife.RemoteIdentity), Pair: oldLife.Pair, Revision: prior.Revision, State: "revoked", Grant: prior.Grant}
	review.Remote.Prior = &proofPriorTrust{Identity: coordPreview(*oldLife.LocalIdentity), Pair: oldLife.Pair, Revision: 4, State: "revoked", Grant: prior.Grant}
	op.Review = coordJSON(t, review)
	op.ReviewHash, _ = mesh.ReviewDigest(review)
	sum := sha256.Sum256([]byte("arm-connect:" + op.Key + ":" + life.LocalHost + ":" + life.RemoteHost))
	result.Pair = hex.EncodeToString(sum[:])
	op.Result = coordJSON(t, result)
	life.Kind = "reconnect"
	life.Digest = op.Digest
	life.Pair = result.Pair
	return op, map[string]mesh.LifecycleOperation{life.Key: life, oldLife.Key: oldLife}
}
func TestResetReconnectStrictHistoryAndCorruptionRefusal(t *testing.T) {
	original, history := reconnectHistoryFixture(t)
	if blockers, e := proveCoordFixture(t, []mesh.Coordination{original}, history); e != nil || len(blockers) != 0 {
		t.Fatal("genuine reconnect history refused", blockers, e)
	}
	for _, kind := range []string{"old-identity", "old-pair", "selected-home", "unreviewed-grant-change", "missing-prior", "wrong-kind", "wrong-new-pair"} {
		t.Run(kind, func(t *testing.T) {
			op := original
			copyHistory := map[string]mesh.LifecycleOperation{}
			for k, v := range history {
				copyHistory[k] = v
			}
			var q meshops.StartRequest
			if e := json.Unmarshal(op.Request, &q); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "old-identity":
				q.Reconnect.Binding.Prior.Identity.Fingerprint = strings.Repeat("f", 64)
			case "old-pair":
				q.Reconnect.Binding.Prior.Pair = strings.Repeat("e", 64)
			case "selected-home":
				q.Reconnect.Binding.Selected.Home += "-other"
			case "unreviewed-grant-change":
				q.Reconnect.Draft.Forward = q.Reconnect.Binding.Prior.Grant
			case "missing-prior":
				delete(copyHistory, q.Reconnect.Binding.Connection.Key)
			case "wrong-kind":
				op.Kind = "connect"
			case "wrong-new-pair":
				var result proofCoordConnectResult
				json.Unmarshal(op.Result, &result)
				result.Pair = strings.Repeat("e", 64)
				op.Result = coordJSON(t, result)
				life := copyHistory[op.Key]
				life.Pair = result.Pair
				copyHistory[op.Key] = life
			}
			op.Request = coordJSON(t, q)
			op.Digest, _ = mesh.ReviewDigest(*q.Reconnect)
			if blockers, e := proveCoordFixture(t, []mesh.Coordination{op}, copyHistory); e == nil {
				t.Fatal("corrupt reconnect evidence accepted", kind, blockers)
			}
		})
	}
}

func TestResetReconnectIndependentExplicitIPValidation(t *testing.T) {
	op, _ := reconnectHistoryFixture(t)
	var q meshops.StartRequest
	if e := json.Unmarshal(op.Request, &q); e != nil {
		t.Fatal(e)
	}
	for _, address := range []string{"not-an-ip", "127.0.0.1:9000", "[::1]", "fe80::1%en0", strings.Repeat("1", 65), " 127.0.0.1"} {
		for _, side := range []string{"local", "remote"} {
			r := *q.Reconnect
			if side == "local" {
				r.Draft.LocalIP = address
			} else {
				r.Draft.RemoteIP = address
			}
			if e := validateProofReconnect(r, r.Binding.Local.Owner, r.Binding.Local.Host); e == nil {
				t.Fatal("independent reader accepted malformed explicit address", side, address)
			}
		}
	}
	for _, address := range []string{"", "127.0.0.1", "::1", "2001:db8::1234", "::ffff:192.0.2.1"} {
		r := *q.Reconnect
		r.Draft.LocalIP = address
		r.Draft.RemoteIP = address
		if e := validateProofReconnect(r, r.Binding.Local.Owner, r.Binding.Local.Host); e != nil {
			t.Fatal("valid saved address refused", address, e)
		}
	}
}
