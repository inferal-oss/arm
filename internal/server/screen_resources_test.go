package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/inferal-oss/arm/internal/model"
)

func screenTransportLeases(t *testing.T, s *Server) (model.Status, []model.Lease) {
	t.Helper()
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	var leases []model.Lease
	for _, lease := range status.Leases {
		if strings.HasPrefix(lease.Request.Key, "screen-transport:") {
			leases = append(leases, lease)
		}
	}
	return status, leases
}

func requireScreenCharge(t *testing.T, s *Server, state string, charged bool) model.Lease {
	t.Helper()
	status, leases := screenTransportLeases(t, s)
	if len(leases) != 1 || leases[0].State != state {
		t.Fatalf("screen lease state=%s: %+v", state, leases)
	}
	lease := leases[0]
	if lease.Request.Kind != "relay-transit" || lease.Request.Queue || lease.Request.Resources.CPU != screenTransportCPU || lease.Request.Resources.Memory != screenTransportMemory || lease.Request.Resources.Disk != 0 {
		t.Fatalf("unexpected screen resource allocation: %+v", lease)
	}
	if charged && (status.Reserved.CPU != screenTransportCPU || status.Reserved.Memory != screenTransportMemory) || !charged && (status.Reserved.CPU != 0 || status.Reserved.Memory != 0) {
		t.Fatalf("screen charged=%v, reserved=%+v", charged, status.Reserved)
	}
	return lease
}

func denyScreenCPU(t *testing.T, s *Server) {
	t.Helper()
	budget, err := s.Manager.BudgetInspect("self")
	if err != nil {
		t.Fatal(err)
	}
	zero := float64(0)
	_, err = s.Manager.MutateBudget(model.BudgetMutation{Operation: "set", ID: "self", Revision: &budget.Budget.Revision, Limits: &model.BudgetLimits{CPU: &zero}, Reason: "deny transport in disposable admission fixture", Key: "screen-deny-cpu"})
	if err != nil {
		t.Fatal(err)
	}
}

func bindScreenBrowser(t *testing.T, s *Server, bound nodeSession) (*httptest.Server, string, <-chan struct{}) {
	t.Helper()
	joined := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(joined)
		s.browserScreen(w, r)
	}))
	s.authority = server.Listener.Addr().String()
	token := strings.Repeat("c", 64)
	s.browser.nodeSessions = map[[32]byte]nodeCredential{sha256.Sum256([]byte(token)): {Expires: time.Now().Add(time.Minute), Context: bound}}
	server.Start()
	t.Cleanup(server.Close)
	return server, token, joined
}

func dialScreenBrowser(ctx context.Context, server *httptest.Server, token string) (*websocket.Conn, error) {
	c, _, err := websocket.Dial(ctx, "ws://"+server.Listener.Addr().String()+"/v1/screen", &websocket.DialOptions{
		Subprotocols: []string{"arm.v1", "arm.auth." + token}, HTTPHeader: http.Header{"Origin": {server.URL}},
	})
	return c, err
}

func TestScreenResourceReleaseRetainsChargeUntilOriginalJoin(t *testing.T) {
	s := meshFixture(t)
	awaitScreenTransportCapacity(t, s)
	ctx, finish, err := s.reserveScreenTransport(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finish)
	lease := requireScreenCharge(t, s, "active", true)
	if _, err = s.Manager.End(lease.ID, "released"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("release did not cancel original screen context")
	}
	requireScreenCharge(t, s, "ending", true)
	finish()
	settled := requireScreenCharge(t, s, "terminal", false)
	if settled.Outcome != "released" || settled.Ended == nil {
		t.Fatal("lost exact end outcome", settled)
	}
	finish()
	if next := requireScreenCharge(t, s, "terminal", false); next.Revision != settled.Revision {
		t.Fatal("repeated original join changed settlement")
	}
}

func TestScreenResourceAdmissionCannotBypassOwnerPolicy(t *testing.T) {
	var dials atomic.Int32
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		http.Error(w, "must never dial without resources", http.StatusForbidden)
	}))
	denyScreenCPU(t, s)
	server, token, joined := bindScreenBrowser(t, s, bound)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := dialScreenBrowser(ctx, server, token)
	if c != nil {
		c.CloseNow()
	}
	if err == nil {
		t.Fatal("screen admitted despite a zero owner CPU ceiling")
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("refused handler retained work")
	}
	status, leases := screenTransportLeases(t, s)
	if dials.Load() != 0 || len(leases) != 0 || status.Reserved.CPU != 0 || status.Reserved.Memory != 0 {
		t.Fatal("resource refusal started a peer dial or retained a lease", dials.Load(), leases, status.Reserved)
	}
}

