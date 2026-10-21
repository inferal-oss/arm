package tui

import (
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
