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
	"github.com/inferal-oss/arm/internal/model"
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
		return 1, model.Err("remote_outcome_unknown", fmt.Sprintf("HTTP service result unknown; inspect the application before retrying: %v", err))
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
	if err := ctx.Err(); err != nil {
		return 0, err
	}
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
	// One shared reader preserves prefetched interim/final bytes. All header
	// blocks share one budget; informational responses cannot reset it.
	reader := bufio.NewReaderSize(conn, (16<<10)+1)
	remaining := 16 << 10
	var response *http.Response
	for informational := 0; ; informational++ {
		if err = peekForwardHTTPHeader(reader, &remaining); err != nil {
			return 0, err
		}
		response, err = http.ReadResponse(reader, req)
		if err != nil {
			return 0, err
		}
		if response.StatusCode >= 200 {
			break
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusSwitchingProtocols {
			return 0, errors.New("HTTP service protocol upgrade is unsupported")
		}
		if informational >= 7 {
			return 0, errors.New("HTTP service informational responses exceed bound")
		}
	}
	defer response.Body.Close()
	if _, err = io.Copy(output, response.Body); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

// Peek only through one header terminator before parsing. The buffered reader
// keeps any prefetched body or subsequent response bytes, so a large body does
// not count against the aggregate header budget. Accept the LF form supported
// by net/http as well as CRLF, without allowing it to bypass the byte bound.
func peekForwardHTTPHeader(reader *bufio.Reader, remaining *int) error {
	for n := 1; n <= *remaining; n++ {
		data, err := reader.Peek(n)
		if err != nil {
			return err
		}
		if data[n-1] == '\n' && (n == 1 || data[n-2] == '\n' || (n >= 3 && data[n-2] == '\r' && data[n-3] == '\n')) {
			*remaining -= n
			return nil
		}
	}
	return errors.New("HTTP service response headers exceed bound")
}
