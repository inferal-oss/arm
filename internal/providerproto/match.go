package providerproto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
)

func digest(b []byte) Digest { s := sha256.Sum256(b); return Digest(hex.EncodeToString(s[:])) }

// These constructors only validate/copy trusted inputs. They DO NOT establish
// trust or authorization. Callers must obtain their inputs from the approved
// registration and durable original typed intent, not from provider responses.
type RegistrationSnapshot struct {
	registration  Registration
	configuration Data
	valid         bool
}

func RegistrationFromTrusted(r Registration, c Data) (RegistrationSnapshot, error) {
	b, e := encode(r, MaxRequestBytes)
	if e != nil {
		return RegistrationSnapshot{}, e
	}
	n, e := parse(b, MaxRequestBytes)
	if e != nil {
		return RegistrationSnapshot{}, e
	}
	var checked Registration
	if e = assign(n, reflect.ValueOf(&checked).Elem()); e != nil {
		return RegistrationSnapshot{}, e
	}
	if e = shapeRegistration(checked); e != nil {
		return RegistrationSnapshot{}, e
	}
	if c.canonical == "" || c.SHA256() != checked.ConfigSHA256 {
		return RegistrationSnapshot{}, fmt.Errorf("registration configuration digest mismatch")
	}
	return RegistrationSnapshot{checked, c, true}, nil
}

type Original struct {
	request Request
	valid   bool
}

// IntentDigest rejects queries: only a saved original mutation can supply their
// intent. Version is literal JSON 1; configuration/specification/data have NO
// numbers. Request kind, wire request ID and the self digest are not intent.
func IntentDigest(q Request) (Digest, error) {
	if q.Method.query() {
		return "", fmt.Errorf("query requires saved original mutation")
	}
	q.Context.IntentSHA256 = Digest(strings.Repeat("0", 64))
	b, e := EncodeRequest(q)
	if e != nil {
		return "", e
	}
	n, e := parse(b, MaxRequestBytes)
	if e != nil {
		return "", e
	}
	delete(n.object, "kind")
	delete(n.object, "request_id")
	context := n.object["context"]
	delete(context.object, "intent_sha256")
	return digest(canonical(n)), nil
}
func OriginalFromTrusted(q Request) (Original, error) {
	if q.Method.query() {
		return Original{}, fmt.Errorf("cannot save query as original")
	}
	b, e := EncodeRequest(q)
	if e != nil {
		return Original{}, e
	}
	decoded, e := DecodeRequest(b)
	if e != nil {
		return Original{}, e
	}
	q = decoded.request
	d, e := IntentDigest(q)
	if e != nil || d != q.Context.IntentSHA256 {
		return Original{}, fmt.Errorf("original typed intent digest mismatch")
	}
	if q.Configuration.SHA256() != q.Registration.ConfigSHA256 {
		return Original{}, fmt.Errorf("original configuration mismatch")
	}
	if c, ok := q.Params.(CreateParams); ok && c.SpecificationSHA256 != c.Specification.SHA256() {
		return Original{}, fmt.Errorf("original specification digest mismatch")
	}
	return Original{cloneRequest(q), true}, nil
}
func (o Original) Request() Request { return cloneRequest(o.request) }

// ExpectedTarget contains expected prior identity/receipt/revision/specification
// from trusted ledger evidence. Decoding a receipt does not create this value.
// The constructor cannot prove ownership, authenticity, readiness or policy.
type ExpectedTarget struct {
	identity      Identity
	receipt       Digest
	revision      Uint
	specification Digest
	valid         bool
}

func TargetFromTrusted(i Identity, receipt Digest, revision Uint, spec Digest) (ExpectedTarget, error) {
	v := struct {
		Identity      Identity `json:"identity"`
		Receipt       Digest   `json:"receipt"`
		Revision      Uint     `json:"revision"`
		Specification Digest   `json:"specification"`
	}{i, receipt, revision, spec}
	b, e := encode(v, MaxRequestBytes)
	if e != nil {
		return ExpectedTarget{}, e
	}
	n, e := parse(b, MaxRequestBytes)
	if e != nil {
		return ExpectedTarget{}, e
	}
	if e = assign(n, reflect.ValueOf(&v).Elem()); e != nil {
		return ExpectedTarget{}, e
	}
	if revision == "0" {
		return ExpectedTarget{}, fmt.Errorf("positive expected target revision required")
	}
	return ExpectedTarget{v.Identity, v.Receipt, v.Revision, v.Specification, true}, nil
}

