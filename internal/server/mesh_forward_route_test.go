package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/meshsshrelay"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
)

func TestForwardThreeNodesHTTPExactOriginalAndRevocation(t *testing.T) {
	if !meshsshrelay.ForwardReviewEnabled {
		t.Skip("development-only arm_forward_review build required; production authority is disabled")
	}
	directory, source, destination, id := graphStreamFixture(t)
	directory.Manager.Start()
	source.Manager.Start()
	destination.Manager.Start()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, e := directory.Manager.Status()
		if e != nil {
			t.Fatal(e)
		}
		if status.CPU != nil && status.CPU.Known && status.CPU.Available >= .0001 && status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= 4<<20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no current relay CPU/memory samples: cpu=%+v memory=%+v", status.CPU, status.Memory)
		}
		time.Sleep(20 * time.Millisecond)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		status, e := destination.Manager.Status()
		if e != nil {
			t.Fatal(e)
		}
		if status.CPU != nil && status.CPU.Known && status.CPU.Available >= .0001 && status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= 4<<20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no destination CPU/memory samples: cpu=%+v memory=%+v", status.CPU, status.Memory)
		}
		time.Sleep(20 * time.Millisecond)
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
	dirID, srcID, dstID := dirService.store.Config().Identity, srcService.store.Config().Identity, dstService.store.Config().Identity
	if _, err := srcService.store.Resolve(dstID.Host); err == nil {
		t.Fatal("source unexpectedly has direct destination enrollment")
	}
	if _, err := dstService.store.Resolve(srcID.Host); err == nil {
		t.Fatal("destination unexpectedly has direct source enrollment")
	}
	if _, err := destination.workspaceRefresh(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	serviceListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		fmt.Fprintf(w, "%s:%s:%s", r.Method, r.URL.Path, body)
	})}
	go service.Serve(serviceListener)
	t.Cleanup(func() { _ = service.Close(); _ = serviceListener.Close() })
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		for {
			c, e := echoListener.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	t.Cleanup(func() { _ = echoListener.Close(); <-echoDone })
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	access, err := source.Manager.WorkspaceRouteAccess(t.Context(), id, srcID.Host, dstID.Host, workspace.RouteSSH)
	if err != nil {
		t.Fatal(err)
	}
	grant := meshforward.Grant{Workspace: id, Generation: access.Generation, Source: srcID.Host, Target: dstID.Host, SourceGeneration: access.OriginGeneration, TargetGeneration: access.DestinationGeneration,
		DirectoryPin: access.Directory.Fingerprint, DirectorySPKI: access.Directory.SPKI, SourcePin: srcID.Fingerprint, SourceSPKI: srcID.SPKI, TargetPin: dstID.Fingerprint, TargetSPKI: dstID.SPKI,
		Account: account.Username, Endpoint: meshforward.Endpoint{Name: "web", Network: "tcp", Address: serviceListener.Addr().String()}, Revision: 1, MaxConnections: 2, BytesPerConnection: 1 << 20, BytesPerSecond: 64 << 20,
		TotalBytes: 4 << 20, CreditNonce: strings.Repeat("9", 32),
		Transit: model.Resources{CPU: .0001, Memory: 4 << 20}, MaxTTL: time.Minute, Expires: time.Now().Add(time.Hour)}
	echoGrant := grant
	echoGrant.Endpoint = meshforward.Endpoint{Name: "echo", Network: "tcp", Address: echoListener.Addr().String()}
	echoGrant.CreditNonce = strings.Repeat("a", 32)
	smallGrant := echoGrant
	smallGrant.Endpoint.Name = "small"
	smallGrant.BytesPerConnection = 4
	smallGrant.CreditNonce = strings.Repeat("b", 32)
	review, err := meshsshrelay.ReviewPolicy(destination.home, dstID.Host, dstID.Fingerprint, "grant", "exact TCP original source test", func(p *meshsshrelay.Policy) error {
		p.Forwards = append(p.Forwards, grant, echoGrant, smallGrant)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	review, err = meshsshrelay.ReviewPolicy(directory.home, dirID.Host, dirID.Fingerprint, "grant", "exact TCP relay test", func(p *meshsshrelay.Policy) error {
		p.Relays = append(p.Relays, meshsshrelay.RelayGrant{Workspace: id, Generation: access.Generation, Local: dirID.Host, Revision: 1, Transport: true, Transit: model.Resources{CPU: .0001, Memory: 4 << 20}, BytesPerSecond: 64 << 20, Expires: time.Now().Add(time.Hour)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	// The public source listener is owned by the backend, independent of the
	// CLI process. Its token never enters the durable Manager request.
	token := strings.Repeat("a", 64)
	digest, err := meshforward.TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	forwardKey := local.ID()
	startRaw, _ := json.Marshal(forwardSourceRequest{Key: forwardKey, Workspace: id, Target: dstID.Host, Service: "web", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: digest, TTL: time.Minute, Reason: "three-node source listener test"})
	started, err := srcService.forwardSourceOperation(t.Context(), "mesh_forward_start", startRaw)
	if err != nil {
		t.Fatalf("source listener start: %v", err)
	}
	forwardStatus := started.(forwardSourceStatus)
	stopRaw, _ := json.Marshal(map[string]string{"key": forwardKey})
	listed, err := srcService.forwardSourceOperation(t.Context(), "mesh_forward_list", json.RawMessage(`{}`))
	if err != nil || len(listed.([]forwardSourceStatus)) == 0 {
		t.Fatalf("source list missing original listener: %v", err)
	}
	current, err := srcService.forwardSourceOperation(t.Context(), "mesh_forward_status", stopRaw)
	if err != nil || current.(forwardSourceStatus).State != "active" {
		t.Fatalf("source status missing active listener: %+v %v", current, err)
	}
	t.Cleanup(func() {
		_, _ = srcService.forwardSourceOperation(context.Background(), "mesh_forward_stop", stopRaw)
	})
	localConn, err := net.DialTimeout("tcp", forwardStatus.Bind, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = localConn.SetDeadline(time.Now().Add(12 * time.Second))
	_, err = io.WriteString(localConn, "POST /source HTTP/1.1\r\nHost: web\r\nProxy-Authorization: Bearer "+token+"\r\nContent-Length: 5\r\n\r\nhello")
	if err != nil {
		t.Fatal(err)
	}
	localResponse, err := http.ReadResponse(bufio.NewReader(localConn), nil)
	if err != nil {
		// Wait for this original connection owner to publish its actual result;
		// do not open another route and guess why the first one closed.
		var diagnostic forwardSourceStatus
		until := time.Now().Add(2 * time.Second)
		for {
			raw, inspectErr := srcService.forwardSourceOperation(t.Context(), "mesh_forward_status", stopRaw)
			if inspectErr != nil {
				t.Fatalf("source failure %v; original diagnostic unavailable: %v", err, inspectErr)
			}
			diagnostic = raw.(forwardSourceStatus)
			if diagnostic.Active == 0 || time.Now().After(until) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("Manager-owned source forward: %v; original opener=%q connection=%q active=%d", err, diagnostic.LastOpenFailure, diagnostic.LastConnectionFailure, diagnostic.Active)
	}
	localBody, err := io.ReadAll(localResponse.Body)
	_ = localResponse.Body.Close()
	_ = localConn.Close()
	if err != nil || string(localBody) != "POST:/source:hello" {
		t.Fatalf("source response %q: %v", localBody, err)
	}
	stopped, err := srcService.forwardSourceOperation(t.Context(), "mesh_forward_stop", stopRaw)
	if err != nil || stopped.(forwardSourceStatus).State != "terminal" && stopped.(forwardSourceStatus).State != "needs_attention" {
		t.Fatalf("source listener not joined: %+v %v", stopped, err)
	}
	if stopped.(forwardSourceStatus).State == "needs_attention" && !strings.HasPrefix(stopped.(forwardSourceStatus).Failure, "Port reclamation unresolved:") {
		t.Fatalf("unexpected source cleanup uncertainty: %+v", stopped)
	}
	// The same backend path also carries non-HTTP duplex TCP without relying
	// on HTTP parsing or a browser-specific proxy request.
	time.Sleep(600 * time.Millisecond)
	rawKey := local.ID()
	rawStart, _ := json.Marshal(forwardSourceRequest{Key: rawKey, Workspace: id, Target: dstID.Host, Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: digest, TTL: time.Minute, Reason: "three-node raw source listener test"})
	rawStarted, err := srcService.forwardSourceOperation(t.Context(), "mesh_forward_start", rawStart)
	if err != nil {
		t.Fatalf("raw source start: %v", err)
	}
	rawStop, _ := json.Marshal(map[string]string{"key": rawKey})
	t.Cleanup(func() { _, _ = srcService.forwardSourceOperation(context.Background(), "mesh_forward_stop", rawStop) })
	rawLocal, err := net.DialTimeout("tcp", rawStarted.(forwardSourceStatus).Bind, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = rawLocal.SetDeadline(time.Now().Add(12 * time.Second))
	if _, err = io.WriteString(rawLocal, "ARM-FORWARD/1 "+token+"\nmesh-raw-duplex"); err != nil {
		t.Fatal(err)
	}
	rawEcho := make([]byte, len("mesh-raw-duplex"))
	if _, err = io.ReadFull(rawLocal, rawEcho); err != nil || string(rawEcho) != "mesh-raw-duplex" {
		t.Fatalf("backend-owned raw TCP %q: %v", rawEcho, err)
	}
	_ = rawLocal.Close()
	if _, err = srcService.forwardSourceOperation(t.Context(), "mesh_forward_stop", rawStop); err != nil {
		t.Fatal(err)
	}
	// A retained listener must not follow replacement endpoint membership even
	// when stable host IDs, service name and numeric grant revision still match.
	staleAuthority := sourceAuthority(access)
	staleAuthority.Destination.Fingerprint = strings.Repeat("f", 64)
	if conn, _, e := srcService.forwardSourceDial(t.Context(), id, dstID.Host, "web", staleAuthority); e == nil {
		conn.Close()
		t.Fatal("old listener followed replacement endpoint identity")
	} else if !strings.Contains(e.Error(), "original TCP listener endpoint membership changed") {
		t.Fatalf("stale listener reached routing instead of original identity fence: %v", e)
	}
	peer, err := srcService.store.Resolve(dirID.Host)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := srcService.store.Call(t.Context(), dirID.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var scope model.NodeAccessScope
	if err = mesh.Decode(raw, &scope); err != nil {
		t.Fatal(err)
	}
	outer, err := srcService.store.ClientTLS(peer)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := srcService.store.RoutedForwardTLS(access.Local, access.Destination, false)
	if err != nil {
		t.Fatal(err)
	}
	connect := func(serviceName string) (net.Conn, error) {
		// The route carrier also enforces two new opens per neighbor-second.
		time.Sleep(600 * time.Millisecond)
		// Admission uses live native headroom, which may disappear during an
		// unrelated host workload. Wait for a current usable sample first.
		until := time.Now().Add(5 * time.Second)
		for {
			relayStatus, re := directory.Manager.Status()
			finalStatus, fe := destination.Manager.Status()
			if re == nil && fe == nil && relayStatus.CPU != nil && finalStatus.CPU != nil && relayStatus.CPU.Known && finalStatus.CPU.Known && relayStatus.CPU.Available >= .0001 && finalStatus.CPU.Available >= .0001 {
				break
			}
			if time.Now().After(until) {
				return nil, fmt.Errorf("native relay/destination headroom unavailable: relay=%v final=%v", re, fe)
			}
			time.Sleep(20 * time.Millisecond)
		}
		o := meshsshrelay.Open{Workspace: id, Generation: access.Generation, Route: local.ID(), Source: srcID.Host, Target: dstID.Host, SourceGeneration: access.OriginGeneration, TargetGeneration: access.DestinationGeneration, DirectoryRevision: access.DirectoryRevision,
			DirectoryPin: access.Directory.Fingerprint, DirectorySPKI: access.Directory.SPKI, SourcePin: srcID.Fingerprint, TargetPin: dstID.Fingerprint, SSHHostKeyPin: strings.Repeat("0", 64), Service: serviceName, Path: []string{srcID.Host, dirID.Host, dstID.Host}, Hop: 1, Class: meshsshrelay.ClassForward, Expires: time.Now().Add(time.Minute), Pair: scope.Pair, EnrollmentRevision: scope.Revision}
		conn, e := meshsshrelay.Dial(t.Context(), peer.Identity.Address, outer, inner, o, func(context.Context) error { return nil })
		if e != nil {
			return nil, e
		}
		binding, e := o.BindingDigest(time.Now())
		if e != nil {
			conn.Close()
			return nil, e
		}
		q := meshforward.Request{Version: 1, Workspace: id, Generation: access.Generation, Source: srcID.Host, Target: dstID.Host, SourcePin: srcID.Fingerprint, TargetPin: dstID.Fingerprint, Service: serviceName, GrantRevision: 1, ConnectionID: local.ID(), TTL: time.Minute, RouteBindingHash: binding}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if e = meshforward.WriteRequest(conn, q); e == nil {
			e = meshforward.ReadAccepted(conn, q)
		}
		_ = conn.SetDeadline(time.Time{})
		if e != nil {
			conn.Close()
			return nil, e
		}
		return conn, nil
	}
	for i := 0; i < 2; i++ {
		conn, e := connect("web")
		if e != nil {
			t.Fatalf("three-node forward %d: %v", i, e)
		}
		request := fmt.Sprintf("POST /item/%d HTTP/1.1\r\nHost: web\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello", i)
		if _, e = io.WriteString(conn, request); e != nil {
			t.Fatal(e)
		}
		response, e := http.ReadResponse(bufio.NewReader(conn), nil)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		if string(body) != fmt.Sprintf("POST:/item/%d:hello", i) {
			t.Fatalf("wrong forwarded response %q", body)
		}
		conn.Close()
	}
	dirService.forwardRouted.Wait()
	dstService.forwardRouted.Wait()
	echoConn, e := connect("echo")
	if e != nil {
		t.Fatalf("three-node raw TCP: %v", e)
	}
	if _, e = io.WriteString(echoConn, "mesh-raw-duplex"); e != nil {
		t.Fatal(e)
	}
	echoed := make([]byte, len("mesh-raw-duplex"))
	if _, e = io.ReadFull(echoConn, echoed); e != nil || string(echoed) != "mesh-raw-duplex" {
		t.Fatalf("raw duplex response %q: %v", echoed, e)
	}
	echoConn.Close()
	dirService.forwardRouted.Wait()
	dstService.forwardRouted.Wait()
	if conn, e := connect("missing"); e == nil {
		conn.Close()
		t.Fatal("ungranted service admitted")
	}
	dirService.forwardRouted.Wait()
	dstService.forwardRouted.Wait()
	smallConn, e := connect("small")
	if e != nil {
		t.Fatalf("bounded TCP service: %v", e)
	}
	_ = smallConn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(smallConn, "12345678")
	tooLong := make([]byte, 8)
	if _, e = io.ReadFull(smallConn, tooLong); e == nil {
		t.Fatal("destination byte ceiling permitted eight-byte echo through four-byte grant")
	}
	_ = smallConn.Close()
	dirService.forwardRouted.Wait()
	dstService.forwardRouted.Wait()
	status, err := destination.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	seenFinal := false
	for _, lease := range status.Leases {
		if strings.HasPrefix(lease.Request.Key, "tcp-final:") {
			seenFinal = true
			if lease.State != "terminal" || lease.Outcome != "forward_closed" {
				t.Fatalf("destination connection resources not settled after joined streams: state=%s outcome=%s", lease.State, lease.Outcome)
			}
		}
	}
	if !seenFinal {
		t.Fatal("destination forward did not reserve a Manager-owned resource hold")
	}
	routeLoss, e := connect("echo")
	if e != nil {
		t.Fatalf("relay transport revocation setup: %v", e)
	}
	if _, e = io.WriteString(routeLoss, "relay"); e != nil {
		t.Fatal(e)
	}
	answer := make([]byte, 5)
	if _, e = io.ReadFull(routeLoss, answer); e != nil || string(answer) != "relay" {
		t.Fatalf("relay stream did not echo before revoke: %q %v", answer, e)
	}
	review, err = meshsshrelay.ReviewPolicy(directory.home, dirID.Host, dirID.Fingerprint, "revoke", "withdraw generic workspace transport", func(p *meshsshrelay.Policy) error {
		p.Relays = []meshsshrelay.RelayGrant{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	_ = routeLoss.SetReadDeadline(time.Now().Add(4 * time.Second))
	var lost [1]byte
	_, e = routeLoss.Read(lost[:])
	if e == nil {
		t.Fatal("revoked relay transport kept live TCP stream open")
	}
	if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
		t.Fatal("relay transport revocation did not close live stream")
	}
	_ = routeLoss.Close()
	dirService.forwardRouted.Wait()
	dstService.forwardRouted.Wait()
	relayStatus, err := directory.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	seenTransit := false
	for _, lease := range relayStatus.Leases {
		if lease.Request.Kind == "relay-transit" {
			seenTransit = true
			if lease.State != "terminal" || lease.Outcome != "relay_closed" {
				t.Fatalf("revoked relay stream charge not settled after join: state=%s outcome=%s", lease.State, lease.Outcome)
			}
		}
	}
	if !seenTransit {
		t.Fatal("relay stream lacked a Manager-owned transport hold")
	}
	// Restore only generic transport for the independent destination-grant
	// revocation below. The relay still does not select a TCP service.
	review, err = meshsshrelay.ReviewPolicy(directory.home, dirID.Host, dirID.Fingerprint, "grant", "restore generic workspace transport", func(p *meshsshrelay.Policy) error {
		p.Relays = append(p.Relays, meshsshrelay.RelayGrant{Workspace: id, Generation: access.Generation, Local: dirID.Host, Revision: 2, Transport: true, Transit: model.Resources{CPU: .0001, Memory: 4 << 20}, BytesPerSecond: 64 << 20, Expires: time.Now().Add(time.Hour)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	live, e := connect("echo")
	if e != nil {
		t.Fatalf("live revocation setup: %v", e)
	}
	if _, e = io.WriteString(live, "alive"); e != nil {
		t.Fatal(e)
	}
	answer = make([]byte, 5)
	if _, e = io.ReadFull(live, answer); e != nil || string(answer) != "alive" {
		t.Fatalf("live TCP stream did not echo before revoke: %q %v", answer, e)
	}
	review, err = meshsshrelay.ReviewPolicy(destination.home, dstID.Host, dstID.Fingerprint, "revoke", "revoke exact TCP original source", func(p *meshsshrelay.Policy) error { p.Forwards = nil; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = meshsshrelay.CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	_ = live.SetReadDeadline(time.Now().Add(4 * time.Second))
	var b [1]byte
	_, e = live.Read(b[:])
	if e == nil {
		t.Fatal("live revoked TCP stream remained readable")
	}
	if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
		t.Fatal("live revoked TCP stream survived until test deadline")
	}
	_ = live.Close()
	dirService.forwardRouted.Wait()
	dstService.forwardRouted.Wait()
	if conn, e := connect("web"); e == nil {
		conn.Close()
		t.Fatal("revoked destination TCP grant still admitted")
	}
}