func TestScreenResourceReservationCoversPendingUpgrade(t *testing.T) {
	entered, release, destinationJoined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(destinationJoined)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	awaitScreenTransportCapacity(t, s)
	server, token, joined := bindScreenBrowser(t, s, bound)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := dialScreenBrowser(ctx, server, token)
		if c != nil {
			c.CloseNow()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("reviewed native upgrade did not start")
	}
	lease := requireScreenCharge(t, s, "active", true)
	if _, err := s.Manager.End(lease.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []<-chan struct{}{joined, destinationJoined} {
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("canceled upgrade retained original native work")
		}
	}
	if err := <-done; err == nil {
		t.Fatal("canceled pending upgrade succeeded")
	}
	requireScreenCharge(t, s, "terminal", false)
}

func TestScreenResourceJoinedOnReleaseRevocationAndStop(t *testing.T) {
	for _, mode := range []string{"release", "revoke", "stop"} {
		t.Run(mode, func(t *testing.T) {
			destinationJoined := make(chan struct{})
			s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(destinationJoined)
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer c.CloseNow()
				stream := screenNetConn(r.Context(), c)
				defer stream.Close()
				_, _ = io.Copy(stream, stream)
			}))
			awaitScreenTransportCapacity(t, s)
			server, token, joined := bindScreenBrowser(t, s, bound)
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			c, err := dialScreenBrowser(ctx, server, token)
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseNow()
			message := []byte("opaque-screen-transport-fixture")
			if err = c.Write(ctx, websocket.MessageBinary, message); err != nil {
				t.Fatal(err)
			}
			_, echoed, err := c.Read(ctx)
			if err != nil || string(echoed) != string(message) {
				t.Fatal("real upgraded screen route did not carry bytes", string(echoed), err)
			}
			lease := requireScreenCharge(t, s, "active", true)
			switch mode {
			case "release":
				_, err = s.Manager.End(lease.ID, "released")
			case "revoke":
				err = s.mesh.store.Revoke(bound.Host, "revoke disposable original screen context")
			case "stop":
				_, err = s.Manager.BeginShutdown(model.ShutdownRequest{Force: true})
				if err == nil {
					_, err = s.Manager.AdvanceShutdown()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = c.Read(ctx); err == nil {
				t.Fatal("retired screen transport remained open")
			}
			for _, ch := range []<-chan struct{}{joined, destinationJoined} {
				select {
				case <-ch:
				case <-ctx.Done():
					t.Fatal("retired screen workers did not join")
				}
			}
			requireScreenCharge(t, s, "terminal", false)
			s.nodeStopMu.Lock()
			defer s.nodeStopMu.Unlock()
			if s.nodeStopCalls["screen"] != 0 {
				t.Fatal("joined screen retained native Stop census")
			}
		})
	}
}

func TestScreenResourceDeadlineRetainsChargeUntilOriginalJoin(t *testing.T) {
	s := meshFixture(t)
	awaitScreenTransportCapacity(t, s)
	parent, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	ctx, finish, err := s.reserveScreenTransport(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finish)
	requireScreenCharge(t, s, "active", true)
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("screen deadline did not cancel owner")
	}
	status, leases := screenTransportLeases(t, s)
	if len(leases) != 1 || (leases[0].State != "active" && leases[0].State != "ending") || status.Reserved.CPU != screenTransportCPU || status.Reserved.Memory != screenTransportMemory {
		t.Fatalf("deadline reclaimed an unjoined original screen: leases=%+v reserved=%+v", leases, status.Reserved)
	}
	finish()
	settled := requireScreenCharge(t, s, "terminal", false)
	if settled.Ended == nil || settled.Outcome == "completed" {
		t.Fatalf("screen closure claimed application completion: %+v", settled)
	}
}

