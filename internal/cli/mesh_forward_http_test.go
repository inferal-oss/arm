package cli

import (
	"errors"

	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/meshsshrelay"
)

func TestForwardHTTPClientProductionFence(t *testing.T) {
	if meshsshrelay.ForwardReviewEnabled {
		t.Skip("development forwarding build")
	}
	var out, stderr bytes.Buffer
	home := filepath.Join(t.TempDir(), "absent-home")
	code := Main([]string{"--home", home, "mesh", "forward", "http", "--key", strings.Repeat("a", 32), "--token-file", "/missing"}, strings.NewReader(""), &out, &stderr)
	if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "disabled while acceptance gates remain incomplete") {
		t.Fatalf("ordinary build opened HTTP forwarding: code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("production fence created state: %v", err)
	}
}

func TestForwardHTTPClientSendsOnlyOriginRequest(t *testing.T) {
	for _, invalid := range []string{"http://elsewhere/", "//elsewhere/", "/bad\r\nInjected: x", "/frag#secret", "/back\\slash", "/raw space"} {
		if _, err := forwardOriginPath(invalid); err == nil {
			t.Fatalf("accepted non-origin path %q", invalid)
		}
	}
	client, service := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		defer service.Close()
		request, err := http.ReadRequest(bufio.NewReader(service))
		if err != nil {
			done <- err
			return
		}
		if request.Method != http.MethodGet || request.URL.RequestURI() != "/item?q=one" || request.Host != "web" || request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Authorization") != "" {
			done <- fmt.Errorf("unexpected forwarded request: %s %s host=%s headers=%v", request.Method, request.URL, request.Host, request.Header)
			return
		}
		_, err = io.WriteString(service, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello")
		done <- err
	}()
	var body bytes.Buffer
	code, err := forwardHTTPRequest(t.Context(), client, http.MethodGet, "/item?q=one", "web", &body)
	if err != nil || code != 200 || body.String() != "hello" {
		t.Fatalf("HTTP result code=%d body=%q err=%v", code, body.String(), err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestForwardHTTPClientDoesNotFollowServiceRedirect(t *testing.T) {
	client, service := net.Pipe()
	defer client.Close()
	go func() {
		defer service.Close()
		_, _ = http.ReadRequest(bufio.NewReader(service))
		_, _ = io.WriteString(service, "HTTP/1.1 302 Found\r\nLocation: http://elsewhere/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}()
	var body strings.Builder
	code, err := forwardHTTPRequest(context.Background(), client, http.MethodGet, "/", "web", &body)
	if err != nil || code != 302 || body.Len() != 0 {
		t.Fatalf("redirect status=%d body=%q err=%v", code, body.String(), err)
	}
}

func TestForwardHTTPClientBoundsServiceResponseHeaders(t *testing.T) {
	client, service := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer service.Close()
		_, _ = http.ReadRequest(bufio.NewReader(service))
		_, _ = io.WriteString(service, "HTTP/1.1 200 OK\r\nX-Large: "+strings.Repeat("x", 16<<10)+"\r\nContent-Length: 0\r\n\r\n")
	}()
	var body bytes.Buffer
	_, err := forwardHTTPRequest(t.Context(), client, http.MethodGet, "/", "web", &body)
	_ = client.Close()
	<-done
	if err == nil || !strings.Contains(err.Error(), "headers exceed bound") || body.Len() != 0 {
		t.Fatalf("oversize response header accepted: body=%q err=%v", body.String(), err)
	}
}

func TestForwardHTTPClientInformationalResponses(t *testing.T) {
	cases := []struct {
		name, response, body, failure string
		method                        string
	}{
		{name: "early hints then success", response: "HTTP/1.1 103 Early Hints\r\nLink: </asset>\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello", body: "hello"},
		{name: "LF response streams body beyond header bound", response: "HTTP/1.1 200 OK\nContent-Length: 20000\n\n" + strings.Repeat("b", 20000), body: strings.Repeat("b", 20000)},
		{name: "aggregate informational header bound", response: "HTTP/1.1 103 Early Hints\r\nX: " + strings.Repeat("a", 9000) + "\r\n\r\nHTTP/1.1 200 OK\r\nX: " + strings.Repeat("b", 9000) + "\r\nContent-Length: 0\r\n\r\n", failure: "headers exceed bound"},
		{name: "bounded informational count", response: strings.Repeat("HTTP/1.1 103 Early Hints\r\n\r\n", 8) + "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n", failure: "informational responses exceed bound"},
		{name: "reject upgrade", response: "HTTP/1.1 101 Switching Protocols\r\nConnection: upgrade\r\nUpgrade: arbitrary\r\n\r\n", failure: "upgrade is unsupported"},
		{name: "HEAD excludes service body", method: http.MethodHead, response: "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, service := net.Pipe()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer service.Close()
				_, _ = http.ReadRequest(bufio.NewReader(service))
				_, _ = io.WriteString(service, tc.response)
			}()
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			var body bytes.Buffer
			code, err := forwardHTTPRequest(t.Context(), client, method, "/", "web", &body)
			_ = client.Close()
			<-done
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) || body.Len() != 0 {
					t.Fatalf("code=%d err=%v output length=%d", code, err, body.Len())
				}
				return
			}
			if err != nil || code != 200 || body.String() != tc.body {
				t.Fatalf("code=%d err=%v output length=%d want=%d", code, err, body.Len(), len(tc.body))
			}
		})
	}
}

func TestForwardHTTPClientAlreadyCanceledSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, service := net.Pipe()
	defer client.Close()
	defer service.Close()
	_, err := forwardHTTPRequest(ctx, client, http.MethodGet, "/", "web", io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request error=%v", err)
	}
}

func TestForwardHTTPClientTerminalGuardPrecedesSourceAccess(t *testing.T) {
	var out bytes.Buffer
	a := &app{ctx: t.Context(), out: &out, outputTTY: true}
	code, err := a.meshForwardHTTP([]string{"--key", strings.Repeat("a", 32), "--token-file", "/missing"})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "terminal") || out.Len() != 0 {
		t.Fatalf("code=%d error=%v stdout=%q", code, err, out.String())
	}
}
