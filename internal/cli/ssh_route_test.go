package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/yrashk/arm/internal/local"
)

func TestRoutedSSHContextUsesExactCancellationForStalledBackend(t *testing.T) {
	hold, entered := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		close(entered)
		<-hold // Deliberately withhold the local backend response.
	}))
	defer func() { close(hold); srv.Close() }()
	home := t.TempDir()
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{
		URL: strings.Replace(srv.URL, "http://", "ws://", 1), Token: "fixture", PID: 1}); err != nil {
		t.Fatal(err)
	}
	a := app{home: home, ctx: context.Background()}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := a.routedSSHContext(ctx, strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatal("routed SSH recheck ignored its exact cancellation context", err)
	}
	select {
	case <-entered:
	default:
		t.Fatal("stalled backend fixture was not reached")
	}
}
