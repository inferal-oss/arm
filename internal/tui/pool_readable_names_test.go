package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

func TestReadableTUIPoolReadLabelsKeepAuthorityAndReviewModels(t *testing.T) {
	const payload = `{"pool":{"id":"pool-exact","name":"Global slots","scope":"global","authority":"localhost","unit":"permits","capacity":9223372036854775807,"revision":7,"state":"active"},"reserved":9223372036854775807,"users":[{"lease":"lease","lease_display_name":"Custom running work","quantity":9223372036854775807,"state":"active"}],"queued":[{"lease":"queued","lease_display_name":"Custom waiting work","quantity":1,"state":"queued"}],"review":{"authority":"localhost","consumers":[{"id":"lease","revision":7}]}}`
	const page = `{"pool":"pool-exact","descendants":true,"entries":[{"use":{"lease":"lease","lease_display_name":"Custom subtree work","quantity":9223372036854775807,"state":"active"},"direct_claims":[]}],"cursor":"next"}`
	m, session := fixtureDashboard(t)
	m.poolUI = &poolDashboard{mode: "list"}
	m.data.names = renderLabels{authority: "localhost"}
	m.data.names.addLease(model.Lease{ID: "lease", Host: "localhost"}, "Older status label", "")
	session.reply = func(operation string, _ any, result any) error {
		switch operation {
		case "pool_list":
			return json.Unmarshal([]byte("["+payload+"]"), result)
		case "pool_inspect":
			return json.Unmarshal([]byte(payload), result)
		case "pool_consumers":
			return json.Unmarshal([]byte(page), result)
		default:
			t.Fatalf("unexpected read %s", operation)
		}
		return nil
	}
	for _, kind := range []string{"list", "detail", "consumers", "prepare"} {
		var draft *poolDraft
		if kind == "prepare" {
			draft = &poolDraft{operation: "set", value: "0", reason: "Review exact existing obligations"}
		}
		command := m.readPools(kind, "pool-exact", 0, draft)
		if command == nil {
			t.Fatal("missing existing pool read")
		}
		m.Update(command())
		want := "Custom running work"
		if kind == "consumers" {
			want = "Custom subtree work"
		}
		if got := m.leaseDisplayName("lease", "localhost"); got != want {
			t.Fatalf("%s dropped response labels: %q", kind, got)
		}
		if m.poolUI.names.authority != "localhost" {
			t.Fatal("global pool Host empty lost exact authority")
		}
		if kind == "detail" {
			if m.poolUI.current.Pool.Host != "" || m.poolUI.current.Pool.Capacity != int64(9223372036854775807) || m.poolUI.current.Users[0].Quantity.String() != "9223372036854775807" {
				t.Fatal("typed pool adapter changed original accounting")
			}
			if !strings.Contains(m.poolText(), "Custom waiting work · ID queued") {
				t.Fatal("queued response label missing")
			}
		}
		if kind == "prepare" {
			if m.poolUI.review == nil || m.poolUI.review.request.ID != "pool-exact" || m.poolUI.review.request.Revision == nil || *m.poolUI.review.request.Revision != 7 || !reflect.DeepEqual(m.poolUI.review.request.Reviewed, m.poolUI.current.Review) {
				t.Fatal("read labels transformed exact review", m.notice, m.poolUI.review)
			}
		}
	}
	if !reflect.DeepEqual(session.operations, []string{"pool_list", "pool_inspect", "pool_consumers", "pool_inspect"}) {
		t.Fatal("labels introduced an RPC", session.operations)
	}
	for _, payload := range []string{`{"pool":{"authority":"localhost"},"users":null,"queued":[]}`, `{"pool":{"authority":"localhost"},"users":[],"queued":null}`} {
		var view poolRenderView
		if err := json.Unmarshal([]byte(payload), &view); err != nil {
			t.Fatal(err)
		}
		model, _ := view.values()
		if (model.Users == nil) != strings.Contains(payload, `"users":null`) || (model.Queued == nil) != strings.Contains(payload, `"queued":null`) {
			t.Fatal("pool adapter lost null/empty distinction")
		}
	}
}

func TestReadableTUIPoolLabelCacheFollowsReadAndSourceFences(t *testing.T) {
	m, session := fixtureDashboard(t)
	m.poolUI = &poolDashboard{mode: "detail", current: &model.PoolView{Pool: model.Pool{ID: "pool", Authority: "localhost", Scope: "global"}}}
	session.reply = func(_ string, _ any, result any) error {
		return json.Unmarshal([]byte(`{"pool":{"id":"pool","authority":"localhost","scope":"global"},"users":[{"lease":"lease","lease_display_name":"Private old pool label"}]}`), result)
	}
	old := m.readPools("detail", "pool", 0, nil)().(poolReadMsg)
	m.poolUI = &poolDashboard{mode: "detail", current: &model.PoolView{Pool: model.Pool{ID: "pool", Authority: "localhost", Scope: "global"}}}
	m.acceptPoolRead(old)
	if len(m.poolUI.names.leases) != 0 {
		t.Fatal("late pool label populated reopened surface")
	}
	m.acceptPoolRead(m.readPools("detail", "pool", 0, nil)().(poolReadMsg))
	if m.leaseDisplayName("lease", "localhost") != "Private old pool label" {
		t.Fatal("exact current read label missing")
	}
	// A later main snapshot owns the newer display label, including when a pool
	// read started earlier returns after that snapshot.
	delayed := m.readPools("detail", "pool", 0, nil)().(poolReadMsg)
	newer := snapshot{status: model.Status{Host: "localhost"}, names: renderLabels{authority: "localhost"}}
	newer.names.addLease(model.Lease{ID: "lease", Host: "localhost"}, "Newer status label", "")
	m.applySnapshot(newer)
	if len(m.poolUI.names.leases) != 0 || m.leaseDisplayName("lease", "localhost") != "Newer status label" {
		t.Fatal("new main snapshot remained masked by older pool label")
	}
	m.acceptPoolRead(delayed)
	if len(m.poolUI.names.leases) != 0 || m.leaseDisplayName("lease", "localhost") != "Newer status label" {
		t.Fatal("earlier in-flight pool read replaced newer status label")
	}
	m.acceptPoolRead(m.readPools("detail", "pool", 0, nil)().(poolReadMsg))
	if m.leaseDisplayName("lease", "localhost") != "Private old pool label" {
		t.Fatal("newer pool read label was not accepted")
	}

	session.reply = func(_ string, _ any, result any) error {
		return json.Unmarshal([]byte(`{"pool":{"id":"pool","authority":"localhost","scope":"global"},"users":[],"queued":[]}`), result)
	}
	m.acceptPoolRead(m.readPools("detail", "pool", 0, nil)().(poolReadMsg))
	if len(m.poolUI.names.leases) != 0 {
		t.Fatal("new pool snapshot retained removed labels")
	}
	m.poolUI.names = old.names
	m.applySnapshot(snapshot{status: model.Status{Host: "new-authority"}})
	if len(m.poolUI.names.leases) != 0 || m.leaseDisplayName("lease", "localhost") != readablename.FromID(readablename.Lease, "lease") {
		t.Fatal("pool labels crossed source authority")
	}
}
