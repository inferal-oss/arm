package customnames

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

const testHost = "0123456789abcdef0123456789abcdef"
const testLease = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testDoc() Document {
	return Document{Version: 1, Owner: "501", Host: testHost, Revision: 1, Entries: []Entry{{Kind: "lease", AuthorityHost: testHost, ID: testLease, BindingLease: "", Name: "build-cache", Active: true}}}
}
func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestCustomNamesDecodeClosedSchemaAndAuthority(t *testing.T) {
	base := string(encode(t, testDoc()))
	for _, tt := range []struct{ name, raw string }{
		{"empty", ""}, {"null", "null"}, {"trailing", base + "{}"}, {"unknown", strings.Replace(base, `"version":1`, `"version":1,"extra":true`, 1)},
		{"case", strings.Replace(base, `"version":1`, `"Version":1`, 1)}, {"duplicate", strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1)},
		{"missing", strings.Replace(base, `"binding_lease":"",`, "", 1)}, {"scalar-null", strings.Replace(base, `"active":true`, `"active":null`, 1)},
		{"entries-null", strings.Replace(base, strings.Split(base, `"entries":`)[1], "null}", 1)},
		{"nested-case", strings.Replace(base, `"kind":"lease"`, `"Kind":"lease"`, 1)}, {"nested-duplicate", strings.Replace(base, `"active":true`, `"active":true,"active":false`, 1)},
		{"wrong-owner", strings.Replace(base, `"owner":"501"`, `"owner":"502"`, 1)}, {"wrong-host", strings.Replace(base, `"host":"`+testHost+`"`, `"host":"`+testLease+`"`, 1)},
		{"version", strings.Replace(base, `"version":1`, `"version":2`, 1)}, {"revision-zero", strings.Replace(base, `"revision":1`, `"revision":0`, 1)},
		{"revision-float", strings.Replace(base, `"revision":1`, `"revision":1.0`, 1)}, {"invalid-utf8", base + string([]byte{255})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode([]byte(tt.raw), "501", testHost); err == nil {
				t.Fatal("invalid metadata accepted", tt.raw)
			}
		})
	}
	if doc, err := Decode([]byte(base), "501", testHost); err != nil || !reflect.DeepEqual(doc, testDoc()) {
		t.Fatal(doc, err)
	}
	doc, err := Decode(nil, "501", testHost)
	if err != nil || doc.Revision != 0 || doc.Entries == nil || len(doc.Entries) != 0 {
		t.Fatal(doc, err)
	}
	if _, err = Decode(nil, "0501", testHost); err == nil {
		t.Fatal("noncanonical owner accepted")
	}
}
func TestCustomNamesReservedUnsafeAndGeneratedNames(t *testing.T) {
	for _, name := range []string{"", "-build", "_build", "a b", "a:b", "a/b", "a\nnext", "工作", "localhost", "LOCALHOST", testHost, "POOL_" + testLease, "abacus-abacus-abacus-abacus", "drop-down-felt-tip-t-shirt-yo-yo", strings.Repeat("a", 129)} {
		if err := ValidateName(name); err == nil {
			t.Fatal("unsafe/reserved name accepted", name)
		}
	}
	for _, name := range []string{"Build-cache", "v1.2_3", strings.Repeat("a", 128), "abacus-abacus-abacus", "custom-42-part-name"} {
		if err := ValidateName(name); err != nil {
			t.Fatal(name, err)
		}
	}
}
func TestCustomNamesScopeCollisionsAndPortIncarnations(t *testing.T) {
	doc := testDoc()
	other := doc.Entries[0]
	other.ID = strings.Repeat("b", 32)
	other.Name = "BUILD-CACHE"
	other.Active = false
	doc.Entries = append(doc.Entries, other)
	if _, err := Decode(encode(t, doc), "501", testHost); err == nil {
		t.Fatal("casefold reservation collision accepted")
	}
	doc = testDoc()
	other = doc.Entries[0]
	other.Name = "second-name"
	doc.Entries = append(doc.Entries, other)
	if _, err := Decode(encode(t, doc), "501", testHost); err == nil {
		t.Fatal("multiple active names accepted")
	}
	doc.Entries[1].Active = false
	if _, err := Decode(encode(t, doc), "501", testHost); err != nil {
		t.Fatal("inactive reservation refused", err)
	}
	port := Entry{Kind: "resource", AuthorityHost: testHost, ID: testHost + "/ports/host/tcp4/127.0.0.1:8080", BindingLease: testLease, Name: "preview-http", Active: true}
	doc.Entries = []Entry{port}
	if _, err := Decode(encode(t, doc), "501", testHost); err != nil {
		t.Fatal(err)
	}
	next := port
	next.BindingLease = strings.Repeat("b", 32)
	if SameBinding(port, next) || ActiveName(doc, next) != "" {
		t.Fatal("reused locator retargeted old alias")
	}
	for _, bad := range []Entry{{Kind: "host", AuthorityHost: testHost, ID: testLease}, {Kind: "resource", AuthorityHost: testHost, ID: port.ID}, {Kind: "resource", AuthorityHost: testHost, ID: testHost + "/cpu", BindingLease: testLease}, {Kind: "resource", AuthorityHost: testHost, ID: testHost + "/ports/host/tcp4/127.0.0.1:0", BindingLease: testLease}, {Kind: "pool", AuthorityHost: testHost, ID: "pool_" + testLease, BindingLease: testLease}} {
		if ValidateBinding(bad) == nil {
			t.Fatal("bad incarnation/binding accepted", bad)
		}
	}
}
func TestCustomNamesMutationCASReservationClearAndReuse(t *testing.T) {
	doc := testDoc()
	before := encode(t, doc)
	target := doc.Entries[0]
	target.Name = "new-cache"
	next, changed, err := Mutate(doc, "set", target, 1)
	retired := doc.Entries[0]
	retired.Active = false
	foundRetired := false
	for _, entry := range next.Entries {
		foundRetired = foundRetired || entry == retired
	}
	if err != nil || !changed || next.Revision != 2 || len(next.Entries) != 2 || !foundRetired || ActiveName(next, target) != "new-cache" {
		t.Fatal(next, changed, err)
	}
	if !bytes.Equal(before, encode(t, doc)) {
		t.Fatal("set mutated original document")
	}
	if _, _, err = Mutate(next, "set", target, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	noop, changed, err := Mutate(next, "set", target, 2)
	if err != nil || changed || !bytes.Equal(encode(t, noop), encode(t, next)) {
		t.Fatal("no-op changed metadata", err)
	}
	old := doc.Entries[0]
	replacement := old
	replacement.ID = strings.Repeat("b", 32)
	if _, _, err = Mutate(next, "set", replacement, 2); !errors.Is(err, ErrNameConflict) {
		t.Fatal("reserved name retargeted", err)
	}
	cleared, changed, err := Mutate(next, "clear", old, 2)
	if err != nil || !changed || len(cleared.Entries) != 1 || cleared.Revision != 3 {
		t.Fatal(cleared, err)
	}
	reused, changed, err := Mutate(cleared, "set", replacement, 3)
	if err != nil || !changed || reused.Revision != 4 || len(reused.Entries) != 2 {
		t.Fatal("explicit clear did not permit deliberate reuse", reused, err)
	}
	entriesByID := make(map[string]Entry, len(reused.Entries))
	for _, entry := range reused.Entries {
		if _, duplicate := entriesByID[entry.ID]; duplicate {
			t.Fatal("reuse duplicated an exact binding", reused)
		}
		entriesByID[entry.ID] = entry
	}
	wantEntries := map[string]Entry{target.ID: target, replacement.ID: replacement}
	if !reflect.DeepEqual(entriesByID, wantEntries) || ActiveName(reused, old) != "new-cache" || ActiveName(reused, replacement) != "build-cache" {
		t.Fatal("reuse lost retained or deliberately reused binding", reused)
	}
	if _, _, err = Mutate(reused, "clear", old, 4); !errors.Is(err, ErrNameConflict) {
		t.Fatal("wrong exact binding clear accepted", err)
	}
}
func TestCustomNamesBoundsStayClearableAndRevisionDoesNotReset(t *testing.T) {
	doc := testDoc()
	doc.Entries = nil
	for i := 0; i < MaxEntries; i++ {
		doc.Entries = append(doc.Entries, Entry{Kind: "lease", AuthorityHost: testHost, ID: fmt.Sprintf("%032x", i+1), Name: fmt.Sprintf("job-%d", i), Active: true})
	}
	target := Entry{Kind: "lease", AuthorityHost: testHost, ID: testLease, Name: "new-job"}
	if _, _, err := Mutate(doc, "set", target, 1); !errors.Is(err, ErrLimit) {
		t.Fatal("entry bound ignored", err)
	}
	smaller, changed, err := Mutate(doc, "clear", doc.Entries[0], 1)
	if err != nil || !changed || len(smaller.Entries) != MaxEntries-1 {
		t.Fatal("bound is not administratively clearable", err)
	}
	if _, _, err = Mutate(smaller, "set", target, 2); err != nil {
		t.Fatal(err)
	}
	single := testDoc()
	empty, _, err := Mutate(single, "clear", single.Entries[0], 1)
	if err != nil || empty.Revision != 2 || empty.Entries == nil {
		t.Fatal(empty, err)
	}
	if _, err = Decode(encode(t, empty), "501", testHost); err != nil {
		t.Fatal(err)
	}
	hugeArray := `{"version":1,"owner":"501","host":"` + testHost + `","revision":1,"entries":[` + strings.Repeat("null,", MaxEntries) + `null]}`
	if _, arrayErr := Decode([]byte(hugeArray), "501", testHost); !errors.Is(arrayErr, ErrLimit) {
		t.Fatal("entry stream allocation bound ignored", arrayErr)
	}
	if _, err = Decode(bytes.Repeat([]byte{'x'}, MaxBytes+1), "501", testHost); !errors.Is(err, ErrLimit) {
		t.Fatal("byte bound ignored", err)
	}
	single.Revision = math.MaxInt64
	if _, changed, err = Mutate(single, "set", single.Entries[0], math.MaxInt64); err != nil || changed {
		t.Fatal("maximum revision no-op failed", err)
	}
	if _, _, err = Mutate(single, "clear", single.Entries[0], math.MaxInt64); !errors.Is(err, ErrLimit) {
		t.Fatal("revision overflow", err)
	}
}

func TestCustomNamesHostNamespaceIsSourceLocalAcrossTargetHosts(t *testing.T) {
	doc := testDoc()
	first := Entry{Kind: "host", AuthorityHost: testHost, ID: testHost, Name: "worker", Active: true}
	second := first
	second.AuthorityHost = testLease
	second.ID = testLease
	second.Name = "Worker"
	doc.Entries = []Entry{first, second}
	if _, err := Decode(encode(t, doc), "501", testHost); err == nil {
		t.Fatal("two target hosts shared a source-local host name")
	}
	doc.Entries = doc.Entries[:1]
	if _, _, err := Mutate(doc, "set", second, 1); !errors.Is(err, ErrNameConflict) {
		t.Fatal("host assignment ignored source-local namespace", err)
	}
	lease := Entry{Kind: "lease", AuthorityHost: testHost, ID: testLease, Name: "worker", Active: true}
	otherLease := lease
	otherLease.AuthorityHost = strings.Repeat("b", 32)
	doc.Entries = []Entry{lease, otherLease}
	if _, err := Decode(encode(t, doc), "501", testHost); err != nil {
		t.Fatal("host namespace rule incorrectly merged lease authority scopes", err)
	}
}
