package meshforward

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestConfirmedRawLocalAdmission(t *testing.T) {
	token := strings.Repeat("a", 64)
	digest, err := TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, refused := range []bool{false, true} {
		client, source := net.Pipe()
		done := make(chan struct{})
		var peer net.Conn
		if !refused {
			remote, other := net.Pipe()
			peer = other
			go func() {
				defer other.Close()
				payload := make([]byte, 4)
				if _, e := io.ReadFull(other, payload); e == nil {
					_, _ = other.Write(payload)
				}
			}()
			go func() {
				defer close(done)
				_ = ServeLocal(t.Context(), source, digest, func(context.Context) (net.Conn, error) { return remote, nil })
			}()
		} else {
			go func() {
				defer close(done)
				_ = ServeLocal(t.Context(), source, digest, func(context.Context) (net.Conn, error) { return nil, errors.New("not granted") })
			}()
		}
		if _, err = io.WriteString(client, "ARM-FORWARD/2 "+token+"\n"); err != nil {
			t.Fatal(err)
		}
		ack, err := bufio.NewReader(client).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if refused {
			if ack != "ARM-FORWARD/2 REFUSED\n" {
				t.Fatal(ack)
			}
		} else {
			if ack != "ARM-FORWARD/2 READY\n" {
				t.Fatal(ack)
			}
			if _, err = io.WriteString(client, "data"); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, 4)
			if _, err = io.ReadFull(client, data); err != nil || string(data) != "data" {
				t.Fatalf("application stream: %q %v", data, err)
			}
		}
		_ = client.Close()
		if peer != nil {
			_ = peer.Close()
		}
		<-done
	}
}

func TestBoundedHTTPResponseHeaderLeavesBodyStreaming(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		valid        bool
	}{
		{"ordinary", "X-Name: value\r\n", true},
		{"oversize", "X-Name: " + strings.Repeat("x", 16<<10) + "\r\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodyExpected := strings.Repeat("b", 64<<10)
			raw := "HTTP/1.1 200 OK\r\n" + tc.header + "Content-Length: 65536\r\n\r\n" + bodyExpected
			response, err := http.ReadResponse(bufio.NewReaderSize(&boundedHTTPHeader{r: strings.NewReader(raw), remaining: 16 << 10}, 4096), nil)
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%v error=%v", err == nil, err)
			}
			if err != nil {
				return
			}
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != bodyExpected {
				t.Fatalf("response body length %d: %v", len(body), err)
			}
		})
	}
}
