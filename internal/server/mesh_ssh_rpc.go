package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
)

func decodeSSHFields(raw []byte, out any, keys ...string) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return model.Err("invalid_request", "one closed SSH parameter object required")
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	seen := map[string]bool{}
	for d.More() {
		token, e = d.Token()
		name, ok := token.(string)
		if e != nil || !ok || !allowed[name] || seen[name] {
			return model.Err("invalid_request", "unknown, alias or duplicate SSH field")
		}
		seen[name] = true
		var value json.RawMessage
		if e = d.Decode(&value); e != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return model.Err("invalid_request", "nonnull SSH field required")
		}
	}
	if _, e = d.Token(); e != nil {
		return e
	}
	if _, e = d.Token(); e != io.EOF {
		return model.Err("invalid_request", "trailing SSH parameters")
	}
	if len(seen) != len(allowed) {
		return model.Err("invalid_request", "required SSH fields missing")
	}
	return mesh.Decode(raw, out)
}
func (m *meshService) dispatchSSH(ctx context.Context, peer mesh.Peer, request mesh.Request) (any, error) {
	if request.Expected == nil {
		return nil, model.Err("invalid_request", "exact destination enrollment required for SSH operations")
	}
	scope, snapshot, e := m.sshScope(ctx, peer)
	if e != nil {
		return nil, e
	}
	switch request.Operation {
	case "ssh_identity":
		var params struct{}
		if e = decodeSSHFields(request.Params, &params); e != nil {
			return nil, e
		}
		local := snapshot.state.MeshIdentity()
		return model.SSHDestinationIdentity{Schema: 1, Source: scope.Source, Target: scope.Target, Owner: scope.Owner, Account: scope.Account, Home: snapshot.home, Pair: scope.Pair, Revision: scope.Revision, Certificate: local.Fingerprint, SPKI: local.SPKI, PublicKey: snapshot.state.PublicKey, ServiceRevision: scope.ServiceRevision, MaxTTL: int64(scope.MaxTTL), Limits: scope.Limits}, nil
	case "ssh_key_status":
		var q struct {
			Key string `json:"key"`
		}
		if e = decodeSSHFields(request.Params, &q, "key"); e != nil {
			return nil, e
		}
		status, e := m.owner.Manager.LookupSSHKey(ctx, peer.Identity.Host, q.Key)
		if e != nil {
			return nil, e
		}
		if status.Fence != nil && status.Fence.Control != nil && (status.Fence.Control.Certificate != peer.Identity.Fingerprint || status.Fence.Control.Pair != scope.Pair || status.Fence.Control.Revision != scope.Revision) {
			return nil, model.Err("forbidden", "SSH key belongs to a different enrollment")
		}
		if status.Execution != nil && status.Execution.Operation != nil && status.Execution.Operation.Request.Certificate != peer.Identity.Fingerprint {
			return nil, model.Err("forbidden", "SSH operation belongs to a previous certificate")
		}
		return compactSSHKeyStatus(status)
	case "ssh_key_seal":
		var q struct {
			Control model.SSHControlCommitment `json:"control"`
			Reason  string                     `json:"reason"`
		}
		if e = decodeSSHFields(request.Params, &q, "control", "reason"); e != nil {
			return nil, e
		}
		// Nested commitment is exact canonical producer evidence, not a permissive
		// second JSON object whose aliases/nulls could alter the reviewed key.
		var raw map[string]json.RawMessage
		if e = json.Unmarshal(request.Params, &raw); e != nil {
			return nil, e
		}
		canonical, _ := json.Marshal(q.Control)
		if !bytes.Equal(canonical, raw["control"]) {
			return nil, model.Err("invalid_request", "exact canonical SSH control required")
		}
		c := q.Control
		if request.Key != c.Key || c.Validate() != nil || c.Source != scope.Source || c.Target != scope.Target || c.Owner != scope.Owner || c.Account != scope.Account || c.HostKeySHA256 != scope.HostKeySHA256 || c.Certificate != scope.Certificate || c.Pair != scope.Pair || c.Revision != scope.Revision {
			return nil, model.Err("forbidden", "SSH seal differs from exact current authenticated authority")
		}
		status, e := m.owner.Manager.SealSSHKey(ctx, c, q.Reason)
		if e != nil {
			return nil, e
		}
		return compactSSHKeyStatus(status)
	}
	return nil, model.Err("forbidden", "unsupported SSH operation")
}

// Operational receipt projection: preserve full durable request/operation and
// all ownership/lifecycle/exit/native identity fields. Diagnostic usage/member
// lists and profile observations are deliberately absent, so a many-descendant
// job cannot overflow source32KiB settlement records just from telemetry.
func compactSSHKeyStatus(status model.SSHKeyStatus) (model.SSHKeyStatus, error) {
	if status.Execution != nil {
		copyLookup := *status.Execution
		status.Execution = &copyLookup
		if copyLookup.Lease != nil {
			l := *copyLookup.Lease
			if sshservice.ValidateSSHLeaseAttachments(l) != nil {
				return status, model.Err("ssh_evidence_unavailable", "SSH receipt contains unsupported attached obligations")
			}
			projected := model.Lease{Budget: l.Budget, ID: l.ID, Host: l.Host, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Request: l.Request, State: l.State, Outcome: l.Outcome, Created: l.Created, Started: l.Started, Expires: l.Expires, Ended: l.Ended, Revision: l.Revision, PID: l.PID, ExitCode: l.ExitCode, Process: l.Process}
			status.Execution.Lease = &projected
		}
	}
	raw, e := json.Marshal(status)
	if e != nil || len(raw) > 60<<10 {
		return status, fmt.Errorf("SSH exact status exceeds bounded wire receipt")
	}
	return status, nil
}
