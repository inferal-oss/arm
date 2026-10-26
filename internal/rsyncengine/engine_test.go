package rsyncengine

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
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
