package rsyncd_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/third_party/rsyncgo/rsyncd"
	"getarm.org/third_party/rsyncgo/rsyncstream"
)

type capReviewDiscard struct{ io.Writer }

func (capReviewDiscard) Close() error { return nil }
func TestStagingCapReviewServerOptionCapturesLocalPolicy(t *testing.T) {
	source, stage := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("bounded content"), 0600); err != nil {
		t.Fatal(err)
	}
	var limit int64
	server, err := rsyncd.NewServer(nil, rsyncd.WithReceiverWriteLimit(&limit), rsyncd.WithStderr(capReviewDiscard{io.Discard}))
	if err != nil {
		t.Fatal(err)
	}
	limit = 1 << 20 // Mutating caller storage cannot change an installed server policy.
	args, err := rsyncstream.ServerArgs(rsyncstream.Push, rsyncstream.Options{}, stage)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := server.HandleConnArgs(ctx, rsyncd.NewConnection(b, b, "private cap fixture"), nil, args)
		b.Close()
		done <- err
	}()
	joined := false
	defer func() {
		cancel()
		a.Close()
		b.Close()
		if !joined {
			<-done
		}
	}()
	_, _ = rsyncstream.RunClient(ctx, a, rsyncstream.Push, rsyncstream.Options{}, source)
	receiverErr := <-done
	joined = true
	if receiverErr == nil || !strings.Contains(receiverErr.Error(), "receiver write limit exceeded") {
		t.Fatal("caller mutation expanded installed server policy", receiverErr)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 0 {
		t.Fatal("zero server policy published stage bytes", len(entries), err)
	}
}
func TestStagingCapReviewServerRejectsNegativePolicyBeforeSetup(t *testing.T) {
	negative := int64(-1)
	if server, err := rsyncd.NewServer(nil, rsyncd.WithReceiverWriteLimit(&negative)); err == nil || server != nil {
		t.Fatal("negative local receiver policy installed")
	}
}
