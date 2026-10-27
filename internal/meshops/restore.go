package meshops

import (
	"encoding/json"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
	"reflect"
)

func existingEnrollment(q mesh.LifecycleInspection, other mesh.Identity, pair string, grant mesh.Grant, identity mesh.Identity, listen string) bool {
	if q.Identity == nil || *q.Identity != identity || q.Config == nil || q.Config.Identity != identity || q.Host != identity.Host || q.Config.Listen != listen || !q.Config.ListenerEnabled() {
		return false
	}
	for _, p := range q.Config.Peers {
		if p.Identity == other {
			return p.State == "active" && p.Pair == pair && reflect.DeepEqual(p.Grant, grant)
		}
	}
	return false
}

// restoreCompleted can only start ordinary backends whose exact enabled trust
// already exists. It never calls prepare/pair/activate, replaces pins or leases.
func (a *Coordinator) restoreCompleted(key string, request ConnectRequest, receipt mesh.Coordination, own, remote mesh.LifecycleInspection, ssh meshssh.Identity, yes bool) (int, error) {
	original, ok, e := mesh.ReadLifecycleOperation(a.Home, key)
	if e != nil {
		return 1, e
	}
	if !ok || original.Phase != "connected" || original.LocalIdentity == nil || original.RemoteIdentity == nil || original.SSHAccount != ssh.Account || original.SSHHostKey != ssh.HostKey {
		return 1, model.Err("setup_changed", "original exact connection authority evidence is unavailable or changed")
	}
	var reviewed struct {
		Local struct {
			Listen string `json:"listen"`
		} `json:"local_prepared"`
		Remote struct {
			Listen string `json:"listen"`
		} `json:"remote_prepared"`
	}
	if e = json.Unmarshal(receipt.Review, &reviewed); e != nil || reviewed.Local.Listen == "" || reviewed.Remote.Listen == "" {
		return 1, model.Err("setup_changed", "original listener review is unavailable; use a fresh reviewed intent")
	}
	same := func() bool {
		return existingEnrollment(own, *original.RemoteIdentity, original.Pair, request.Reverse, *original.LocalIdentity, reviewed.Local.Listen) && existingEnrollment(remote, *original.LocalIdentity, original.Pair, request.Forward, *original.RemoteIdentity, reviewed.Remote.Listen)
	}
	if !same() {
		return 1, model.Err("setup_changed", "pins/endpoints/grants/ledger or enrollment changed; a completed key cannot enroll new trust")
	}
	if !own.Running || !remote.Running {
		a.attemptParent = key
		if e = a.begin(key, "connect", receipt.Digest, "host:"+remote.Host); e != nil {
			return 1, e
		}
		plan := map[string]any{"operation": "restore-existing-connection", "original_key": key, "pair": original.Pair, "local": lifecycleNodePreview(own, remote.Host), "remote": lifecycleNodePreview(remote, own.Host), "effect": "start only stopped ordinary ARM backends using their existing saved settings and enabled exact trust; do not enroll, replace pins, change grants or retry execution"}
		if e = a.Confirm(plan, yes, false); e != nil {
			return 1, e
		}
		if e = a.markApproved(); e != nil {
			return 1, e
		}
		own, e = a.Ensure(own.Startup)
		if e != nil {
			return 1, e
		}
		expectedOwner, expectedHost := remote.Owner.UID, remote.Host
		if _, e = a.ssh(request.Destination, request.Binary, Helper{Home: request.RemoteHome, Action: "lifecycle_ensure", Startup: remote.Startup, ExpectedHost: expectedHost, ExpectedOwner: expectedOwner}, ssh, &remote); e != nil {
			return 1, e
		}
		if !same() || !own.Running || !remote.Running {
			return 1, model.Err("setup_changed", "restored backend no longer matches the original connection")
		}
	}
	params, _ := json.Marshal(map[string]string{"pair": original.Pair})
	var proven mesh.Identity
	if e = a.call("mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{remote.Host, mesh.Request{Operation: "hello", Params: params}}, &proven); e != nil {
		return 1, e
	}
	if proven != *original.RemoteIdentity {
		return 1, model.Err("wrong_host", "current mTLS proof does not match original completed receipt")
	}
	return 0, a.Print(receipt.Result)
}

// An incomplete record is not a completed-key read retry. Even when its native
// lifecycle journal says connected, recovering that outcome requires a new
// concrete decision and fresh current mTLS proof, without changing trust.
func (a *Coordinator) verifyRecoveredConnection(key string, request ConnectRequest, original mesh.LifecycleOperation, own, remote mesh.LifecycleInspection, ssh meshssh.Identity, yes bool) (int, error) {

	revision := func(config *mesh.Config, host string) int {
		if config != nil {
			for _, peer := range config.Peers {
				if peer.Identity.Host == host {
					return peer.Revision
				}
			}
		}
		return -1
	}
	localRevision, remoteRevision := revision(own.Config, remote.Host), revision(remote.Config, own.Host)
	plan := map[string]any{"operation": "verify-existing-connection", "original_key": key, "pair": original.Pair, "local_revision": localRevision, "remote_revision": remoteRevision, "local_listen": own.Config.Listen, "remote_listen": remote.Config.Listen, "source_permissions_on_target": request.Forward, "target_permissions_on_source": request.Reverse, "local": lifecycleNodePreview(own, remote.Host), "remote": lifecycleNodePreview(remote, own.Host), "effect": "read-only recovery: verify the exact existing enabled enrollment, identities, endpoints and directional grants using fresh mTLS; do not change trust/listeners, start backends or retry execution"}
	if e := a.Confirm(plan, yes, false); e != nil {
		return 1, e
	}
	if e := a.markApproved(); e != nil {
		return 1, e
	}
	currentOwn, e := a.Inspect()
	if e != nil {
		return 1, e
	}
	var currentRemote mesh.LifecycleInspection
	if _, e = a.ssh(request.Destination, request.Binary, Helper{Home: request.RemoteHome, Action: "lifecycle_inspect"}, ssh, &currentRemote); e != nil {
		return 1, e
	}
	if revision(currentOwn.Config, remote.Host) != localRevision || revision(currentRemote.Config, own.Host) != remoteRevision || original.LocalIdentity == nil || original.RemoteIdentity == nil || !currentOwn.Running || !currentRemote.Running || !existingEnrollment(currentOwn, *original.RemoteIdentity, original.Pair, request.Reverse, *original.LocalIdentity, own.Config.Listen) || !existingEnrollment(currentRemote, *original.LocalIdentity, original.Pair, request.Forward, *original.RemoteIdentity, remote.Config.Listen) {
		return 1, model.Err("setup_changed", "existing enrollment changed after recovery review; no trust was modified")
	}
	params, _ := json.Marshal(map[string]string{"pair": original.Pair})
	var proven mesh.Identity
	if e = a.call("mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{remote.Host, mesh.Request{Operation: "hello", Params: params}}, &proven); e != nil {
		return 1, e
	}
	if proven != *original.RemoteIdentity {
		return 1, model.Err("wrong_host", "current mTLS proof differs from the reviewed recovery identity")
	}
	if e = a.check(); e != nil {
		return 1, e
	}
	return 0, a.Print(map[string]any{"connected": remote.Host, "name": remote.Identity.Name, "key": key, "pair": original.Pair, "local_identity": currentOwn.Identity, "remote_identity": currentRemote.Identity, "source_permissions_on_target": request.Forward, "target_permissions_on_source": request.Reverse, "reused": true})
}
