package tui

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func poolFixture() model.PoolView {
	return model.PoolView{Pool: model.Pool{ID: "pool-exact", Name: "Build permits", Scope: "global", Authority: "localhost", Owner: "501", OwnerUsername: "alice", Unit: "permits", Capacity: math.MaxInt64, Revision: 7, State: "active"}, Reserved: math.MaxInt64, Review: &model.PoolReview{Authority: "localhost", Consumers: []model.LeaseRevision{{ID: "consumer", Revision: 4}}}, Users: []model.ResourceUse{{Lease: "consumer", Quantity: "9223372036854775807", Reason: "build before deployment", Owner: "501", OwnerUsername: "alice", State: "active"}}}
}
func poolPlanFixture(t *testing.T) *poolPlan {
	t.Helper()
	v := poolFixture()
	p, e := preparePoolPlan(poolDraft{operation: "set", value: "0", reason: "capacity reduced, retain obligations"}, &v, "localhost")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func poolChangeFor(r model.PoolMutation) model.PoolChange {
	v := poolFixture()
	before := v.Pool
	after := before
	after.Revision++
	if r.Capacity != nil {
		after.Capacity = *r.Capacity
	}
	return model.PoolChange{Sequence: 9, Key: r.Key, Operation: r.Operation, Reason: r.Reason, Previous: &before, Pool: after, Actor: "501", ActorUsername: "alice", At: time.Now()}
}
func TestPoolExactPlansAndInvalidInputs(t *testing.T) {
	p := poolPlanFixture(t)
	if p.reserved != math.MaxInt64 || p.deficit != math.MaxInt64 || p.capacity != 0 || p.available != 0 {
		t.Fatal(p)
	}
	v := poolFixture()
	p, e := preparePoolPlan(poolDraft{operation: "adjust", value: "-9223372036854775807", reason: "return capacity"}, &v, "localhost")
	if e != nil || *p.request.Delta != -math.MaxInt64 || p.deficit != math.MaxInt64 {
		t.Fatal(p, e)
	}
	v.Review.Consumers[0].Revision = 99
	if p.request.Reviewed.Consumers[0].Revision != 4 {
		t.Fatal("review aliases live snapshot")
	}
	for _, value := range []string{"1.0", "1e3", "9223372036854775808", "-1", "1 2"} {
		if _, e := parsePoolInteger(value, false); e == nil {
			t.Fatal("accepted", value)
		}
	}
	v = poolFixture()
	for _, d := range []poolDraft{{operation: "adjust", value: "1", reason: "overflow"}, {operation: "adjust", value: "-9223372036854775808", reason: "underflow"}, {operation: "set", value: "1", reason: " "}} {
		if _, e := preparePoolPlan(d, &v, "localhost"); e == nil {
			t.Fatal("accepted", d)
		}
	}
	v.Pool.Revision = math.MaxInt64
	if _, e := preparePoolPlan(poolDraft{operation: "drain", reason: "review"}, &v, "localhost"); e == nil {
		t.Fatal("exhausted revision")
	}
	for _, name := range []string{"bad\x1bname", string([]byte{0xff})} {
		if _, e := preparePoolPlan(poolDraft{operation: "create", name: name, scope: "host", value: "1", reason: "create"}, nil, "localhost"); e == nil {
			t.Fatal("bad name")
		}
	}
}
func TestPoolReviewDeclineMouseAndExactConfirmation(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width = 140
	m.height = 40
	m.poolUI = &poolDashboard{mode: "detail", review: poolPlanFixture(t)}
	m.syncViewport()
	old := m.View().OnMouse
	var confirm mouseRegion
	for _, r := range m.mouseRegions() {
		if r.key == "y" {
			confirm = r
		}
	}
	stale := old(tea.MouseClickMsg{X: confirm.x, Y: confirm.y, Button: tea.MouseLeft})
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "n" })
	if m.poolUI.review != nil || len(s.operations) != 0 {
		t.Fatal("decline sent mutation")
	}
	if stale != nil {
		m.Update(stale())
	}
	if len(s.operations) != 0 {
		t.Fatal("stale frame sent mutation")
	}
	m.poolUI.review = poolPlanFixture(t)
	request := copyPoolMutation(m.poolUI.review.request)
	s.reply = func(op string, p, out any) error {
		if op != "pool_mutate" {
			t.Fatal(op)
		}
		*out.(*model.PoolChange) = poolChangeFor(p.(model.PoolMutation))
		return nil
	}
	cmd := clickRegion(t, m, func(r mouseRegion) bool { return r.key == "y" })
	if cmd == nil {
		t.Fatal("confirm not routed")
	}
	if press(m, "y") != nil {
		t.Fatal("duplicate in flight")
	}
	m.Update(cmd())
	if len(s.operations) != 1 || !samePoolRequest(s.params[0].(model.PoolMutation), request) || m.poolPending.state != "recorded" {
		t.Fatal(s.operations, m.poolPending)
	}
}
func TestPoolUnknownCorrelationLookupAndAuthority(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.poolUI = &poolDashboard{mode: "detail", review: poolPlanFixture(t)}
	r := copyPoolMutation(m.poolUI.review.request)
	s.reply = func(string, any, any) error { return errors.New("reply lost") }
	cmd := m.sendPoolRequest(r, false)
	m.Update(cmd())
	if m.poolPending.state != "unknown" {
		t.Fatal(m.poolPending)
	}
	s.reply = func(op string, p, out any) error {
		if op != "pool_lookup" {
			t.Fatal(op)
		}
		*out.(*manager.PoolMutationLookup) = manager.PoolMutationLookup{Found: false}
		return nil
	}
	lookup := m.lookupPoolRequest()
	if lookup == nil || m.lookupPoolRequest() != nil || m.sendPoolRequest(r, true) != nil {
		t.Fatal("lookup/send overlap")
	}
	msg := lookup()
	m.Update(msg)
	if m.poolPending.state != "unknown" || !strings.Contains(m.poolPending.error, "may still commit") {
		t.Fatal(m.poolPending)
	}
	s.reply = func(op string, p, out any) error { *out.(*model.PoolChange) = poolChangeFor(r); return nil }
	retry := m.sendPoolRequest(r, true)
	m.Update(msg)
	if m.poolPending.busy != "send" {
		t.Fatal("late lookup erased retry")
	}
	m.Update(retry())
	if m.poolPending.state != "recorded" {
		t.Fatal(m.poolPending)
	}
	before := len(s.operations)
	m.data.status.Host = "other-authority"
	if m.lookupPoolRequest() != nil || m.sendPoolRequest(r, true) != nil || len(s.operations) != before || !strings.Contains(m.notice, "saved pool authority") {
		t.Fatal("foreign replay")
	}
	m.poolPending = &poolOperation{request: r, state: "unknown", busy: "send", sequence: 99}
	bad := poolChangeFor(r)
	bad.Key = "other"
	m.acceptPoolResult(poolResultMsg{sequence: 99, request: r, change: bad})
	if m.poolPending.state != "unknown" {
		t.Fatal("wrong result accepted")
	}
}
func TestPoolJournalAndViewsStayBounded(t *testing.T) {
	for _, size := range [][2]int{{160, 46}, {110, 32}, {70, 24}, {45, 14}} {
		for _, mode := range []string{"list", "detail", "history", "actions", "review", "operation", "form"} {
			m, _ := fixtureDashboard(t)
			v := poolFixture()
			m.width, m.height = size[0], size[1]
			m.poolUI = &poolDashboard{mode: mode, views: []model.PoolView{v}, current: &v, history: model.PoolHistory{Entries: []model.PoolChange{poolChangeFor(poolPlanFixture(t).request)}, More: true, Cursor: math.MaxInt64}}
			switch mode {
			case "review":
				m.poolUI.review = poolPlanFixture(t)
			case "operation":
				m.poolPending = &poolOperation{request: poolPlanFixture(t).request, state: "unknown"}
			case "form":
				m.beginPoolForm("create")
			}
			m.resize()
			m.syncViewport()
			text := ansi.Strip(m.View().Content)
			lines := strings.Split(text, "\n")
			if len(lines) != size[1] {
				t.Fatalf("%v %s height %d", size, mode, len(lines))
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("%v %s overflow %q", size, mode, line)
				}
			}
			if mode == "list" && !strings.Contains(text, "9223372036854775807") {
				t.Fatalf("exact capacity lost %v %s", size, text)
			}
		}
	}
	m, s := fixtureDashboard(t)
	v := poolFixture()
	m.poolUI = &poolDashboard{mode: "history", current: &v, history: model.PoolHistory{Cursor: math.MaxInt64, More: true}}
	cmd := press(m, "n")
	if cmd == nil {
		t.Fatal("next page")
	}
	s.reply = func(op string, p, out any) error {
		if p.(map[string]any)["after"] != int64(math.MaxInt64) {
			t.Fatal("cursor rounded", p)
		}
		return nil
	}
	m.Update(cmd())
	if len(m.poolUI.previous) != 1 {
		t.Fatal("history stack")
	}
}

