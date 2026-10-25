//go:build arm_devtrace

package meshops

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yrashk/arm/internal/mesh"
)

// A failed comparison is diagnostic evidence, never an accepted trace row.
type reconnectMismatch struct {
	Row      int               `json:"row"`
	Actual   reconnectObserved `json:"actual"`
	Expected reconnectObserved `json:"expected"`
	Fact     reconnectRawFact  `json:"actual_fact"`
}

// Diagnostic native byte strings use the separately bounded artifact encoding;
// the original state reader still rejects any scalar larger than 16 KiB.
func reconnectDiagnosticRead(path, base string, out any) error {
	if e := reconnectFormalPath(path, base); e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return fmt.Errorf("bounded diagnostic file required")
	}
	raw, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if e != nil || len(raw) > 4<<20 {
		return fmt.Errorf("bounded diagnostic read: %v", e)
	}
	memberLimit := 128 * 8192
	if _, single := out.(*reconnectMismatch); single {
		// One native fact plus the two small observed-state maps/envelope.
		memberLimit = 8192 + 64
	}
	if e = reconnectJSONUniqueBound(raw, 4*(((1<<20)+2)/3), memberLimit); e != nil {
		return e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(out); e != nil {
		return e
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("diagnostic EOF required: %v", e)
	}
	var facts []reconnectRawFact
	switch v := out.(type) {
	case *reconnectMismatch:
		if v.Row < 1 || v.Row > 128 || len(v.Actual) > 24 || len(v.Expected) > 24 {
			return fmt.Errorf("bounded diagnostic mismatch envelope required")
		}
		for _, state := range []reconnectObserved{v.Actual, v.Expected} {
			for key, value := range state {
				if len(key) > 128 {
					return fmt.Errorf("bounded diagnostic state key required")
				}
				switch scalar := value.(type) {
				case string:
					if len(scalar) > 128 {
						return fmt.Errorf("bounded diagnostic state scalar required")
					}
				case bool, json.Number:
				default:
					return fmt.Errorf("diagnostic state scalar required")
				}
			}
		}
		facts = []reconnectRawFact{v.Fact}
	case *[]reconnectRawFact:
		facts = *v
	default:
		return fmt.Errorf("closed diagnostic target required")
	}
	if len(facts) > 128 {
		return fmt.Errorf("bounded diagnostic facts required")
	}
	for _, fact := range facts {
		encoded, e := json.Marshal(fact)
		if e != nil {
			return e
		}
		if e = reconnectJSONUniqueBound(encoded, 4*(((1<<20)+2)/3), 8192); e != nil {
			return fmt.Errorf("per-fact diagnostic bound: %w", e)
		}
		for _, artifact := range fact.Artifacts {
			sum := sha256.Sum256(artifact.Raw)
			if len(artifact.Raw) == 0 || len(artifact.Raw) > 1<<20 || artifact.SHA256 != hex.EncodeToString(sum[:]) {
				return fmt.Errorf("diagnostic native artifact size/hash mismatch")
			}
		}
	}
	return nil
}

func (d *reconnectDriver) retainFailure(prefix, base string) error {
	// Separate bounded files avoid doubling the retained raw facts in an envelope.
	// The exclusive writer refuses replacement and includes its newline in 4 MiB.
	var failures []error
	for _, part := range []struct {
		suffix string
		value  any
	}{{"-states.json", d.actual}, {"-facts.json", d.facts}, {"-mismatch.json", d.mismatch}} {
		if e := reconnectFormalWrite(prefix+part.suffix, base, part.value); e != nil {
			failures = append(failures, fmt.Errorf("%s: %w", part.suffix, e))
		}
	}
	return errors.Join(failures...)
}

// The coordinator remains synchronously paused at a real source callback while
// an independent coordinator reads the exact saved key. No expected effect is
// skipped or relabeled. The model legitimately allows these offline reads
// between Review and approval, as well as later source observations.
func (d *reconnectDriver) checkpoint(action string) error {
	if e := d.emit(action); e != nil {
		return e
	}
	for !d.recordOnly && d.want() == "lookup" {
		if e := d.offlineLookup(); e != nil {
			return e
		}
	}
	return nil
}

