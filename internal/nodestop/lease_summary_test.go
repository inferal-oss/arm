package nodestop

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLeasePreviewCloneAndClosedDecodeReplaceOptionalState(t *testing.T) {
	b, p, _, now := contractFixture(t)
	p.Impact.Outstanding = 1
	p.Impact.Obligations = []Obligation{{ID: "lease:one", Kind: "lease", State: "active", Revision: 1, LeaseDetails: "permitted", Lease: &LeaseSummary{Owner: "501", Username: "recorded-owner", Reason: "build real project"}}}
	prepared, err := b.Prepare("preview", p.Actor, p.DesiredRevision, p.Policy, p.Reason, p.Impact, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	p.Impact.Obligations[0].Lease.Reason = "mutated original pointer"
	if prepared.Impact.Obligations[0].Lease.Reason != "build real project" {
		t.Fatal("prepared plan aliased caller summary")
	}
	copy := clonePlan(prepared)
	copy.Impact.Obligations[0].Lease.Username = "changed clone"
	if prepared.Impact.Obligations[0].Lease.Username != "recorded-owner" {
		t.Fatal("plan clones share optional summary")
	}
	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Plan
	if err = Decode(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	old := decoded.Impact.Obligations[0].Lease
	redacted := clonePlan(prepared)
	redacted.Impact.Obligations[0].Lease = nil
	redacted.Impact.Obligations[0].LeaseDetails = "not-permitted"
	raw, _ = json.Marshal(redacted)
	if err = Decode(raw, &decoded); err != nil || decoded.Impact.Obligations[0].Lease != nil {
		t.Fatal("omitted preview retained private prior decode", err)
	}
	if old.Username != "recorded-owner" {
		t.Fatal("fresh decode mutated old externally held summary")
	}
	before, _ := json.Marshal(decoded)
	for _, alter := range []func([]byte) []byte{
		func(v []byte) []byte {
			return bytes.Replace(v, []byte(`"lease_details":"not-permitted"`), []byte(`"lease_details":"permitted","lease":null`), 1)
		},
		func(v []byte) []byte {
			return bytes.Replace(v, []byte(`"lease_details":"not-permitted"`), []byte(`"lease_details":"not-permitted","Lease":{}`), 1)
		},
		func(v []byte) []byte {
			return bytes.Replace(v, []byte(`"lease_details":"not-permitted"`), []byte(`"lease_details":"not-permitted","lease_details":"permitted"`), 1)
		},
	} {
		if Decode(alter(raw), &decoded) == nil {
			t.Fatal("bad optional schema accepted")
		}
		after, _ := json.Marshal(decoded)
		if !bytes.Equal(before, after) {
			t.Fatal("failed decode partially overwrote retained state")
		}
	}
}
func TestLeaseSummaryDigestAndPlanBound(t *testing.T) {
	_, p, _, _ := contractFixture(t)
	p.Impact.Outstanding = 1
	p.Impact.Obligations = []Obligation{{ID: "lease:one", Kind: "lease", State: "active", Revision: 1, LeaseDetails: "permitted", Lease: &LeaseSummary{Owner: "501", Reason: "purpose"}}}
	a, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	p.Impact.Obligations[0].Lease.Reason = "other purpose"
	b, err := p.Digest()
	if err != nil || a == b {
		t.Fatal("purpose not bound", err)
	}
	p.Reason = strings.Repeat("<", 4096)
	p.Target.Home = "/" + strings.Repeat("<", 4090)
	if _, err = p.Digest(); err == nil {
		t.Fatal("encoded plan over 48KiB accepted")
	}
}
