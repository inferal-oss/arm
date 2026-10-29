package resident

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testIdentity() Identity {
	return Identity{Host: strings.Repeat("1", 32), Owner: strconv.Itoa(os.Geteuid()), Generation: strings.Repeat("2", 32)}
}
func testWorker(n string) WorkerIdentity {
	return WorkerIdentity{Generation: strings.Repeat(n, 32), SHA256: strings.Repeat("a", 64), Version: "fixture-v1"}
}

func testAuthority(t *testing.T, dispatch Dispatch) (*Authority, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "receipts")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(dir, testIdentity(), dispatch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a, dir
}

// Pure gate fixtures do not claim an executable handshake or native cleanup.
func unitBinding(t *testing.T, a *Authority, n string) *Binding {
	t.Helper()
	b, err := a.Stage(testWorker(n), Principal{Kind: "local-fixture", Owner: a.identity.Owner})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	b.handshake = true
	close(b.ready)
	a.mu.Unlock()
	if err = a.Activate(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func unitRequest(a *Authority, b *Binding, op, key string, params string) Request {
	return Request{Protocol: Protocol, Resident: a.identity, Worker: b.worker, Capability: b.capability, ID: 1, Operation: op, Key: key, Params: json.RawMessage(params)}
}

func TestResidentEpochScopeAndReplay(t *testing.T) {
	var calls atomic.Int32
	a, _ := testAuthority(t, func(_ context.Context, c Call) (json.RawMessage, error) {
		if c.Principal.Kind != "local-fixture" || c.Principal.Owner != testIdentity().Owner {
			t.Error("caller scope changed")
		}
		calls.Add(1)
		return json.RawMessage(`{"lease":"original"}`), nil
	})
	b := unitBinding(t, a, "3")
	r := unitRequest(a, b, "run", "one", `{"command":"fixture"}`)
	wrong := r
	wrong.Resident.Generation = strings.Repeat("9", 32)
	if reply := a.call(b, wrong); reply.Error == nil || reply.Error.Code != "binding_changed" {
		t.Fatal(reply)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid binding reached dispatch")
	}
	first := a.call(b, r)
	if first.Error != nil || first.Outcome != "complete" {
		t.Fatal(first)
	}
	if done, err := a.Seal(b); err != nil {
		t.Fatal(err)
	} else {
		<-done
	}
	if reply := a.call(b, r); reply.Error == nil || reply.Error.Code != "stale_epoch" {
		t.Fatal(reply)
	}
	b2 := unitBinding(t, a, "4")
	replay := a.call(b2, unitRequest(a, b2, "run", "one", `{"command":"fixture"}`))
	if replay.Error != nil || !bytes.Equal(first.Result, replay.Result) || calls.Load() != 1 {
		t.Fatal("replay dispatched again", replay, calls.Load())
	}
	conflict := a.call(b2, unitRequest(a, b2, "run", "one", `{"command":"other"}`))
	if conflict.Error == nil || conflict.Error.Code != "idempotency_conflict" || calls.Load() != 1 {
		t.Fatal(conflict)
	}
	for _, op := range []string{"reset", "shutdown", "mesh_run", "run_append"} {
		if reply := a.call(b2, unitRequest(a, b2, op, "", `{}`)); reply.Error == nil || reply.Error.Code != "operation_forbidden" {
			t.Fatal(reply)
		}
	}
	if _, err := a.Stage(testWorker("5"), b.principal); err == nil {
		t.Fatal("retained generation bound bypassed")
	}
}

func TestResidentPendingAndJournalFaultNeverReplay(t *testing.T) {
	var calls atomic.Int32
	a, dir := testAuthority(t, func(_ context.Context, _ Call) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{}`), nil
	})
	b := unitBinding(t, a, "3")
	r := unitRequest(a, b, "run", "pending", `{}`)
	if err := a.journal.put(receipt{Resident: a.identity, Principal: b.principal, Key: r.Key, Operation: r.Operation, SHA256: requestHash(r), State: "pending"}, true); err != nil {
		t.Fatal(err)
	}
	reply := a.call(b, r)
	if reply.Outcome != "unknown" || calls.Load() != 0 {
		t.Fatal("pending replayed", reply)
	}
	// A real filesystem refusal after the effect is a commit uncertainty cut.
	a.dispatch = func(_ context.Context, _ Call) (json.RawMessage, error) {
		calls.Add(1)
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		return json.RawMessage(`{}`), nil
	}
	r = unitRequest(a, b, "renew", "fault", `{}`)
	reply = a.call(b, r)
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if reply.Outcome != "unknown" || reply.Error == nil || reply.Error.Code != "journal_uncertain" || calls.Load() != 1 {
		t.Fatal(reply)
	}
	if retry := a.call(b, r); retry.Error == nil || retry.Error.Code != "authority_unavailable" || calls.Load() != 1 {
		t.Fatal("fault replayed", retry)
	}
	raw, err := a.journal.read(receiptName(b.principal, r.Key), MaxResponse)
	var saved receipt
	if err != nil || decode(raw, &saved) != nil || saved.State != "pending" {
		t.Fatal("lost original pending receipt", err, saved.State)
	}
}

func TestResidentCloseAndSealDoNotCancelAdmittedEffect(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	a, _ := testAuthority(t, func(ctx context.Context, _ Call) (json.RawMessage, error) {
		close(entered)
		select {
		case <-finish:
			return json.RawMessage(`{}`), nil
		case <-ctx.Done():
			return nil, errors.New("requester cancelled resident effect")
		}
	})
	b := unitBinding(t, a, "3")
	result := make(chan Reply, 1)
	go func() { result <- a.call(b, unitRequest(a, b, "run", "held", `{}`)) }()
	<-entered
	if err := a.Close(); err == nil || err.(*Failure).Code != "dispatch_busy" {
		t.Fatal(err)
	}
	sealed := make(chan error, 1)
	go func() {
		done, err := a.Seal(b)
		if err == nil {
			<-done
		}
		sealed <- err
	}()
	close(finish)
	select {
	case reply := <-result:
		if reply.Error != nil {
			t.Fatal(reply)
		}
	case <-time.After(time.Second):
		t.Fatal("effect not joined")
	}
	if err := <-sealed; err != nil {
		t.Fatal(err)
	}
}

func TestResidentJournalExclusiveIdentityAndConservativeRecovery(t *testing.T) {
	a, dir := testAuthority(t, func(context.Context, Call) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	if other, err := Open(dir, a.identity, a.dispatch); err == nil {
		other.Close()
		t.Fatal("second journal writer admitted")
	}
	b := unitBinding(t, a, "3")
	if _, err := a.Seal(b); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir, a.identity, a.dispatch); err == nil {
		other.Close()
		t.Fatal("old worker epochs reconstructed")
	}
	changed := a.identity
	changed.Generation = strings.Repeat("9", 32)
	if other, err := Open(dir, changed, a.dispatch); err == nil {
		other.Close()
		t.Fatal("resident identity adopted")
	}
}

func TestResidentFrameAndHTTPClaimBounds(t *testing.T) {
	var raw bytes.Buffer
	_ = binary.Write(&raw, binary.BigEndian, uint32(MaxRequest+1))
	if err := readFrame(&raw, MaxRequest, new(Request)); err == nil {
		t.Fatal("oversized frame accepted")
	}
	if decode([]byte(`{"operation":"run","params":{},"principal":{"kind":"native"}}`), new(ClientRequest)) == nil {
		t.Fatal("HTTP caller claim accepted")
	}
	if decode([]byte(`{"operation":"status","params":{}} {}`), new(ClientRequest)) == nil {
		t.Fatal("trailing request accepted")
	}
	if decode([]byte(`{"operation":"status","operation":"run","params":{}}`), new(ClientRequest)) == nil {
		t.Fatal("duplicate operation accepted")
	}
	if decode([]byte(`{"operation":"run","params":{"key":"a","key":"b"}}`), new(ClientRequest)) == nil {
		t.Fatal("nested duplicate key accepted")
	}
	if decode([]byte(strings.Repeat("[", 34)+"0"+strings.Repeat("]", 34)), new(any)) == nil {
		t.Fatal("deep request accepted")
	}
}

func TestResidentDispatchPanicRetainsUnknownOutcome(t *testing.T) {
	var calls atomic.Int32
	a, _ := testAuthority(t, func(context.Context, Call) (json.RawMessage, error) {
		calls.Add(1)
		panic("fixture effect uncertainty")
	})
	b := unitBinding(t, a, "3")
	r := unitRequest(a, b, "run", "panic", `{}`)
	for i := 0; i < 2; i++ {
		reply := a.call(b, r)
		if reply.Outcome != "unknown" || reply.Error == nil || reply.Error.Code != "effect_unknown" {
			t.Fatal(reply)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("panic outcome executed again")
	}
}

func TestResidentJournalRejectsMalformedTerminalReceipt(t *testing.T) {
	a, dir := testAuthority(t, func(context.Context, Call) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	p := Principal{Kind: "local-fixture", Owner: a.identity.Owner}
	r := receipt{Resident: a.identity, Principal: p, Key: "malformed", Operation: "run", SHA256: strings.Repeat("a", 64), State: "complete"}
	name := receiptName(p, r.Key)
	if err := a.journal.write(name, r, true); err != nil {
		t.Fatal(err)
	}
	before, err := a.journal.read(name, MaxResponse)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir, a.identity, a.dispatch); err == nil {
		other.Close()
		t.Fatal("malformed terminal result accepted")
	}
	after, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("malformed receipt was repaired/erased", err)
	}
}

func TestResidentOutcomeOwnsDispatchBuffer(t *testing.T) {
	buffer := json.RawMessage(`{"value":1}`)
	a, _ := testAuthority(t, func(context.Context, Call) (json.RawMessage, error) { return buffer, nil })
	b := unitBinding(t, a, "3")
	r := unitRequest(a, b, "run", "owned-outcome", `{}`)
	first := a.call(b, r)
	if first.Error != nil {
		t.Fatal(first)
	}
	copy(buffer, []byte(`{"value":2}`))
	replay := a.call(b, r)
	if string(replay.Result) != `{"value":1}` || !bytes.Equal(first.Result, replay.Result) {
		t.Fatal("acknowledged receipt borrowed a mutable dispatch buffer")
	}
}

func TestResidentTypedFieldAliasesRejected(t *testing.T) {
	for _, raw := range []string{
		`{"operation":"status","Operation":"run","params":{}}`,
		`{"Operation":"run","operation":"status","params":{}}`,
		`{"operation":"status","key":"original","KEY":"replacement","params":{}}`,
		`{"Operation":"status","params":{}}`,
		`{"operation":"status","KEY":"replacement","params":{}}`,
		`{"operation":"status","key":"original","\u212aey":"replacement","params":{}}`,
	} {
		var request ClientRequest
		if err := decode([]byte(raw), &request); err == nil {
			t.Errorf("typed field alias accepted: %s", raw)
		}
	}
	var h hello
	if decode([]byte(`{"protocol":1,"resident":{"host":"one","Host":"two","owner":"1","generation":"g"},"worker":{"generation":"w","sha256":"s","version":"v"},"capability":"c"}`), &h) == nil {
		t.Error("nested typed identity alias accepted")
	}
	var saved receipt
	if decode([]byte(`{"state":"pending","State":"complete"}`), &saved) == nil {
		t.Error("saved outcome alias accepted")
	}
}

func TestResidentCanonicalTypedAndOpaqueKeys(t *testing.T) {
	params := `{"Key":1,"key":2,"KEY":3,"\u212aey":4,"nested":{"Operation":"opaque","operation":"data"}}`
	var request ClientRequest
	if err := decode([]byte(`{"operation":"status","key":"original","params":`+params+`}`), &request); err != nil {
		t.Fatal("canonical envelope with opaque keys refused", err)
	}
	if request.Operation != "status" || request.Key != "original" || string(request.Params) != params {
		t.Fatal("opaque request bytes changed")
	}
	var opaque map[string]any
	if err := decode([]byte(params), &opaque); err != nil || len(opaque) != 5 {
		t.Fatal("legitimate case-sensitive map keys refused", err)
	}
	type typed struct {
		Key string `json:"key"`
	}
	var keyed map[string]typed
	if err := decode([]byte(`{"Key":{"key":"upper"},"key":{"key":"lower"}}`), &keyed); err != nil || len(keyed) != 2 || keyed["Key"].Key != "upper" || keyed["key"].Key != "lower" {
		t.Fatal("typed map lost distinct keys", err)
	}
	if decode([]byte(`{"Key":{"key":"upper","KEY":"alias"}}`), &keyed) == nil {
		t.Error("typed map value alias accepted")
	}
	type embedded struct {
		Key string `json:"key"`
	}
	var promoted struct {
		embedded
		Operation string     `json:"operation"`
		IDs       []Identity `json:"ids"`
	}
	if err := decode([]byte(`{"key":"promoted","operation":"status","ids":[{"host":"h","owner":"1","generation":"g"}]}`), &promoted); err != nil || promoted.Key != "promoted" {
		t.Fatal("canonical embedded fields refused", err)
	}
	if decode([]byte(`{"key":"promoted","operation":"status","ids":[{"host":"h","HOST":"alias"}]}`), &promoted) == nil {
		t.Error("typed slice element alias accepted")
	}
	// A shallow map shadows a promoted typed field. Its data keys must not
	// accidentally inherit the shadowed struct's schema.
	type withTyped struct {
		Payload typed `json:"payload"`
	}
	var shallow struct {
		withTyped
		Payload map[string]int `json:"payload"`
	}
	if err := decode([]byte(`{"payload":{"key":1,"KEY":2}}`), &shallow); err != nil || len(shallow.Payload) != 2 {
		t.Fatal("shadowed typed field changed map semantics", err)
	}
	// At equal depth encoding/json selects the explicitly tagged field.
	type plain struct{ Payload typed }
	type tagged struct {
		Data map[string]int `json:"Payload"`
	}
	var dominant struct {
		plain
		tagged
	}
	if err := decode([]byte(`{"Payload":{"key":1,"KEY":2}}`), &dominant); err != nil || len(dominant.Data) != 2 {
		t.Fatal("tagged field dominance changed", err)
	}
	var dash struct {
		Value string `json:"-,omitempty"`
	}
	if err := decode([]byte(`{"-":"canonical"}`), &dash); err != nil || dash.Value != "canonical" {
		t.Fatal("valid dash wire name refused", err)
	}
}
