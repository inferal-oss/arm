package meshforward

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// TokenDigest accepts only the digest of a 256-bit random hex token. The
// backend keeps this digest, never the bearer secret or a logged URL copy.
func TokenDigest(token string) (string, error) {
	if len(token) != 64 {
		return "", errors.New("local TCP forward token must be 256-bit hex")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", err
	}
	for _, c := range token {
		if c >= 'A' && c <= 'F' {
			return "", errors.New("local TCP forward token must use canonical lowercase hex")
		}
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), nil
}

func equalToken(given, digest string) bool {
	if len(given) != 64 || !hexID(digest, 32) {
		return false
	}
	actual, err := TokenDigest(given)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(digest)) == 1
}

// AuthenticateLocal consumes only the first bounded local authentication
// record. HTTP is handled as one request per connection so a later header
// cannot leak the token through a persistent connection.
func AuthenticateLocal(c net.Conn, digest string) (io.Reader, []byte, bool, error) {
	return authenticateLocal(c, digest, nil)
}

func authenticateLocal(c net.Conn, digest string, confirmed *bool) (io.Reader, []byte, bool, error) {
	if !hexID(digest, 32) {
		return nil, nil, false, errors.New("private local access digest required")
	}
	r := bufio.NewReaderSize(c, 4096)
	line, err := r.ReadSlice('\n')
	if err != nil {
		return nil, nil, false, err
	}
	if len(line) > 4096 {
		return nil, nil, false, errors.New("oversize local forward control")
	}
	version2 := strings.HasPrefix(string(line), "ARM-FORWARD/2 ")
	if version2 || strings.HasPrefix(string(line), "ARM-FORWARD/1 ") {
		version := "ARM-FORWARD/1 "
		if version2 {
			version = "ARM-FORWARD/2 "
		}
		given := strings.TrimSpace(strings.TrimPrefix(string(line), version))
		if !equalToken(given, digest) {
			return nil, nil, false, errors.New("invalid local forward token")
		}
		if confirmed != nil {
			*confirmed = version2
		}
		return r, nil, false, nil
	}
	if !strings.Contains(string(line), " HTTP/1.") {
		return nil, nil, false, errors.New("local forward requires authenticated TCP or HTTP")
	}
	prefix := append([]byte(nil), line...)
	total := len(prefix)
	found := false
	for {
		line, err = r.ReadSlice('\n')
		if err != nil {
			return nil, nil, false, err
		}
		total += len(line)
		if total > 16<<10 {
			return nil, nil, false, errors.New("oversize HTTP authentication headers")
		}
		if string(line) == "\r\n" {
			prefix = append(prefix, line...)
			break
		}
		lower := strings.ToLower(string(line))
		// HTTP convenience mode is deliberately one ordinary request. A
		// chunked body can carry an undeclared Proxy-Authorization trailer
		// after this authentication record, so require authenticated raw TCP
		// for streaming/chunked protocols.
		if strings.HasPrefix(lower, "transfer-encoding:") || strings.HasPrefix(lower, "trailer:") {
			return nil, nil, false, errors.New("HTTP forward trailers and transfer encodings require authenticated raw TCP")
		}
		if strings.HasPrefix(strings.ToLower(string(line)), "proxy-authorization:") {
			if found {
				return nil, nil, false, errors.New("duplicate HTTP proxy authorization")
			}
			found = true
			given := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(line[len("proxy-authorization:"):])), "Bearer "))
			if !equalToken(given, digest) {
				return nil, nil, false, errors.New("invalid HTTP forward token")
			}
			continue
		}
		prefix = append(prefix, line...)
	}
	if !found {
		return nil, nil, false, errors.New("HTTP proxy authorization required")
	}
	return r, prefix, true, nil
}

// ServeLocal connects one authenticated local user to one already reviewed
// destination stream. open must finish exact remote control admission before
// returning. No redirect, DNS lookup or alternate target is performed here.
func ServeLocal(ctx context.Context, local net.Conn, digest string, open func(context.Context) (net.Conn, error)) error {
	if ctx == nil || local == nil || open == nil {
		return errors.New("owned local TCP connection required")
	}
	defer local.Close()
	_ = local.SetReadDeadline(time.Now().Add(5 * time.Second))
	var confirmed bool
	reader, prefix, httpMode, err := authenticateLocal(local, digest, &confirmed)
	if err != nil {
		return err
	}
	_ = local.SetReadDeadline(time.Time{})
	remote, err := open(ctx)
	if err != nil {
		if confirmed {
			_ = local.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, _ = io.WriteString(local, "ARM-FORWARD/2 REFUSED\n")
		}
		return err
	}
	defer remote.Close()
	if confirmed {
		_ = local.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err = io.WriteString(local, "ARM-FORWARD/2 READY\n"); err != nil {
			return err
		}
		_ = local.SetWriteDeadline(time.Time{})
	}
	watchDone := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = local.Close()
			_ = remote.Close()
		case <-joined:
		}
	}()
	defer func() { close(joined); <-watchDone }()
	if httpMode {
		request, e := http.ReadRequest(bufio.NewReader(io.MultiReader(bytes.NewReader(prefix), reader)))
		if e != nil {
			return e
		}
		if request.Method == http.MethodConnect || request.URL.IsAbs() || request.Header.Get("Upgrade") != "" || request.Header.Get("Expect") != "" {
			return errors.New("local HTTP mode permits one ordinary origin-form request; use authenticated raw TCP for upgrades")
		}
		request.Header.Del("Proxy-Authorization")
		request.Trailer.Del("Proxy-Authorization")
		request.Close = true
		if e = request.Write(remote); e != nil {
			return e
		}
		response, e := http.ReadResponse(bufio.NewReaderSize(&boundedHTTPHeader{r: remote, remaining: 16 << 10}, 4096), request)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		response.Close = true
		return response.Write(local)
	}
	type result struct{ err error }
	finished := make(chan result, 2)
	go func() {
		_, copyErr := io.Copy(remote, reader)
		if copyErr != nil {
			_ = local.Close()
			_ = remote.Close()
		}
		if h, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = h.CloseWrite()
		}
		finished <- result{copyErr}
	}()
	go func() {
		_, copyErr := io.Copy(local, remote)
		if h, ok := local.(interface{ CloseWrite() error }); ok {
			_ = h.CloseWrite()
		}
		_ = local.Close()
		finished <- result{copyErr}
	}()
	first, second := <-finished, <-finished
	if first.err != nil {
		return first.err
	}
	return second.err
}

// boundedHTTPHeader limits only the response header. Once CRLFCRLF is read,
// the same reader continues streaming the body under the destination grant.
type boundedHTTPHeader struct {
	r         io.Reader
	remaining int
	last      [4]byte
	done      bool
}

func (b *boundedHTTPHeader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if b.done {
		return n, err
	}
	for _, c := range p[:n] {
		b.remaining--
		if b.remaining < 0 {
			return 0, errors.New("TCP forward response headers exceed bound")
		}
		b.last = [4]byte{b.last[1], b.last[2], b.last[3], c}
		if b.last == [4]byte{'\r', '\n', '\r', '\n'} {
			b.done = true
			break
		}
	}
	return n, err
}