type MatchedRequest struct{ request Request }

func (m MatchedRequest) Request() Request { return cloneRequest(m.request) }

type MatchedResponse struct{ response Response }

func (m MatchedResponse) Response() Response { return cloneResponse(m.response) }

func trustedExpected(reg RegistrationSnapshot, original Original, target *ExpectedTarget) error {
	if !reg.valid || !original.valid {
		return fmt.Errorf("trusted registration/original required")
	}
	q := original.request
	if q.Registration != reg.registration || q.Configuration != reg.configuration {
		return fmt.Errorf("original differs from selected registration")
	}
	if target != nil && (!target.valid || target.identity.ExternalScope != reg.registration.ExternalScope) {
		return fmt.Errorf("trusted expected target mismatch")
	}
	switch p := q.Params.(type) {
	case CreateParams:
		if target != nil && target.specification != p.SpecificationSHA256 {
			return fmt.Errorf("expected create specification mismatch")
		}
	case RenewParams:
		if target == nil || target.identity != p.ExternalIdentity || target.receipt != p.PriorReceiptSHA256 || target.revision != p.ExpectedRevision {
			return fmt.Errorf("renewal requires exact trusted prior target")
		}
	case ReleaseParams:
		if target == nil || target.identity != p.ExternalIdentity || target.receipt != p.PriorReceiptSHA256 || target.revision != p.ExpectedRevision {
			return fmt.Errorf("release requires exact trusted prior target")
		}
	default:
		if target != nil {
			return fmt.Errorf("diagnostic cannot have resource target")
		}
	}
	return nil
}

// MatchRequest compares untrusted decoded data to trusted ORIGINAL typed input.
// For queries expected context/digest always come from the original mutation;
// current query-actor authorization/audit is independently enforced by ARM.
func MatchRequest(decoded DecodedRequest, reg RegistrationSnapshot, original Original, target *ExpectedTarget) (MatchedRequest, error) {
	if e := trustedExpected(reg, original, target); e != nil {
		return MatchedRequest{}, e
	}
	q := decoded.request
	expected := cloneRequest(original.request)
	expected.RequestID = q.RequestID
	if q.Method.query() {
		if !original.request.Method.mutation() {
			return MatchedRequest{}, fmt.Errorf("query target is not a saved mutation")
		}
		expected.Method = q.Method
		p := QueryParams{TargetMethod: original.request.Method}
		switch v := original.request.Params.(type) {
		case CreateParams:
			if target != nil {
				i, d := target.identity, target.receipt
				p.ExternalIdentity = &i
				p.PriorReceiptSHA256 = &d
			}
		case RenewParams:
			i, d := v.ExternalIdentity, v.PriorReceiptSHA256
			p.ExternalIdentity = &i
			p.PriorReceiptSHA256 = &d
		case ReleaseParams:
			i, d := v.ExternalIdentity, v.PriorReceiptSHA256
			p.ExternalIdentity = &i
			p.PriorReceiptSHA256 = &d
		}
		expected.Params = p
	}
	got, e := EncodeRequest(q)
	if e != nil {
		return MatchedRequest{}, e
	}
	want, e := EncodeRequest(expected)
	if e != nil {
		return MatchedRequest{}, e
	}
	if !bytes.Equal(got, want) {
		return MatchedRequest{}, fmt.Errorf("request differs from trusted original operation/target")
	}
	return MatchedRequest{cloneRequest(q)}, nil
}

