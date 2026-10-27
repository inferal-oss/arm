package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshsshrelay"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
)

// recoverRoutedSSH sends only an exact lookup or nonexecution seal. Every
// authority field is taken from the durable original source intent; current
// graph reachability cannot silently replace its destination grant or pin.
func (a *app) recoverRoutedSSH(store *mesh.Store, intent sshservice.SourceIntent, action string, commitment model.SSHControlCommitment, reason string) (model.SSHKeyStatus, error) {
	pin := intent.Request.Pin
	if pin.RouteWorkspace == "" || (action != "lookup" && action != "seal") {
		return model.SSHKeyStatus{}, model.Err("invalid_request", "exact routed SSH recovery intent required")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	selection, err := a.selectSSHRoute(ctx, store, pin.Target, pin.RouteWorkspace)
	if err != nil {
		return model.SSHKeyStatus{}, err
	}
	v := selection.access
	if v.Generation != pin.RouteGeneration || v.OriginGeneration != pin.RouteSourceGeneration ||
		v.DestinationGeneration != pin.RouteTargetGeneration || v.Destination.Fingerprint != pin.TLSFingerprint ||
		v.Directory.Fingerprint != pin.RouteDirectoryPin || v.Directory.SPKI != pin.RouteDirectorySPKI ||
		v.Destination.SPKI != pin.TLSSPKI || v.Origin.Fingerprint != intent.Request.SourceCertificate {
		return model.SSHKeyStatus{}, model.Err("ssh_context_changed", "original routed SSH workspace, endpoint or certificate changed; preserve pending evidence")
	}
	identity := selection.identity
	if identity.Source != pin.Source || identity.Target != pin.Target ||
		identity.Pair != pin.Pair || identity.Revision != pin.Revision ||
		identity.Certificate != pin.TLSFingerprint || identity.SPKI != pin.TLSSPKI ||
		identity.Account != pin.Account || identity.PublicKey != pin.PublicKey ||
		identity.Owner != intent.Request.TargetOwner {
		return model.SSHKeyStatus{}, model.Err("ssh_context_changed", "original routed SSH destination grant/account/host key changed; preserve pending evidence")
	}
	hostHash, err := sshKeyHash(pin.PublicKey)
	if err != nil {
		return model.SSHKeyStatus{}, err
	}
	q := meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationLookup, Key: intent.Request.Key}
	if action == "seal" {
		q.Operation = meshsshrelay.OperationSeal
		q.Commitment = &commitment
		q.Reason = reason
	}
	out, conn, err := selection.request(ctx, meshsshrelay.ClassSSH, hostHash, q)
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil {
		return model.SSHKeyStatus{}, err
	}
	if out.Status == nil {
		return model.SSHKeyStatus{}, fmt.Errorf("routed SSH destination omitted exact key status")
	}
	return *out.Status, nil
}