func (d *reconnectDriver) offlineLookup() error {
	before, factBefore, e := d.sample("lookup")
	if e != nil {
		return e
	}
	counts := [5]int{d.f.pairCalls, d.f.ensures, d.f.applies, d.f.sshCalls, len(d.confirmationInputs)}
	r, e := ResolveReconnect(d.f.home, d.key, d.resolved.Draft)
	if e != nil {
		return e
	}
	op, ok, e := mesh.ReadCoordination(d.f.home, d.key)
	if e != nil || !ok {
		return fmt.Errorf("exact saved lookup receipt required: %v", e)
	}
	reader := d.f.coordinator()
	var receipt *Operation
	reader.Print = func(v any) error {
		p, ok := v.(Operation)
		if !ok || receipt != nil {
			return fmt.Errorf("one exact projected lookup receipt required")
		}
		raw, e := json.Marshal(p)
		if e != nil {
			return e
		}
		var detached Operation
		if e = json.Unmarshal(raw, &detached); e != nil {
			return e
		}
		receipt = &detached
		return nil
	}
	if code, e := reader.Reconnect(d.key, r, false, false); e != nil || code != 0 {
		return fmt.Errorf("actual offline lookup %d %v", code, e)
	}
	expected, e := json.Marshal(Project(op))
	if e != nil || receipt == nil {
		return fmt.Errorf("lookup receipt missing: %v", e)
	}
	printed, e := json.Marshal(receipt)
	if e != nil || !bytes.Equal(expected, printed) {
		return fmt.Errorf("lookup did not print exact saved original receipt: %v", e)
	}
	after, factAfter, e := d.sample("lookup")
	if e != nil {
		return e
	}
	if counts != [5]int{d.f.pairCalls, d.f.ensures, d.f.applies, d.f.sshCalls, len(d.confirmationInputs)} || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(factBefore, factAfter) {
		return fmt.Errorf("offline lookup changed native entries or captured authority")
	}
	d.lookupReceipts = append(d.lookupReceipts, *receipt)
	return d.emit("lookup")
}

func reconnectRunTrace(t *testing.T, d *reconnectDriver) error {
	t.Helper()
	for !d.stopped {
		if e := d.step(); e != nil && !errors.Is(e, errReconnectCut) {
			return e
		}
	}
	return nil
}

func TestReconnectActualPreparingLookupCut(t *testing.T) {
	// This is the exact retained shortest TLC lookup witness from the failed
	// all-target gate. Its review/preparing cut exposed the missing driver yield.
	raw, e := os.ReadFile("testdata/reconnect-lookup-witness.json")
	if e != nil {
		t.Fatal(e)
	}
	var expected []reconnectObserved
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e = decoder.Decode(&expected); e != nil {
		t.Fatal(e)
	}
	d := newReconnectDriver(t, expected)
	if e = reconnectRunTrace(t, d); e != nil {
		t.Fatal(e)
	}
	if len(d.lookupReceipts) != 1 || !d.lookupReceipts[0].WorkerLive || d.lookupReceipts[0].Approved || d.lookupReceipts[0].Phase != "preparing" || d.f.pairCalls != 0 || d.f.applies != 0 {
		t.Fatal("preparing lookup must read live exact review without native dispatch", d.lookupReceipts, d.f.pairCalls, d.f.applies)
	}
	last := d.actual[len(d.actual)-1]
	if last["action"] != "lookup" || last["active_accepted"] != false || last["active_approval"] != "unapproved" {
		t.Fatal(last)
	}
}

func reconnectActualRecovery(t *testing.T) *reconnectDriver {
	t.Helper()
	d := reconnectControlReady(t)
	d.control = "lostpair"
	if e := d.connect(false); e != nil {
		t.Fatal(e)
	}
	d.control = ""
	if e := d.connect(true); e != nil {
		t.Fatal(e)
	}
	return d
}

func TestReconnectActualRecoveryPairOrderAndLookupContinuation(t *testing.T) {
	d := reconnectActualRecovery(t)
	var entries []string
	for _, row := range d.actual {
		if row["action"] == "remote_pair" || row["action"] == "local_pair" {
			entries = append(entries, fmt.Sprintf("%v:%v", row["action"], row["pair_calls"]))
		}
	}
	if !reflect.DeepEqual(entries, []string{"remote_pair:1", "remote_pair:2", "local_pair:3"}) || d.f.pairCalls != 3 {
		t.Fatal("each exact attempt must execute remote Pair before its local Pair", entries, d.f.pairCalls)
	}
	// Replay independently observed source rows with a real lookup inserted at
	// the first paused review, and then continue through the actual child calls.
	expected := append([]reconnectObserved(nil), d.actual...)
	for i, row := range expected {
		if row["action"] == "review" {
			lookup := reconnectObserved{}
			for k, v := range row {
				lookup[k] = v
			}
			lookup["action"] = "lookup"
			expected = append(expected[:i+1], append([]reconnectObserved{lookup}, expected[i+1:]...)...)
			break
		}
	}
	replay := newReconnectDriver(t, expected)
	if e := reconnectRunTrace(t, replay); e != nil {
		t.Fatal(e)
	}
	if len(replay.lookupReceipts) != 1 || replay.f.pairCalls != 3 || replay.actual[len(replay.actual)-1]["phase"] != "completed" {
		t.Fatal("read must not suppress later exact native calls", replay.f.pairCalls)
	}
}

