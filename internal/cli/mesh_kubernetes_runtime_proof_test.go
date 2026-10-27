package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestKubernetesRuntimeProofUsesExactAuthenticatedLocalEndpoint(t *testing.T) {
	owner := strconv.Itoa(os.Geteuid())
	cpu, memory, disk := 0.25, int64(256<<20), int64(512<<20)
	status := model.Status{Host: strings.Repeat("a", 32), Owner: owner, CPU: &model.CPUBudget{Ceiling: &cpu}, Memory: &model.MemoryBudget{Ceiling: &memory}, Disk: &model.DiskBudget{Ceiling: &disk}}
	token := strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		var request model.WireRequest
		if err := wsjson.Read(r.Context(), conn, &request); err != nil || request.Operation != "status" {
			return
		}
		_ = wsjson.Write(r.Context(), conn, model.WireResponse{Version: 1, ID: request.ID, Result: status})
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/ws"
	home := t.TempDir()
	endpoint := local.Endpoint{URL: url, Token: token, PID: 123, Generation: strings.Repeat("c", 32)}
	raw, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "endpoint.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	proof, err := verifiedLocalKubeRuntime(context.Background(), home, url)
	if err != nil || proof.Host != status.Host || proof.Generation != endpoint.Generation || proof.CPU != "0.25" || proof.Memory != strconv.FormatInt(memory, 10) || proof.Disk != strconv.FormatInt(disk, 10) {
		t.Fatalf("authenticated runtime proof = %+v, %v", proof, err)
	}
	if _, err := verifiedLocalKubeRuntime(context.Background(), home, "ws://127.0.0.1:9443/v1/ws"); err == nil {
		t.Fatal("changed endpoint URL was accepted")
	}
	status.CPU.Ceiling = nil
	if _, err := verifiedLocalKubeRuntime(context.Background(), home, url); err == nil {
		t.Fatal("runtime without reviewed CPU ceiling was accepted")
	}
}

func TestKubernetesRuntimeProofRefusesRedirectBeforeBearerDisclosure(t *testing.T) {
	var requests atomic.Int32
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "redirect target must not be contacted", http.StatusForbidden)
	}))
	defer redirected.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirected.URL+"/v1/ws", http.StatusFound)
	}))
	defer origin.Close()
	endpointURL := "ws" + strings.TrimPrefix(origin.URL, "http") + "/v1/ws"
	home := t.TempDir()
	ep := local.Endpoint{URL: endpointURL, Token: strings.Repeat("b", 64), PID: 123, Generation: strings.Repeat("c", 32)}
	raw, err := json.Marshal(ep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "endpoint.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifiedLocalKubeRuntime(t.Context(), home, endpointURL); err == nil {
		t.Fatal("redirected endpoint acquired runtime proof")
	}
	if requests.Load() != 0 {
		t.Fatal("private runtime request followed redirect to another listener")
	}
}
