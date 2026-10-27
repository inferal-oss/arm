package syncworker

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/rsyncengine"
	"getarm.org/internal/syncfs"
)

type capReviewTransfer struct {
	net.Conn
	done                  <-chan error
	closes, joins, aborts int
	peerErr               error
}

func (h *capReviewTransfer) Close() error { h.closes++; return h.Conn.Close() }
func (h *capReviewTransfer) Abort() error { h.aborts++; return h.Conn.Close() }
func (h *capReviewTransfer) Join() error  { h.joins++; h.peerErr = <-h.done; return nil }

func TestStagingCapReviewWorkerReceiversRequireCapAndJoin(t *testing.T) {
	for _, role := range []rsyncengine.Role{rsyncengine.ServerReceiver, rsyncengine.ClientReceiver} {
		for _, invalid := range []string{"missing", "negative"} {
			t.Run(fmt.Sprintf("role=%d/%s", role, invalid), func(t *testing.T) {
				source, stage := t.TempDir(), t.TempDir()
				if err := os.WriteFile(filepath.Join(source, "file"), []byte("private source bytes"), 0600); err != nil {
					t.Fatal(err)
				}
				a, b := net.Pipe()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				sender := rsyncengine.ClientSender
				if role == rsyncengine.ClientReceiver {
					sender = rsyncengine.ServerSender
				}
				go func() {
					_, err := rsyncengine.Run(ctx, a, sender, source, rsyncengine.Metadata{})
					a.Close()
					done <- err
				}()
				h := &capReviewTransfer{Conn: b, done: done}
				defer func() {
					cancel()
					b.Close()
					a.Close()
					if h.joins == 0 {
						<-done
					}
				}()
				var limit *int64
				if invalid == "negative" {
					n := int64(-1)
					limit = &n
				}
				err := runTransfer(ctx, h, role, stage, syncfs.Metadata{}, limit)
				if err == nil || invalid == "missing" && !strings.Contains(err.Error(), "requires an explicit nonnegative write limit") || invalid == "negative" && !strings.Contains(err.Error(), "negative") {
					t.Fatal("fixed worker receiver admitted missing/invalid local policy", err)
				}
				if h.closes != 1 || h.joins != 1 || h.aborts != 1 || h.peerErr == nil {
					t.Fatal("policy refusal lost original close/join", h.closes, h.joins, h.aborts, h.peerErr)
				}
				entries, err := os.ReadDir(stage)
				if err != nil || len(entries) != 0 {
					t.Fatal("policy refusal wrote receiver stage", len(entries), err)
				}
			})
		}
	}
}

func TestStagingCapReviewNegativeLocalPolicyDoesNotWaitForCancellation(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runLocalBatch(ctx, source, target, syncfs.Metadata{}, -1) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "negative") {
			t.Fatal("invalid local cap did not refuse before transport", err)
		}
	case <-time.After(time.Second):
		cancel()
		<-done // original local helper and both original pipe workers joined
		t.Fatal("negative local cap waited for transport cancellation")
	}
}

func TestStagingCapReviewLocalOverrunPreservesOriginalTarget(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	payload := []byte("new receiver content")
	if err := os.WriteFile(filepath.Join(source, "file"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("retained basis"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := runLocalBatch(ctx, source, target, syncfs.Metadata{}, 1)
	if err == nil || !strings.Contains(err.Error(), "receiver write limit exceeded") {
		t.Fatal("local reconstructed bytes escaped cap", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "file"))
	if err != nil || string(got) != "retained basis" {
		t.Fatal("failed local exchange replaced original basis", err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 1 {
		t.Fatal("failed local exchange left unpublished temporary files", len(entries), err)
	}
}
