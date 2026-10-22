package mesh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/yrashk/arm/internal/model"
	"io"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"
)

// ExpectedEnrollment binds a scoped gateway read to the destination grant
// observed when its window/session was opened. The destination checks this
// under the same peer lock as authorization, not in a separate preliminary RPC.
type ExpectedEnrollment struct {
	Pair     string `json:"pair"`
	Revision int    `json:"revision"`
}
type Request struct {
	Expected  *ExpectedEnrollment `json:"expected_enrollment,omitempty"`
	Version   int                 `json:"version"`
	Host      string              `json:"host"`
	Operation string              `json:"operation"`
	Key       string              `json:"idempotency_key,omitempty"`
	Params    json.RawMessage     `json:"params"`
}
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *model.Error    `json:"error,omitempty"`
}

func (s *Store) WithPeer(host string, fn func(Peer) (any, error)) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, model.Err("mesh_unavailable", "mesh store closed")
	}
	for _, p := range s.config.Peers {
		_, blocked := s.revoking.Load(p.Identity.Host)
		if !blocked && p.Identity.Host == host && p.State == "active" {
			return fn(clonePeer(p))
		}
	}
	return nil, model.Err("forbidden", "peer is not active")
}
func Decode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		return model.Err("invalid_request", e.Error())
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return model.Err("invalid_request", "one JSON value required")
	}
	return nil
}
func Mutating(op string) bool {
	return op == "acquire" || op == "run" || op == "release" || op == "cancel" || op == "renew" || op == "message"
}
func (s *Store) Call(ctx context.Context, target string, r Request) (json.RawMessage, error) {
	peer, e := s.Resolve(target)
	if e != nil {
		return nil, e
	}
	if peer.State != "active" && r.Operation != "hello" {
		return nil, model.Err("forbidden", "peer pairing is not active")
	}
	r.Version = 1
	r.Host = peer.Identity.Host
	config, e := s.ClientTLS(peer)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > 65536 {
		return nil, model.Err("invalid_request", "mesh request exceeds 64KiB")
	}
	request, e := http.NewRequestWithContext(ctx, "POST", "https://"+peer.Identity.Address+"/v1/mesh", bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true, Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	defer transport.CloseIdleConnections()
	response, e := (&http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if e != nil {
		if Mutating(r.Operation) {
			return nil, model.Err("remote_outcome_unknown", fmt.Sprintf("remote operation outcome unknown at host %s; inspect/retry the exact key %q, never choose a new key blindly", peer.Identity.Host, r.Key))
		}
		return nil, model.Err("host_unavailable", e.Error())
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if e != nil || len(data) > 4<<20 {
		return nil, responseFailure(peer.Identity.Host, r, "response unavailable or too large")
	}
	if response.StatusCode != 200 {
		if Mutating(r.Operation) {
			return nil, model.Err("remote_outcome_unknown", fmt.Sprintf("host %s returned HTTP %d; inspect exact key %q before retry", peer.Identity.Host, response.StatusCode, r.Key))
		}
		return nil, model.Err("remote_error", fmt.Sprintf("mesh returned HTTP %d", response.StatusCode))
	}
	var result Response
	if e = Decode(data, &result); e != nil {
		return nil, responseFailure(peer.Identity.Host, r, "malformed or incomplete response")
	}
	hasResult := len(result.Result) > 0 && string(bytes.TrimSpace(result.Result)) != "null"
	if hasResult == (result.Error != nil) {
		return nil, responseFailure(peer.Identity.Host, r, "ambiguous response envelope")
	}
	if result.Error != nil {
		if result.Error.Code == "" {
			return nil, responseFailure(peer.Identity.Host, r, "invalid response error")
		}
		if Mutating(r.Operation) && (result.Error.Code == "mesh_error" || result.Error.Code == "operation_failed") {
			return nil, responseFailure(peer.Identity.Host, r, "target reported an uncertain internal failure")
		}
		return nil, result.Error
	}
	return result.Result, nil
}
func Key(peer, key string) string {
	sum := sha256.Sum256([]byte("arm-mesh-key-v1\x00" + peer + "\x00" + key))
	return "mesh:" + hex.EncodeToString(sum[:])
}
func ValidKey(key string) bool {
	if len(key) == 0 || len(key) > 128 || !utf8.ValidString(key) {
		return false
	}
	for _, r := range key {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

func responseFailure(host string, request Request, detail string) error {
	if Mutating(request.Operation) {
		return model.Err("remote_outcome_unknown", fmt.Sprintf("host %s operation %s outcome unknown (%s); inspect/retry exact key %q, never choose a new key blindly", host, request.Operation, detail, request.Key))
	}
	return model.Err("remote_error", fmt.Sprintf("host %s: %s", host, detail))
}
