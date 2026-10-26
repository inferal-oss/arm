package sender

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type stalledFile struct{ *os.File }

func (stalledFile) Read([]byte) (int, error) { return 0, nil }

func TestMapFileRejectsNoProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ms := mapFile(stalledFile{f}, 4, 700, 700)
	if _, err := ms.ptr(0, 4); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("got %v", err)
	}
}
