package tui

import (
	"errors"
	"testing"

	"getarm.org/internal/model"
)

func TestHierarchyReviewRefusalAndPriorUnknownController(t *testing.T) {
	for _, code := range []string{"hierarchy_review_too_large", "hierarchy_review_required"} {
		t.Run(code, func(t *testing.T) {
			m, s := fixtureDashboard(t)
			m.poolUI = &poolDashboard{mode: "detail"}
			r := copyPoolMutation(poolPlanFixture(t).request)
			s.reply = func(string, any, any) error {
				return &model.Error{Code: code, Message: "Complete hierarchy review refused"}
			}
			cmd := m.sendPoolRequest(r, false)
			if cmd == nil {
				t.Fatal(m.notice)
			}
			m.Update(cmd())
			if m.poolPending.state != "rejected" {
				t.Fatal("initial definite refusal became unknown", m.poolPending)
			}
			m, s = fixtureDashboard(t)
			m.poolUI = &poolDashboard{mode: "detail"}
			s.reply = func(string, any, any) error { return errors.New("reply lost") }
			cmd = m.sendPoolRequest(r, false)
			m.Update(cmd())
			if m.poolPending.state != "unknown" {
				t.Fatal(m.poolPending)
			}
			s.reply = func(string, any, any) error {
				return &model.Error{Code: code, Message: "Complete hierarchy review refused"}
			}
			cmd = m.sendPoolRequest(r, true)
			if cmd == nil {
				t.Fatal(m.notice)
			}
			m.Update(cmd())
			if m.poolPending.state != "unknown" {
				t.Fatal("earlier unknown was silently settled", m.poolPending)
			}
		})
	}
}
