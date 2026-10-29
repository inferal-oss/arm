package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
	"weak"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"github.com/coder/websocket"
)

// Failure/delay is injected at the real accepted connection, never by replacing
// the sink writer or manufacturing a successful receipt.
type responseGateConn struct {
	net.Conn
	mu          sync.Mutex
	armed, fail bool
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
}

func (c *responseGateConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	armed := c.armed
	fail := c.fail
	c.mu.Unlock()
	if armed {
		c.once.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-time.After(4 * time.Second):
			return 0, errors.New("owned write gate timed out")
		}
		if fail {
			return 0, errors.New("owned connection write failed")
		}
	}
	return c.Conn.Write(p)
}

type responseGateListener struct {
	net.Listener
	accepted chan *responseGateConn
}

func (l *responseGateListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &responseGateConn{Conn: c, entered: make(chan struct{}), release: make(chan struct{})}
	l.accepted <- wrapped
	return wrapped, nil
}
func responseFixture(t *testing.T) (*Server, *websocket.Conn, *residentResponseSink, *responseGateConn) {
	t.Helper()
	s := meshFixture(t)
	residentFixtureIdentity(t, s)
	s.token = local.ID() + local.ID()
	h := httptest.NewUnstartedServer(http.HandlerFunc(s.ws))
	listener := &responseGateListener{Listener: h.Listener, accepted: make(chan *responseGateConn, 2)}
	h.Listener = listener
	h.Start()
	s.authority = strings.TrimPrefix(h.URL, "http://")
	c, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(h.URL, "http"), residentNativeOptions(s))
	if err != nil {
		h.Close()
		t.Fatal(err)
	}
	var gated *responseGateConn
	select {
	case gated = <-listener.accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("original connection missing")
	}
	// A real response establishes that the original writer is running.
	response := nativeNodeRequest(t, t.Context(), c, "status", struct{}{})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	caller := residentOnlyCaller(t, s, true)
	s.callers.Lock()
	sink := s.callers.entries[caller].sink
	s.callers.Unlock()
	if sink == nil {
		t.Fatal("original sink missing")
	}
	t.Cleanup(func() {
		defer h.Close()
		gated.mu.Lock()
		select {
		case <-gated.release:
		default:
			close(gated.release)
		}
		gated.mu.Unlock()
		c.CloseNow()
		residentEventually(t, func() bool { _, joined := sink.snapshot(); return joined && residentCallerCount(s) == 0 })
	})
	return s, c, sink, gated
}
func armResponseGate(c *responseGateConn, fail bool) {
	c.mu.Lock()
	c.armed = true
	c.fail = fail
	c.mu.Unlock()
}
func awaitResponseState(t *testing.T, s *Server, r *responseReceipt, state responseState) {
	t.Helper()
	residentEventually(t, func() bool { snapshot, ok := s.responseSettlement(r); return ok && snapshot.State == state })
}

type responsePayload struct {
	count   *atomic.Int32
	padding [64]byte
}

func (p *responsePayload) MarshalJSON() ([]byte, error) {
	p.count.Add(1)
	return []byte(`{"integer":9007199254740993,"decimal":1.0000000000000001}`), nil
}
func enqueueResponsePayload(sink *residentResponseSink, receipt *responseReceipt, count *atomic.Int32) weak.Pointer[responsePayload] {
	payload := &responsePayload{count: count}
	pointer := weak.Make(payload)
	if !sink.enqueue(model.WireResponse{Version: 1, ID: "observed", Result: payload, NamingDiagnostic: &model.Error{Code: "naming_unavailable", Message: "fixture diagnostic"}}, receipt) {
		panic("owned payload not accepted")
	}
	return pointer
}
func collectedResponsePayload(t *testing.T, pointer weak.Pointer[responsePayload]) {
	t.Helper()
	residentEventually(t, func() bool { runtime.GC(); return pointer.Value() == nil })
}

