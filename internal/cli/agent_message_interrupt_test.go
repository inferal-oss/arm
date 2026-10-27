package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/cli"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func TestAgentMessageInterruptedCLIHelper(t *testing.T) {
	if os.Getenv("ARM_AGENT_MESSAGE_INTERRUPT_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i + 1
			break
		}
	}
	if separator == 0 {
		os.Exit(97)
	}
	os.Exit(cli.Main(os.Args[separator:], strings.NewReader(""), os.Stdout, os.Stderr))
}
func TestAgentMessageInterruptedCLIRetainsOriginalKeyAndUnknownOutcome(t *testing.T) {
	entered := make(chan model.WireRequest, 1)
	var calls atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		var request model.WireRequest
		if err := wsjson.Read(r.Context(), conn, &request); err != nil {
			return
		}
		calls.Add(1)
		entered <- request
		// Intentionally do not answer after the request crossed the wire. An
		// interrupted client cannot know whether the backend committed it.
		_, _, _ = conn.Read(r.Context())
	}))
	defer httpServer.Close()
	home := t.TempDir()
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: strings.Replace(httpServer.URL, "http://", "ws://", 1), Token: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	key := "interrupt-original-key"
	command := exec.CommandContext(ctx, executable, "-test.run=^TestAgentMessageInterruptedCLIHelper$", "--", "--home", home, "agent", "send", "--id", "sender", "--session", "b49339cf-a8fd-4a62-98d5-5d892be0589a", "--registration", "exact-registration", "--to", "operator", "--text", "interrupted request", "--key", key)
	command.Env = append(os.Environ(), "ARM_AGENT_MESSAGE_INTERRUPT_HELPER=1")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waited := false
	defer func() {
		if !waited {
			command.Process.Kill()
			<-done
		}
	}()
	select {
	case request := <-entered:
		if request.Operation != "agent_message" || !bytes.Contains(request.Params, []byte(key)) {
			t.Fatalf("wrong request %+v", request)
		}
	case <-ctx.Done():
		t.Fatal("CLI never reached uncertain send boundary")
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		waited = true
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 130 {
			t.Fatalf("exit %v stdout=%s stderr=%s", err, &stdout, &stderr)
		}
	case <-ctx.Done():
		t.Fatal("interrupted CLI did not exit")
	}
	var result struct {
		Error *model.Error `json:"error"`
	}
	if stdout.Len() != 0 || json.Unmarshal(stderr.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "agent_message_outcome_unknown" || !strings.Contains(result.Error.Message, key) || !strings.Contains(result.Error.Message, "message-lookup") {
		t.Fatalf("interruption lost key/uncertainty: stdout=%s stderr=%s", &stdout, &stderr)
	}
	if calls.Load() != 1 {
		t.Fatal("interrupted CLI retried delivery", calls.Load())
	}
}