// Matching is correlation/semantic validation, NOT proof of external truth or
// permission to free capacity. Even matched errors, failed/unknown and SIGKILL
// cannot establish no-effect, readiness, reclamation or a native join.
func MatchResponse(decoded DecodedResponse, sent MatchedRequest, reg RegistrationSnapshot, original Original, target *ExpectedTarget) (MatchedResponse, error) {
	if _, e := MatchRequest(DecodedRequest{sent.request}, reg, original, target); e != nil {
		return MatchedResponse{}, e
	}
	r := decoded.response
	q := sent.request
	// Revalidate copies, including explicit zero wrappers, before use.
	raw, e := EncodeResponse(r)
	if e != nil {
		return MatchedResponse{}, e
	}
	fresh, e := DecodeResponse(raw)
	if e != nil {
		return MatchedResponse{}, e
	}
	r = fresh.response
	if r.RequestID != q.RequestID || r.Method != q.Method || r.Registration != reg.registration || r.OperationKey != original.request.Context.OperationKey || r.IntentSHA256 != original.request.Context.IntentSHA256 {
		return MatchedResponse{}, fmt.Errorf("response correlation mismatch")
	}
	if r.Error != nil {
		return MatchedResponse{cloneResponse(r)}, nil
	}
	switch v := r.Result.(type) {
	case Descriptor:
		if v.ImplementationVersion != reg.registration.ImplementationVersion {
			return MatchedResponse{}, fmt.Errorf("provider implementation version mismatch")
		}
		found := false
		for _, t := range v.ResourceTypes {
			if t == reg.registration.ResourceType {
				found = true
			}
		}
		if !found {
			return MatchedResponse{}, fmt.Errorf("provider resource type unavailable")
		}
	case Validated:
		if v.ConfigurationSHA256 != reg.registration.ConfigSHA256 {
			return MatchedResponse{}, fmt.Errorf("validated different configuration")
		}
	case Planned:
		p, ok := original.request.Params.(PlanParams)
		if !ok || v.SpecificationSHA256 != p.Specification.SHA256() {
			return MatchedResponse{}, fmt.Errorf("planned different specification")
		}
	case OperationReceipt:
		if e := matchReceipt(v, reg, original, target); e != nil {
			return MatchedResponse{}, e
		}
	}
	return MatchedResponse{cloneResponse(r)}, nil
}
func matchReceipt(r OperationReceipt, reg RegistrationSnapshot, original Original, target *ExpectedTarget) error {
	if r.Evidence.SHA256 != r.Evidence.Data.SHA256() {
		return fmt.Errorf("operation evidence digest mismatch")
	}
	if r.ExternalIdentity != nil && r.ExternalIdentity.ExternalScope != reg.registration.ExternalScope {
		return fmt.Errorf("external account/project/namespace mismatch")
	}
	var prior *Digest
	var identity *Identity
	var spec Digest
	var revision Uint
	switch p := original.request.Params.(type) {
	case CreateParams:
		spec = p.SpecificationSHA256
		if target != nil {
			i := target.identity
			identity = &i
			revision = target.revision
		}
	case RenewParams:
		i, d := p.ExternalIdentity, p.PriorReceiptSHA256
		identity = &i
		prior = &d
		revision = p.ExpectedRevision
		spec = target.specification
	case ReleaseParams:
		i, d := p.ExternalIdentity, p.PriorReceiptSHA256
		identity = &i
		prior = &d
		revision = p.ExpectedRevision
		spec = target.specification
	default:
		return fmt.Errorf("operation response lacks saved original mutation")
	}
	if r.SpecificationSHA256 != spec || !reflect.DeepEqual(r.PriorReceiptSHA256, prior) {
		return fmt.Errorf("operation specification/prior receipt mismatch")
	}
	if r.ExternalIdentity != nil && identity != nil && *r.ExternalIdentity != *identity {
		return fmt.Errorf("operation immutable identity mismatch")
	}
	if original.request.Method == Release {
		if r.State == "ready" {
			return fmt.Errorf("release returned creation readiness")
		}
	} else if r.State == "ending" || r.State == "released" {
		return fmt.Errorf("non-release returned deletion outcome")
	}
	if r.State == "no_effect" && original.request.Method == Create && r.ExternalIdentity != nil {
		return fmt.Errorf("create no-effect carries allocated identity")
	}
	// Any claimed positive revision is evidence, even on pending/error-like
	// outcomes. Unknown revision 0 is allowed only by the state schema and
	// remains data; it never replaces this known prior floor.
	if revision != "" && r.Revision != "0" {
		got, _ := uintValue(string(r.Revision))
		old, _ := uintValue(string(revision))
		if got < old {
			return fmt.Errorf("operation revision regressed")
		}
	}
	return nil
}
