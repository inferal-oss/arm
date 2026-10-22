package cli

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWorkspaceDocumentFIFORefusedWithoutWaitingForWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invitation-fifo")
	if e := syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	go func() { var v map[string]any; result <- readWorkspaceDocument(path, &v) }()
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("document open blocked on FIFO before regular-file check")
	}
}
