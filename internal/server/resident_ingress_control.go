package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"getarm.org/internal/model"
)

const residentPendingControlSchema = "arm.resident-control.pending.v1"

type residentRuntimeInspection struct {
	Schema               string `json:"schema"`
	Generation           string `json:"backend_generation"`
	SettingsSHA256       string `json:"settings_sha256"`
	Phase                string `json:"phase"`
	Ready                bool   `json:"ready"`
	ManagerLoopsLaunched bool   `json:"manager_loops_launched"`
	HTTPServeEntered     bool   `json:"control_serve_entered"`
	HTTPServeReturned    bool   `json:"control_serve_returned"`
	Unresolved           bool   `json:"unresolved"`
}

func residentControlEmptyParamsData(raw []byte) bool {
	v := bytes.TrimSpace(raw)
	return len(v) == 0 || bytes.Equal(v, []byte("null")) || bytes.Equal(v, []byte("{}"))
}

func (p *residentIngressPending) inspectOriginalRuntime(raw []byte) (residentRuntimeInspection, error) {
	var value residentRuntimeInspection
	if !p.original() || !residentControlEmptyParamsData(raw) {
		return value, errResidentServerOriginal
	}
	value.Generation = p.server.server.generation
	value.Schema = residentPendingControlSchema
	value.SettingsSHA256 = p.root.bootstrap.SettingsSHA256
	value.Phase = "core_bound_worker_pending"
	p.server.mu.Lock()
	value.ManagerLoopsLaunched = p.server.started
	p.server.mu.Unlock()
	p.mu.Lock()
	if p.serve != nil {
		value.HTTPServeEntered, value.HTTPServeReturned = p.serve.entered, p.serve.returned
		value.Unresolved = p.serve.abnormal || p.firstError != nil
	}
	p.mu.Unlock()
	if value.Unresolved || value.HTTPServeReturned {
		value.Phase = "resident_control_unavailable"
	}
	return value, nil // Ready stays false, no native/resource closure projection
}

func (s *Server) originalPendingIngress() *residentIngressPending {
	if s == nil || s.resident == nil {
		return nil
	}
	o := s.resident
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ingress
}

func (s *Server) guardPendingIngressOperation(caller *authenticatedCaller, operation string) error {
	p := s.originalPendingIngress()
	if p == nil {
		return nil
	} // unchanged legacy/unpublished prepared Server
	if !p.original() || p.server.server != s || caller == nil || caller.owner != s || caller.proof == nil {
		return errResidentServerOriginal
	}
	if caller.proof.admission.Browser || caller.proof.admission.Node != nil || caller.proof.nativeNode {
		return model.Err("forbidden", "pending resident control accepts only original local native callers")
	}
	switch operation {
	case "status", "startup_inspect", "resident_runtime_inspect":
		return nil
	default:
		return model.Err("resident_worker_pending", "resident control is pending; ordinary API/UI admission is not ready")
	}
}

func (s *Server) pendingResponseFits(response model.WireResponse) bool {
	if s.originalPendingIngress() == nil {
		return true
	}
	// Bound the actual K0 projection BEFORE invoking arbitrary result encoding.
	// The later marshal check is only the queued encoded-envelope limit, not a
	// bound on a general domain Status or an unbounded MarshalJSON implementation.
	if response.Version != 1 || len(response.ID) > 256 || response.Event != nil || response.Telemetry != nil || response.Subscription != "" || response.NamingDiagnostic != nil {
		return false
	}
	if response.Error != nil && (len(response.Error.Code) > 64 || len(response.Error.Message) > 1024) {
		return false
	}
	if response.Result != nil {
		value, ok := response.Result.(residentRuntimeInspection)
		if !ok || value.Schema != residentPendingControlSchema || value.Ready || len(value.Generation) != 32 || len(value.SettingsSHA256) != 64 || value.Phase != "core_bound_worker_pending" && value.Phase != "resident_control_unavailable" {
			return false
		}
	}
	raw, err := json.Marshal(response)
	return err == nil && len(raw) < residentControlResponseBytes // include Encoder's newline
}

func (s *Server) pendingRequestEnvelopeFits(request model.WireRequest) bool {
	if s.originalPendingIngress() == nil {
		return true
	}
	// Bound strings BEFORE error reflection/projection/marshaling. The existing
	// WS full-input limit separately bounds decoding and raw Params. All three
	// K0 operations return the fixed bounded diagnosis shape, never full Status.
	return len(request.ID) <= 256 && len(request.Operation) <= 64 && len(request.Params) <= residentControlRequestBytes
}

func (p *residentIngressPending) serveControl(w http.ResponseWriter, r *http.Request) {
	if !p.original() || r == nil {
		return
	}
	conn, ok := r.Context().Value(residentIngressConnectionKey{}).(*residentIngressConnection)
	if !ok || conn.owner != p || conn.beginHandler(r.Context()) != nil {
		return
	}
	normal := false
	defer conn.finishHandler(&normal)
	s := p.server.server
	if !p.original() || !s.validHost(r.Host) || r.Method != http.MethodGet || r.URL.Path != "/v1/ws" || r.URL.RawQuery != "" || len(r.Header.Values("Arm-Node-Context")) != 0 {
		http.Error(w, "pending resident control unavailable", http.StatusForbidden)
		normal = true
		return
	}
	admission, authorized := s.authenticateAdmission(r, time.Now())
	if !authorized || admission.Browser || admission.Node != nil || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "local native authorization required", http.StatusUnauthorized)
		normal = true
		return
	}
	// Actual Server owns auth/caller registration/prepared dispatch/sink/WS joins.
	// The new operation guard runs BEFORE its subscription/monitor/watch branches.
	s.ws(w, r)
	normal = true
}
