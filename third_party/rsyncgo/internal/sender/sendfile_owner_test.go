package sender

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/third_party/rsyncgo"
	"getarm.org/third_party/rsyncgo/internal/rsyncopts"
	"getarm.org/third_party/rsyncgo/internal/rsyncos"
	"getarm.org/third_party/rsyncgo/internal/rsyncwire"
	"github.com/mmcloughlin/md4"
)

type heldSendFile struct {
	*os.File
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
	once    sync.Once
	closeN  atomic.Int32
}

func (f *heldSendFile) Read(p []byte) (int, error) {
	f.once.Do(func() { close(f.entered); <-f.release })
	return f.File.Read(p)
}
func (f *heldSendFile) Close() error {
	f.closeN.Add(1)
	err := f.File.Close()
	close(f.closed)
	return err
}

type singleSendSource struct {
	f     *heldSendFile
	opens atomic.Int32
}

func (s *singleSendSource) Open(string) (File, error) {
	if s.opens.Add(1) != 1 {
		return nil, errors.New("unexpected independent hash reader")
	}
	return s.f, nil
}
func (*singleSendSource) FS() fs.FS                       { return nil }
func (*singleSendSource) Readlink(string) (string, error) { return "", errors.New("no links") }
func (*singleSendSource) Close() error                    { return nil }

type failedPayloadWriter struct{ headers int }

var errPayload = errors.New("fixture payload write failure")

func (w *failedPayloadWriter) Write(p []byte) (int, error) {
	// File index and four checksum-header int32 fields precede the first
	// literal token. Fail the literal's payload after the original Read.
	if w.headers < 24 {
		w.headers += len(p)
		return len(p), nil
	}
	return 0, errPayload
}
func (*failedPayloadWriter) Close() error { return nil }

func TestSendFileWireFailureRetainsOriginalReadOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("one original bounded read"), 0600); err != nil {
		t.Fatal(err)
	}
	native, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &heldSendFile{File: native, entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	source := &singleSendSource{f: f}
	var release sync.Once
	unblock := func() { release.Do(func() { close(f.release) }) }
	done := make(chan error, 1)
	t.Cleanup(func() {
		unblock()
		select {
		case <-f.closed:
		case <-time.After(time.Second):
			t.Error("original source owner did not close")
		}
	})
	env := &rsyncos.Env{}
	st := &Transfer{Opts: rsyncopts.NewOptionsWithGokrazyDefaults(env), Conn: &rsyncwire.Conn{Writer: &failedPayloadWriter{}}}
	go func() { done <- st.sendFile(0, file{source: source, path: "source", regular: true}) }()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("original source Read did not begin")
	}
	select {
	case err := <-done:
		t.Fatal("transfer returned while its original Read was held", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-done:
		if !errors.Is(err, errPayload) {
			t.Fatal("original wire error lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("original transfer did not return after Read unblocked")
	}
	select {
	case <-f.closed:
	default:
		t.Fatal("transfer returned before its original source Close")
	}
	if source.opens.Load() != 1 || f.closeN.Load() != 1 {
		t.Fatal("extra hash reader or repeated native Close", source.opens.Load(), f.closeN.Load())
	}
}

var _ io.ReadSeekCloser = (*heldSendFile)(nil)

type overrideReadFile struct {
	*os.File
	read func([]byte) (int, error)
	size int64
}

func (f overrideReadFile) Read(p []byte) (int, error) { return f.read(p) }
func (f overrideReadFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return overrideSizeInfo{info, f.size}, nil
}

type overrideSizeInfo struct {
	fs.FileInfo
	size int64
}

func (i overrideSizeInfo) Size() int64 { return i.size }

type fixedSendSource struct{ file File }

func (s fixedSendSource) Open(string) (File, error)     { return s.file, nil }
func (fixedSendSource) FS() fs.FS                       { return nil }
func (fixedSendSource) Readlink(string) (string, error) { return "", errors.New("no links") }
func (fixedSendSource) Close() error                    { return nil }

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

func TestSendFileFinalBytesAndZeroProgress(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(map[bool]string{false: "final-bytes-with-EOF", true: "zero-progress"}[stalled], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
			native, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			f := overrideReadFile{File: native, size: 4, read: func(p []byte) (int, error) {
				reads++
				if stalled {
					return 0, nil
				}
				if reads > 1 {
					t.Error("read after final data+EOF")
					return 0, io.EOF
				}
				return copy(p, "data"), io.EOF
			}}
			wire := &bufferWriteCloser{}
			st := &Transfer{Seed: 42, Opts: rsyncopts.NewOptionsWithGokrazyDefaults(&rsyncos.Env{}), Conn: &rsyncwire.Conn{Writer: wire}}
			err = st.sendFile(0, file{source: fixedSendSource{f}, path: "source", regular: true})
			if _, statErr := native.Stat(); !errors.Is(statErr, os.ErrClosed) {
				t.Fatal("original source descriptor still open", statErr)
			}
			if stalled {
				if !errors.Is(err, io.ErrNoProgress) || wire.Len() != 20 || reads != 1 {
					t.Fatal("stalled reader emitted a terminator or failed to stop", err, wire.Len(), reads)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			payload := wire.Bytes()[20:]
			if len(payload) != 28 || binary.LittleEndian.Uint32(payload[:4]) != 4 || string(payload[4:8]) != "data" || binary.LittleEndian.Uint32(payload[8:12]) != 0 {
				t.Fatal("final bytes/terminator missing from wire", payload)
			}
			hash := md4.New()
			_ = binary.Write(hash, binary.LittleEndian, int32(42))
			_, _ = hash.Write([]byte("data"))
			if !bytes.Equal(payload[12:], hash.Sum(nil)) {
				t.Fatal("checksum does not cover final data")
			}
		})
	}
}

func TestDeltaRechecksSourceSizeBeforeWireOrRead(t *testing.T) {
	for _, size := range []int64{-1, rsync.MaxFileLength + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			native, err := os.CreateTemp(t.TempDir(), "source")
			if err != nil {
				t.Fatal(err)
			}
			f := overrideReadFile{File: native, size: size, read: func([]byte) (int, error) {
				t.Error("unsupported current source size reached payload read")
				return 0, io.EOF
			}}
			wire := &bufferWriteCloser{}
			st := &Transfer{Logger: log.New(io.Discard, "", 0), Conn: &rsyncwire.Conn{Writer: wire}}
			err = st.hashSearch(nil, nil, rsync.SumHead{}, 0, file{source: fixedSendSource{f}, path: "source", regular: true})
			if err == nil || wire.Len() != 0 {
				t.Fatal("unsupported current source size accepted or wrote protocol bytes", err, wire.Len())
			}
			if _, statErr := native.Stat(); !errors.Is(statErr, os.ErrClosed) {
				t.Fatal("original delta source descriptor still open", statErr)
			}
		})
	}
}
