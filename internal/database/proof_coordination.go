package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
)

// proveResetCoordinations accepts a COMPLETE namespace snapshot under lifecycle
// exclusion. Success only allows explicit LOCAL archival, never remote cleanup.
// Every opaque producer payload crosses strictEvidence's canonical/null/shape
// boundary again before it can establish a durable outcome.
func proveResetCoordinations(raws map[string][]byte, owner, host string, lifecycle map[string]mesh.LifecycleOperation) ([]ProofBlocker, error) {
	if len(raws) > 128 {
		return nil, fmt.Errorf("complete coordinator history exceeds128 records")
	}
	records := map[string]*proofCoordRecord{}
	for name, raw := range raws {
		var op mesh.Coordination
		if err := strictEvidence(string(raw), &op); err != nil {
			return nil, err
		}
		path, err := mesh.LifecycleOperationPath("", op.Key)
		if err != nil || "coordinator-"+filepath.Base(path) != name || op.Schema != 1 || op.Revision < 1 || op.Actor.UID != owner || op.Updated.IsZero() || !validDigest(op.Digest) || len(op.Targets) == 0 || len(op.Targets) > 4 {
			return nil, fmt.Errorf("invalid coordinator provenance/path")
		}
		if _, ok := records[op.Key]; ok {
			return nil, fmt.Errorf("duplicate coordinator key")
		}
		r := &proofCoordRecord{op: op}
		if err = strictEvidence(string(op.Request), &r.request, "key", "kind"); err != nil {
			return nil, err
		}
		q := r.request
		if q.Provision != nil {
			if err = validateProvisionSeedJSON(op.Request, "provision", "seed_catalog"); err != nil {
				return nil, err
			}
		}
		if !mesh.ValidKey(q.Key) || q.Kind != op.Kind {
			return nil, fmt.Errorf("coordinator immutable request identity differs")
		}
		canonical, err := json.Marshal(q)
		if err != nil {
			return nil, err
		}
		r.canonicalRequest = string(canonical)
		var digest string
		if q.Stop != nil && op.Kind != "stop" {
			return nil, fmt.Errorf("Stop draft mixed with other coordinator kind")
		}
		switch op.Kind {
		case "stop":
			if q.Stop == nil || q.Connect != nil || q.Reconnect != nil || q.Disconnect != nil || q.Provision != nil {
				return nil, fmt.Errorf("invalid Stop coordinator draft")
			}
			if err = q.Stop.Validate(); err != nil {
				return nil, err
			}
			digest, err = mesh.ReviewDigest(*q.Stop)
		case "connect":
			if q.Connect == nil || q.Connect.Reconnect != nil || q.Reconnect != nil || q.Disconnect != nil || q.Provision != nil {
				return nil, fmt.Errorf("invalid connect payload")
			}
			c := q.Connect
			if err = (mesh.Connector{ID: "proof", Destination: c.Destination, Home: c.RemoteHome, Binary: c.Binary, Reason: c.Reason}).Validate(); err != nil {
				return nil, err
			}
			if err = c.Forward.Validate(); err != nil {
				return nil, err
			}
			if err = c.Reverse.Validate(); err != nil {
				return nil, err
			}
			for _, ip := range []string{c.LocalIP, c.RemoteIP} {
				if ip != "" && net.ParseIP(ip) == nil {
					return nil, fmt.Errorf("invalid immutable connect address")
				}
			}
			digest, err = mesh.ReviewDigest(*c)
		case "reconnect":
			if q.Reconnect == nil || q.Connect != nil || q.Disconnect != nil || q.Provision != nil {
				return nil, fmt.Errorf("invalid reconnect payload")
			}
			if err = validateProofReconnect(*q.Reconnect, owner, host); err != nil {
				return nil, err
			}
			digest, err = mesh.ReviewDigest(*q.Reconnect)
		case "disconnect":
			if q.Disconnect == nil || q.Connect != nil || q.Reconnect != nil || q.Provision != nil || !validHost(q.Disconnect.Host) || !validDigest(q.Disconnect.Pair) || strings.TrimSpace(q.Disconnect.Reason) == "" || len(q.Disconnect.Reason) > 4096 {
				return nil, fmt.Errorf("invalid disconnect payload")
			}
			d := q.Disconnect
			digest, err = mesh.ReviewDigest(struct{ Host, Pair, Reason string }{d.Host, d.Pair, d.Reason})
		case "provision":
			if q.Provision == nil || q.Connect != nil || q.Reconnect != nil || q.Disconnect != nil || q.Key != q.Provision.Transfer.Key {
				return nil, fmt.Errorf("invalid linked provision request")
			}
			if err = q.Provision.Validate(); err != nil {
				return nil, err
			}
			digest, err = mesh.ReviewDigest(*q.Provision)
		default:
			return nil, fmt.Errorf("unknown coordinator intent")
		}
		if err != nil || digest != op.Digest {
			return nil, fmt.Errorf("coordinator canonical intent digest differs")
		}
		if err = parseCoordReview(r); err != nil {
			return nil, err
		}
		if err = parseCoordResult(r); err != nil {
			return nil, err
		}
		records[op.Key] = r
	}
	blockers := []ProofBlocker{}
	attempts := map[string]int{}
	for _, r := range records {
		op := r.op
		root := r
		if op.ParentKey != "" {
			var ok bool
			root, ok = records[op.ParentKey]
			if !ok || root.op.ParentKey != "" || root.op.Kind != op.Kind || root.op.Digest != op.Digest || root.canonicalRequest != r.canonicalRequest || op.ResumeFrom == "" || !oneOf(root.op.Phase, "incomplete", "completed") {
				return nil, fmt.Errorf("coordinator attempt changed immutable root intent")
			}
			attempts[root.op.Key]++
			if attempts[root.op.Key] > 16 {
				return nil, fmt.Errorf("coordinator family exceeds16 attempts")
			}
			at := r
			seen := map[string]bool{}
			for at.op.ParentKey != "" {
				if seen[at.op.Key] || len(seen) >= 16 {
					return nil, fmt.Errorf("cyclic coordinator resume ancestry")
				}
				seen[at.op.Key] = true
				previous, ok := records[at.op.ResumeFrom]
				if !ok || previous.op.Key != root.op.Key && previous.op.ParentKey != root.op.Key || previous.canonicalRequest != root.canonicalRequest {
					return nil, fmt.Errorf("coordinator resume provenance changed family")
				}
				at = previous
			}
		} else if op.ResumeFrom != "" {
			return nil, fmt.Errorf("original coordinator has attempt provenance")
		}
		if r.request.Key != root.op.Key {
			return nil, fmt.Errorf("coordinator request is not bound to original key")
		}
		if err := validateProvisionLink(r, root, records); err != nil {
			return nil, err
		}
		if err := validateCoordPayload(r, root, host, lifecycle); err != nil {
			return nil, err
		}
		block := func(detail string) { blockers = append(blockers, ProofBlocker{"mesh_coordinator", op.Key, detail}) }
		if op.Worker != "" {
			block("A recorded worker remains unresolved, including expired workers")
			continue
		}
		if !op.WorkerExpires.IsZero() {
			return nil, fmt.Errorf("inactive coordinator retains contradictory worker deadline")
		}
		switch op.Phase {
		case "completed":
			if op.Error != nil || op.ResolvedBy != "" || op.Handoff != nil {
				return nil, fmt.Errorf("completed coordinator contradicts durable outcome")
			}
			if !op.Approved {
				return nil, fmt.Errorf("completed coordinator lacks reviewed approval")
			}
			if len(op.Review) == 0 || len(op.Result) == 0 {
				return nil, fmt.Errorf("completed coordinator lacks full review/outcome")
			}
		case "rejected", "declined", "cancelled":
			if op.Approved || len(op.Result) > 0 || op.ResolvedBy != "" || op.Handoff != nil {
				return nil, fmt.Errorf("preapproval rejection carries applied outcome")
			}
		case "incomplete":
			if op.ResolvedBy == "" {
				block("An approved mesh mutation remains incomplete")
				continue
			}
			if !op.Approved {
				return nil, fmt.Errorf("unapproved incomplete coordinator cannot claim resolution")
			}
			child, ok := records[op.ResolvedBy]
			if !ok || child.op.ParentKey != root.op.Key || child.op.Phase != "completed" || child.op.Worker != "" || !child.op.Approved || child.op.Error != nil || child.canonicalRequest != root.canonicalRequest {
				return nil, fmt.Errorf("incomplete coordinator has no exact settled child receipt")
			}
		case "handed_off":
			block("Workflow handoff does not prove remote mutation settlement")
		default:
			block("Mesh workflow is active, unknown or unresolved")
		}
	}
	if len(blockers) > MaxProofBlockers {
		return nil, fmt.Errorf("complete coordinator blocker bound exceeded")
	}
	return blockers, nil
}

