package manager

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

func TestNodeStopLeasePreviewsFollowOriginReadGrant(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	local, err := m.Acquire(model.Request{Resources: model.Resources{CPU: .25}, Reason: "private local purpose", TTL: time.Minute, Key: "local"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	own, err := m.AcquirePeer(actor.Host, model.Request{Resources: model.Resources{CPU: .25}, Reason: "own peer purpose", TTL: time.Minute, Key: "own"}, nil, model.Resources{CPU: 1}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := m.AcquirePeer(strings.Repeat("9", 32), model.Request{Resources: model.Resources{CPU: .25}, Reason: "private foreign purpose", TTL: time.Minute, Key: "foreign"}, nil, model.Resources{CPU: 1}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		scope nodestop.LeaseReadScope
		ids   []string
	}{{"none", nodestop.LeaseReadScope{Source: actor.Host}, nil}, {"own", nodestop.LeaseReadScope{Source: actor.Host, Own: true}, []string{own.ID}}, {"all", nodestop.LeaseReadScope{Source: actor.Host, All: true}, []string{local.ID, own.ID, foreign.ID}}, {"spoof", nodestop.LeaseReadScope{Source: strings.Repeat("9", 32), All: true}, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := m.PlanNodeStop("preview-"+tc.name, actor, model.ShutdownRequest{Wait: true}, "review", external, tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			if p.Impact.Outstanding != 3 || len(p.Impact.Obligations) != 3 {
				t.Fatal("redaction dropped obligations")
			}
			raw, _ := json.Marshal(p)
			for _, item := range p.Impact.Obligations {
				allowed := false
				for _, id := range tc.ids {
					allowed = allowed || item.ID == "lease:"+id
				}
				if allowed {
					if item.Lease == nil || item.Lease.Owner != m.owner || item.Lease.Username == "" || item.LeaseDetails != "permitted" {
						t.Fatal("recorded account or allowed summary absent", item)
					}
				} else if item.Lease != nil || item.LeaseDetails != "not-permitted" {
					t.Fatal("private summary disclosed", item)
				}
			}
			if tc.name == "none" || tc.name == "spoof" {
				for _, secret := range []string{local.Request.Reason, own.Request.Reason, foreign.Request.Reason} {
					if strings.Contains(string(raw), secret) {
						t.Fatal("redacted private purpose leaked")
					}
				}
			}
		})
	}
}
func TestNodeStopPreviewBudgetCountsEscapedBytesAndRetainsCensus(t *testing.T) {
	items := make([]nodestop.Obligation, nodestop.MaxItems)
	for n := range items {
		items[n] = nodestop.Obligation{ID: "lease:kept", Kind: "lease", State: "active", Revision: 1, LeaseDetails: "permitted", Lease: &nodestop.LeaseSummary{Owner: "501", Username: strings.Repeat("名<", 100), Reason: strings.Repeat("<>&😀", 1000), Agent: strings.Repeat("a", 300), Project: strings.Repeat("p", 300)}}
	}
	boundNodeStopPreviews(items)
	total := 0
	for _, item := range items {
		raw, err := json.Marshal(item.Lease)
		if err != nil || len(raw) > 8192/len(items) || !utf8.ValidString(item.Lease.Reason) || !item.Lease.Truncated || item.ID != "lease:kept" || item.LeaseDetails != "permitted" {
			t.Fatal("preview budget or attribution meaning changed", len(raw), err)
		}
		total += len(raw)
	}
	if total > 8192 || len(items) != nodestop.MaxItems {
		t.Fatal("encoded preview budget exceeded or census lost")
	}
}

// This deliberate durable-data alteration is a scope-tamper control. It is not
// an example of a supported purpose-edit API or a native process observation.
func TestNodeStopPrivateCensusBindsTextBeyondPreview(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	l, err := m.Acquire(model.Request{Resources: model.Resources{CPU: .25}, Reason: strings.Repeat("<", 4096), TTL: time.Minute, Key: "hidden-tail"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := nodestop.LeaseReadScope{Source: actor.Host, All: true}
	p, err := m.PlanNodeStop("hidden-private-stop", actor, model.ShutdownRequest{Wait: true}, "review", external, view)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Impact.Obligations[0].Lease.Truncated {
		t.Fatal("control did not actually truncate escaped purpose")
	}
	original, _ := json.Marshal(p.Impact.Obligations[0].Lease)
	l.Request.Reason = l.Request.Reason[:4095] + ">"
	data, _ := json.Marshal(l)
	if _, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(data), l.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := m.PlanNodeStop("hidden-private-second", actor, model.ShutdownRequest{Wait: true}, "review", external, view)
	if err != nil {
		t.Fatal(err)
	}
	visible, _ := json.Marshal(fresh.Impact.Obligations[0].Lease)
	if string(original) != string(visible) || p.Impact.SHA256 == fresh.Impact.SHA256 {
		t.Fatal("private hidden tail not bound independently of same preview")
	}
	hash, _ := p.Digest()
	_, err = m.BeginNodeStop(nodestop.Apply{Key: p.Key, ReviewSHA256: hash, Plan: p}, actor, true, external, view)
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "node_stop_blocked" {
		t.Fatal("altered private lease text did not reach complete-census refusal", err)
	}
	after, err := m.Inspect(l.ID)
	if err != nil || after.State != "active" {
		t.Fatal("refused review changed lease", after, err)
	}
}