func TestScreenResourceFailedBrowserUpgradeJoinsAndSettles(t *testing.T) {
	destinationJoined := make(chan struct{})
	var upgraded atomic.Bool
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(destinationJoined)
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		upgraded.Store(true)
		_, _, _ = c.Read(r.Context())
	}))
	awaitScreenTransportCapacity(t, s)
	s.authority = "127.0.0.1:1234"
	token := strings.Repeat("d", 64)
	s.browser.nodeSessions = map[[32]byte]nodeCredential{sha256.Sum256([]byte(token)): {Expires: time.Now().Add(time.Minute), Context: bound}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "http://"+s.authority+"/v1/screen", nil).WithContext(ctx)
	request.Header.Set("Origin", "http://"+s.authority)
	request.Header.Set("Sec-WebSocket-Protocol", "arm.v1, arm.auth."+token)
	// Authentication is exact, but the browser never sends valid Upgrade
	// headers. The separately authenticated outgoing mesh upgrade succeeds first.
	response := httptest.NewRecorder()
	s.browserScreen(response, request)
	if response.Code == http.StatusSwitchingProtocols || !upgraded.Load() {
		t.Fatal("fixture did not fail after the outgoing native upgrade", response.Code, upgraded.Load())
	}
	select {
	case <-destinationJoined:
	case <-ctx.Done():
		t.Fatal("failed browser upgrade retained outgoing mesh socket")
	}
	lease := requireScreenCharge(t, s, "terminal", false)
	if lease.Outcome != "relay_closed" {
		t.Fatalf("transport closure misreported application outcome: %s", lease.Outcome)
	}
	s.nodeStopMu.Lock()
	defer s.nodeStopMu.Unlock()
	if s.nodeStopCalls["screen"] != 0 {
		t.Fatal("failed browser upgrade retained native Stop census")
	}
}

func TestScreenResourceDestinationCannotBypassOwnerPolicy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native macOS screen endpoint gate")
	}
	s, bound, right := screenPairAt(t, meshAddress(t))
	denyScreenCPU(t, s)
	s.mesh.screenSlots = make(chan struct{}, 8)
	certificate, err := x509.ParseCertificate(right.ServerTLS().Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:1234/v1/screen", nil)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}
	request.Header.Set("Arm-Screen-Pair", bound.Pair)
	request.Header.Set("Arm-Screen-Revision", strconv.Itoa(bound.LocalRevision))
	response := httptest.NewRecorder()
	s.mesh.meshScreen(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("destination bypassed zero owner CPU ceiling: HTTP%d", response.Code)
	}
	status, leases := screenTransportLeases(t, s)
	if len(leases) != 0 || status.Reserved.CPU != 0 || status.Reserved.Memory != 0 {
		t.Fatal("refused destination retained transport allocation", leases, status.Reserved)
	}
	s.mesh.mu.Lock()
	defer s.mesh.mu.Unlock()
	if len(s.mesh.screens) != 0 {
		t.Fatal("refused destination retained screen registry")
	}
}

// Positive transport fixtures need real current native capacity. Waiting here
// performs read-only observations before dispatch; it neither retries an
// operation nor changes the native observation, policy or .05CPU/8MiB request.
func awaitScreenTransportCapacity(t *testing.T, s *Server) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for {
		status, se := s.Manager.Status()
		policy, pe := s.Manager.BudgetInspect("self")
		cpu := status.CPU
		memory := status.Memory
		policyReady := (policy.Available.CPU == nil || *policy.Available.CPU >= screenTransportCPU) && (policy.Available.Memory == nil || *policy.Available.Memory >= screenTransportMemory)
		if se == nil && pe == nil && !status.Draining && cpu != nil && cpu.Known && !cpu.Stale && cpu.Available >= screenTransportCPU && memory != nil && !memory.Stale && memory.Observation.Available != nil && memory.Available >= screenTransportMemory && policyReady {
			return
		}
		if time.Now().After(until) {
			data, _ := json.Marshal(map[string]any{"cpu_budget": cpu, "memory_budget": memory, "reserved": status.Reserved, "policy_limits": policy.Budget.Limits, "policy_available": policy.Available, "status_read_ok": se == nil, "policy_read_ok": pe == nil, "draining": status.Draining})
			t.Fatalf("positive screen fixture lacks actual fresh .05CPU/8MiB headroom: %s", data)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal(t.Context().Err())
		}
	}
}
