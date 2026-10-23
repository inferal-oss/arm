package providerproto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func username(s Username) *Username { return &s }

func data(t *testing.T, s string) Data {
	t.Helper()
	d, e := ParseData([]byte(s))
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func seal(t *testing.T, q Request) Request {
	t.Helper()
	d, e := IntentDigest(q)
	if e != nil {
		t.Fatal(e)
	}
	q.Context.IntentSHA256 = d
	return q
}
func base(t *testing.T, m Method) Request {
	t.Helper()
	spec := data(t, `{"payload":"test"}`)
	c := data(t, `{"account":"account","state_root":"/unused"}`)
	r := Registration{ID: "registration", Generation: "1", CodeSHA256: Digest(strings.Repeat("1", 64)), ImplementationVersion: "fixture-1", ResourceType: "object", ConfigSHA256: c.SHA256(), ExternalScope: ExternalScope{"account", "project", "namespace"}}
	q := Request{Protocol: Protocol, Version: Version, Kind: "request", RequestID: "wire", Registration: r, Configuration: c, CredentialRefs: []string{}, Method: m, Context: Context{Authority: "authority", Scope: Scope{Kind: "host", HostID: "host"}, OwnerUID: "1000", OwnerName: username("owner"), Reason: "test operation", OperationKey: "operation"}}
	i := Identity{r.ExternalScope, "external-immutable", "incarnation-1"}
	d := Digest(strings.Repeat("2", 64))
	switch m {
	case Describe:
		q.Params = DescribeParams{}
	case Validate:
		q.Params = ValidateParams{}
	case Plan:
		q.Params = PlanParams{spec, []Budget{{"objects", "1"}}, "2030-01-01T00:00:00Z"}
	case Create:
		q.Params = CreateParams{spec, spec.SHA256(), []Budget{{"objects", "1"}}, "2030-01-01T00:00:00Z", []Identity{}}
	case Renew:
		q.Params = RenewParams{i, d, "1", "2030-01-02T00:00:00Z"}
	case Release:
		q.Params = ReleaseParams{i, d, "1", "delete"}
	default:
		t.Fatal(m)
	}
	return seal(t, q)
}
func trust(t *testing.T, q Request) (RegistrationSnapshot, Original, *ExpectedTarget) {
	t.Helper()
	r, e := RegistrationFromTrusted(q.Registration, q.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	o, e := OriginalFromTrusted(q)
	if e != nil {
		t.Fatal(e)
	}
	var target *ExpectedTarget
	spec := data(t, `{"payload":"test"}`).SHA256()
	var i Identity
	var d Digest
	var v Uint
	switch p := q.Params.(type) {
	case RenewParams:
		i, d, v = p.ExternalIdentity, p.PriorReceiptSHA256, p.ExpectedRevision
	case ReleaseParams:
		i, d, v = p.ExternalIdentity, p.PriorReceiptSHA256, p.ExpectedRevision
	}
	if v != "" {
		x, e := TargetFromTrusted(i, d, v, spec)
		if e != nil {
			t.Fatal(e)
		}
		target = &x
	}
	return r, o, target
}
func query(t *testing.T, q Request, m Method) Request {
	t.Helper()
	p := QueryParams{TargetMethod: q.Method}
	switch x := q.Params.(type) {
	case RenewParams:
		i, d := x.ExternalIdentity, x.PriorReceiptSHA256
		p.ExternalIdentity = &i
		p.PriorReceiptSHA256 = &d
	case ReleaseParams:
		i, d := x.ExternalIdentity, x.PriorReceiptSHA256
		p.ExternalIdentity = &i
		p.PriorReceiptSHA256 = &d
	}
	q = cloneRequest(q)
	q.Method = m
	q.Params = p
	q.RequestID = "query-wire"
	return q
}
func dec(t *testing.T, q Request) DecodedRequest {
	t.Helper()
	b, e := EncodeRequest(q)
	if e != nil {
		t.Fatal(e)
	}
	d, e := DecodeRequest(b)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func reply(q Request) Response {
	return Response{Protocol: Protocol, Version: Version, Kind: "response", RequestID: q.RequestID, Registration: q.Registration, Method: q.Method, OperationKey: q.Context.OperationKey, IntentSHA256: q.Context.IntentSHA256}
}
func decReply(t *testing.T, r Response) DecodedResponse {
	t.Helper()
	b, e := EncodeResponse(r)
	if e != nil {
		t.Fatal(e)
	}
	d, e := DecodeResponse(b)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestCanonicalUnicodeNumbersAndAliases(t *testing.T) {
	d := data(t, "{\"z\":\"\\uD83D\\uDE00\\/\\u2028<>&\\b\\t\\n\\f\\r\\u0001\",\"a\":\"é\"}")
	want := "{\"a\":\"é\",\"z\":\"😀/\u2028<>&\\b\\t\\n\\f\\r\\u0001\"}"
	if string(d.Canonical()) != want {
		t.Fatalf("canonical %q want %q", d.Canonical(), want)
	}
	b := d.Canonical()
	b[0] = 'x'
	if string(d.Canonical()) != want {
		t.Fatal("alias")
	}
	for _, raw := range []string{`1`, `{"n":1}`, `[1]`, `-1`, `1.0`, `1e0`, `01`, `{"x":NaN}`, `{"x":null,"\u0078":false}`, `{"x":"\uD800"}`, `{"x":"\uDC00"}`, `{"x":"\uD800\u0041"}`, `{"x":"\u0000"}`, `{"\u0000":true}`, string([]byte{'"', 0xff, '"'}), "\"a\x00b\""} {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			if _, e := ParseData([]byte(raw)); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	q := base(t, Validate)
	b, e := EncodeRequest(q)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"1.0", "1e0", "-1", "2", "\"1\""} {
		bad := bytes.Replace(b, []byte(`"version":1`), []byte(`"version":`+n), 1)
		if _, e = DecodeRequest(bad); e == nil {
			t.Fatal("version", n)
		}
	}
}
func TestJSONLimits(t *testing.T) {
	for _, n := range []int{4096, 4097} {
		_, e := ParseData([]byte(`"` + strings.Repeat("x", n) + `"`))
		if (e == nil) != (n == 4096) {
			t.Fatal(n, e)
		}
	}
	for _, n := range []int{2048, 2049} {
		_, e := ParseData([]byte(`"` + strings.Repeat("é", n) + `"`))
		if (e == nil) != (n == 2048) {
			t.Fatal("UTF8 bytes", n, e)
		}
	}
	for _, n := range []int{256, 257} {
		_, e := ParseData([]byte("[" + strings.TrimSuffix(strings.Repeat("null,", n), ",") + "]"))
		if (e == nil) != (n == 256) {
			t.Fatal("array", n, e)
		}
	}
	for _, n := range []int{64, 65} {
		var parts []string
		for i := range n {
			parts = append(parts, fmt.Sprintf(`"k%d":null`, i))
		}
		_, e := ParseData([]byte("{" + strings.Join(parts, ",") + "}"))
		if (e == nil) != (n == 64) {
			t.Fatal("members", n, e)
		}
	}
	for _, n := range []int{15, 16} {
		_, e := ParseData([]byte(strings.Repeat("[", n) + "null" + strings.Repeat("]", n)))
		if (e == nil) != (n == 15) {
			t.Fatal("depth", n, e)
		}
	}
	small := `{"a":null,"b":null,"c":null,"d":null,"e":null,"f":null,"g":null,"h":null}`
	if _, e := ParseData([]byte("[" + strings.TrimSuffix(strings.Repeat(small+",", 256), ",") + "]")); e == nil {
		t.Fatal("aggregate nodes")
	}
	if _, e := DecodeRequest(bytes.Repeat([]byte{' '}, MaxRequestBytes+1)); e == nil {
		t.Fatal("request bytes")
	}
	if _, e := DecodeResponse(bytes.Repeat([]byte{' '}, MaxResponseBytes+1)); e == nil {
		t.Fatal("response bytes")
	}
	large := "[" + strings.TrimSuffix(strings.Repeat(`"`+strings.Repeat("x", 4096)+`",`, 4), ",") + "]"
	if _, e := ParseData([]byte(large)); e == nil {
		t.Fatal("canonical data bytes")
	}
}
func TestClosedTypesAndConfiguration(t *testing.T) {
	q := base(t, Create)
	b, e := EncodeRequest(q)
	if e != nil {
		t.Fatal(e)
	}
	cases := [][2]string{{`"protocol":`, `"Protocol":`}, {`"protocol":`, `"extra":null,"protocol":`}, {`"request_id":"wire"`, `"request_id":"wire","request_\u0069d":"alias"`}, {`"configuration":`, `"absent_configuration":`}, {`"credential_refs":[]`, `"credential_refs":null`}, {`"params":{`, `"params":{"configuration":null,`}, {`"limit":"1"`, `"limit":1`}, {`"limit":"1"`, `"limit":"9223372036854775808"`}, {`"reason":"test operation"`, `"reason":null`}}
	for _, c := range cases {
		bad := bytes.Replace(b, []byte(c[0]), []byte(c[1]), 1)
		if bytes.Equal(b, bad) {
			t.Fatal("bad fixture", c)
		}
		if _, e = DecodeRequest(bad); e == nil {
			t.Fatal("accepted", c)
		}
	}
	for _, good := range []string{"0", "1", "9223372036854775807"} {
		if _, e = uintValue(good); e != nil {
			t.Fatal(e)
		}
	}
	for _, bad := range []string{"", "-1", "+1", "01", " 1", "1.0", "1e2", "9223372036854775808", "99999999999999999999"} {
		if _, e = uintValue(bad); e == nil {
			t.Fatal(bad)
		}
	}
	q.Context.Reason = Text(string([]byte{0xff}))
	if _, e = EncodeRequest(q); e == nil {
		t.Fatal("Go Unicode replacement")
	}
}

type oneByte struct{ r io.Reader }

func (r oneByte) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.r.Read(p)
}

type shortWriter struct{ b bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return w.b.Write(p)
}

type prefixOnly struct {
	prefix []byte
	reads  int
}

func (r *prefixOnly) Read(p []byte) (int, error) {
	r.reads++
	if len(r.prefix) == 0 {
		panic("read oversized body")
	}
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	return n, nil
}

type noProgress struct{}

func (noProgress) Read([]byte) (int, error)  { return 0, nil }
func (noProgress) Write([]byte) (int, error) { return 0, nil }
func TestFramesExactAndBounded(t *testing.T) {
	q := base(t, Validate)
	var w shortWriter
	if e := WriteRequest(&w, q); e != nil {
		t.Fatal(e)
	}
	frame := w.b.Bytes()
	if _, e := ReadRequest(oneByte{bytes.NewReader(frame)}); e != nil {
		t.Fatal(e)
	}
	for _, b := range [][]byte{nil, frame[:3], frame[:len(frame)-1], append(append([]byte{}, frame...), 0), append(append([]byte{}, frame...), frame...)} {
		if _, e := ReadRequest(bytes.NewReader(b)); e == nil {
			t.Fatal("bad frame")
		}
	}
	for _, n := range []uint32{0, MaxRequestBytes + 1, ^uint32(0)} {
		var p [4]byte
		binary.BigEndian.PutUint32(p[:], n)
		r := &prefixOnly{prefix: p[:]}
		if _, e := ReadRequest(r); e == nil || r.reads != 1 {
			t.Fatal(n, e)
		}
	}
	if _, e := ReadRequest(noProgress{}); !errors.Is(e, io.ErrNoProgress) {
		t.Fatal(e)
	}
	if e := WriteRequest(noProgress{}, q); !errors.Is(e, io.ErrShortWrite) {
		t.Fatal(e)
	}
}
func TestTrustedMatchingAndOriginalQueryIntent(t *testing.T) {
	for _, m := range []Method{Describe, Validate, Plan, Create, Renew, Release} {
		t.Run(string(m), func(t *testing.T) {
			q := base(t, m)
			reg, o, target := trust(t, q)
			if _, e := MatchRequest(dec(t, q), reg, o, target); e != nil {
				t.Fatal(e)
			}
			if m.mutation() {
				for _, qm := range []Method{Lookup, Reconcile} {
					qr := query(t, q, qm)
					if _, e := IntentDigest(qr); e == nil {
						t.Fatal("query digest")
					}
					if _, e := OriginalFromTrusted(qr); e == nil {
						t.Fatal("query promoted")
					}
					if _, e := MatchRequest(dec(t, qr), reg, o, target); e != nil {
						t.Fatal(e)
					}
					for _, change := range []func(*Request){func(v *Request) { v.Context.Reason = "new reason" }, func(v *Request) { v.Context.IntentSHA256 = Digest(strings.Repeat("3", 64)) }, func(v *Request) { v.Context.OperationKey = "new-key" }, func(v *Request) { v.Configuration = NullData() }, func(v *Request) {
						p := v.Params.(QueryParams)
						p.TargetMethod = Create
						if q.Method == Create {
							p.TargetMethod = Release
						}
						v.Params = p
					}, func(v *Request) {
						p := v.Params.(QueryParams)
						i := Identity{q.Registration.ExternalScope, "retargeted", "new"}
						p.ExternalIdentity = &i
						v.Params = p
					}} {
						bad := cloneRequest(qr)
						change(&bad)
						if _, e := MatchRequest(dec(t, bad), reg, o, target); e == nil {
							t.Fatal("query escaped original")
						}
					}
					if _, e := MatchRequest(dec(t, qr), reg, Original{}, target); e == nil {
						t.Fatal("missing original")
					}
				}
			}
			snapshot := o.Request()
			snapshot.Context.Reason = "mutated"
			if p, ok := snapshot.Params.(CreateParams); ok {
				p.Budgets[0].Limit = "9"
			}
			if _, e := MatchRequest(dec(t, q), reg, o, target); e != nil {
				t.Fatal("alias", e)
			}
		})
	}
}
func TestReplyCorrelationAndUnknownIsData(t *testing.T) {
	q := base(t, Create)
	reg, o, target := trust(t, q)
	sent, e := MatchRequest(dec(t, q), reg, o, target)
	if e != nil {
		t.Fatal(e)
	}
	spec := q.Params.(CreateParams).SpecificationSHA256
	ev := data(t, `{"record":"durable-external-row"}`)
	r := reply(q)
	r.Result = OperationReceipt{Kind: "operation", ProviderRequestID: "provider-operation", State: "ready", ExternalIdentity: &Identity{q.Registration.ExternalScope, "id", "incarnation"}, Revision: "1", SpecificationSHA256: spec, Evidence: Evidence{"fixture.sqlite/v1", ev, ev.SHA256()}, Metadata: NullData()}
	if _, e = MatchResponse(decReply(t, r), sent, reg, o, target); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Response){func(v *Response) { v.RequestID = "other" }, func(v *Response) { v.IntentSHA256 = Digest(strings.Repeat("a", 64)) }, func(v *Response) { v.Registration.Generation = "2" }, func(v *Response) {
		x := v.Result.(OperationReceipt)
		x.ExternalIdentity.ExternalScope.Namespace = "other"
		v.Result = x
	}, func(v *Response) {
		x := v.Result.(OperationReceipt)
		x.SpecificationSHA256 = Digest(strings.Repeat("b", 64))
		v.Result = x
	}, func(v *Response) {
		x := v.Result.(OperationReceipt)
		x.Evidence.SHA256 = Digest(strings.Repeat("c", 64))
		v.Result = x
	}, func(v *Response) { x := v.Result.(OperationReceipt); x.State = "released"; v.Result = x }} {
		bad := cloneResponse(r)
		change(&bad)
		if _, e = MatchResponse(decReply(t, bad), sent, reg, o, target); e == nil {
			t.Fatal("mismatch")
		}
	}
	r.Result = nil
	r.Error = &ProtocolError{Code: "not_found", Message: "no record"}
	got, e := MatchResponse(decReply(t, r), sent, reg, o, target)
	if e != nil || got.Response().Error.Code != "not_found" {
		t.Fatal(e)
	}
	r.Error = nil
	r.Result = OperationReceipt{Kind: "operation", ProviderRequestID: "unknown-lookup", State: "unknown", Revision: "0", SpecificationSHA256: spec, Evidence: Evidence{"fixture.sqlite/v1", NullData(), NullData().SHA256()}, Metadata: NullData()}
	if _, e = MatchResponse(decReply(t, r), sent, reg, o, target); e != nil {
		t.Fatal(e)
	}
}
func TestConcurrentImmutableMatching(t *testing.T) {
	q := base(t, Create)
	reg, o, target := trust(t, q)
	d := dec(t, q)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 10 {
				got, e := MatchRequest(d, reg, o, target)
				if e != nil {
					t.Error(e)
					return
				}
				c := got.Request()
				c.Context.Reason = "different"
				p := c.Params.(CreateParams)
				p.Budgets[0].Limit = "5"
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(d.Request(), q) {
		t.Fatal("alias")
	}
}
func TestFreshHelper(t *testing.T) {
	if os.Getenv("ARM_PROVIDERPROTO_HELPER") != "1" {
		return
	}
	d, e := ReadRequest(os.Stdin)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(41)
	}
	q := d.Request()
	var c struct {
		Account string `json:"account"`
		Root    string `json:"state_root"`
		Spec    Digest `json:"specification_sha256"`
	}
	if e = json.Unmarshal(q.Configuration.Canonical(), &c); e != nil || q.Configuration.SHA256() != q.Registration.ConfigSHA256 || c.Account != string(q.Registration.ExternalScope.Account) {
		os.Exit(42)
	}
	b, e := os.ReadFile(filepath.Join(c.Root, "sentinel"))
	if e != nil || string(b) != "owned-config-only-store" {
		os.Exit(43)
	}
	r := reply(q)
	switch p := q.Params.(type) {
	case DescribeParams:
		r.Result = Descriptor{Kind: "descriptor", ImplementationID: "fixture", ImplementationVersion: q.Registration.ImplementationVersion, ProtocolVersions: []int{1}, ResourceTypes: []Name{q.Registration.ResourceType}, Methods: []Method{Describe, Validate, Plan, Create, Lookup, Renew, Release, Reconcile}, Metadata: NullData()}
	case ValidateParams:
		r.Result = Validated{"validated", q.Configuration.SHA256()}
	case PlanParams:
		r.Result = Planned{"planned", p.Specification.SHA256(), []Budget{}, "delayed", NullData()}
	default:
		var prior *Digest
		switch x := q.Params.(type) {
		case QueryParams:
			prior = x.PriorReceiptSHA256
		case RenewParams:
			d := x.PriorReceiptSHA256
			prior = &d
		case ReleaseParams:
			d := x.PriorReceiptSHA256
			prior = &d
		}
		r.Result = OperationReceipt{Kind: "operation", ProviderRequestID: "helper-unknown", State: "unknown", Revision: "0", SpecificationSHA256: c.Spec, PriorReceiptSHA256: prior, Evidence: Evidence{"helper-config/v1", NullData(), NullData().SHA256()}, Metadata: NullData()}
	}
	if e = WriteResponse(os.Stdout, r); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(44)
	}
	os.Exit(0)
}
func TestAllEightFreshProcessesReceiveActualConfiguration(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "sentinel"), []byte("owned-config-only-store"), 0600); e != nil {
		t.Fatal(e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range []Method{Describe, Validate, Plan, Create, Lookup, Renew, Release, Reconcile} {
		t.Run(string(m), func(t *testing.T) {
			om := m
			if m == Lookup {
				om = Create
			}
			if m == Reconcile {
				om = Release
			}
			q := base(t, om)
			q.Context.OwnerName = nil // OS lookup may be unavailable in a fresh invocation.
			spec := data(t, `{"payload":"test"}`).SHA256()
			raw, e := json.Marshal(map[string]string{"account": "account", "state_root": root, "specification_sha256": string(spec)})
			if e != nil {
				t.Fatal(e)
			}
			q.Configuration = data(t, string(raw))
			q.Registration.ConfigSHA256 = q.Configuration.SHA256()
			q = seal(t, q)
			reg, o, target := trust(t, q)
			if m.query() {
				q = query(t, q, m)
			}
			sent, e := MatchRequest(dec(t, q), reg, o, target)
			if e != nil {
				t.Fatal(e)
			}
			var frame bytes.Buffer
			if e = WriteRequest(&frame, q); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(exe, "-test.run=^TestFreshHelper$")
			cmd.Env = []string{"ARM_PROVIDERPROTO_HELPER=1"}
			cmd.Stdin = &frame
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			if e = cmd.Run(); e != nil {
				t.Fatalf("fresh %s: %v %s", m, e, stderr.Bytes())
			}
			d, e := ReadResponse(&out)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = MatchResponse(d, sent, reg, o, target); e != nil {
				t.Fatal(e)
			}
		})
	}
}

type hostileParams struct{ DescribeParams }

func (hostileParams) MarshalJSON() ([]byte, error) { panic("caller callback executed") }

type hostileResult struct{ Validated }

func (hostileResult) MarshalJSON() ([]byte, error) { panic("caller callback executed") }
func TestClosedGoInterfacesAndDetachedCopies(t *testing.T) {
	q := base(t, Describe)
	q.Params = hostileParams{}
	if _, e := EncodeRequest(q); e == nil {
		t.Fatal("open callback-bearing params")
	}
	q = base(t, Validate)
	r := reply(q)
	r.Result = hostileResult{}
	if _, e := EncodeResponse(r); e == nil {
		t.Fatal("open callback-bearing result")
	}
	id := ID("exact-agent")
	q.Context.Agent = &id
	q = seal(t, q)
	reg, o, target := trust(t, q)
	*q.Context.Agent = "mutated-after-trust"
	original := o.Request()
	if *original.Context.Agent != "exact-agent" {
		t.Fatal("trusted original pointer alias")
	}
	copy := o.Request()
	*copy.Context.Agent = "another-mutation"
	if _, e := MatchRequest(dec(t, original), reg, o, target); e != nil {
		t.Fatal(e)
	}
	r = reply(original)
	r.Error = &ProtocolError{Code: "unavailable", Message: "private diagnostic"}
	d := decReply(t, r)
	out := d.Response()
	out.Error.Message = "changed"
	if d.Response().Error.Message != "private diagnostic" {
		t.Fatal("response pointer alias")
	}
}
func TestKnownCreateQueryAndRenewalTargetFences(t *testing.T) {
	q := base(t, Create)
	reg, o, _ := trust(t, q)
	identity := Identity{q.Registration.ExternalScope, "existing-id", "incarnation"}
	receipt := Digest(strings.Repeat("4", 64))
	target, e := TargetFromTrusted(identity, receipt, "1", q.Params.(CreateParams).SpecificationSHA256)
	if e != nil {
		t.Fatal(e)
	}
	qr := query(t, q, Lookup)
	p := qr.Params.(QueryParams)
	p.ExternalIdentity = &identity
	p.PriorReceiptSHA256 = &receipt
	qr.Params = p
	if _, e = MatchRequest(dec(t, qr), reg, o, &target); e != nil {
		t.Fatal(e)
	}
	badTarget := target
	badTarget.identity.Incarnation = "changed"
	if _, e = MatchRequest(dec(t, qr), reg, o, &badTarget); e == nil {
		t.Fatal("known create target retargeted")
	}
	renewal := base(t, Renew)
	rr, ro, rt := trust(t, renewal)
	if _, e = MatchRequest(dec(t, renewal), rr, ro, nil); e == nil {
		t.Fatal("renewal accepted missing prior target")
	}
	badTarget = *rt
	badTarget.revision = "2"
	if _, e = MatchRequest(dec(t, renewal), rr, ro, &badTarget); e == nil {
		t.Fatal("renewal revision retargeted")
	}
}
func TestMalformedResponseAndQueryDerivedDigestRefused(t *testing.T) {
	q := base(t, Validate)
	r := reply(q)
	r.Result = Validated{"validated", q.Registration.ConfigSHA256}
	b, e := EncodeResponse(r)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range [][2]string{{`"result":{`, `"configuration":null,"result":{`}, {`"kind":"validated"`, `"kind":"validated","extra":null`}, {`"error":null`, `"error":{"code":"not_found","message":"missing","retry_after_seconds":null}`}, {`"kind":"validated"`, `"kind":"planned"`}, {`"config_sha256":`, `"Config_sha256":`}} {
		bad := bytes.Replace(b, []byte(c[0]), []byte(c[1]), 1)
		if bytes.Equal(b, bad) {
			t.Fatal("fixture", c)
		}
		if _, e = DecodeResponse(bad); e == nil {
			t.Fatal("malformed response", c)
		}
	}
	original := base(t, Create)
	reg, o, target := trust(t, original)
	qr := query(t, original, Lookup)
	raw, e := EncodeRequest(qr)
	if e != nil {
		t.Fatal(e)
	}
	n, e := parse(raw, MaxRequestBytes)
	if e != nil {
		t.Fatal(e)
	}
	delete(n.object, "kind")
	delete(n.object, "request_id")
	ctx := n.object["context"]
	delete(ctx.object, "intent_sha256")
	qr.Context.IntentSHA256 = digest(canonical(n))
	if qr.Context.IntentSHA256 == original.Context.IntentSHA256 {
		t.Fatal("bad fixture hash")
	}
	if _, e = MatchRequest(dec(t, qr), reg, o, target); e == nil {
		t.Fatal("lookup-derived hash accepted")
	}
}
func FuzzDataRoundTrip(f *testing.F) {
	for _, s := range []string{`null`, `{"z":"\uD83D\uDE00","a":[true,false,null]}`, `{"x":null,"\u0078":false}`, `"\uD800"`, `1`, string([]byte{0xff})} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		d, e := ParseData(b)
		if e != nil {
			return
		}
		again, e := ParseData(d.Canonical())
		if e != nil || again != d {
			t.Fatal("canonical data roundtrip", e)
		}
	})
}
func FuzzRequestRoundTrip(f *testing.F) {
	examples, err := schemaFiles.ReadFile("schema/examples-v1.json")
	if err != nil {
		f.Fatal(err)
	}
	var seeds struct {
		Requests map[string]json.RawMessage `json:"requests"`
	}
	if err = json.Unmarshal(examples, &seeds); err != nil {
		f.Fatal(err)
	}
	for _, name := range []string{"validate", "create", "lookup"} {
		f.Add([]byte(seeds.Requests[name]))
	}
	for _, s := range []string{`{}`, `null`, `{"version":1}`, `{"request_id":"\uDC00"}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		d, e := DecodeRequest(b)
		if e != nil {
			return
		}
		out, e := EncodeRequest(d.Request())
		if e != nil {
			t.Fatal(e)
		}
		again, e := DecodeRequest(out)
		if e != nil || !reflect.DeepEqual(d.Request(), again.Request()) {
			t.Fatal("request roundtrip", e)
		}
	})
}

func TestHumanUsernamesAndRequiredReasonText(t *testing.T) {
	for _, name := range []Username{"rémy", "re\u0301my", Username(strings.Repeat("é", 128))} {
		q := base(t, Validate)
		q.Context.OwnerName = &name
		q.Context.Reason = "  exact reason\nwith spacing\t"
		q = seal(t, q)
		reg, o, target := trust(t, q)
		d := dec(t, q)
		if d.request.Context.OwnerName == nil || *d.request.Context.OwnerName != name || d.request.Context.Reason != q.Context.Reason || d.request.Context.OwnerUID != "1000" {
			t.Fatal("human attribution changed")
		}
		if _, e := MatchRequest(d, reg, o, target); e != nil {
			t.Fatal(e)
		}
	}
	q := base(t, Validate)
	q.Context.OwnerName = username("rémy")
	q = seal(t, q)
	reg, o, target := trust(t, q)
	changed := cloneRequest(q)
	changed.Context.OwnerName = username("re\u0301my")
	if _, e := MatchRequest(dec(t, changed), reg, o, target); e == nil {
		t.Fatal("silently normalized changed username")
	}
	raw, e := EncodeRequest(q)
	if e != nil {
		t.Fatal(e)
	}
	escaped := bytes.Replace(raw, []byte(`rémy`), []byte(`r\u00e9my`), 1)
	d, e := DecodeRequest(escaped)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := MatchRequest(d, reg, o, target); e != nil {
		t.Fatal("equivalent JSON escape changed name", e)
	}
	for _, bad := range []Username{"", Username(strings.Repeat("é", 129)), Username(string([]byte{0xff})), "a\x00b"} {
		v := cloneRequest(q)
		v.Context.OwnerName = &bad
		if _, e := EncodeRequest(v); e == nil {
			t.Fatal("invalid username", bad)
		}
	}
	for _, reason := range []Text{"", " ", "\t\r\n", "\u0085\u00a0\u1680\u2000\u200a\u2028\u2029\u202f\u205f\u3000"} {
		v := cloneRequest(q)
		v.Context.Reason = reason
		if _, e := EncodeRequest(v); e == nil {
			t.Fatal("blank reason", reason)
		}
	}
	v := cloneRequest(q)
	v.RequestID = "rémy"
	if _, e := EncodeRequest(v); e == nil {
		t.Fatal("opaque ID accepted Unicode username policy")
	}
}

func TestKnownCreateQueryRejectsPositiveRevisionRegression(t *testing.T) {
	q := base(t, Create)
	reg, o, _ := trust(t, q)
	i := Identity{q.Registration.ExternalScope, "known-id", "incarnation"}
	prior := Digest(strings.Repeat("4", 64))
	spec := q.Params.(CreateParams).SpecificationSHA256
	target, e := TargetFromTrusted(i, prior, "2", spec)
	if e != nil {
		t.Fatal(e)
	}
	qr := query(t, q, Lookup)
	qr.Params = QueryParams{TargetMethod: Create, ExternalIdentity: &i, PriorReceiptSHA256: &prior}
	sent, e := MatchRequest(dec(t, qr), reg, o, &target)
	if e != nil {
		t.Fatal(e)
	}
	r := reply(qr)
	r.Result = OperationReceipt{Kind: "operation", ProviderRequestID: "provider-record", State: "ready", ExternalIdentity: &i, Revision: "1", SpecificationSHA256: spec, Evidence: Evidence{"fixture.sqlite/v1", NullData(), NullData().SHA256()}, Metadata: NullData()}
	if _, e = MatchResponse(decReply(t, r), sent, reg, o, &target); e == nil {
		t.Fatal("positive known target revision regressed")
	}
	p := r.Result.(OperationReceipt)
	p.Revision = "2"
	r.Result = p
	if _, e = MatchResponse(decReply(t, r), sent, reg, o, &target); e != nil {
		t.Fatal(e)
	}
}