func TestReconnectStrictMismatchRetainsActualPrefix(t *testing.T) {
	actual := reconnectActualRecovery(t)
	for _, corruption := range []string{"missing-child-remote", "extra-child-remote"} {
		t.Run(corruption, func(t *testing.T) {
			expected := append([]reconnectObserved(nil), actual.actual...)
			for i, row := range expected {
				if row["action"] == "remote_pair" && row["child"] == true {
					if corruption == "missing-child-remote" {
						expected = append(expected[:i:i], expected[i+1:]...)
					} else {
						expected = append(expected[:i+1:i+1], append([]reconnectObserved{row}, expected[i+1:]...)...)
					}
					break
				}
			}
			d := newReconnectDriver(t, expected)
			if e := reconnectRunTrace(t, d); e == nil || d.mismatch == nil {
				t.Fatal("strict comparison must refuse missing/extra real native effect", e)
			}
			base, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			prefix := filepath.Join(base, "actual-divergence")
			if e = d.retainFailure(prefix, base); e != nil {
				t.Fatal(e)
			}
			var mismatch reconnectMismatch
			if e = reconnectDiagnosticRead(prefix+"-mismatch.json", base, &mismatch); e != nil {
				t.Fatal(e)
			}
			if mismatch.Row != len(d.actual)+1 || mismatch.Fact.PairCalls != d.f.pairCalls || len(mismatch.Fact.Artifacts) < 4 {
				t.Fatal("actual mismatch/raw evidence lost", mismatch)
			}
			for _, artifact := range mismatch.Fact.Artifacts {
				if len(artifact.Raw) == 0 {
					t.Fatal("raw native bytes missing")
				}
			}
			if e = d.retainFailure(prefix, base); e == nil {
				t.Fatal("existing diagnostic must not be replaced")
			}
			var facts []reconnectRawFact
			if e = reconnectDiagnosticRead(prefix+"-facts.json", base, &facts); e != nil || len(facts) != len(d.actual) {
				t.Fatal("matched actual prefix not retained", e)
			}
			raw, e := os.ReadFile(prefix + "-facts.json")
			if e != nil {
				t.Fatal(e)
			}
			var graph any
			if e = json.Unmarshal(raw, &graph); e != nil {
				t.Fatal(e)
			}
			var memberCount func(any) int
			memberCount = func(v any) int {
				n := 0
				switch x := v.(type) {
				case map[string]any:
					n = len(x)
					for _, value := range x {
						n += memberCount(value)
					}
				case []any:
					for _, value := range x {
						n += memberCount(value)
					}
				}
				return n
			}
			t.Logf("actual retained prefix facts=%d bytes=%d members=%d (diagnostic cap128*8192; original state cap8192)", len(facts), len(raw), memberCount(graph))
		})
	}
}

func TestReconnectFailureOversizeFactsStillRetainsMismatch(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	d := &reconnectDriver{
		actual:   []reconnectObserved{{"action": "init"}},
		facts:    []reconnectRawFact{{Root: json.RawMessage(`"` + string(bytes.Repeat([]byte("x"), 4<<20)) + `"`)}},
		mismatch: &reconnectMismatch{Row: 2, Actual: reconnectObserved{"action": "remote_pair"}, Expected: reconnectObserved{"action": "local_pair"}},
	}
	prefix := filepath.Join(base, "oversize")
	if e = d.retainFailure(prefix, base); e == nil {
		t.Fatal("oversize facts must retain a failure")
	}
	if _, e = os.Stat(prefix + "-facts.json"); !os.IsNotExist(e) {
		t.Fatal("oversize artifact must not be published", e)
	}
	var mismatch reconnectMismatch
	if e = reconnectDiagnosticRead(prefix+"-mismatch.json", base, &mismatch); e != nil || mismatch.Row != 2 {
		t.Fatal("independent mismatch evidence must survive facts refusal", e)
	}
	statesBefore, e := os.ReadFile(prefix + "-states.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = d.retainFailure(prefix, base); e == nil {
		t.Fatal("existing diagnostic replacement must refuse")
	}
	statesAfter, e := os.ReadFile(prefix + "-states.json")
	if e != nil || !bytes.Equal(statesBefore, statesAfter) {
		t.Fatal("existing evidence changed", e)
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"duplicate", []byte(`{"row":1,"row":2}`)},
		{"escaped-duplicate", []byte(`{"row":1,"\u0072ow":2}`)},
		{"unknown", []byte(`{"row":1,"unrecognized":true}`)},
		{"trailing", []byte(`{"row":1} {}`)},
		{"invalid-utf8", []byte{'{', '"', 'r', 'o', 'w', '"', ':', '"', 255, '"', '}'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(base, test.name+".json")
			if e := os.WriteFile(path, test.raw, 0600); e != nil {
				t.Fatal(e)
			}
			var out reconnectMismatch
			if e := reconnectDiagnosticRead(path, base, &out); e == nil {
				t.Fatal("strict diagnostic reader accepted malformed/unknown input")
			}
		})
	}
	if e := reconnectJSONUnique([]byte(`"` + string(bytes.Repeat([]byte("x"), 16385)) + `"`)); e == nil {
		t.Fatal("original state scalar bound changed")
	}
}
