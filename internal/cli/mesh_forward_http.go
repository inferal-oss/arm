package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/inferal-oss/arm/internal/meshforward"
)

// meshForwardHTTP is a token-file-backed HTTP client for one reviewed service.
// It emits only the response body. It never puts the local bearer in a URL,
// application header, or process argument and never follows a redirect.
func (a *app) meshForwardHTTP(args []string) (int, error) {
	f := a.flags("mesh forward http")
	key := f.String("key", "", "exact active source listener key")
	tokenFile := f.String("token-file", "", "private mode-0600 token file from source start")
	method := f.String("method", http.MethodGet, "GET or HEAD")
	path := f.String("path", "/", "one origin-form HTTP path and optional query")
	allowTTY := f.Bool("allow-tty", false, "permit service response bytes on a terminal")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 || len(*key) != 32 || *tokenFile == "" || a.forceJSON {
		return 2, errors.New("exact --key and --token-file required; HTTP body cannot use --json")
	}
	if *method != http.MethodGet && *method != http.MethodHead {
		return 2, errors.New("HTTP client supports GET and HEAD; use an authenticated raw stream for other methods")
	}
	if _, err := forwardOriginPath(*path); err != nil {
		return 2, err
	}
	if a.outputTTY && !*allowTTY {
		return 2, errors.New("HTTP service bytes may control a terminal; pipe output or explicitly use --allow-tty")
	}
	conn, status, err := a.dialForwardSource(*key, *tokenFile)
	if err != nil {
		return 1, err
	}
	defer conn.Close()
	if err := (meshforward.Endpoint{Name: status.Service, Network: "tcp", Address: "127.0.0.1:1"}).Validate(); err != nil {
		return 1, errors.New("source listener has no valid HTTP service name")
	}
	code, err := forwardHTTPRequest(a.ctx, conn, *method, *path, status.Service, a.out)
	if err != nil {
		return 1, fmt.Errorf("HTTP service result unknown; inspect the application before retrying: %w", err)
	}
	if code >= 300 && code < 400 {
		return 1, fmt.Errorf("HTTP service returned redirect status %d; redirect not followed", code)
	}
	if code < 200 || code >= 400 {
		return 1, fmt.Errorf("HTTP service returned status %d", code)
	}
	return 0, nil
}

func forwardOriginPath(path string) (*url.URL, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "#") || len(path) > 4096 {
		return nil, errors.New("exact origin-form HTTP path required")
	}
	for _, c := range path {
		if c <= ' ' || c == 0x7f || c == '\\' {
			return nil, errors.New("exact origin-form HTTP path required")
		}
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" {
		return nil, errors.New("exact origin-form HTTP path required")
	}
	return u, nil
}

func forwardHTTPRequest(ctx context.Context, conn net.Conn, method, path, host string, output io.Writer) (int, error) {
	u, err := forwardOriginPath(path)
	if err != nil {
		return 0, err
	}
	if method != http.MethodGet && method != http.MethodHead {
		return 0, errors.New("unsupported HTTP method")
	}
	finished := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-finished:
		}
	}()
	defer func() { close(finished); <-watchDone }()
	req := &http.Request{Method: method, URL: u, Host: host, Header: make(http.Header), ProtoMajor: 1, ProtoMinor: 1, Close: true}
	if err = req.Write(conn); err != nil {
		return 0, err
	}
	response, err := http.ReadResponse(bufio.NewReaderSize(&forwardHTTPHeaderLimit{reader: conn, remaining: 16 << 10}, 4096), req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if _, err = io.Copy(output, response.Body); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

// The raw local stream bypasses the source listener's HTTP parser. Bound the
// service response header here while leaving its body streaming under the
// destination grant's byte and lifetime limits.
type forwardHTTPHeaderLimit struct {
	reader    io.Reader
	remaining int
	last      [4]byte
	done      bool
}

func (b *forwardHTTPHeaderLimit) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if b.done {
		return n, err
	}
	for _, c := range p[:n] {
		b.remaining--
		if b.remaining < 0 {
			return 0, errors.New("HTTP service response headers exceed bound")
		}
		b.last = [4]byte{b.last[1], b.last[2], b.last[3], c}
		if b.last == [4]byte{'\r', '\n', '\r', '\n'} {
			b.done = true
			break
		}
	}
	return n, err
}
