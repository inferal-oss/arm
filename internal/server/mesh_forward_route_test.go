package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshforward"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/workspace"
)

func TestForwardThreeNodesHTTPExactOriginalAndRevocation(t *testing.T) {
	if !meshsshrelay.ForwardReviewEnabled {
		t.Skip("development-only arm_forward_review build required; production authority is disabled")
	}
	directory, source, destination, id := graphStreamFixtureWith(t, shortForwardMeshFixture)
	samplersStarted := time.Now()
	directory.Manager.Start()
	source.Manager.Start()
	destination.Manager.Start()
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
	// Observe original admission callbacks only; preserve every result and
	// leave the existing native samplers, authority and lifetime unchanged.
	admissionDiagnostic := &forwardAdmissionDiagnostic{epoch: time.Now()}
	admissionDiagnostic.wrap("relay", directory, dirService.forwardRouted)
	admissionDiagnostic.wrap("destination", destination, dstService.forwardRouted)
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
	// The first listener admission needs its own fresh source observation.
	// Policy setup can outlive the startup CPU interval; a recent publication
	// with an unknown or overlong original interval still cannot admit it.
	listenerNeed := model.Resources{CPU: .0001, Memory: 4 << 20}
	sourceReadyUntil := time.Now().Add(10 * time.Second)
	for {
		sourceStatus, statusErr := source.Manager.Status()
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if sourceStatus.CPU != nil && sourceStatus.CPU.Known && !sourceStatus.CPU.Stale &&
			sourceStatus.CPU.Observation.Known && sourceStatus.CPU.Observation.Interval >= 100*time.Millisecond &&
			sourceStatus.CPU.Observation.Interval <= observe.CPUObservationMaxGap &&
			sourceStatus.CPU.Observation.At.After(samplersStarted) && sourceStatus.CPU.Available >= listenerNeed.CPU &&
			sourceStatus.Memory != nil && !sourceStatus.Memory.Stale &&
			sourceStatus.Memory.Observation.At.After(samplersStarted) && sourceStatus.Memory.Available >= listenerNeed.Memory {
			break
		}
		if !time.Now().Before(sourceReadyUntil) {
			t.Fatalf("no current post-Start source listener headroom for %+v: CPU=%+v memory=%+v", listenerNeed, sourceStatus.CPU, sourceStatus.Memory)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	// Setup and grant review can outlive the startup CPU interval. Require
	// current post-Start observations from both actual admission owners just
	// before the first request; never retry a connection or replace a refusal.
	headroomReady := func(status model.Status) bool {
		return status.CPU != nil && status.CPU.Known && !status.CPU.Stale &&
			status.CPU.Observation.At.After(samplersStarted) && status.CPU.Available >= grant.Transit.CPU &&
			status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= grant.Transit.Memory
	}
	headroomUntil := time.Now().Add(5 * time.Second)
	for {
		relayStatus, err := directory.Manager.Status()
		if err != nil {
			t.Fatal(err)
		}
		destinationStatus, err := destination.Manager.Status()
		if err != nil {
			t.Fatal(err)
		}
		if headroomReady(relayStatus) && headroomReady(destinationStatus) {
			break
		}
		if !time.Now().Before(headroomUntil) {
			t.Fatalf("no current post-Start forwarding headroom for %+v: relay CPU=%+v memory=%+v; destination CPU=%+v memory=%+v", grant.Transit, relayStatus.CPU, relayStatus.Memory, destinationStatus.CPU, destinationStatus.Memory)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	admissionDiagnostic.logInitialResult(t)
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
	// The private original Unix worker uses the same pinned B→A→C route and
	// destination grant, but is never installed by public start/dispatch.
	srcService.forwardSourcesMu.Lock()
	original := srcService.forwardSources[forwardKey]
	srcService.forwardSourcesMu.Unlock()
	if original == nil {
		t.Fatal("original source owner missing")
	}
	childJoined := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-childJoined:
		default:
			close(childJoined)
		}
	})
	child, err := original.handle.IssueUnixChild(t.Context(), childJoined)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := child.IssueStoragePair(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	worker, err := original.startPrivateUnixWorker(t.Context(), pair)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = worker.Close(cleanupCtx)
	})
	storageStatus, err := source.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	var unixPath, unixStorageID string
	for _, lease := range storageStatus.Leases {
		if lease.Request.Kind != "storage" || lease.Request.Reason != "Forward Unix source directory for "+original.handle.LeaseID() {
			continue
		}
		unixStorageID = lease.ID
		entries, readErr := os.ReadDir(lease.ManagedDir)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".sock") {
				unixPath = filepath.Join(lease.ManagedDir, entry.Name())
			}
		}
	}
	if unixPath == "" {
		t.Fatal("original Unix socket not found in charged storage directory")
	}
	unixConn, err := net.DialTimeout("unix", unixPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = unixConn.SetDeadline(time.Now().Add(12 * time.Second))
	if _, err = io.WriteString(unixConn, "POST /unix HTTP/1.1\r\nHost: web\r\nConnection: close\r\nContent-Length: 4\r\n\r\nmesh"); err != nil {
		t.Fatal(err)
	}
	unixResponse, err := http.ReadResponse(bufio.NewReader(unixConn), nil)
	if err != nil {
		worker.mu.Lock()
		cleanup, application, active := worker.cleanupErr, worker.applicationErr, len(worker.active)
		worker.mu.Unlock()
		t.Fatalf("original Unix response: %v; cleanup=%v application=%v active=%d", err, cleanup, application, active)
	}
	unixBody, err := io.ReadAll(unixResponse.Body)
	_ = unixResponse.Body.Close()
	_ = unixConn.Close()
	if err != nil || string(unixBody) != "POST:/unix:mesh" {
		t.Fatalf("original Unix routed response %q: %v", unixBody, err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		worker.mu.Lock()
		active := len(worker.active)
		worker.mu.Unlock()
		if active == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	closeCtx, closeCancel := context.WithTimeout(t.Context(), 45*time.Second)
	closeErr := worker.Close(closeCtx)
	closeCancel()
	if closeErr != nil {
		activity := inspectForwardUnixCloseActivity(t.Context(), source.Manager, original.handle.LeaseID(), unixStorageID)
		t.Fatalf("original Unix worker and storage did not settle: %v; original-process activity=%+v", closeErr, activity)
	}
	close(childJoined)
	if err = child.FinishAfterJoin(); err != nil {
		t.Fatal(err)
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
	if conn, _, e := srcService.forwardSourceDial(t.Context(), id, dstID.Host, "web", staleAuthority, func(context.Context) error { return nil }); e == nil {
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

func shortForwardMeshFixture(t *testing.T) *Server {
	t.Helper()
	var root string
	for i := 0; i < 32 && root == ""; i++ {
		var nonce [1]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			t.Fatal(err)
		}
		candidate := filepath.Join("/private/tmp", hex.EncodeToString(nonce[:]))
		if err := os.Mkdir(candidate, 0700); err == nil {
			root = candidate
		} else if !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	if root == "" {
		t.Fatal("short private mesh root unavailable")
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "arm")
	m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 32 << 20, Disk: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return &Server{Manager: m, home: home, lifecycleContext: context.Background()}
}

// This observer records the first real source admission. It never retries,
// supplies authority, or changes a callback result. Later test traffic retains
// the wrapper but recording is disabled after the first HTTP response result.
type forwardAdmissionDiagnostic struct {
	mu      sync.Mutex
	epoch   time.Time
	stopped bool
	omitted uint64
	events  []forwardAdmissionEvent
}

type forwardAdmissionEvent struct {
	Role           string          `json:"role"`
	Stage          string          `json:"stage"`
	Boundary       string          `json:"boundary"`
	AtNS           int64           `json:"at_ns"`
	ElapsedNS      int64           `json:"elapsed_ns,omitempty"`
	Category       string          `json:"category,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	MessageClipped bool            `json:"error_message_clipped,omitempty"`
	Context        string          `json:"context"`
	DeadlineLeftNS *int64          `json:"deadline_left_ns,omitempty"`
	NativeSnapshot json.RawMessage `json:"native_snapshot,omitempty"`
}

func forwardAdmissionCategory(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline"
	}
	if errors.Is(err, io.EOF) {
		return "io_eof"
	}
	var typed *model.Error
	if errors.As(err, &typed) {
		return "model:" + typed.Code
	}
	return fmt.Sprintf("type:%T", err)
}

func (d *forwardAdmissionDiagnostic) record(role, stage, boundary string, ctx context.Context, started time.Time, err error, server *Server) {
	d.mu.Lock()
	stopped := d.stopped
	d.mu.Unlock()
	if stopped {
		return
	}
	row := forwardAdmissionEvent{Role: role, Stage: stage, Boundary: boundary, AtNS: time.Since(d.epoch).Nanoseconds(), Context: forwardAdmissionCategory(ctx.Err())}
	if boundary == "returned" {
		row.ElapsedNS = time.Since(started).Nanoseconds()
		row.Category = forwardAdmissionCategory(err)
		if err != nil {
			row.ErrorMessage = err.Error()
			if len(row.ErrorMessage) > 2048 {
				row.ErrorMessage = row.ErrorMessage[:2048]
				row.MessageClipped = true
			}
		}
	}
	if deadline, ok := ctx.Deadline(); ok {
		left := time.Until(deadline).Nanoseconds()
		row.DeadlineLeftNS = &left
	}
	failed := boundary == "returned" && err != nil
	if failed {
		row.NativeSnapshot = json.RawMessage(`{"pending":true}`)
	}
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	if len(d.events) == 128 {
		d.omitted++
		d.mu.Unlock()
		return
	}
	index := len(d.events)
	d.events = append(d.events, row)
	d.mu.Unlock()
	if failed {
		// Preserve the actual returned error BEFORE any optional snapshot IO.
		// Read only after the original callback released its locks. Never
		// start a sampler or replace its refusal with a later observation.
		status, snapshotErr := server.Manager.Status()
		snapshot, encodeErr := json.Marshal(struct {
			AtNS     int64           `json:"at_ns"`
			Reserved model.Resources `json:"reserved"`
			CPU      any             `json:"cpu"`
			Memory   any             `json:"memory"`
			Error    string          `json:"error"`
		}{time.Since(d.epoch).Nanoseconds(), status.Reserved, status.CPU, status.Memory, forwardAdmissionCategory(snapshotErr)})
		if encodeErr != nil {
			snapshot = json.RawMessage(`{"encoding_failed":true}`)
		}
		d.mu.Lock()
		d.events[index].NativeSnapshot = snapshot
		d.mu.Unlock()
	}
}

func (d *forwardAdmissionDiagnostic) wrap(role string, server *Server, handler *meshsshrelay.Handler) {
	authorize, reserve := handler.Authorize, handler.ReserveTransit
	handler.Authorize = func(ctx context.Context, outer meshsshrelay.Outer, open meshsshrelay.Open) (meshsshrelay.Permit, error) {
		started := time.Now()
		d.record(role, "authorize", "entered", ctx, started, nil, server)
		permit, err := authorize(ctx, outer, open)
		d.record(role, "authorize", "returned", ctx, started, err, server)
		return permit, err
	}
	handler.ReserveTransit = func(admission, lifetime context.Context, joined <-chan struct{}, outer meshsshrelay.Outer, open meshsshrelay.Open, permit meshsshrelay.Permit) (meshsshrelay.TransitReservation, error) {
		started := time.Now()
		d.record(role, "reserve_transit", "entered", admission, started, nil, server)
		reservation, err := reserve(admission, lifetime, joined, outer, open, permit)
		d.record(role, "reserve_transit", "returned", admission, started, err, server)
		if check := reservation.CheckActive; check != nil {
			reservation.CheckActive = func(ctx context.Context) error {
				started := time.Now()
				d.record(role, "check_active", "entered", ctx, started, nil, server)
				err := check(ctx)
				d.record(role, "check_active", "returned", ctx, started, err, server)
				return err
			}
		}
		return reservation, err
	}
}

func (d *forwardAdmissionDiagnostic) logInitialResult(t *testing.T) {
	t.Helper()
	d.mu.Lock()
	d.stopped = true
	raw, err := json.Marshal(struct {
		Events  []forwardAdmissionEvent `json:"events"`
		Omitted uint64                  `json:"omitted"`
	}{d.events, d.omitted})
	d.mu.Unlock()
	if err != nil {
		t.Fatalf("initial forwarding admission diagnostic unavailable: %v", err)
	}
	t.Logf("initial forwarding admission diagnostic: %s", raw)
}
