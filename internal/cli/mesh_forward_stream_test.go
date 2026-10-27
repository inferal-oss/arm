package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"time"

	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForwardStreamTokenFileSecurity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "token")
	token := strings.Repeat("a", 64)
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readForwardToken(path); err != nil || got != token {
		t.Fatalf("private token refused: %v", err)
	}
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(link); err == nil {
		t.Fatal("symlink token accepted")
	}
	hard := filepath.Join(root, "hardlink")
	if err := os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(path); err == nil {
		t.Fatal("multiply linked token accepted")
	}
	if err := os.Remove(hard); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(path); err == nil {
		t.Fatal("world-readable token accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(token+"\nextra"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(path); err == nil {
		t.Fatal("token with trailing data accepted")
	}
}

func TestForwardSourceAdmissionCancellation(t *testing.T) {
	for _, phase := range []string{"write", "ack"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client, service := net.Pipe()
			defer client.Close()
			defer service.Close()
			entered := make(chan struct{})
			peerDone := make(chan struct{})
			go func() {
				defer close(peerDone)
				if phase == "ack" {
					_, _ = bufio.NewReader(service).ReadString('\n')
				}
				close(entered)
				// In the write case don't consume the bearer until cancellation has closed
				// the client. In the ACK case the peer deliberately withholds admission.
				<-ctx.Done()
				var one [1]byte
				_, _ = service.Read(one[:])
			}()
			done := make(chan error, 1)
			go func() { done <- confirmForwardSource(ctx, client, strings.Repeat("a", 64)) }()
			<-entered
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled source admission remains blocked")
			}
			<-peerDone
		})
	}
}

func TestForwardSourceAdmissionACKPreservesApplicationBytes(t *testing.T) {
	for _, ack := range []string{"ARM-FORWARD/2 READY\n", "ARM-FORWARD/2 REFUSED\n", strings.Repeat("x", 32)} {
		t.Run(strings.TrimSpace(ack), func(t *testing.T) {
			client, service := net.Pipe()
			defer client.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer service.Close()
				_, _ = bufio.NewReader(service).ReadString('\n')
				_, _ = io.WriteString(service, ack+"first-service-bytes")
			}()
			err := confirmForwardSource(t.Context(), client, strings.Repeat("a", 64))
			if strings.Contains(ack, "READY") {
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(client)
				if readErr != nil || string(data) != "first-service-bytes" {
					t.Fatalf("payload=%q error=%v", data, readErr)
				}
			} else if err == nil {
				t.Fatal("unconfirmed source accepted")
			}
			_ = client.Close()
			<-done
		})
	}
}

type forwardCountingInput struct{ reads int }

func (r *forwardCountingInput) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func TestForwardStreamAlreadyCanceledDoesNotReadInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client, service := net.Pipe()
	defer client.Close()
	defer service.Close()
	input := &forwardCountingInput{}
	if err := bridgeForwardStream(ctx, input, io.Discard, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if input.reads != 0 {
		t.Fatalf("canceled stream read application input %d times", input.reads)
	}
}
