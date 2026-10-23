package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

type memoShape struct {
	N       int             `json:"n"`
	Text    string          `json:"text"`
	Values  []string        `json:"values"`
	Child   *memoChild      `json:"child,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
type memoChild struct {
	Name string `json:"name"`
}

func memoForTest() *validationMemo {
	return &validationMemo{maxEntries: validationMemoEntries, maxBytes: validationMemoBytes}
}
func memoStats(m *validationMemo) (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count, m.bytes
}
func memoDecode(t *testing.T, m *validationMemo, raw []byte, out any) {
	t.Helper()
	if e := decodeWithMemo(raw, out, m); e != nil {
		t.Fatal(e)
	}
}

const memoValid = `{"n":1,"text":"original","values":["one"],"child":{"name":"child"},"payload":{"x":1}}`

func TestDecodeMemoOwnsExactBytesBeforeValidationAndFreshDecode(t *testing.T) {
	m := memoForTest()
	raw := []byte(memoValid)
	var a, b, c memoShape
	memoDecode(t, m, raw, &a)
	if n, sz := memoStats(m); n != 1 || sz != len(raw) {
		t.Fatal(n, sz)
	}
	// The cache's string cannot alias any byte in the caller's input backing.
	m.mu.Lock()
	for key := range m.entries {
		if unsafe.StringData(key.raw) == unsafe.SliceData(raw) {
			t.Fatal("borrowed immutable key")
		}
	}
	m.mu.Unlock()
	memoDecode(t, m, raw, &b)
	a.Values[0] = "changed"
	a.Child.Name = "changed"
	a.Payload[0] = '['
	if b.Values[0] != "one" || b.Child.Name != "child" || string(b.Payload) != "{\"x\":1}" {
		t.Fatal("shared decoded objects", b)
	}
	// Same mutable backing and same length, different bytes: full validation.
	copy(raw[bytes.Index(raw, []byte("original")):], []byte("mutated!"))
	memoDecode(t, m, raw, &c)
	if c.Text != "mutated!" {
		t.Fatal("stale parsed output", c)
	}
	if n, _ := memoStats(m); n != 2 {
		t.Fatal("changed exact bytes reused validation", n)
	}
	copy(raw, []byte(memoValid))
	raw[2] = 'N'
	if e := decodeWithMemo(raw, &c, m); e == nil {
		t.Fatal("case-mutated key accepted")
	}
	if n, _ := memoStats(m); n != 2 {
		t.Fatal("negative cache entry", n)
	}
}
func TestDecodeMemoExactReflectTypeAndRequiredFields(t *testing.T) {
	type first struct {
		A string `json:"a"`
	}
	type second struct {
		A string `json:"a"`
		B bool   `json:"b"`
	}
	type sameShape first
	m := memoForTest()
	raw := []byte(`{"a":"hello"}`)
	var a first
	memoDecode(t, m, raw, &a)
	var b second
	if e := decodeWithMemo(raw, &b, m); e == nil {
		t.Fatal("type-changed missing required field accepted")
	}
	var c sameShape
	memoDecode(t, m, raw, &c)
	if n, _ := memoStats(m); n != 2 {
		t.Fatal("exact distinct named types not retained", n)
	}
	if reflect.TypeOf(&a) == reflect.TypeOf(&c) {
		t.Fatal("fixture type alias")
	}
	var absent *first
	var invalid *json.InvalidUnmarshalError
	if e := decodeWithMemo(raw, absent, m); !errors.As(e, &invalid) {
		t.Fatal("cached type skipped nil destination check", e)
	}
}
func TestDecodeMemoRejectsMalformedEvenAfterPositiveWarmup(t *testing.T) {
	list := strings.Repeat(`"x",`, MaxMembers) + `"x"`
	cases := map[string]string{
		"duplicate-root":     `{"n":1,"n":2,"text":"x","values":[]}`,
		"duplicate-escaped":  `{"n":1,"\u006e":2,"text":"x","values":[]}`,
		"duplicate-nested":   `{"n":1,"text":"x","values":[],"child":{"name":"a","name":"b"}}`,
		"duplicate-raw":      `{"n":1,"text":"x","values":[],"payload":{"a":1,"a":2}}`,
		"alias-case":         `{"N":1,"text":"x","values":[]}`,
		"unknown":            `{"n":1,"text":"x","values":[],"extra":false}`,
		"missing":            `{"n":1,"values":[]}`,
		"null-root":          `null`,
		"null-primitive":     `{"n":null,"text":"x","values":[]}`,
		"null-optional":      `{"n":1,"text":"x","values":[],"child":null}`,
		"null-raw":           `{"n":1,"text":"x","values":[],"payload":null}`,
		"null-list":          `{"n":1,"text":"x","values":null}`,
		"null-list-element":  `{"n":1,"text":"x","values":[null]}`,
		"null-pointer-field": `{"n":1,"text":"x","values":[],"child":{"name":null}}`,
		"primitive-type":     `{"n":"one","text":"x","values":[]}`,
		"list-type":          `{"n":1,"text":"x","values":{}}`,
		"child-type":         `{"n":1,"text":"x","values":[],"child":1}`,
		"trailing":           memoValid + ` {}`,
		"trailing-invalid":   memoValid + ` x`,
		"truncated":          `{"n":1,`,
		"syntax":             `{"n":1,"text":"x",,"values":[]}`,
		"excess-collection":  `{"n":1,"text":"x","values":[` + list + `]}`,
		"depth":              `{"n":1,"text":"x","values":[],"payload":` + strings.Repeat(`[`, 32) + `0` + strings.Repeat(`]`, 32) + `}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			m := memoForTest()
			var good memoShape
			memoDecode(t, m, []byte(memoValid), &good)
			n, sz := memoStats(m)
			for attempt := 0; attempt < 2; attempt++ {
				var bad memoShape
				if e := decodeWithMemo([]byte(raw), &bad, m); e == nil {
					t.Fatal("malformed cached/accepted", raw)
				}
			}
			if nn, ss := memoStats(m); nn != n || ss != sz {
				t.Fatal("negative result retained", nn, ss)
			}
		})
	}
}
func TestDecodeMemoPreservesDepthCollectionUTF8AndTargetBoundaries(t *testing.T) {
	m := memoForTest()
	raw := []byte(`{"n":1,"text":"x","values":[],"payload":` + strings.Repeat(`[`, 31) + `0` + strings.Repeat(`]`, 31) + `}`)
	var out memoShape
	memoDecode(t, m, raw, &out)
	memoDecode(t, m, raw, &out)
	vals := strings.TrimSuffix(strings.Repeat(`"x",`, MaxMembers), ",")
	memoDecode(t, m, []byte(`{"n":1,"text":"x","values":[`+vals+`]}`), &out)
	for _, raw := range [][]byte{nil, {}, []byte{0xff}, append([]byte(`{"n":1,"text":"`), 0xff), bytes.Repeat([]byte{' '}, MaxRecordBytes+1)} {
		n, sz := memoStats(m)
		if e := decodeWithMemo(raw, &out, m); e == nil {
			t.Fatal("length/UTF8 accepted")
		}
		if nn, ss := memoStats(m); nn != n || ss != sz {
			t.Fatal("invalid bytes retained")
		}
	}
	for _, target := range []any{nil, 12, new(int), new([]string), new(*memoShape), map[string]any{}} {
		if e := decodeWithMemo([]byte(memoValid), target, m); e == nil {
			t.Fatal("invalid target accepted", target)
		}
	}
	type noTags struct{ A string }
	if e := decodeWithMemo([]byte(`{"A":"x"}`), new(noTags), m); e == nil {
		t.Fatal("untagged struct accepted")
	}
	type freeMap struct {
		M map[string]string `json:"m"`
	}
	if e := decodeWithMemo([]byte(`{"m":{}}`), new(freeMap), m); e == nil {
		t.Fatal("free map accepted")
	}
}