func TestPoolNavigationLeavesBrowserAndRetainsUnknownRequest(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.width = 140
	m.height = 40
	m.view = 1
	m.poolUI = &poolDashboard{mode: "operation"}
	m.poolPending = &poolOperation{request: poolPlanFixture(t).request, state: "unknown"}
	retained := m.poolPending
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "nav0" })
	if m.view != 0 || m.poolUI != nil || m.poolPending != retained {
		t.Fatal("mouse navigation did not leave pool browser or discarded unknown operation")
	}
	m.view = 1
	m.poolUI = &poolDashboard{mode: "list"}
	press(m, "3")
	if m.view != 2 || m.poolUI != nil || m.poolPending != retained {
		t.Fatal("keyboard navigation")
	}
}

func TestPoolOperationBackRestoresStableListSelection(t *testing.T) {
	for _, mode := range []string{"operation", "actions", "detail"} {
		m, s := fixtureDashboard(t)
		v := poolFixture()
		other := poolFixture()
		other.Pool.ID = "other"
		m.poolUI = &poolDashboard{mode: mode, selected: 3, current: &v, views: []model.PoolView{other, v}}
		m.poolPending = &poolOperation{request: poolPlanFixture(t).request, state: "recorded"}
		press(m, "esc")
		if m.poolUI.mode != "list" || m.poolUI.selected != 1 {
			t.Fatalf("%s failed stable ID selection: %+v", mode, m.poolUI)
		}
		cmd := press(m, "enter")
		if cmd == nil {
			t.Fatal("selected pool cannot open")
		}
		s.reply = func(op string, p, out any) error {
			if op != "pool_inspect" || p.(map[string]string)["id"] != v.Pool.ID {
				t.Fatal("wrong selected pool", op, p)
			}
			*out.(*model.PoolView) = v
			return nil
		}
		m.Update(cmd())
		if m.poolUI.mode != "detail" || m.poolUI.current.Pool.ID != v.Pool.ID {
			t.Fatal("fresh drilldown failed")
		}
	}
}
