package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/presence"
)

const peerPollLimit = 32
const peerFrameLimit = 64 << 10

type peerChallenge struct {
	Nonce     []byte            `json:"nonce"`
	Identity  presence.Identity `json:"identity"`
	Signature []byte            `json:"signature"`
}
type peerRequest struct {
	Record    string `json:"record"`
	Nonce     []byte `json:"nonce"`
	Signature []byte `json:"signature"`
}
type peerResponse struct {
	Generation string             `json:"generation"`
	Snapshot   model.PeerSnapshot `json:"snapshot"`
	Signature  []byte             `json:"signature"`
}

func peerChallengeProof(nonce []byte, server presence.Identity) []byte {
	raw, _ := json.Marshal(struct {
		Nonce  []byte
		Server presence.Identity
	}{nonce, server})
	return append([]byte("arm-peer-challenge-v2\x00"), raw...)
}
func peerClientProof(serverNonce, clientNonce []byte, server, client presence.Identity) []byte {
	raw, _ := json.Marshal(struct {
		ServerNonce, ClientNonce []byte
		Server, Client           presence.Identity
	}{serverNonce, clientNonce, server, client})
	return append([]byte("arm-peer-client-v2\x00"), raw...)
}
func peerServerProof(serverNonce, clientNonce []byte, server, client presence.Identity, snapshot model.PeerSnapshot) []byte {
	raw, _ := json.Marshal(struct {
		ServerNonce, ClientNonce []byte
		Server, Client           presence.Identity
		Snapshot                 model.PeerSnapshot
	}{serverNonce, clientNonce, server, client, snapshot})
	return append([]byte("arm-peer-server-v2\x00"), raw...)
}
func randomPeerNonce() ([]byte, error) { b := make([]byte, 32); _, e := rand.Read(b); return b, e }

func presenceEnabled(home string) bool {
	_, err := os.Lstat(filepath.Join(home, "presence-disabled"))
	return os.IsNotExist(err)
}

func (s *Server) presenceCurrent() (*presence.Publication, *presence.Directory, string) {
	s.presenceMu.RLock()
	defer s.presenceMu.RUnlock()
	return s.presencePub, s.presenceDir, s.presenceError
}
func (s *Server) presenceLoop(ctx context.Context) {
	d, err := presence.Open()
	if err != nil {
		s.presenceMu.Lock()
		s.presenceError = err.Error()
		s.presenceMu.Unlock()
		return
	}
	s.presenceMu.Lock()
	s.presenceDir = d
	s.presenceMu.Unlock()
	defer func() {
		s.presenceMu.Lock()
		if s.presencePub != nil {
			_ = s.presencePub.Close()
			s.presencePub = nil
		}
		s.presenceDir = nil
		s.presenceMu.Unlock()
		_ = d.Close()
	}()
	refresh := func() {
		s.presenceMu.Lock()
		defer s.presenceMu.Unlock()
		if !presenceEnabled(s.home) {
			if s.presencePub != nil {
				_ = s.presencePub.Close()
				s.presencePub = nil
			}
			s.presenceError = "opted out"
			return
		}
		if s.presencePub == nil {
			var e error
			s.presencePub, e = presence.Publish(d, "ws://"+s.authority+"/v1/peers", s.Manager.HostID(), s.generation)
			if e != nil {
				s.presenceError = e.Error()
				return
			}
		} else if e := s.presencePub.Refresh(); e != nil {
			s.presenceError = e.Error()
			_ = s.presencePub.Close()
			s.presencePub = nil
			return
		}
		s.presenceError = ""
	}
	refresh()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			refresh()
		}
	}
}
func (s *Server) startPresence(ctx context.Context) {
	s.presenceDone = make(chan struct{})
	go func() {
		defer close(s.presenceDone)
		cacheDone := make(chan struct{})
		go func() { defer close(cacheDone); s.peerCacheLoop(ctx) }()
		s.presenceLoop(ctx)
		<-cacheDone
	}()
}

// Only this original, fixed-rate worker queries the database. Untrusted requests
// read a bounded copy, never native probes or an admission/status lock.
func (s *Server) peerCacheLoop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		snapshot, err := s.Manager.PeerSnapshotContext(ctx)
		s.peerCacheMu.Lock()
		s.peerCache, s.peerCacheErr = snapshot, err
		s.peerCacheMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Server) cachedPeerSnapshot() (model.PeerSnapshot, error) {
	s.peerCacheMu.RLock()
	defer s.peerCacheMu.RUnlock()
	snapshot := s.peerCache
	if s.peerCacheErr != nil || snapshot.Version != 1 || time.Since(snapshot.At) < 0 || time.Since(snapshot.At) > 3*time.Second {
		return model.PeerSnapshot{}, errors.New("local peer summary is temporarily unavailable")
	}
	snapshot.Items = append([]model.PeerItem{}, snapshot.Items...)
	return snapshot, nil
}
func (s *Server) waitPresence() {
	if s.presenceDone != nil {
		<-s.presenceDone
	}
}