var memoCustomCalls atomic.Int64
var memoCustomReject atomic.Bool
var errMemoCustom = errors.New("custom decoder rejected current call")

type memoCustom struct {
	N int `json:"n"`
}

func (m *memoCustom) UnmarshalJSON(raw []byte) error {
	memoCustomCalls.Add(1)
	if memoCustomReject.Load() {
		return errMemoCustom
	}
	type plain memoCustom
	return json.Unmarshal(raw, (*plain)(m))
}
func TestDecodeMemoAlwaysRunsFreshTypedCustomDecode(t *testing.T) {
	memoCustomCalls.Store(0)
	memoCustomReject.Store(false)
	t.Cleanup(func() { memoCustomReject.Store(false) })
	m := memoForTest()
	raw := []byte(`{"n":1}`)
	var out memoCustom
	memoDecode(t, m, raw, &out)
	memoDecode(t, m, raw, &out)
	memoCustomReject.Store(true)
	if e := decodeWithMemo(raw, &out, m); !errors.Is(e, errMemoCustom) {
		t.Fatal("fresh typed check skipped", e)
	}
	if n := memoCustomCalls.Load(); n != 3 {
		t.Fatal("custom decoder not rerun", n)
	}
	n, sz := memoStats(m)
	if e := decodeWithMemo([]byte(`{"n":2}`), &out, m); !errors.Is(e, errMemoCustom) {
		t.Fatal(e)
	}
	if nn, ss := memoStats(m); nn != n || ss != sz {
		t.Fatal("failed typed decode cached")
	}
}
func TestDecodeMemoBoundsFIFOAndOversizedBypass(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		m := &validationMemo{maxEntries: 2, maxBytes: 1024}
		typ := reflect.TypeOf(new(memoShape))
		for n := 0; n < 3; n++ {
			raw := fmt.Sprintf(`{"n":%d,"text":"x","values":[]}`, n)
			var out memoShape
			memoDecode(t, m, []byte(raw), &out)
		}
		n, sz := memoStats(m)
		if n != 2 || sz > 1024 || m.contains(validationKey{typ, `{"n":0,"text":"x","values":[]}`}) {
			t.Fatal("FIFO entry cap", n, sz)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		m := &validationMemo{maxEntries: 256, maxBytes: 90}
		for n := 0; n < 9; n++ {
			raw := fmt.Sprintf(`{"n":%d,"text":"x","values":[]}`, n)
			var out memoShape
			memoDecode(t, m, []byte(raw), &out)
			count, sz := memoStats(m)
			if count > 256 || sz > 90 {
				t.Fatal("byte cap", count, sz)
			}
		}
		var out memoShape
		memoDecode(t, m, []byte(memoValid+strings.Repeat(" ", 100)), &out)
		if _, sz := memoStats(m); sz > 90 {
			t.Fatal("oversized key retained")
		}
	})
	t.Run("hard", func(t *testing.T) {
		m := memoForTest()
		for n := 0; n < validationMemoEntries+20; n++ {
			raw := fmt.Sprintf(`{"n":%d,"text":"x","values":[]}`, n)
			var out memoShape
			memoDecode(t, m, []byte(raw), &out)
		}
		count, sz := memoStats(m)
		if count != validationMemoEntries || sz > validationMemoBytes {
			t.Fatal("production entry bounds", count, sz)
		}
		m.mu.Lock()
		sum := 0
		for key := range m.entries {
			sum += len(key.raw)
		}
		if sum != m.bytes || len(m.entries) != m.count || len(m.order) != m.maxEntries {
			t.Fatal("owned-key accounting")
		}
		m.mu.Unlock()
	})
}
func TestDecodeMemoConcurrentMutationIndependence(t *testing.T) {
	m := memoForTest()
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	for worker := 0; worker < 32; worker++ {
		worker := worker
		wg.Go(func() {
			for n := 0; n < 24; n++ {
				raw := []byte(fmt.Sprintf(`{"n":%d,"text":"worker-%d","values":["a"]}`, n, worker))
				var out memoShape
				if e := decodeWithMemo(raw, &out, m); e != nil {
					failures <- e
					return
				}
				if out.N != n || out.Text != fmt.Sprintf("worker-%d", worker) {
					failures <- errors.New("cross-caller decoded value")
					return
				}
				for i := range raw {
					raw[i] = 'x'
				} // mutation AFTER return must not corrupt owned keys
			}
		})
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	count, sz := memoStats(m)
	if count > validationMemoEntries || sz > validationMemoBytes {
		t.Fatal("concurrent bounds", count, sz)
	}
}

func TestDecodeMemoExactMaximumRecordLength(t *testing.T) {
	m := memoForTest()
	prefix := `{"n":1,"text":"`
	suffix := `","values":[]}`
	raw := []byte(prefix + strings.Repeat("x", MaxRecordBytes-len(prefix)-len(suffix)) + suffix)
	if len(raw) != MaxRecordBytes {
		t.Fatal("bad boundary fixture")
	}
	var out memoShape
	memoDecode(t, m, raw, &out)
	memoDecode(t, m, raw, &out)
	n, sz := memoStats(m)
	if n != 1 || sz != MaxRecordBytes {
		t.Fatal("maximum byte accounting", n, sz)
	}
	if e := decodeWithMemo(append(raw, ' '), &out, m); e == nil {
		t.Fatal("one byte over limit accepted")
	}
	if nn, ss := memoStats(m); nn != n || ss != sz {
		t.Fatal("oversize changed positive memo")
	}
}

func TestDecodeMemoProductionByteCapEvictsExactLargeKeys(t *testing.T) {
	m := memoForTest()
	for n := 0; n < 6; n++ {
		prefix := fmt.Sprintf(`{"n":%d,"text":"`, n)
		suffix := `","values":[]}`
		raw := []byte(prefix + strings.Repeat("x", MaxRecordBytes-len(prefix)-len(suffix)) + suffix)
		var out memoShape
		memoDecode(t, m, raw, &out)
		count, size := memoStats(m)
		if count > 4 || size > validationMemoBytes {
			t.Fatal("actual four-MiB cap exceeded", count, size)
		}
	}
	count, size := memoStats(m)
	if count != 4 || size != validationMemoBytes {
		t.Fatal("not exact byte-cap fixture", count, size)
	}
	m.mu.Lock()
	sum := 0
	for key := range m.entries {
		sum += len(key.raw)
	}
	m.mu.Unlock()
	if sum != size {
		t.Fatal("large key accounting", sum, size)
	}
}