func TestResidentResponseOriginalReceiptAndDelayedWire(t *testing.T) {
	s, c, sink, gate := responseFixture(t)
	r, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	forged := &responseReceipt{owner: r.owner, identity: r.identity, slot: r.slot}
	if _, ok := s.responseSettlement(forged); ok {
		t.Fatal("copied receipt accepted")
	}
	if _, ok := (&Server{}).responseSettlement(r); ok {
		t.Fatal("foreign Server accepted receipt")
	}
	fake := &residentResponseSink{owner: s, caller: sink.caller, connection: sink.connection}
	if _, err := fake.reserve(); err == nil {
		t.Fatal("copied sink bound to original ingress")
	}
	if s.releaseResponseReceipt(r) {
		t.Fatal("pending receipt released")
	}
	armResponseGate(gate, false)
	var count atomic.Int32
	pointer := enqueueResponsePayload(sink, r, &count)
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("original write did not start")
	}
	awaitResponseState(t, s, r, responseWriteAttempt)
	for range 100 {
		observed, ok := s.responseSettlement(r)
		if !ok || observed.State != responseWriteAttempt || observed.TeardownJoined {
			t.Fatal("observer invented completion")
		}
	}
	close(gate.release)
	_, wire, err := c.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"integer":9007199254740993,"decimal":1.0000000000000001`) || !strings.Contains(string(wire), `"naming_diagnostic"`) {
		t.Fatal("wire values changed")
	}
	awaitResponseState(t, s, r, responseWriteSucceeded)
	if count.Load() != 1 {
		t.Fatal("duplicate observer serialized payload", count.Load())
	}
	collectedResponsePayload(t, pointer)
	if !s.releaseResponseReceipt(r) {
		t.Fatal("settled receipt not released")
	}
	next, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.responseSettlement(r); ok || sink.enqueue(model.WireResponse{}, r) {
		t.Fatal("retired receipt resurrected")
	}
	if !sink.cancelReservation(next) {
		t.Fatal("unused reservation not settled")
	}
	awaitResponseState(t, s, next, responseNotEnqueued)
	if !s.releaseResponseReceipt(next) {
		t.Fatal("unused reservation retained")
	}
}

func TestResidentResponseFullQueueWaitsForOriginalWriterJoin(t *testing.T) {
	s, c, sink, gate := responseFixture(t)
	active, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	queued, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	overflow, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	armResponseGate(gate, true)
	if !sink.enqueue(model.WireResponse{Version: 1, ID: "active", Result: true}, active) {
		t.Fatal("active enqueue")
	}
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write missing")
	}
	var count atomic.Int32
	pointer := enqueueResponsePayload(sink, queued, &count)
	for range 63 {
		if !sink.enqueue(model.WireResponse{Version: 1, ID: "queued", Result: true}, nil) {
			t.Fatal("queue capacity reduced")
		}
	}
	if sink.enqueue(model.WireResponse{Version: 1, ID: "overflow"}, overflow) {
		t.Fatal("full queue accepted")
	}
	if got, ok := s.responseSettlement(queued); !ok || got.State != responseQueueOwned || got.TeardownJoined {
		t.Fatal("timeout/cancellation abandoned before original join", got)
	}
	runtime.GC()
	if pointer.Value() == nil {
		t.Fatal("queued payload owner released before join")
	}
	close(gate.release)
	c.CloseNow()
	residentEventually(t, func() bool { _, joined := sink.snapshot(); return joined })
	awaitResponseState(t, s, active, responseWriteFailed)
	awaitResponseState(t, s, queued, responseAbandoned)
	awaitResponseState(t, s, overflow, responseNotEnqueued)
	totals, joined := sink.snapshot()
	// One real readiness response was written before the gated cohort.
	if !joined || totals.Accepted != 66 || totals.Attempted != 2 || totals.Written != 1 || totals.Failed != 1 || totals.Abandoned != 64 || len(sink.out) != 0 {
		t.Fatal("original ownership census mismatch", totals)
	}
	if count.Load() != 0 {
		t.Fatal("abandoned payload was serialized")
	}
	collectedResponsePayload(t, pointer)
	if !s.releaseResponseReceipt(active) || !s.releaseResponseReceipt(queued) || !s.releaseResponseReceipt(overflow) {
		t.Fatal("terminal receipts retained")
	}
}

func TestResidentResponseCancelledWriteAndUnusedReservationSettle(t *testing.T) {
	s, c, sink, gate := responseFixture(t)
	active, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	unused, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	armResponseGate(gate, false)
	if !sink.enqueue(model.WireResponse{Version: 1, ID: "cancelled", Result: true}, active) {
		t.Fatal("enqueue")
	}
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write missing")
	}
	c.CloseNow()
	// The original gated Write has not returned, regardless of context timeout.
	got, _ := s.responseSettlement(active)
	if got.State != responseWriteAttempt || got.TeardownJoined {
		t.Fatal("cancellation claimed joined write", got)
	}
	close(gate.release)
	residentEventually(t, func() bool { _, joined := sink.snapshot(); return joined })
	got, _ = s.responseSettlement(active)
	if !got.State.terminal() || !got.TeardownJoined || got.State == responseAbandoned {
		t.Fatal("attempted write lost its outcome", got)
	}
	awaitResponseState(t, s, unused, responseNotEnqueued)
	if !s.releaseResponseReceipt(active) || !s.releaseResponseReceipt(unused) {
		t.Fatal("retired metadata retained")
	}
}

func TestResidentResponseEnqueueCloseRaceLeavesNoAcceptedOrphans(t *testing.T) {
	s, c, sink, _ := responseFixture(t)
	receipts := make([]*responseReceipt, responseTrackingLimit)
	for i := range receipts {
		r, err := sink.reserve()
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = r
	}
	start := make(chan struct{})
	var senders sync.WaitGroup
	for _, r := range receipts {
		senders.Add(1)
		go func(r *responseReceipt) {
			defer senders.Done()
			<-start
			sink.enqueue(model.WireResponse{Version: 1, ID: "race", Result: true}, r)
		}(r)
	}
	close(start)
	c.CloseNow()
	senders.Wait()
	residentEventually(t, func() bool { _, joined := sink.snapshot(); return joined })
	totals, _ := sink.snapshot()
	if totals.Accepted != totals.Written+totals.Failed+totals.Abandoned || len(sink.out) != 0 {
		t.Fatal("accepted response orphan", totals)
	}
	for _, r := range receipts {
		state, ok := s.responseSettlement(r)
		if !ok || !state.State.terminal() || !state.TeardownJoined || !s.releaseResponseReceipt(r) {
			t.Fatal("receipt not settled", state)
		}
	}
}

func TestResidentResponseTrackingCapacityKeepsPublicResponsesDirect(t *testing.T) {
	s, c, sink, _ := responseFixture(t)
	receipts := make([]*responseReceipt, responseTrackingLimit)
	for i := range receipts {
		r, err := sink.reserve()
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = r
	}
	if _, err := sink.reserve(); err == nil {
		t.Fatal("tracking capacity unbounded")
	}
	response := nativeNodeRequest(t, t.Context(), c, "status", struct{}{})
	if response.Error != nil {
		t.Fatal("tracking capacity blocked public response", response.Error)
	}
	other, _, err := websocket.Dial(t.Context(), "ws://"+s.authority, residentNativeOptions(s))
	if err != nil {
		t.Fatal(err)
	}
	defer other.CloseNow()
	otherResponse := nativeNodeRequest(t, t.Context(), other, "status", struct{}{})
	if otherResponse.Error != nil {
		t.Fatal(otherResponse.Error)
	}
	var otherSink *residentResponseSink
	s.callers.Lock()
	for _, ingress := range s.callers.entries {
		if ingress.sink != nil && ingress.sink != sink {
			otherSink = ingress.sink
		}
	}
	s.callers.Unlock()
	if otherSink == nil {
		t.Fatal("second actual sink missing")
	}
	if _, err := otherSink.reserve(); err == nil {
		t.Fatal("tracking capacity was per connection instead of per Server")
	}
	if otherSink.enqueue(model.WireResponse{Version: 1, ID: "foreign"}, receipts[0]) {
		t.Fatal("cross-connection receipt accepted")
	}
	if state, _ := s.responseSettlement(receipts[0]); state.State != responseReserved {
		t.Fatal("foreign sink altered original reservation")
	}
	for _, r := range receipts {
		if !sink.cancelReservation(r) || !s.releaseResponseReceipt(r) {
			t.Fatal("reservation leaked")
		}
	}
}

func TestResidentResponseMetadataCosts(t *testing.T) {
	value := map[string]uint64{
		"registry_identity":     uint64(unsafe.Sizeof(responseRegistryIdentity{})),
		"server_fixed":          uint64(unsafe.Sizeof(responseTracking{})),
		"sink":                  uint64(unsafe.Sizeof(residentResponseSink{})),
		"identity":              uint64(unsafe.Sizeof(responseSinkIdentity{})),
		"ingress_pointer_delta": uint64(unsafe.Sizeof((*residentResponseSink)(nil))),
		"response_header":       uint64(unsafe.Sizeof(model.WireResponse{})),
		"envelope_header":       uint64(unsafe.Sizeof(responseEnvelope{})),
		"channel_entry_delta":   uint64(unsafe.Sizeof(responseEnvelope{}) - unsafe.Sizeof(model.WireResponse{})),
		"tracked_handle":        uint64(unsafe.Sizeof(responseReceipt{})),
		"tracking_slot":         uint64(unsafe.Sizeof(responseRecord{})),
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resident_response_logical_costs=%s", encoded)
}

func TestResidentResponseEncodingFailureIsNotDelivery(t *testing.T) {
	s, c, sink, gate := responseFixture(t)
	receipt, err := sink.reserve()
	if err != nil {
		t.Fatal(err)
	}
	armResponseGate(gate, false)
	if !sink.enqueue(model.WireResponse{Version: 1, ID: "invalid-result", Result: json.RawMessage(`{`)}, receipt) {
		t.Fatal("response not enqueued")
	}
	residentEventually(t, func() bool { _, joined := sink.snapshot(); return joined })
	state, ok := s.responseSettlement(receipt)
	if !ok || state.State != responseWriteFailed || !state.TeardownJoined {
		t.Fatal("encoding failure claimed delivery", state)
	}
	select {
	case <-gate.entered:
		t.Fatal("malformed result reached native socket write")
	default:
	}
	if _, _, err := c.Read(t.Context()); err == nil {
		t.Fatal("failed encoder produced a response")
	}
	if !s.releaseResponseReceipt(receipt) {
		t.Fatal("encoding failure metadata retained")
	}
}

func TestResidentResponseReceiptDoesNotRetainStoppedServer(t *testing.T) {
	var receipt *responseReceipt
	var original weak.Pointer[Server]
	t.Run("owned_ingress", func(t *testing.T) {
		s, c, sink, _ := responseFixture(t)
		var err error
		receipt, err = sink.reserve()
		if err != nil {
			t.Fatal(err)
		}
		if !sink.enqueue(model.WireResponse{Version: 1, ID: "completed", Result: true}, receipt) {
			t.Fatal("enqueue")
		}
		if _, _, err := c.Read(t.Context()); err != nil {
			t.Fatal(err)
		}
		awaitResponseState(t, s, receipt, responseWriteSucceeded)
		original = weak.Make(s)
	})
	residentEventually(t, func() bool { runtime.GC(); return original.Value() == nil })
	runtime.KeepAlive(receipt)
}
