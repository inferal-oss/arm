package providerproto

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func TestExpectedTargetDetachesEveryBoundedString(t *testing.T) {
	// Every small field deliberately aliases the same actual 8 MiB allocation.
	// Length-only validation cannot establish the retained-memory bound.
	backing := strings.Repeat("1", 8<<20)
	defer runtime.KeepAlive(backing)
	id := ID(backing[17:25])
	receipt := Digest(backing[43:107])
	spec := Digest(backing[131:195])
	revision := Uint(backing[211:212])
	identity := Identity{ExternalScope: ExternalScope{id, id, id}, ID: id, Incarnation: id}
	target, e := TargetFromTrusted(identity, receipt, revision, spec)
	if e != nil {
		t.Fatal(e)
	}
	if target.identity != identity || target.receipt != receipt || target.revision != revision || target.specification != spec {
		t.Fatal("detachment changed field values")
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(backing)))
	end := start + uintptr(len(backing))
	for field, s := range map[string]string{"id": string(target.identity.ID), "incarnation": string(target.identity.Incarnation), "account": string(target.identity.ExternalScope.Account), "project": string(target.identity.ExternalScope.Project), "namespace": string(target.identity.ExternalScope.Namespace), "receipt": string(target.receipt), "revision": string(target.revision), "specification": string(target.specification)} {
		address := uintptr(unsafe.Pointer(unsafe.StringData(s)))
		if address >= start && address < end {
			t.Errorf("%s retains arbitrary caller backing despite bounded validated value", field)
		}
	}
}
func TestKnownTargetRevisionFloorAcrossEveryOutcome(t *testing.T) {
	for _, state := range []string{"pending", "ready", "ending", "released", "failed", "unknown", "no_effect"} {
		t.Run(state, func(t *testing.T) {
			method := Renew
			if state == "ending" || state == "released" {
				method = Release
			}
			q := base(t, method)
			var identity Identity
			var prior Digest
			switch p := q.Params.(type) {
			case RenewParams:
				p.ExpectedRevision = "2"
				q.Params = p
				identity, prior = p.ExternalIdentity, p.PriorReceiptSHA256
			case ReleaseParams:
				p.ExpectedRevision = "2"
				q.Params = p
				identity, prior = p.ExternalIdentity, p.PriorReceiptSHA256
			}
			q = seal(t, q)
			reg, original, target := trust(t, q)
			sent, e := MatchRequest(dec(t, q), reg, original, target)
			if e != nil {
				t.Fatal(e)
			}
			r := reply(q)
			r.Result = OperationReceipt{Kind: "operation", ProviderRequestID: "provider-record", State: state, ExternalIdentity: &identity, Revision: "1", SpecificationSHA256: target.specification, PriorReceiptSHA256: &prior, Evidence: Evidence{"fixture/v1", NullData(), NullData().SHA256()}, Metadata: NullData()}
			if _, e = MatchResponse(decReply(t, r), sent, reg, original, target); e == nil {
				t.Errorf("%s positively claims revision 1 below trusted revision 2", state)
			}
			p := r.Result.(OperationReceipt)
			p.Revision = "2"
			r.Result = p
			if _, e = MatchResponse(decReply(t, r), sent, reg, original, target); e != nil {
				t.Fatal("equal positive revision should match", e)
			}
			p.Revision = "0"
			r.Result = p
			if state == "ready" || state == "ending" || state == "released" {
				if _, e = EncodeResponse(r); e == nil {
					t.Fatal("positive outcome accepted unknown revision")
				}
			} else {
				matched, e := MatchResponse(decReply(t, r), sent, reg, original, target)
				if e != nil {
					t.Fatal("explicit unknown revision remains data", e)
				}
				if matched.Response().Result.(OperationReceipt).Revision != "0" || target.revision != "2" {
					t.Fatal("unknown observation erased known target floor")
				}
			}
		})
	}
}
