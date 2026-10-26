package cli

import (
	"github.com/inferal-oss/arm/internal/meshforward"
	"io"
	"net"
	"strings"
	"testing"
)

func TestLocalForwardRequiresTokenAndStripsProxyHeader(t *testing.T) {
	token := strings.Repeat("a", 64)
	digest, err := meshforward.TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, want string
		allow             bool
	}{
		{"raw", "ARM-FORWARD/1 " + token + "\nhello", "hello", true},
		{"http", "GET / HTTP/1.1\r\nHost: example\r\nProxy-Authorization: Bearer " + token + "\r\n\r\n", "GET / HTTP/1.1\r\nHost: example\r\n\r\n", true},
		{"missing", "GET / HTTP/1.1\r\nHost: example\r\n\r\n", "", false},
		{"wrong", "ARM-FORWARD/1 " + strings.Repeat("b", 64) + "\n", "", false},
		{"duplicate", "GET / HTTP/1.1\r\nProxy-Authorization: Bearer " + token + "\r\nProxy-Authorization: Bearer " + token + "\r\n\r\n", "", false},
		{"declared trailer", "POST / HTTP/1.1\r\nProxy-Authorization: Bearer " + token + "\r\nTrailer: Proxy-Authorization\r\n\r\n", "", false},
		{"chunked undeclared trailer", "POST / HTTP/1.1\r\nProxy-Authorization: Bearer " + token + "\r\nTransfer-Encoding: chunked\r\n\r\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			go func() { _, _ = io.WriteString(b, tc.input) }()
			reader, prefix, _, err := meshforward.AuthenticateLocal(a, digest)
			if (err == nil) != tc.allow {
				t.Fatalf("allow=%v error=%v", tc.allow, err)
			}
			if !tc.allow {
				return
			}
			if tc.name == "raw" {
				body := make([]byte, 5)
				_, err = io.ReadFull(reader, body)
				if err != nil || string(body) != "hello" {
					t.Fatalf("raw payload %q: %v", body, err)
				}
				return
			}
			if string(prefix) != tc.want {
				t.Fatalf("forwarded headers %q", prefix)
			}
		})
	}
}
