package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/meshsshrelay"
)

func TestForwardHTTPClientProductionFence(t *testing.T) {
	if meshsshrelay.ForwardReviewEnabled {
		t.Skip("development forwarding build")
	}
	var out, stderr bytes.Buffer
	code := Main([]string{"--home", t.TempDir(), "mesh", "forward", "http", "--key", strings.Repeat("a", 32), "--token-file", "/missing"}, strings.NewReader(""), &out, &stderr)
	if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "disabled while acceptance gates remain incomplete") {
		t.Fatalf("ordinary build opened HTTP forwarding: code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
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
