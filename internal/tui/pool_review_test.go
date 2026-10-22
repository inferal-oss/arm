package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func TestIndependentPoolReadCannotCrossReopenedUIIdentity(t *testing.T) {
	for _, kind := range []string{"list", "prepare"} {
		t.Run(kind, func(t *testing.T) {
			m, transport := fixtureDashboard(t)
			oldView, newView := poolFixture(), poolFixture()
			oldView.Pool.ID = "old-view"
			newView.Pool.ID = "new-view"
			transport.reply = func(operation string, params, out any) error {
				if operation == "pool_list" {
					*out.(*[]model.PoolView) = []model.PoolView{oldView}
				} else if operation == "pool_inspect" {
					*out.(*model.PoolView) = oldView
				} else {
					t.Fatal(operation)
				}
				return nil
			}
			m.poolUI = &poolDashboard{mode: "list"}
			var oldResult any
			if kind == "prepare" {
				draft := poolDraft{operation: "set", value: "0", reason: "old abandoned form"}
				oldResult = m.readPools("prepare", oldView.Pool.ID, 0, &draft)()
			} else {
				oldResult = m.readPools("list", "", 0, nil)()
			}
			// Closing/reopening resets per-window generation counters while using the
			// same native Session. A read must belong to its window identity as well.
			m.poolUI = nil
			transport.reply = func(operation string, params, out any) error {
				if operation != "pool_list" {
					t.Fatal(operation)
				}
				*out.(*[]model.PoolView) = []model.PoolView{newView}
				return nil
			}
			newRead := m.openPools()
			m.Update(newRead())
			current := m.poolUI
			m.Update(oldResult)
			if m.poolUI != current || m.poolUI.mode != "list" || m.poolUI.review != nil || m.poolUI.current != nil || len(m.poolUI.views) != 1 || m.poolUI.views[0].Pool.ID != newView.Pool.ID {
				t.Fatalf("stale %s read replaced reopened UI scope: %+v", kind, m.poolUI)
			}
		})
	}
}

func TestSubpoolReviewInheritsExactParentAndCountsBackingOnce(t *testing.T) {
	v := poolFixture()
	v.Pool.Capacity = 10
	v.Reserved = 1
	v.Available = 5
	v.Children = []string{"child"}
	v.Partition = &model.PoolPartition{Configured: 10, Direct: 1, ChildBacking: 4, Committed: 5, Held: 10, Available: 5}
	v.Review.Hierarchy = &model.PoolHierarchyReview{Schema: 1, Pool: v.Pool.ID, Root: v.Pool.ID, Pools: 2, Consumers: 1, SHA256: strings.Repeat("a", 64)}
	p, e := preparePoolPlan(poolDraft{operation: "create-child", name: "child", value: "3", reason: "carve build slots"}, &v, v.Pool.Authority)
	if e != nil {
		t.Fatal(e)
	}
	if p.request.Operation != "create" || p.request.Parent != v.Pool.ID || p.request.ParentRevision == nil || *p.request.ParentRevision != v.Pool.Revision || p.request.Scope != v.Pool.Scope || p.request.Unit != v.Pool.Unit || p.request.Reviewed.Hierarchy == nil || p.reserved != 0 {
		t.Fatal(p)
	}
	reduced, e := preparePoolPlan(poolDraft{operation: "set", value: "2", reason: "retain outstanding backing"}, &v, v.Pool.Authority)
	if e != nil || reduced.reserved != 5 || reduced.deficit != 3 {
		t.Fatal(reduced, e)
	}
	child := v
	child.Pool.ID = "child"
	child.Pool.Parent = v.Pool.ID
	tree, e := poolTreeOrder([]model.PoolView{child, v})
	if e != nil || len(tree) != 2 || tree[0].Pool.ID != v.Pool.ID || tree[1].Pool.ID != "child" {
		t.Fatal(tree, e)
	}
	if _, e = poolTreeOrder([]model.PoolView{child}); e == nil {
		t.Fatal("missing ancestry accepted")
	}
	v.Review.Hierarchy = nil
	if _, e = preparePoolPlan(poolDraft{operation: "create-child", name: "child", value: "3", reason: "no snapshot"}, &v, v.Pool.Authority); e == nil {
		t.Fatal("unguarded child allowed")
	}
}

func TestSubpoolConsumerPagingFailureAndReopenedScope(t *testing.T) {
	m, s := fixtureDashboard(t)
	v := poolFixture()
	m.poolUI = &poolDashboard{mode: "consumers", current: &v, consumerAfter: "", consumers: model.PoolConsumerPage{Pool: v.Pool.ID, Descendants: true, More: true, Cursor: "next"}}
	fail := true
	s.reply = func(op string, q, out any) error {
		if op != "pool_consumers" {
			t.Fatal(op)
		}
		if fail {
			return fmt.Errorf("connection lost")
		}
		*out.(*model.PoolConsumerPage) = model.PoolConsumerPage{Pool: v.Pool.ID, Descendants: true, Entries: []model.PoolConsumer{{Use: model.ResourceUse{Lease: "child-lease", Reason: "Exact child work", State: "active"}}}}
		return nil
	}
	read := m.poolKey("n")
	m.Update(read())
	if len(m.poolUI.consumerPrevious) != 0 || m.poolUI.consumerAfter != "" {
		t.Fatal("failed page changed cursor", m.poolUI)
	}
	fail = false
	read = m.poolKey("n")
	old := read()
	m.Update(old)
	if len(m.poolUI.consumerPrevious) != 1 || m.poolUI.consumerAfter != "next" {
		t.Fatal(m.poolUI)
	}
	if !strings.Contains(m.poolSubtreeText(), "Exact child work") {
		t.Fatal("reason lost")
	}
	m.poolUI = &poolDashboard{mode: "detail", current: &v}
	current := m.poolUI
	m.Update(old)
	if m.poolUI != current || m.poolUI.mode != "detail" {
		t.Fatal("old read crossed window identity")
	}
}

func TestSubpoolUnavailableReviewStaysReadOnly(t *testing.T) {
	m, _ := fixtureDashboard(t)
	v := poolFixture()
	v.Review.Unavailable = "hierarchy_review_too_large"
	m.poolUI = &poolDashboard{mode: "detail", current: &v}
	if len(m.poolActionOptions()) != 0 || !strings.Contains(m.poolText(), "READ ONLY") {
		t.Fatal("unavailable scope offered incomplete changes")
	}
	if _, e := preparePoolPlan(poolDraft{operation: "set", value: "1", reason: "never truncate"}, &v, v.Pool.Authority); e == nil {
		t.Fatal("unavailable review accepted")
	}
}
