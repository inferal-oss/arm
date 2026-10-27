package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/presence"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func peerReviewFixture(t *testing.T) (*Server, *presence.Publication, presence.Candidate) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	m, err := manager.Open(home, model.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	d, err := presence.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	self, err := presence.Publish(d, "ws://127.0.0.1:1/v1/peers", strings.Repeat("a", 32), strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = self.Close() })
	s := &Server{Manager: m, home: home, generation: strings.Repeat("c", 32), peerSlots: make(chan struct{}, 8), presenceDir: d}
	server := httptest.NewServer(http.HandlerFunc(s.peerWebSocket))
	t.Cleanup(server.Close)
	s.authority = strings.TrimPrefix(server.URL, "http://")
	pub, err := presence.Publish(d, "ws://"+s.authority+"/v1/peers", m.HostID(), s.generation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	s.presencePub = pub
	snapshot, err := m.PeerSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	s.peerCache = snapshot
	candidate, err := d.Read(pub.Name)
	if err != nil {
		t.Fatal(err)
	}
	return s, self, candidate
}

func TestReviewPeerCopiedAnnouncementCannotMisattributeInstance(t *testing.T) {
	_, self, target := peerReviewFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := queryPeer(ctx, self, target); err != nil {
		t.Fatal("positive authenticated fixture", err)
	}
	for _, change := range []string{"owner", "record-name"} {
		t.Run(change, func(t *testing.T) {
			copied := target
			if change == "owner" {
				copied.UID = uint32(os.Geteuid() + 1)
			} else {
				copied.Name = "arm-presence-v1-" + strings.Repeat("d", 32) + ".json"
			}
			if _, err := queryPeer(ctx, self, copied); err == nil {
				t.Fatal("copied public locator accepted as a different announcement owner/identity")
			}
		})
	}
}

func TestReviewPeerDiscoveryNeverFollowsAnnouncementRedirect(t *testing.T) {
	_, self, target := peerReviewFixture(t)
	var visited atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visited.Store(true)
		http.Error(w, "not an ARM endpoint", 400)
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/v1/peers", http.StatusFound)
	}))
	defer redirect.Close()
	target.Record.URL = "ws" + strings.TrimPrefix(redirect.URL, "http") + "/v1/peers"
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, _ = queryPeer(ctx, self, target)
	if visited.Load() {
		t.Fatal("local announcement redirected polling to an unreviewed endpoint")
	}
}

func TestReviewPeerServerProvesChallengeBeforeClientSigns(t *testing.T) {
	_, self, target := peerReviewFixture(t)
	var received atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		_ = wsjson.Write(ctx, c, peerChallenge{Nonce: make([]byte, 32)})
		var request json.RawMessage
		if wsjson.Read(ctx, c, &request) == nil {
			received.Store(true)
		}
	}))
	defer fake.Close()
	target.Record.URL = "ws" + strings.TrimPrefix(fake.URL, "http") + "/v1/peers"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, _ = queryPeer(ctx, self, target)
	if received.Load() {
		t.Fatal("unauthenticated challenge obtained local instance signature")
	}
}