func parseCoordReview(r *proofCoordRecord) error {
	op := r.op
	if len(op.Review) == 0 {
		if op.ReviewHash != "" || op.ReviewRevision != 0 {
			return fmt.Errorf("coordinator review evidence is incomplete")
		}
		return nil
	}
	canonical, err := compactProofJSON(op.Review)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(canonical))
	if !validDigest(op.ReviewHash) || hex.EncodeToString(sum[:]) != op.ReviewHash || op.ReviewRevision < 1 || op.ReviewRevision > op.Revision {
		return fmt.Errorf("immutable coordinator review hash/revision differs")
	}
	// Read only the discriminator here; every member is strictly decoded into
	// its COMPLETE concrete shape below, including nested previews and grants.
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(op.Review, &fields); err != nil {
		return err
	}
	var operation string
	if err = json.Unmarshal(fields["operation"], &operation); err != nil {
		return err
	}
	r.reviewOperation = operation
	switch operation {
	case "stop-arm-node":
		var v meshops.StopReview
		if err = strictEvidence(string(op.Review), &v); err != nil {
			return err
		}
		if err = v.Validate(); err != nil {
			return err
		}
		q := r.request.Stop
		if op.Kind != "stop" || q == nil || v.Key != r.request.Key || v.Plan.Target.Host != q.Host || v.Plan.Reason != q.Reason || v.Plan.Policy != q.Policy {
			return fmt.Errorf("Stop review differs from immutable draft")
		}
		r.stopReview = &v
	case "install-and-start-arm-node", "verify-existing-provision":
		var v meshops.ProvisionPlan
		if err = strictEvidence(string(op.Review), &v); err != nil {
			return err
		}
		if err = validateProvisionSeedJSON(op.Review, "request", "seed_catalog"); err != nil {
			return err
		}
		q := r.request.Provision
		if op.Kind != "provision" || q == nil || v.Key != q.Transfer.Key || !reflect.DeepEqual(v.Request, *q) || v.Binary != provisionBinary(*q) || v.ArtifactSource != "native trusted local catalog / SHA256 "+q.Transfer.SHA256 || v.Effect == "" {
			return fmt.Errorf("provision review differs from exact artifact/account/path/startup intent")
		}
		if operation == "verify-existing-provision" && op.ParentKey == "" {
			return fmt.Errorf("provision verification has no immutable original")
		}
		r.provisionReview = &v
	case "connect-direct-peers", "reconnect-direct-peers":
		var v proofCoordConnectReview
		if err = strictEvidence(string(op.Review), &v); err != nil {
			return err
		}
		expected := proofConnectRequest(r.request)
		if (operation == "reconnect-direct-peers") != (op.Kind == "reconnect") {
			return fmt.Errorf("connection review kind changed")
		}
		if expected == nil || v.Key != r.request.Key || !reflect.DeepEqual(v.Request, *expected) || v.Warning == "" || v.SSH.Account == "" || v.SSH.HostKey == "" || v.LocalPrepared == nil || v.RemotePrepared == nil {
			return fmt.Errorf("connect review differs from immutable request")
		}
		if err = validCoordNode(v.Local); err != nil {
			return err
		}
		if err = validCoordNode(v.Remote); err != nil {
			return err
		}
		for _, p := range []*proofPreparedPreview{v.LocalPrepared, v.RemotePrepared} {
			if p.Expires.IsZero() || !literalCoordAddress(p.Listen) || !validCoordPreview(p.Identity) {
				return fmt.Errorf("invalid prepared listener review")
			}
		}
		r.connectReview = &v
	case "restore-existing-connection":
		var v proofCoordRestoreReview
		if err = strictEvidence(string(op.Review), &v); err != nil {
			return err
		}
		if op.Kind != "connect" || op.ParentKey == "" || v.Original != r.request.Key || !validDigest(v.Pair) || v.Effect == "" {
			return fmt.Errorf("restore review differs from original intent")
		}
		if err = validCoordNode(v.Local); err != nil {
			return err
		}
		if err = validCoordNode(v.Remote); err != nil {
			return err
		}
		r.restoreReview = &v
	case "verify-existing-connection":
		var v proofCoordVerifyReview
		if err = strictEvidence(string(op.Review), &v); err != nil {
			return err
		}
		if !oneOf(op.Kind, "connect", "reconnect") || op.ParentKey == "" || v.Original != r.request.Key || !validDigest(v.Pair) || !literalCoordAddress(v.LocalListen) || !literalCoordAddress(v.RemoteListen) || v.LocalRevision < 1 || v.RemoteRevision < 1 || v.Effect == "" || proofConnectRequest(r.request) == nil || !reflect.DeepEqual(v.Forward, proofConnectRequest(r.request).Forward) || !reflect.DeepEqual(v.Reverse, proofConnectRequest(r.request).Reverse) {
			return fmt.Errorf("verify-existing review differs from immutable intent")
		}
		if err = validCoordNode(v.Local); err != nil {
			return err
		}
		if err = validCoordNode(v.Remote); err != nil {
			return err
		}
		r.verifyReview = &v
	case "disconnect-direct-peer":
		var v proofCoordDisconnectReview
		if err = strictEvidence(string(op.Review), &v); err != nil {
			return err
		}
		d := r.request.Disconnect
		if d == nil || v.Key != r.request.Key || v.Host.Host != d.Host || v.Pair != d.Pair || v.Reason != d.Reason || v.LocalRevision < 1 || v.RemoteRevision < 1 || v.SSHAccount == "" || v.SSHHostKey == "" || v.Effect == "" {
			return fmt.Errorf("disconnect review differs from exact enrollment request")
		}
		r.disconnectReview = &v
	default:
		return fmt.Errorf("unsupported coordinator review shape")
	}
	return nil
}
func parseCoordResult(r *proofCoordRecord) error {
	if len(r.op.Result) == 0 {
		return nil
	}
	if r.op.Kind == "stop" {
		var v meshops.StopResult
		if err := strictEvidence(string(r.op.Result), &v); err != nil {
			return err
		}
		if r.stopReview == nil {
			return fmt.Errorf("Stop outcome lacks original review")
		}
		if err := v.Validate(*r.stopReview); err != nil {
			return err
		}
		r.stopResult = &v
		return nil
	}
	if r.op.Kind == "provision" {
		var v meshops.ProvisionResult
		if err := strictEvidence(string(r.op.Result), &v); err != nil {
			return err
		}
		if err := validateProvisionSeedJSON(r.op.Result, "receipt", "catalog_seed"); err != nil {
			return err
		}
		if err := validateProvisionResult(r, v); err != nil {
			return err
		}
		r.provisionResult = &v
		return nil
	}
	if oneOf(r.op.Kind, "connect", "reconnect") {
		var v proofCoordConnectResult
		if err := strictEvidence(string(r.op.Result), &v); err != nil {
			return err
		}
		c := proofConnectRequest(r.request)
		if c == nil || v.Key != r.request.Key || v.Host != v.Remote.Host || v.Name != v.Remote.Name || !validDigest(v.Pair) || !reflect.DeepEqual(v.Forward, c.Forward) || !reflect.DeepEqual(v.Reverse, c.Reverse) {
			return fmt.Errorf("completed connect result differs from exact request")
		}
		if err := mesh.ValidateRegistry(mesh.Config{Identity: v.Local, Listen: v.Local.Address, Peers: []mesh.Peer{{Identity: v.Remote, Grant: v.Forward, Pair: v.Pair, State: "revoked", Revision: 1}}}); err != nil {
			return err
		}
		r.connectResult = &v
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(r.op.Result, &fields); err != nil {
		return err
	}
	if _, ok := fields["disconnected"]; ok {
		var v proofCoordDisconnectResult
		if err := strictEvidence(string(r.op.Result), &v); err != nil {
			return err
		}
		if v.Key != r.request.Key || v.Pending || v.Host != r.request.Disconnect.Host {
			return fmt.Errorf("disconnect result is absent, pending or changed")
		}
		r.disconnectResult = &v
		return nil
	}
	var v proofCoordPendingResult
	if err := strictEvidence(string(r.op.Result), &v); err != nil {
		return err
	}
	if r.op.Phase != "incomplete" || v.Key != r.request.Key || !v.Pending || v.Host != r.request.Disconnect.Host || v.Detail == "" || v.Retry == "" {
		return fmt.Errorf("invalid incomplete disconnect outcome")
	}
	return nil
}
func validateCoordPayload(r, root *proofCoordRecord, host string, lifecycles map[string]mesh.LifecycleOperation) error {
	op := r.op
	if e := validateProofReconnectHistory(r, root, host, lifecycles); e != nil {
		return e
	}
	target := map[string]bool{}
	for _, v := range op.Targets {
		if v == "" || len(v) > 512 || target[v] {
			return fmt.Errorf("invalid duplicate coordinator target")
		}
		target[v] = true
	}
	if op.Kind == "stop" {
		q := r.request.Stop
		if q == nil || len(target) != 1 || !target["host:"+q.Host] || op.ParentKey != "" || op.ResumeFrom != "" || op.ResolvedBy != "" || op.Handoff != nil {
			return fmt.Errorf("Stop source cannot borrow target or recovery authority")
		}
		if r.stopReview != nil && r.stopReview.Plan.Actor.Host != host {
			return fmt.Errorf("Stop review belongs to another source authority")
		}
		if op.Phase == "completed" && (r.stopResult == nil || r.stopResult.Status != "stopped" || !op.Approved) {
			return fmt.Errorf("Stop completed without positive original-generation outcome")
		}
	}
	if r.request.Provision != nil {
		q := r.request.Provision
		if len(target) != 1 || !target["ssh:"+q.Destination] {
			return fmt.Errorf("provision target binding differs")
		}
		if r.provisionResult != nil {
			if r.provisionReview == nil {
				return fmt.Errorf("provision result lacks full exact reviewed installation")
			}
			if r.op.ParentKey != "" {
				if root.provisionReview == nil || !root.op.Approved || r.provisionReview.Operation != "verify-existing-provision" || !r.provisionResult.Reused {
					return fmt.Errorf("provision recovery did not preserve original approved installation")
				}
			}
		}
	}
	if r.request.Disconnect != nil && !target["host:"+r.request.Disconnect.Host] {
		return fmt.Errorf("disconnect target binding missing")
	}
	if r.connectResult != nil {
		result := r.connectResult
		life, ok := lifecycles[root.op.Key]
		if !ok || life.Kind != op.Kind || life.Phase != "connected" || life.LocalHost != host || life.Pair != result.Pair || life.LocalIdentity == nil || life.RemoteIdentity == nil || *life.LocalIdentity != result.Local || *life.RemoteIdentity != result.Remote || result.Local.Host != host || !target["host:"+result.Remote.Host] {
			return fmt.Errorf("connect outcome differs from complete lifecycle receipt")
		}
		review := r.connectReview
		if review == nil && (r.restoreReview != nil || r.verifyReview != nil) {
			review = root.connectReview
		}
		if review == nil {
			return fmt.Errorf("connect outcome lacks original strict reviewed enrollment")
		}
		if r.op.ParentKey != "" {
			prior := root.connectReview
			if !root.op.Approved || prior == nil || !reflect.DeepEqual(prior.LocalPrepared.Identity, review.LocalPrepared.Identity) || !reflect.DeepEqual(prior.RemotePrepared.Identity, review.RemotePrepared.Identity) || prior.LocalPrepared.Listen != review.LocalPrepared.Listen || prior.RemotePrepared.Listen != review.RemotePrepared.Listen || prior.SSH != review.SSH {
				return fmt.Errorf("connect recovery changed original approved pins/listeners/SSH identity")
			}
		}
		if review.Local.Host != result.Local.Host || review.Remote.Host != result.Remote.Host || review.Local.Owner.UID != result.Local.Owner || review.Remote.Owner.UID != result.Remote.Owner || review.Local.Identity != nil && !previewMatches(*review.Local.Identity, result.Local) || review.Remote.Identity != nil && !previewMatches(*review.Remote.Identity, result.Remote) || review.Remote.Home != life.RemoteHome {
			return fmt.Errorf("connect node snapshots differ from actual reviewed authorities")
		}
		if !previewMatches(review.LocalPrepared.Identity, result.Local) || !previewMatches(review.RemotePrepared.Identity, result.Remote) || review.SSH.Account != life.SSHAccount || review.SSH.HostKey != life.SSHHostKey {
			return fmt.Errorf("connect outcome pins differ from approved preview")
		}
		if r.restoreReview != nil {
			v := r.restoreReview
			if root.op.Phase != "completed" || v.Pair != result.Pair || v.Local.Identity == nil || v.Remote.Identity == nil || !previewMatches(*v.Local.Identity, result.Local) || !previewMatches(*v.Remote.Identity, result.Remote) || root.connectResult == nil || !reflect.DeepEqual(*root.connectResult, *result) {
				return fmt.Errorf("restored outcome differs from immutable completed root")
			}
		}
		if r.verifyReview != nil {
			v := r.verifyReview
			if !root.op.Approved || root.connectReview == nil || v.Pair != result.Pair || !result.Reused || v.LocalListen != root.connectReview.LocalPrepared.Listen || v.RemoteListen != root.connectReview.RemotePrepared.Listen || v.Local.Identity == nil || v.Remote.Identity == nil || !previewMatches(*v.Local.Identity, result.Local) || !previewMatches(*v.Remote.Identity, result.Remote) || !coordPriorMatches(v.Local.Prior, result.Remote, result.Reverse, v.Pair, v.LocalRevision) || !coordPriorMatches(v.Remote.Prior, result.Local, result.Forward, v.Pair, v.RemoteRevision) {
				return fmt.Errorf("verified-existing outcome differs from original approved enrollment")
			}
		}
	}
	if r.disconnectResult != nil || r.op.Kind == "disconnect" && r.op.Approved {
		life, ok := lifecycles[root.op.Key]
		review := r.disconnectReview
		if !ok || life.Kind != "disconnect" || life.LocalHost != host || life.Pair != r.request.Disconnect.Pair || life.RemoteHost != r.request.Disconnect.Host || life.RemoteIdentity == nil || life.LocalIdentity == nil || review == nil || review.Host != *life.RemoteIdentity || review.SSHAccount != life.SSHAccount || review.SSHHostKey != life.SSHHostKey || review.LocalRevision != life.LocalRevision || review.RemoteRevision != life.RemoteRevision {
			return fmt.Errorf("disconnect outcome/review differs from exact lifecycle enrollment")
		}
		if r.disconnectResult != nil && life.Phase != "disconnected" {
			return fmt.Errorf("disconnect coordinator completed before lifecycle settlement")
		}
		if err := mesh.ValidateRegistry(mesh.Config{Identity: *life.LocalIdentity, Listen: life.LocalIdentity.Address, Peers: []mesh.Peer{{Identity: *life.RemoteIdentity, Grant: mesh.Grant{}, Pair: life.Pair, State: "revoked", Revision: 1}}}); err != nil {
			return err
		}
	}
	if op.Phase == "completed" && r.connectResult == nil && r.disconnectResult == nil && r.provisionResult == nil && r.stopResult == nil {
		return fmt.Errorf("completed coordinator lacks known final result")
	}
	return nil
}

func coordPriorMatches(prior *proofPriorTrust, peer mesh.Identity, grant mesh.Grant, pair string, revision int) bool {
	return prior != nil && previewMatches(prior.Identity, peer) && prior.State == "active" && prior.Pair == pair && prior.Revision == revision && reflect.DeepEqual(prior.Grant, grant)
}
func validCoordPreview(v proofIdentityPreview) bool {
	return validHost(v.Host) && v.Name != "" && v.Owner != "" && literalCoordAddress(v.Address) && validDigest(v.Fingerprint) && validDigest(v.SPKI) && !v.Expires.IsZero()
}
func previewMatches(v proofIdentityPreview, i mesh.Identity) bool {
	return v.Host == i.Host && v.Name == i.Name && v.Owner == i.Owner && v.Username == i.OwnerUsername && v.Address == i.Address && v.Fingerprint == i.Fingerprint && v.SPKI == i.SPKI && v.Expires.Equal(i.Expires)
}
func literalCoordAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || net.ParseIP(host) == nil {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number > 0 && number <= 65535
}
func validCoordNode(v proofNodePreview) error {
	if !validHost(v.Host) || !filepath.IsAbs(v.Home) || v.OS == "" || v.Arch == "" || v.Version == "" || v.Owner.UID == "" || len(v.PrivateIPs) > 64 {
		return fmt.Errorf("invalid node review identity")
	}
	if err := v.Startup.Validate(); err != nil {
		return err
	}
	if v.Route != nil && (net.ParseIP(v.Route.ClientIP) == nil || net.ParseIP(v.Route.ServerIP) == nil) {
		return fmt.Errorf("invalid reviewed direct SSH route")
	}
	for _, ip := range v.PrivateIPs {
		if net.ParseIP(ip) == nil {
			return fmt.Errorf("invalid reviewed private interface")
		}
	}
	if v.Identity != nil {
		if !validCoordPreview(*v.Identity) || v.Identity.Host != v.Host || v.Identity.Owner != v.Owner.UID || v.IdentityStatus != "" {
			return fmt.Errorf("reviewed node public pins differ")
		}
	} else if v.IdentityStatus != "not created; actual public pins will be shown after private preparation" {
		return fmt.Errorf("reviewed node has no explicit identity status")
	}
	if v.Prior != nil {
		if !validCoordPreview(v.Prior.Identity) || !validDigest(v.Prior.Pair) || v.Prior.Revision < 1 || !oneOf(v.Prior.State, "active", "pending", "revoked") {
			return fmt.Errorf("invalid reviewed prior enrollment")
		}
		if err := v.Prior.Grant.Validate(); err != nil {
			return err
		}
	}
	return nil
}