func (s *Server) peerWebSocket(w http.ResponseWriter, r *http.Request) {
	leave, err := s.beginStopWork("websocket")
	if err != nil {
		http.Error(w, "backend stopping", http.StatusServiceUnavailable)
		return
	}
	defer leave()
	if !s.validHost(r.Host) || r.Method != "GET" || r.URL.Path != "/v1/peers" || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pub, d, _ := s.presenceCurrent()
	if pub == nil || d == nil || !presenceEnabled(s.home) {
		http.Error(w, "peer summaries unavailable", http.StatusServiceUnavailable)
		return
	}
	s.presenceMu.RLock()
	select {
	case s.peerSlots <- struct{}{}:
	default:
		s.presenceMu.RUnlock()
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	s.presenceMu.RUnlock()
	defer func() { <-s.peerSlots }()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(peerFrameLimit)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	nonce, err := randomPeerNonce()
	if err != nil {
		return
	}
	serverIdentity := pub.Identity()
	if wsjson.Write(ctx, c, peerChallenge{Nonce: nonce, Identity: serverIdentity, Signature: pub.Sign(peerChallengeProof(nonce, serverIdentity))}) != nil {
		return
	}
	var req peerRequest
	if wsjson.Read(ctx, c, &req) != nil || len(req.Nonce) != 32 || len(req.Signature) != ed25519.SignatureSize {
		return
	}
	candidate, err := d.Read(req.Record)
	if err != nil || !ed25519.Verify(candidate.Record.PublicKey, peerClientProof(nonce, req.Nonce, serverIdentity, candidate.Identity()), req.Signature) {
		return
	}
	if !presenceEnabled(s.home) {
		return
	}
	snapshot, err := s.cachedPeerSnapshot()
	if err != nil {
		return
	}
	resp := peerResponse{Generation: pub.Record.Generation, Snapshot: snapshot}
	resp.Signature = pub.Sign(peerServerProof(nonce, req.Nonce, serverIdentity, candidate.Identity(), resp.Snapshot))
	_ = wsjson.Write(ctx, c, resp)
}

func queryPeer(ctx context.Context, self *presence.Publication, candidate presence.Candidate) (model.PeerSnapshot, error) {
	var zero model.PeerSnapshot
	if !presence.ValidURL(candidate.Record.URL) {
		return zero, errors.New("invalid peer loopback endpoint")
	}
	endpoint, err := url.Parse(candidate.Record.URL)
	if err != nil {
		return zero, err
	}
	exact := endpoint.Host
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != exact {
			return nil, errors.New("peer endpoint changed")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c, _, err := websocket.Dial(ctx, candidate.Record.URL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		return zero, err
	}
	defer c.CloseNow()
	c.SetReadLimit(peerFrameLimit)
	var challenge peerChallenge
	if err = wsjson.Read(ctx, c, &challenge); err != nil || len(challenge.Nonce) != 32 || !presence.EqualIdentity(challenge.Identity, candidate.Identity()) || len(challenge.Signature) != ed25519.SignatureSize || !ed25519.Verify(candidate.Record.PublicKey, peerChallengeProof(challenge.Nonce, challenge.Identity), challenge.Signature) {
		return zero, errors.New("invalid peer challenge")
	}
	nonce, err := randomPeerNonce()
	if err != nil {
		return zero, err
	}
	req := peerRequest{Record: self.Name, Nonce: nonce, Signature: self.Sign(peerClientProof(challenge.Nonce, nonce, challenge.Identity, self.Identity()))}
	if err = wsjson.Write(ctx, c, req); err != nil {
		return zero, err
	}
	var resp peerResponse
	if err = wsjson.Read(ctx, c, &resp); err != nil {
		return zero, err
	}
	if resp.Generation != candidate.Record.Generation || resp.Snapshot.Version != 1 || resp.Snapshot.Host != candidate.Record.Host ||
		len(resp.Signature) != ed25519.SignatureSize || !ed25519.Verify(candidate.Record.PublicKey, peerServerProof(challenge.Nonce, nonce, challenge.Identity, self.Identity(), resp.Snapshot), resp.Signature) {
		return zero, errors.New("peer identity or response signature changed")
	}
	if age := time.Since(resp.Snapshot.At); age < 0 || age > 5*time.Second || !validPeerSnapshot(resp.Snapshot) {
		return zero, errors.New("peer summary is invalid or stale")
	}
	return resp.Snapshot, nil
}

func (s *Server) peerOverview(ctx context.Context) (model.PeerOverview, error) {
	select {
	case s.peerSurveySlots <- struct{}{}:
		defer func() { <-s.peerSurveySlots }()
	default:
		return model.PeerOverview{}, errors.New("peer survey busy")
	}
	ctx, cancelOverview := context.WithTimeout(ctx, 2*time.Second)
	defer cancelOverview()
	local, err := s.cachedPeerSnapshot()
	if err != nil {
		return model.PeerOverview{}, err
	}
	out := model.PeerOverview{At: time.Now().UTC(), Local: local, Instances: []model.PeerInstance{}, TotalsComplete: true, Advisory: "Peer-reported promises and queued requests are context, not measured use or shared admission authority."}
	pub, d, unavailable := s.presenceCurrent()
	if pub == nil || d == nil || !presenceEnabled(s.home) {
		out.DiscoveryUnavailable = unavailable
		if out.DiscoveryUnavailable == "" {
			out.DiscoveryUnavailable = "local presence unavailable"
		}
		out.TotalsComplete = false
		return out, nil
	}
	scan := d.Scan()
	out.DiscoveryUnavailable = scan.Unavailable
	out.DiscoveryTruncated = scan.Truncated
	out.TotalsComplete = scan.Unavailable == "" && !scan.Truncated
	count := 0
	for _, candidate := range scan.Candidates {
		if candidate.Name == pub.Name {
			continue
		}
		if ctx.Err() != nil {
			out.DiscoveryTruncated = true
			out.TotalsComplete = false
			break
		}
		count++
		if count > peerPollLimit {
			out.DiscoveryTruncated = true
			out.TotalsComplete = false
			break
		}
		item := model.PeerInstance{UID: candidate.UID, Host: candidate.Record.Host, Generation: candidate.Record.Generation, URL: candidate.Record.URL}
		callCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		snapshot, e := queryPeer(callCtx, pub, candidate)
		cancel()
		if e != nil {
			item.Error = "unreachable or unverified"
			out.TotalsComplete = false
		} else {
			item.Snapshot = &snapshot
		}
		out.Instances = append(out.Instances, item)
	}
	first := map[string]int{}
	for i := range out.Instances {
		item := &out.Instances[i]
		if item.Snapshot == nil {
			continue
		}
		identity := fmt.Sprintf("%d/%s", item.UID, item.Host)
		if prior, found := first[identity]; found {
			out.Instances[prior].Snapshot = nil
			out.Instances[prior].Error = "duplicate live identity"
			item.Snapshot = nil
			item.Error = "duplicate live identity"
			out.TotalsComplete = false
		} else {
			first[identity] = i
		}
	}
	if err := accumulatePeerTotals(&out); err != nil {
		return model.PeerOverview{}, err
	}
	return out, nil
}
func accumulatePeerTotals(out *model.PeerOverview) error {
	for i := range out.Instances {
		item := &out.Instances[i]
		if item.Snapshot == nil {
			continue
		}
		held, e1 := managerAddPeer(out.PeerHeld, item.Snapshot.Held)
		queued, e2 := managerAddPeer(out.PeerQueued, item.Snapshot.Queued)
		if e1 != nil || e2 != nil || !validPeerSnapshot(*item.Snapshot) {
			item.Snapshot = nil
			item.Error = "invalid or overflowing peer report"
			out.TotalsComplete = false
			continue
		}
		out.PeerHeld, out.PeerQueued = held, queued
		out.PeerHeldCount += item.Snapshot.HeldCount
		out.PeerQueuedCount += item.Snapshot.QueuedCount
	}
	return nil
}

func managerAddPeer(a, b model.Resources) (model.Resources, error) {
	if !validPeerResources(a) || !validPeerResources(b) || a.Memory > math.MaxInt64-b.Memory || a.Disk > math.MaxInt64-b.Disk || math.IsInf(a.CPU+b.CPU, 0) {
		return a, errors.New("invalid peer total")
	}
	return a.Add(b), nil
}
func validPeerResources(r model.Resources) bool {
	return r.CPU >= 0 && !math.IsNaN(r.CPU) && !math.IsInf(r.CPU, 0) && r.Memory >= 0 && r.Disk >= 0
}
func validPeerSnapshot(s model.PeerSnapshot) bool {
	if s.HeldCount < 0 || s.QueuedCount < 0 || s.HeldCount > 1000000 || s.QueuedCount > 1000000 || len(s.Items) > 128 || !validPeerResources(s.Held) || !validPeerResources(s.Queued) {
		return false
	}
	if len(s.Items) > s.HeldCount+s.QueuedCount {
		return false
	}
	if (!s.Truncated && len(s.Items) != s.HeldCount+s.QueuedCount) || (s.Truncated && len(s.Items) != 128) {
		return false
	}
	var held, queued model.Resources
	hc, qc := 0, 0
	for _, item := range s.Items {
		if !validPeerResources(item.Resources) || item.QueuePosition < 0 || item.QueuePosition > 1000000 || item.RequestedTTL < 0 || item.RequestedTTL > 8*24*time.Hour {
			return false
		}
		switch item.State {
		case "active", "provisioning", "ending", "needs_attention", "queued", "reclaiming":
		default:
			return false
		}
		var err error
		if item.State == "queued" {
			qc++
			queued, err = managerAddPeer(queued, item.Resources)
		} else {
			hc++
			held, err = managerAddPeer(held, item.Resources)
		}
		if err != nil {
			return false
		}
	}
	if hc > s.HeldCount || qc > s.QueuedCount || held.CPU > s.Held.CPU || held.Memory > s.Held.Memory || held.Disk > s.Held.Disk || queued.CPU > s.Queued.CPU || queued.Memory > s.Queued.Memory || queued.Disk > s.Queued.Disk {
		return false
	}
	if !s.Truncated && (held != s.Held || queued != s.Queued) {
		return false
	}
	return true
}
