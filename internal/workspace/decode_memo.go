package workspace

import (
	"reflect"
	"sync"
)

const (
	validationMemoEntries = 256
	validationMemoBytes   = 4 << 20
)

// validationKey deliberately uses exact bytes AND exact output reflect.Type.
// Hash collisions in Go's map do not replace equality. No decoded values or
// errors are shared, and this memo does not cache any authorization/policy work.
type validationKey struct {
	typ reflect.Type
	raw string
}

// FIFO eviction keeps bounded work/metadata and does not update mutable state
// on hits. Every retained string owns its bytes before strict verification.
// The shared byte bound excludes each caller's bounded temporary Decode copy.
type validationMemo struct {
	mu                   sync.Mutex
	entries              map[validationKey]struct{}
	order                []validationKey
	first, count, bytes  int
	maxEntries, maxBytes int
}

var strictValidationMemo = validationMemo{maxEntries: validationMemoEntries, maxBytes: validationMemoBytes}

func (m *validationMemo) contains(key validationKey) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	_, ok := m.entries[key]
	m.mu.Unlock()
	return ok
}
func (m *validationMemo) remember(key validationKey) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// A disabled/oversized insertion is not a negative result entry. Validation
	// still succeeded and every subsequent call can simply validate again.
	if m.maxEntries <= 0 || m.maxEntries > validationMemoEntries || m.maxBytes <= 0 || m.maxBytes > validationMemoBytes || len(key.raw) > m.maxBytes {
		return
	}
	if _, found := m.entries[key]; found {
		return
	}
	if m.entries == nil {
		m.entries = make(map[validationKey]struct{}, m.maxEntries)
		m.order = make([]validationKey, m.maxEntries)
	}
	for m.count >= m.maxEntries || len(key.raw) > m.maxBytes-m.bytes {
		old := m.order[m.first]
		delete(m.entries, old)
		m.bytes -= len(old.raw)
		m.order[m.first] = validationKey{}
		m.first = (m.first + 1) % m.maxEntries
		m.count--
	}
	index := (m.first + m.count) % m.maxEntries
	m.order[index] = key
	m.entries[key] = struct{}{}
	m.count++
	m.bytes += len(key.raw)
}
