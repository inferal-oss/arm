package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshsshrelay"
	"github.com/inferal-oss/arm/internal/meshstream"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/workspace"
	"golang.org/x/crypto/ssh"
)

// This uses three running mesh services. The original and final endpoint have
// no direct enrollment, and the middle server uses its production route handler.
func TestRoutedSSHThreeNodesOriginalSourceExecAndRevocation(t *testing.T) {
	directory, source, destination, id := graphStreamFixture(t)
	// The transport hold depends on current native CPU and memory samples, as
	// it does in a running daemon. The shared graph fixture opens Managers but
	// leaves their recurring observation workers stopped.
	directory.Manager.Start()
	destination.Manager.Start()
	awaitTransitHeadroom := func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			status, statusErr := directory.Manager.Status()
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if status.CPU != nil && status.CPU.Known && status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= 4<<20 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("relay fixture lacks fresh CPU/memory observations: cpu=%+v memory=%+v", status.CPU, status.Memory)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	dirService, err := directory.workspaceService()
	if err != nil {
		t.Fatal(err)
	}
	srcService, err := source.workspaceService()
	if err != nil {
		t.Fatal(err)
	}
	dstService, err := destination.workspaceService()
	if err != nil {
		t.Fatal(err)
	}
	dirStore, srcStore, dstStore := dirService.store, srcService.store, dstService.store
	srcID, dstID, dirID := srcStore.Config().Identity, dstStore.Config().Identity, dirStore.Config().Identity
	if _, err = srcStore.Resolve(dstID.Host); err == nil {
		t.Fatal("source gained a direct destination peer")
	}
	if _, err = dstStore.Resolve(srcID.Host); err == nil {
		t.Fatal("destination gained a direct source peer")
	}
	if _, err = destination.workspaceRefresh(t.Context(), id); err != nil {
		t.Fatal(err)
	}

	binding, _, err := destination.Manager.SSHAccount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stateHome, err := filepath.EvalSymlinks(destination.home)
	if err != nil {
		t.Fatal(err)
	}
	review, err := sshservice.ReviewService(t.Context(), stateHome, binding, true, "three node routed SSH test")
	if err != nil {
		t.Fatal(err)
	}
	if err = sshservice.ApplyService(t.Context(), stateHome, binding, review); err != nil {
		t.Fatal(err)
	}
	snapshot, err := dstService.sshSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	access, err := source.Manager.WorkspaceRouteAccess(t.Context(), id, srcID.Host, dstID.Host, workspace.RouteSSH)
	if err != nil {
		t.Fatal(err)
	}
	grant := meshsshrelay.DestinationGrant{Workspace: id, Generation: access.Generation, Source: srcID.Host, Target: dstID.Host,
		SourceGeneration: access.OriginGeneration, TargetGeneration: access.DestinationGeneration,
		DirectoryPin: access.Directory.Fingerprint, DirectorySPKI: access.Directory.SPKI,
		SourcePin: srcID.Fingerprint, SourceSPKI: srcID.SPKI, TargetPin: dstID.Fingerprint, TargetSPKI: dstID.SPKI,
		SSHHostKeyPin: sshPublicHash(snapshot.key.PublicKey()), Account: binding.Account, Owner: binding.Owner,
		Revision: 1, SSH: true, MaxTTL: time.Minute, Limits: model.Resources{CPU: 1}, Expires: time.Now().Add(time.Hour)}
	grantReview, err := meshsshrelay.ReviewPolicy(destination.home, dstID.Host, dstID.Fingerprint, "grant", "exact original source test",
		func(p *meshsshrelay.Policy) error { p.Destinations = append(p.Destinations, grant); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(grantReview); err != nil {
		t.Fatal(err)
	}
	relayReview, err := meshsshrelay.ReviewPolicy(directory.home, dirID.Host, dirID.Fingerprint, "grant", "opaque middle hop test",
		func(p *meshsshrelay.Policy) error {
			p.Relays = append(p.Relays, meshsshrelay.RelayGrant{
				Workspace: id, Generation: access.Generation, Local: dirID.Host, Revision: 1, SSH: true,
				Transit: model.Resources{CPU: .01, Memory: 4 << 20}, BytesPerSecond: 64 << 20, Expires: time.Now().Add(time.Hour)})
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(relayReview); err != nil {
		t.Fatal(err)
	}
	peer, err := srcStore.Resolve(dirID.Host)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := srcStore.Call(t.Context(), dirID.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var scope model.NodeAccessScope
	if err = mesh.Decode(raw, &scope); err != nil {
		t.Fatal(err)
	}
	open := meshsshrelay.Open{Workspace: id, Generation: access.Generation, Route: local.ID(), Source: srcID.Host, Target: dstID.Host,
		SourceGeneration: access.OriginGeneration, TargetGeneration: access.DestinationGeneration, DirectoryRevision: access.DirectoryRevision,
		DirectoryPin: access.Directory.Fingerprint, DirectorySPKI: access.Directory.SPKI,
		SourcePin: srcID.Fingerprint, TargetPin: dstID.Fingerprint, Path: []string{srcID.Host, dirID.Host, dstID.Host}, Hop: 1,
		Pair: scope.Pair, EnrollmentRevision: scope.Revision, Expires: time.Now().Add(2 * time.Minute)}
	probe := open
	probe.Class, probe.SSHHostKeyPin = meshsshrelay.ClassSSHIdentity, strings.Repeat("0", 64)
	middlePermit, err := dirService.routedSSHAdmission(t.Context(), meshsshrelay.Outer{Source: srcID.Host, Target: dirID.Host, Pair: scope.Pair,
		Certificate: srcID.Fingerprint, Revision: scope.Revision, Expires: time.Now().Add(time.Minute)}, probe)
	if err != nil {
		t.Fatal("middle-hop admission:", err)
	}
	awaitTransitHeadroom()
	preflightJoined := make(chan struct{})
	if reservation, reserveErr := dirService.reserveSSHRelayTransit(t.Context(), t.Context(), preflightJoined, meshsshrelay.Outer{}, probe, middlePermit); reserveErr != nil {
		status, _ := directory.Manager.Status()
		t.Fatalf("middle-hop transit reservation: %v; reserved=%+v cpu=%+v memory=%+v", reserveErr, status.Reserved, status.CPU, status.Memory)
	} else {
		reservation.End()
		close(preflightJoined)
		reservation.Finish()
	}
	nextPeer, err := dirStore.Resolve(dstID.Host)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = dirStore.Call(t.Context(), dstID.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var nextScope model.NodeAccessScope
	if err = mesh.Decode(raw, &nextScope); err != nil {
		t.Fatal(err)
	}
	probe, err = probe.Forward(time.Now(), meshsshrelay.NextAuthority{Local: dirID.Host, DirectPeer: dstID.Host,
		Pair: nextPeer.Pair, EnrollmentRevision: nextScope.Revision, Workspace: id, Generation: access.Generation,
		SSHRelayPermit: true, ValidUntil: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dstService.routedSSHAdmission(t.Context(), meshsshrelay.Outer{Source: dirID.Host, Target: dstID.Host,
		Pair: nextScope.Pair, Certificate: dirID.Fingerprint, Revision: nextScope.Revision, Expires: time.Now().Add(time.Minute)}, probe); err != nil {
		t.Fatal("destination admission:", err)
	}
	outer, err := srcStore.ClientTLS(peer)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := srcStore.RoutedSSHTLS(access.Local, access.Destination, false)
	if err != nil {
		t.Fatal(err)
	}
	connect := func(class, hostKey string, q meshsshrelay.Request) (*tls.Conn, meshsshrelay.Response, error) {
		awaitTransitHeadroom()
		o := open
		o.Route = local.ID()
		o.Class, o.SSHHostKeyPin = class, hostKey
		conn, e := meshsshrelay.Dial(t.Context(), peer.Identity.Address, outer, inner, o, func(context.Context) error { return nil })
		if e != nil {
			return nil, meshsshrelay.Response{}, e
		}
		if e = conn.SetDeadline(time.Now().Add(5 * time.Second)); e != nil {
			conn.Close()
			return nil, meshsshrelay.Response{}, e
		}
		if e = meshsshrelay.WriteRequest(conn, q); e != nil {
			conn.Close()
			return nil, meshsshrelay.Response{}, e
		}
		response, e := meshsshrelay.ReadResponse(conn, q)
		if e != nil {
			conn.Close()
			return nil, meshsshrelay.Response{}, e
		}
		if e = conn.SetDeadline(time.Time{}); e != nil {
			conn.Close()
			return nil, meshsshrelay.Response{}, e
		}
		return conn, response, nil
	}
	waitRelayIdle := func() {
		dirService.sshRouted.Wait()
		dstService.sshRouted.Wait()
	}
	identityConn, identityResponse, err := connect(meshsshrelay.ClassSSHIdentity, strings.Repeat("0", 64), meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationIdentity})
	if err != nil {
		t.Fatal(err)
	}
	_ = identityConn.Close()
	waitRelayIdle()
	if identityResponse.Identity == nil || identityResponse.Identity.Source != srcID.Host || identityResponse.Identity.Target != dstID.Host {
		t.Fatal(identityResponse)
	}
	identity := *identityResponse.Identity
	if _, err = dstStore.Resolve(srcID.Host); err == nil {
		t.Fatal("identity lookup created a direct enrollment")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	command := "printf routed-ssh-works"
	hash := sha256.Sum256([]byte(command))
	control := meshstream.Control{Version: 1, Source: srcID.Host, Target: dstID.Host, Pair: identity.Pair, Revision: identity.Revision,
		Account: identity.Account, Key: local.ID(), PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
		Reason: "actual three node opaque SSH execution", TTL: 30 * time.Second, Resources: model.Resources{CPU: .1},
		CWD: identity.Home, TargetOwner: identity.Owner, HostKeySHA256: grant.SSHHostKeyPin, Kind: "exec", CommandSHA256: hex.EncodeToString(hash[:])}
	if err = control.Validate(); err != nil {
		t.Fatal(err)
	}
	conn, accepted, err := connect(meshsshrelay.ClassSSH, grant.SSHHostKeyPin,
		meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationExecute, Control: &control})
	if err != nil {
		status, _ := directory.Manager.Status()
		t.Fatalf("routed SSH execution admission: %v; reserved=%+v cpu=%+v memory=%+v", err, status.Reserved, status.CPU, status.Memory)
	}
	if !accepted.Accepted {
		conn.Close()
		t.Fatal("destination did not admit original-source SSH stream", accepted)
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(identity.PublicKey))
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	sshConn, channels, requests, err := ssh.NewClientConn(conn, "arm-"+dstID.Host, &ssh.ClientConfig{
		User: control.Account, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(hostKey), Timeout: 5 * time.Second})
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	client := ssh.NewClient(sshConn, channels, requests)
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	out, err := session.CombinedOutput(command)
	_ = client.Close()
	waitRelayIdle()
	if err != nil || string(out) != "routed-ssh-works" {
		t.Fatalf("routed original-source execution: %q: %v", out, err)
	}
	lookupConn, lookup, err := connect(meshsshrelay.ClassSSH, grant.SSHHostKeyPin,
		meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationLookup, Key: control.Key})
	if err != nil {
		t.Fatal(err)
	}
	_ = lookupConn.Close()
	waitRelayIdle()
	if lookup.Status == nil || lookup.Status.Execution == nil || lookup.Status.Execution.Operation == nil ||
		lookup.Status.Execution.Operation.Request.Source != srcID.Host || lookup.Status.Execution.Operation.Request.Pair != identity.Pair {
		t.Fatal("routed SSH receipt lost exact original source", lookup)
	}
	sealedControl := control
	sealedControl.Key = local.ID()
	commitment, err := sealedControl.Commitment(srcID.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	sealConn, sealed, err := connect(meshsshrelay.ClassSSH, grant.SSHHostKeyPin,
		meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationSeal,
			Key: sealedControl.Key, Commitment: &commitment, Reason: "prevent delayed original-source execution"})
	if err != nil {
		t.Fatal(err)
	}
	_ = sealConn.Close()
	waitRelayIdle()
	if sealed.Status == nil || sealed.Status.State != "sealed" || sealed.Status.Fence == nil ||
		sealed.Status.Fence.Control == nil || sealed.Status.Fence.Control.Pair != identity.Pair {
		t.Fatal("routed SSH seal lost original-source control", sealed)
	}
	liveControl := control
	liveControl.Key = local.ID()
	liveConn, liveResponse, err := connect(meshsshrelay.ClassSSH, grant.SSHHostKeyPin,
		meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationExecute, Control: &liveControl})
	if err != nil {
		t.Fatal(err)
	}
	defer liveConn.Close()
	if !liveResponse.Accepted {
		t.Fatal("destination did not admit held SSH stream", liveResponse)
	}
	status, err := directory.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	var transitID string
	for _, lease := range status.Leases {
		if lease.State == "active" && strings.HasPrefix(lease.Request.Key, "ssh-relay:") &&
			lease.Request.Resources == (model.Resources{CPU: .01, Memory: 4 << 20}) {
			if lease.Request.Project != "mesh:"+id || !strings.Contains(lease.Request.Reason, srcID.Host) ||
				!strings.Contains(lease.Request.Reason, dstID.Host) || !strings.Contains(lease.Request.Reason, meshsshrelay.ClassSSH) {
				t.Fatal("transit charge omitted exact original source, destination or route class", lease.Request)
			}
			transitID = lease.ID
			break
		}
	}
	if transitID == "" {
		t.Fatal("held SSH relay stream has no active reviewed CPU/memory reservation")
	}
	revoke, err := meshsshrelay.ReviewPolicy(directory.home, dirID.Host, dirID.Fingerprint, "revoke", "close middle hop",
		func(p *meshsshrelay.Policy) error { p.Relays = nil; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(revoke); err != nil {
		t.Fatal(err)
	}
	if err = liveConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, liveConn)
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("revoked relay left held SSH stream open until read deadline")
	}
	_ = liveConn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		lease, inspectErr := directory.Manager.Inspect(transitID)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if lease.State == "terminal" && lease.Outcome == "relay_closed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revoked relay transit reservation was not released: state=%s outcome=%s", lease.State, lease.Outcome)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, _, err = connect(meshsshrelay.ClassSSHIdentity, strings.Repeat("0", 64), meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationIdentity})
	if err == nil {
		t.Fatal("revoked relay admitted a new original-source SSH stream")
	}
}
