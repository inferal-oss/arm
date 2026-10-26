package kubeprovision

import (
	"bytes"
	"errors"
	"sync"
)

// boundedOutput is for untrusted remote diagnostics. Oversize output fails the
// exec stream and remains bounded in memory even if the remote ignores it.
type boundedOutput struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func newBoundedOutput(limit int) *boundedOutput { return &boundedOutput{limit: limit} }
func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return 0, errors.New("remote output exceeded bound")
	}
	if len(p) > b.limit-b.buf.Len() {
		b.overflow = true
		return 0, errors.New("remote output exceeded bound")
	}
	return b.buf.Write(p)
}
func (b *boundedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }
func (b *boundedOutput) Len() int       { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.Len() }
func (b *boundedOutput) Overflow() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.overflow }
