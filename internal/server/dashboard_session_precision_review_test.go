package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDashboardSessionWideIntegerREST(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("wire fixture covers native 64-bit revision range")
	}
	s := meshFixture(t)
	h, ctx := loginLinkServer(t, s)
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal("read disposable authority")
	}
	local64, remote64 := int64(9007199254740993), int64(9223372036854775807)
	// Synthetic revision fields on an owned local context exercise the real HTTP
	// encoder. This does not claim a remote enrollment at these revisions.
	bound := nodeSession{Host: status.Host, Source: status.Host, Local: true, LocalRevision: int(local64), RemoteRevision: int(remote64)}
	nonce := strings.Repeat("1", 32)
	issue := func(t *testing.T, context nodeSession) string {
		t.Helper()
		ticket, _, err := s.browser.issueNode(context, nonce, time.Now())
		if err != nil {
			t.Fatal("issue disposable scoped ticket")
		}
		return ticket
	}
	exchange := func(t *testing.T, ticket, providedNonce string) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"ticket": ticket, "host": status.Host, "window_nonce": providedNonce})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL+"/v1/browser/session", bytes.NewReader(raw))
		if err != nil {
			t.Fatal("create session request")
		}
		request.Header.Set("Origin", h.URL)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal("exchange disposable scoped ticket")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if err != nil {
			t.Fatal("read session response")
		}
		return response.StatusCode, body
	}
	ticket := issue(t, bound)
	t.Run("exact_native_JSON_integers", func(t *testing.T) {
		code, body := exchange(t, ticket, nonce)
		if code != http.StatusOK {
			t.Fatalf("session HTTP status %d", code)
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var value map[string]any
		if decoder.Decode(&value) != nil {
			t.Fatal("decode session JSON")
		}
		context, ok := value["context"].(map[string]any)
		if !ok || context["host"] != status.Host || context["source_host"] != status.Host || context["local"] != true {
			t.Fatal("session lost exact native context")
		}
		for key, want := range map[string]int64{"local_revision": local64, "remote_revision": remote64} {
			number, ok := context[key].(json.Number)
			if !ok || number.String() != strconv.FormatInt(want, 10) {
				t.Fatalf("%s lost native integer precision", key)
			}
		}
		token, ok := value["token"].(string)
		if !ok || len(token) != 64 {
			t.Fatal("missing private scoped credential")
		}
	})
	t.Run("one_use_preserved", func(t *testing.T) {
		if code, _ := exchange(t, ticket, nonce); code != http.StatusUnauthorized {
			t.Fatal("consumed scoped ticket accepted")
		}
	})
	t.Run("wrong_nonce_refused_without_consuming_ticket", func(t *testing.T) {
		next := issue(t, bound)
		if code, _ := exchange(t, next, strings.Repeat("2", 32)); code != http.StatusUnauthorized {
			t.Fatal("wrong nonce accepted")
		}
		if code, _ := exchange(t, next, nonce); code != http.StatusOK {
			t.Fatal("correct nonce no longer usable after refusal")
		}
	})
	t.Run("changed_authority_refused", func(t *testing.T) {
		changed := bound
		changed.Source = strings.Repeat("3", 32)
		if code, _ := exchange(t, issue(t, changed), nonce); code != http.StatusUnauthorized {
			t.Fatal("changed authority accepted")
		}
	})
}
