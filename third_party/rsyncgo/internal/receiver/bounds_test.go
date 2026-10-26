package receiver

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/third_party/rsyncgo"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/progress"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncopts"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncwire"
)

func wireInt(b *bytes.Buffer, v int32) { _ = binary.Write(b, binary.LittleEndian, v) }

func TestMalformedFileEntries(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "a/../b", "a//b"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			wireInt(&b, int32(len(name)))
			b.WriteString(name)
			rt := &Transfer{Conn: &rsyncwire.Conn{Reader: io.NopCloser(&b)}}
			if _, err := rt.receiveFileEntry(rsync.XMIT_LONG_NAME, new(File)); err == nil || !strings.Contains(err.Error(), "invalid wire path") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestNegativeLongNameLengthRefusedBeforeAllocation(t *testing.T) {
	for _, length := range []int32{-1, -1 << 31} {
		for _, prefix := range []bool{false, true} {
			t.Run(fmt.Sprintf("length=%d/prefix=%v", length, prefix), func(t *testing.T) {
				var b bytes.Buffer
				flags := uint16(rsync.XMIT_LONG_NAME)
				last := &File{Name: "old"}
				if prefix {
					flags |= rsync.XMIT_SAME_NAME
					b.WriteByte(1)
				}
				wireInt(&b, length)
				rt := &Transfer{Conn: &rsyncwire.Conn{Reader: io.NopCloser(&b)}}
				if _, err := rt.receiveFileEntry(flags, last); err == nil || !strings.Contains(err.Error(), "invalid path suffix length") {
					t.Fatalf("got %v", err)
				}
			})
		}
	}
}

func TestCaseCollisionRejectedBeforeTransfer(t *testing.T) {
	var b bytes.Buffer
	for _, name := range []string{"A", "a"} {
		b.WriteByte(byte(rsync.XMIT_LONG_NAME))
		wireInt(&b, int32(len(name)))
		b.WriteString(name)
		wireInt(&b, 1)
		wireInt(&b, 0)
		wireInt(&b, rsync.S_IFREG|0600)
	}
	b.WriteByte(0)
	rt := &Transfer{Opts: &TransferOpts{DebugGTE: func(rsyncopts.DebugLevel, uint16) bool { return false }}, Conn: &rsyncwire.Conn{Reader: io.NopCloser(&b)}}
	if _, err := rt.ReceiveFileList(); err == nil || !strings.Contains(err.Error(), "case-colliding") {
		t.Fatalf("got %v", err)
	}
}

func TestLiteralTokenBound(t *testing.T) {
	var b bytes.Buffer
	wireInt(&b, maxLiteralToken+1)
	rt := &Transfer{Conn: &rsyncwire.Conn{Reader: io.NopCloser(&b)}}
	if _, _, err := rt.recvToken(); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("got %v", err)
	}
}

func TestCorruptAndRangeTokensRetainOldFile(t *testing.T) {
	for _, tc := range []struct {
		name     string
		token    int32
		payload  string
		checksum bool
		want     string
	}{
		{"bad hash", 4, "new!", true, "corruption"},
		{"bad range", -2, "", false, "out of bounds"},
		{"oversize literal", 8, "12345678", false, "exceeds file length"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootPath := t.TempDir()
			target := filepath.Join(rootPath, "file")
			if err := os.WriteFile(target, []byte("old!"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			old, err := root.Open("file")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var b bytes.Buffer
			for _, v := range []int32{1, 4, 16, 0} {
				wireInt(&b, v)
			}
			wireInt(&b, tc.token)
			b.WriteString(tc.payload)
			if tc.checksum {
				wireInt(&b, 0)
				b.Write(make([]byte, 16))
			}
			rt := &Transfer{Dest: rootPath, DestRoot: root, Opts: &TransferOpts{
				DebugGTE: func(rsyncopts.DebugLevel, uint16) bool { return false },
			}, Conn: &rsyncwire.Conn{Reader: io.NopCloser(&b)}, Progress: progress.NewPrinter(io.Discard, time.Now)}
			err = rt.receiveData(&File{Name: "file", Length: 4}, old, 0600)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "old!" {
				t.Fatalf("old file changed: %q, %v", got, err)
			}
		})
	}
}
