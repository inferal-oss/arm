//go:build arm_devtrace

package agents

import (
	"fmt"
	"sync"
)

type nativeTraceKey struct{ ID, Session, Registration string }
type NativeDeliveryTrace struct{ key nativeTraceKey }

var nativeTraces = struct {
	sync.Mutex
	counts map[nativeTraceKey]uint64
}{counts: map[nativeTraceKey]uint64{}}

// BeginNativeDeliveryTrace scopes observations to one exact owned registration,
// without installing adapter callbacks or changing delivery behavior.
func BeginNativeDeliveryTrace(c Contact) (*NativeDeliveryTrace, error) {
	k := nativeTraceKey{c.ID, c.Session, c.Registration}
	if k.ID == "" || k.Session == "" || k.Registration == "" {
		return nil, fmt.Errorf("exact native trace registration required")
	}
	nativeTraces.Lock()
	defer nativeTraces.Unlock()
	if _, ok := nativeTraces.counts[k]; ok || len(nativeTraces.counts) >= 8 {
		return nil, fmt.Errorf("native trace scope already used or full")
	}
	nativeTraces.counts[k] = 0
	return &NativeDeliveryTrace{k}, nil
}
func (t *NativeDeliveryTrace) Count() uint64 {
	nativeTraces.Lock()
	defer nativeTraces.Unlock()
	return nativeTraces.counts[t.key]
}
func (t *NativeDeliveryTrace) Close() {
	nativeTraces.Lock()
	delete(nativeTraces.counts, t.key)
	nativeTraces.Unlock()
}
func traceNativeDelivery(c Contact) {
	if c.Delivery != "native" {
		return
	}
	k := nativeTraceKey{c.ID, c.Session, c.Registration}
	nativeTraces.Lock()
	if n, ok := nativeTraces.counts[k]; ok {
		nativeTraces.counts[k] = n + 1
	}
	nativeTraces.Unlock()
}
