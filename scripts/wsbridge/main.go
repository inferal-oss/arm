// wsbridge is a test-only JSONL bridge to Codex's WebSocket-over-Unix transport.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/coder/websocket"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: wsbridge UNIX_SOCKET")
		os.Exit(2)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", os.Args[1])
	}}
	defer transport.CloseIdleConnections()
	c, _, e := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer c.CloseNow()
	c.SetReadLimit(8 << 20)
	done := make(chan error, 2)
	go func() {
		for {
			_, b, e := c.Read(ctx)
			if e != nil {
				done <- e
				return
			}
			if _, e = fmt.Fprintf(os.Stdout, "%s\n", b); e != nil {
				done <- e
				return
			}
		}
	}()
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			if e := c.Write(ctx, websocket.MessageText, scanner.Bytes()); e != nil {
				done <- e
				return
			}
		}
		done <- scanner.Err()
	}()
	if e = <-done; e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
