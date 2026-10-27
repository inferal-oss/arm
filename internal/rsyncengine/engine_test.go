package rsyncengine

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunStagesStandardRsyncExchange(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	stage := filepath.Join(t.TempDir(), "stage")
	for _, root := range []string{source, stage} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	want := bytes.Repeat([]byte("ARM rsync block\n"), 1<<12)
	if err := os.WriteFile(filepath.Join(source, "data"), want, 0600); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { _, err := Run(ctx, b, ServerReceiver, stage, Metadata{}); serverDone <- err }()
	stats, err := Run(ctx, a, ClientSender, source, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(stage, "data"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("staged file mismatch: %v", err)
	}
	if stats.FileBytes < int64(len(want)) {
		t.Fatalf("reported %d file bytes", stats.FileBytes)
	}
}

func TestReceiverWriteLimitAcrossFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int64
		fail  bool
	}{
		{name: "exact", limit: 16 << 10},
		{name: "cumulative overrun", limit: 12 << 10, fail: true},
		{name: "zero", limit: 0, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, stage := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "stage")
			for _, path := range []string{source, stage} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"a", "b"} {
				if err := os.WriteFile(filepath.Join(source, name), bytes.Repeat([]byte(name), 8<<10), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			serverDone := make(chan error, 1)
			go func() {
				_, err := Run(ctx, b, ServerReceiver, stage, Metadata{ReceiverWriteLimit: &tc.limit})
				serverDone <- err
			}()
			_, senderErr := Run(ctx, a, ClientSender, source, Metadata{})
			receiverErr := <-serverDone
			if tc.fail {
				if receiverErr == nil || !strings.Contains(receiverErr.Error(), "receiver write limit exceeded") {
					t.Fatalf("receiver error = %v, sender error = %v", receiverErr, senderErr)
				}
				if _, err := os.Stat(filepath.Join(stage, "b")); !os.IsNotExist(err) {
					t.Fatalf("over-limit file published into stage: %v", err)
				}
				return
			}
			if senderErr != nil || receiverErr != nil {
				t.Fatalf("exact-limit exchange: sender=%v receiver=%v", senderErr, receiverErr)
			}
			for _, name := range []string{"a", "b"} {
				got, err := os.ReadFile(filepath.Join(stage, name))
				if err != nil || !bytes.Equal(got, bytes.Repeat([]byte(name), 8<<10)) {
					t.Fatalf("staged %s: %v", name, err)
				}
			}
		})
	}
}

func TestReceiverWriteLimitCountsBasisBlocks(t *testing.T) {
	source, stage := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "stage")
	for _, path := range []string{source, stage} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	data := bytes.Repeat([]byte("same basis block\n"), 1024)
	for _, path := range []string{source, stage} {
		if err := os.WriteFile(filepath.Join(path, "file"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	limit := int64(0)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		_, err := Run(ctx, b, ServerReceiver, stage, Metadata{ReceiverWriteLimit: &limit})
		serverDone <- err
	}()
	_, _ = Run(ctx, a, ClientSender, source, Metadata{})
	if err := <-serverDone; err == nil || !strings.Contains(err.Error(), "receiver write limit exceeded") {
		t.Fatalf("basis reconstruction escaped receiver limit: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(stage, "file"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("existing basis changed on overrun: %v", err)
	}
}
