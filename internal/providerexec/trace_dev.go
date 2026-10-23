//go:build arm_devtrace

package providerexec

import (
	"encoding/json"
	"fmt"
	"sync"
)

type traceEvent struct {
	Ticket  uint64          `json:"ticket"`
	Kind    string          `json:"kind"`
	Attempt string          `json:"attempt"`
	Word    uint64          `json:"word"`
	Raw     json.RawMessage `json:"raw"`
}
type traceRecorder struct {
	mu     sync.Mutex
	events []traceEvent
	bytes  int
	failed bool
}
type observer struct{ r *traceRecorder }

func (o observer) emit(kind, attempt string, word uint64) { o.append(kind, attempt, word, nil) }
func (o observer) append(kind, attempt string, word uint64, raw []byte) {
	if o.r == nil {
		return
	}
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	n := len(kind) + len(attempt) + len(raw)
	if len(o.r.events) >= 4096 || len(kind) > 64 || len(attempt) > 128 || n > 160<<10 || o.r.bytes+n > 4<<20 {
		o.r.failed = true
		return
	}
	o.r.bytes += n
	o.r.events = append(o.r.events, traceEvent{uint64(len(o.r.events) + 1), kind, attempt, word, append(json.RawMessage(nil), raw...)})
}

// Fixture SQL observations contain exact written record bytes AFTER the real
// acknowledged statement. Byte slices use base64 so enclosing pretty JSON cannot
// alter whitespace/escaping in exact SQL bytes. No reducer/generated successor.
func (o observer) sql(kind, attempt string, initial, terminal []byte, remaining int) {
	if o.r == nil {
		return
	}
	raw, err := json.Marshal(struct {
		Initial   []byte `json:"initial"`
		Terminal  []byte `json:"terminal"`
		Remaining int    `json:"remaining"`
	}{initial, terminal, remaining})
	if err != nil {
		o.r.mu.Lock()
		o.r.failed = true
		o.r.mu.Unlock()
		return
	}
	o.append(kind, attempt, uint64(remaining), raw)
}
func (o observer) raw(kind, attempt string, raw []byte) { o.append(kind, attempt, 0, raw) }
func (r *traceRecorder) snapshot() ([]traceEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed {
		return nil, fmt.Errorf("worker observation bound exceeded")
	}
	v := append([]traceEvent(nil), r.events...)
	for i := range v {
		v[i].Raw = append(json.RawMessage(nil), v[i].Raw...)
	}
	return v, nil
}
