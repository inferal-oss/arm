package receiver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/third_party/rsyncgo"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/progress"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncopts"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncwire"
	"github.com/mmcloughlin/md4"
)

func TestStagingCapReviewBudgetSurvivesProtocolPhaseBoundary(t *testing.T) {
	path := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte("old!"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var wire bytes.Buffer
	packet := func(index int32, payload string) {
		wireInt(&wire, index)
		for range 4 {
			wireInt(&wire, 0)
		} // no basis signatures
		wireInt(&wire, int32(len(payload)))
		wire.WriteString(payload)
		wireInt(&wire, 0)
		hash := md4.New()
		binary.Write(hash, binary.LittleEndian, int32(0))
		hash.Write([]byte(payload))
		wire.Write(hash.Sum(nil))
	}
	packet(0, "new!")
	wireInt(&wire, -1)
	packet(1, "next")
	limit := int64(4)
	rt := &Transfer{Dest: path, DestRoot: root, Opts: &TransferOpts{ReceiverWriteLimit: &limit, DebugGTE: func(rsyncopts.DebugLevel, uint16) bool { return false }}, Conn: &rsyncwire.Conn{Reader: io.NopCloser(&wire)}, Progress: progress.NewPrinter(io.Discard, time.Now)}
	err = rt.RecvFiles([]*File{{Name: "a", Length: 4, Mode: rsync.S_IFREG | 0600}, {Name: "b", Length: 4, Mode: rsync.S_IFREG | 0600}})
	if !errors.Is(err, errReceiverWriteLimit) {
		t.Fatal("protocol phase reset the exchange allowance", err)
	}
	for name, want := range map[string]string{"a": "new!", "b": "old!"} {
		got, err := os.ReadFile(filepath.Join(path, name))
		if err != nil || string(got) != want {
			t.Fatal("limit failure changed retained stage prefix/basis", name, err)
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 2 {
		t.Fatal("limit failure left unpublished temporary bytes", len(entries), err)
	}
	if limit != 4 {
		t.Fatal("exchange accounting mutated caller policy")
	}
}
