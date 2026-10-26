package kubeprovision

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReviewedBinaryReplacementWithFIFOReturnsWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "arm")
	bytes := []byte("reviewed executable bytes")
	sum := sha256.Sum256(bytes)
	digest := hex.EncodeToString(sum[:])
	if err := os.WriteFile(path, bytes, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := OpenReviewedBinary(path, int64(len(bytes)), digest)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		file, err := OpenReviewedBinary(path, int64(len(bytes)), digest)
		if file != nil {
			file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO replacement was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO replacement blocked the shipment path")
	}
}
