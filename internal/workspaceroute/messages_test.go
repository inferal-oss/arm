package workspaceroute

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/published"
	"github.com/inferal-oss/arm/internal/routing"
	"github.com/inferal-oss/arm/internal/workspace"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

func messageFixture() (Open, Request) {
	now := time.Now().UTC()
	o := Open{Version: Version, Workspace: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Route: strings.Repeat("3", 32), Source: strings.Repeat("a", 32), Target: strings.Repeat("c", 32), SourceGeneration: 1, TargetGeneration: 2, DirectoryRevision: 7, Path: []string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)}, Hop: 2, Class: ClassMessage, Expires: now.Add(10 * time.Second), Pair: strings.Repeat("d", 64), Revision: 1}
	e := routing.Envelope{Schema: routing.Schema, Workspace: o.Workspace, Generation: o.Generation, ID: strings.Repeat("4", 32), Key: "private-key", Origin: o.Source, Target: o.Target, From: routing.HostEndpoint(1), To: routing.HostEndpoint(2), OriginMembership: 1, TargetMembership: 2, OriginPin: strings.Repeat("5", 64), TargetPin: strings.Repeat("6", 64), Body: "private message", Created: now, Expires: now.Add(time.Minute)}
	return o, Request{Version: Version, Route: o.Route, Workspace: o.Workspace, Generation: o.Generation, Source: o.Source, Target: o.Target, Operation: OperationMessage, Envelope: &e}
}
func framed(raw []byte) []byte {
	out := make([]byte, len(raw)+4)
	binary.BigEndian.PutUint32(out, uint32(len(raw)))
	copy(out[4:], raw)
	return out
}
func TestClosedMessageWireBindsOriginalEndpointsClassesAndReceipt(t *testing.T) {
	o, q := messageFixture()
	var b bytes.Buffer
	if e := WriteRequest(&b, o, q); e != nil {
		t.Fatal(e)
	}
	read, e := ReadRequest(&b, o)
	if e != nil || read.Envelope.Digest() != q.Envelope.Digest() {
		t.Fatal(read, e)
	}
	for _, op := range []string{"run", "acquire", "ssh", "message_send", "workspace_deny", "node_open"} {
		copy := q
		copy.Operation = op
		if copy.Validate(o) == nil {
			t.Fatalf("foreign operation %s admitted", op)
		}
	}
	copy := o
	copy.Source = copy.Path[1]
	if q.Validate(copy) == nil {
		t.Fatal("relay replaced original source")
	}
	copy = o
	copy.SourceGeneration++
	if q.Validate(copy) == nil {
		t.Fatal("different source membership admitted")
	}
	q.Operation = OperationLookup
	if q.Validate(o) == nil {
		t.Fatal("lookup consumed a message slot")
	}
	o.Class = ClassLookup
	if e := q.Validate(o); e != nil {
		t.Fatal(e)
	}
	result := q.Response()
	result.Lookup = &LookupResult{}
	if e := WriteReply(&b, q, result); e != nil {
		t.Fatal(e)
	}
	got, e := ReadReply(&b, q)
	if e != nil || got.Lookup.Found {
		t.Fatal(got, e)
	}
	result.Lookup.Found = true
	if result.Validate(q) == nil {
		t.Fatal("found receipt without exact receipt accepted")
	}
	receipt := routing.Receipt{Schema: routing.Schema, Workspace: o.Workspace, Generation: o.Generation, ID: q.Envelope.ID, Digest: q.Envelope.Digest(), Origin: q.Source, From: q.Envelope.From, Target: q.Target, To: q.Envelope.To, State: "accepted", Revision: 1, At: time.Now()}
	result.Lookup.Receipt = &receipt
	if e := result.Validate(q); e != nil {
		t.Fatal(e)
	}
	result.Source = o.Path[1]
	if result.Validate(q) == nil {
		t.Fatal("relay could claim final receipt")
	}
}
func TestClosedMessageWireStrictJSONAndLengthBeforeAllocation(t *testing.T) {
	o, q := messageFixture()
	raw, e := json.Marshal(q)
	if e != nil {
		t.Fatal(e)
	}
	cases := [][]byte{
		bytes.Replace(raw, []byte(`"operation"`), []byte(`"Operation"`), 1),
		append(append([]byte{}, raw...), []byte(` {}`)...),
		bytes.Replace(raw, []byte(`"operation":"message_accept"`), []byte(`"operation":"message_accept","params":{"command":["touch","bad"]}`), 1),
		bytes.Replace(raw, []byte(`"operation":"message_accept"`), []byte(`"operation":"message_accept","refresh":null`), 1),
		bytes.Replace(raw, []byte(`"operation":"message_accept"`), []byte(`"operation":"message_accept","operation":"run"`), 1),
	}
	for _, bad := range cases {
		if _, e := ReadRequest(bytes.NewReader(framed(bad)), o); e == nil {
			t.Fatalf("malformed record admitted: %s", bad)
		}
	}
	for _, size := range []uint32{0, MaxRequestBytes + 1, ^uint32(0)} {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], size)
		if _, e := ReadRequest(bytes.NewReader(prefix[:]), o); e == nil {
			t.Fatal("unbounded record admitted", size)
		}
	}
	for _, mutate := range []func(*routing.Envelope){func(e *routing.Envelope) { e.OriginPin = "" }, func(e *routing.Envelope) { e.Body = strings.Repeat("x", routing.MaxBodyBytes+1) }} {
		env := *q.Envelope
		mutate(&env)
		changed := q
		changed.Envelope = &env
		if WriteRequest(&bytes.Buffer{}, o, changed) == nil {
			t.Fatal("invalid immutable message admitted")
		}
	}
}

func TestPublishedEndpointPageClosedWireAndPollOnlyCards(t *testing.T) {
	o, q := messageFixture()
	o.Class = ClassLookup
	q.Operation = OperationEndpoints
	q.Envelope = nil
	q.Endpoints = &published.Query{Workspace: o.Workspace, Nonce: strings.Repeat("7", 32), Limit: 16}
	if e := q.Validate(o); e != nil {
		t.Fatal(e)
	}
	r := q.Response()
	r.Endpoints = &published.Page{Workspace: o.Workspace, Generation: o.Generation, Host: o.Target, Nonce: q.Endpoints.Nonce, Expires: o.Expires, Cards: []published.Card{{Endpoint: published.Address{Kind: "agent", ID: strings.Repeat("8", 32), Generation: strings.Repeat("9", 32)}, Label: "Minimal public label", Delivery: "poll", Revision: 1}}, Total: 1, Next: 1}
	var b bytes.Buffer
	if e := WriteRequest(&b, o, q); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadRequest(&b, o); e != nil {
		t.Fatal(e)
	}
	if e := r.Validate(q); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Request){func(v *Request) { v.Refresh = &workspace.RefreshRequest{} }, func(v *Request) { _, original := messageFixture(); v.Envelope = original.Envelope }, func(v *Request) { v.Report = &workspacegraph.Report{} }} {
		bad := q
		mutate(&bad)
		if bad.Validate(o) == nil {
			t.Fatal("mixed endpoint query admitted")
		}
	}
	r.Endpoints.Cards[0].Delivery = "native"
	if r.Validate(q) == nil {
		t.Fatal("poll endpoint discovery promoted to native")
	}
	r.Endpoints.Cards[0].Delivery = "poll"
	r.Endpoints.Nonce = strings.Repeat("0", 32)
	if r.Validate(q) == nil {
		t.Fatal("page retargeted query nonce")
	}
}
