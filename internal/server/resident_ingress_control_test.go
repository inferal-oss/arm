package server

import (
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func pendingControlDataServer() *Server {
	return &Server{resident: &residentServerPending{ingress: &residentIngressPending{}}}
}

func TestPendingControlEnvelopeDataBoundsAndLegacyNoop(t *testing.T) {
	s := pendingControlDataServer()
	request := model.WireRequest{ID: strings.Repeat("i", 256), Operation: strings.Repeat("o", 64), Params: make([]byte, 65536)}
	if !s.pendingRequestEnvelopeFits(request) {
		t.Fatal("exact DATA limits refused")
	}
	for _, dimension := range []string{"id", "operation", "params"} {
		t.Run(dimension, func(t *testing.T) {
			v := request
			switch dimension {
			case "id":
				v.ID += "x"
			case "operation":
				v.Operation += "x"
			case "params":
				v.Params = append(append([]byte{}, v.Params...), 0)
			}
			if s.pendingRequestEnvelopeFits(v) {
				t.Fatal("oversize pending DATA accepted")
			}
			if !(&Server{}).pendingRequestEnvelopeFits(v) {
				t.Fatal("legacy request behavior changed")
			}
		})
	}
	base := model.WireResponse{Version: 1, ID: "i", Result: residentRuntimeInspection{Schema: residentPendingControlSchema, Generation: strings.Repeat("a", 32), SettingsSHA256: strings.Repeat("b", 64), Phase: "core_bound_worker_pending"}}
	if !s.pendingResponseFits(base) {
		t.Fatal("bounded diagnosis DATA refused")
	}
	base.Result = strings.Repeat("x", 16384)
	if s.pendingResponseFits(base) {
		t.Fatal("unbounded/general domain projection accepted")
	}
	if !(&Server{}).pendingResponseFits(base) {
		t.Fatal("legacy response behavior changed")
	}
}

func TestPendingControlDiagnosisCannotProjectReadyOrOrdinaryStatus(t *testing.T) {
	s := pendingControlDataServer()
	for _, value := range []residentRuntimeInspection{
		{Generation: strings.Repeat("a", 32), SettingsSHA256: strings.Repeat("b", 64), Phase: "core_bound_worker_pending"},
		{Schema: residentPendingControlSchema, Generation: strings.Repeat("a", 32), SettingsSHA256: strings.Repeat("b", 64), Phase: "core_bound_worker_pending", Ready: true},
		{Schema: residentPendingControlSchema, Generation: strings.Repeat("a", 32), SettingsSHA256: strings.Repeat("b", 64), Phase: "ready"},
	} {
		if s.pendingResponseFits(model.WireResponse{Version: 1, ID: "i", Result: value}) {
			t.Fatal("pending diagnosis accepted an ordinary or ready schema/state")
		}
	}
}

type rejectedControlMarshaler struct{ calls int }

func (m *rejectedControlMarshaler) MarshalJSON() ([]byte, error) {
	m.calls++
	panic("general domain marshaler must not run for pending diagnosis")
}

func TestPendingControlRejectsGeneralProjectionBeforeMarshal(t *testing.T) {
	value := &rejectedControlMarshaler{}
	if pendingControlDataServer().pendingResponseFits(model.WireResponse{Version: 1, ID: "i", Result: value}) {
		t.Fatal("general projection accepted")
	}
	if value.calls != 0 {
		t.Fatal("unbounded domain encoding was invoked")
	}
}

func TestPendingControlDiagnosisEmptyParamsData(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", " {} "} {
		if !residentControlEmptyParamsData([]byte(raw)) {
			t.Fatalf("empty data %q refused", raw)
		}
	}
	for _, raw := range []string{`{"extra":1}`, `[]`, `{} {}`, `{"id":"x"}`} {
		if residentControlEmptyParamsData([]byte(raw)) {
			t.Fatalf("nonempty data %q accepted", raw)
		}
	}
}

func TestPendingControlUnissuedOwnerCannotAuthorizeOperations(t *testing.T) {
	s := pendingControlDataServer()
	caller := &authenticatedCaller{owner: s, proof: &ingressProof{}}
	for _, operation := range []string{"status", "startup_inspect", "resident_runtime_inspect", "subscribe", "monitor_subscribe", "mesh_operator_watch", "run", "shutdown"} {
		if err := s.guardPendingIngressOperation(caller, operation); err != errResidentServerOriginal {
			t.Fatalf("unissued %s = %v", operation, err)
		}
	}
	if err := (&Server{}).guardPendingIngressOperation(nil, "run"); err != nil {
		t.Fatalf("legacy gate changed: %v", err)
	}
}
