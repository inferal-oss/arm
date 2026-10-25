package database

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"reflect"
	"strings"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
)

// Reconstruct the closed wire shape, without invoking producer resolution,
// registry lookup, startup, recovery, or trust mutation.
func proofConnectRequest(q meshops.StartRequest) *meshops.ConnectRequest {
	if q.Connect != nil {
		return q.Connect
	}
	if q.Reconnect == nil {
		return nil
	}
	r := q.Reconnect
	d, b := r.Draft, r.Binding
	return &meshops.ConnectRequest{Destination: b.Selected.Destination, Binary: b.Selected.Binary, RemoteHome: b.Selected.Home, LocalIP: d.LocalIP, RemoteIP: d.RemoteIP, Public: d.Public, Reason: d.Reason, Forward: d.Forward, Reverse: d.Reverse, ReplaceRevoked: true, Reconnect: r}
}
func proofSameReconnectPrincipal(a, b mesh.Identity) bool {
	return a.Host == b.Host && a.Owner == b.Owner && a.Certificate == b.Certificate && a.Fingerprint == b.Fingerprint && a.SPKI == b.SPKI && a.Expires.Equal(b.Expires)
}
func validateProofReconnect(r meshops.ReconnectRequest, owner, host string) error {
	d, b := r.Draft, r.Binding
	if !validHost(d.Host) || b.Local.Host != host || b.Local.Owner != owner || b.Prior.Identity.Host != d.Host || b.Prior.State != "revoked" || strings.TrimSpace(d.Reason) == "" || len(d.Reason) > 4096 {
		return fmt.Errorf("invalid exact reconnect authority/enrollment")
	}
	// Independently validate saved explicit route syntax; malformed drafts
	// cannot become archival authority merely because a native stage refused them.
	for _, address := range []string{d.LocalIP, d.RemoteIP} {
		if address != "" && (len(address) > 64 || net.ParseIP(address) == nil) {
			return fmt.Errorf("invalid saved reconnect explicit IP literal")
		}
	}
	if err := mesh.ValidateRegistry(mesh.Config{Identity: b.Local, Listen: b.Local.Address, Peers: []mesh.Peer{b.Prior}}); err != nil {
		return err
	}
	if err := d.Forward.Validate(); err != nil {
		return err
	}
	if err := d.Reverse.Validate(); err != nil {
		return err
	}
	if err := b.Selected.Validate(); err != nil {
		return err
	}
	if d.Connector == "" && b.Selected.ID != "reconnect" {
		return fmt.Errorf("reconnect selector identity changed")
	}
	if b.Selected.Reason != d.Reason {
		return fmt.Errorf("reconnect reason changed in selected helper")
	}
	if d.Connector != "" && (d.Destination != "" || d.Binary != "" || d.RemoteHome != "" || b.Selected.ID != d.Connector) {
		return fmt.Errorf("reconnect connector provenance changed")
	}
	if d.Destination != "" && d.Destination != b.Selected.Destination || d.Binary != "" && d.Binary != b.Selected.Binary || d.RemoteHome != "" && d.RemoteHome != b.Selected.Home {
		return fmt.Errorf("selected helper differs from explicit reconnect draft")
	}
	c := b.Connection
	if c != nil {
		if !mesh.ValidKey(c.Key) || !oneOf(c.Kind, "connect", "reconnect") || c.Phase != "connected" || c.Pair != b.Prior.Pair || c.LocalHost != host || c.RemoteHost != d.Host || c.LocalIdentity == nil || c.RemoteIdentity == nil || !proofSameReconnectPrincipal(*c.LocalIdentity, b.Local) || !proofSameReconnectPrincipal(*c.RemoteIdentity, b.Prior.Identity) {
			return fmt.Errorf("reconnect prior receipt differs from original exact enrollment")
		}
		binary := c.Binary
		if binary == "" {
			binary = "arm"
		}
		if d.Connector == "" && (d.Destination == "" && b.Selected.Destination != c.SSHLocator || d.Binary == "" && b.Selected.Binary != binary || d.RemoteHome == "" && b.Selected.Home != c.RemoteHome) {
			return fmt.Errorf("reconnect silently replaced retained native locator")
		}
	} else if d.Connector == "" && d.Destination == "" {
		return fmt.Errorf("reconnect has no selected locator provenance")
	}
	return nil
}
func validateProofReconnectHistory(r, root *proofCoordRecord, host string, lifecycles map[string]mesh.LifecycleOperation) error {
	if r.request.Reconnect == nil {
		return nil
	}
	q := r.request.Reconnect
	b := q.Binding
	count := 0
	for _, life := range lifecycles {
		if oneOf(life.Kind, "connect", "reconnect") && life.Pair == b.Prior.Pair && life.RemoteHost == q.Draft.Host {
			count++
			if b.Connection == nil || !reflect.DeepEqual(life, *b.Connection) {
				return fmt.Errorf("reconnect binding differs from complete prior lifecycle history")
			}
		}
	}
	if count > 1 || count == 0 && b.Connection != nil {
		return fmt.Errorf("ambiguous or missing exact prior reconnect receipt")
	}
	if !containsCoordTarget(r.op.Targets, "host:"+q.Draft.Host) {
		return fmt.Errorf("reconnect stable target binding missing")
	}
	expected := sha256.Sum256([]byte("arm-connect:" + r.request.Key + ":" + host + ":" + q.Draft.Host))
	newPair := hex.EncodeToString(expected[:])
	if r.connectResult != nil && (r.connectResult.Pair != newPair || newPair == b.Prior.Pair) {
		return fmt.Errorf("reconnect outcome did not use its original key-derived new enrollment")
	}
	if v := r.connectReview; v != nil {
		priorOK := func(p *proofPriorTrust, other mesh.Identity, grant mesh.Grant, local bool) bool {
			if p == nil {
				return false
			}
			if p.Pair == b.Prior.Pair && p.State == "revoked" && previewPrincipalMatches(p.Identity, other) {
				return !local || p.Revision == b.Prior.Revision && reflect.DeepEqual(p.Grant, b.Prior.Grant)
			}
			return r.op.ParentKey != "" && root.op.Approved && p.Pair == newPair && oneOf(p.State, "pending", "active") && previewPrincipalMatches(p.Identity, other) && reflect.DeepEqual(p.Grant, grant)
		}
		if v.Operation != "reconnect-direct-peers" || !priorOK(v.Local.Prior, b.Prior.Identity, q.Draft.Reverse, true) || !priorOK(v.Remote.Prior, b.Local, q.Draft.Forward, false) {
			return fmt.Errorf("reconnect review did not bind old revocations or exact admitted recovery")
		}
		if v.Remote.Host != q.Draft.Host || v.Local.Host != host || !previewPrincipalMatches(v.LocalPrepared.Identity, b.Local) || !previewPrincipalMatches(v.RemotePrepared.Identity, b.Prior.Identity) {
			return fmt.Errorf("reconnect review inherited different authority")
		}
		if c := b.Connection; c != nil {
			if v.SSH.Account != c.SSHAccount || v.SSH.HostKey != c.SSHHostKey || v.Remote.Home != c.RemoteHome {
				return fmt.Errorf("reconnect changed known SSH account or ARM home")
			}
		}
	}

	return nil
}
func containsCoordTarget(all []string, target string) bool {
	for _, s := range all {
		if s == target {
			return true
		}
	}
	return false
}
func previewPrincipalMatches(v proofIdentityPreview, i mesh.Identity) bool {
	return v.Host == i.Host && v.Owner == i.Owner && v.Fingerprint == i.Fingerprint && v.SPKI == i.SPKI && v.Expires.Equal(i.Expires)
}
