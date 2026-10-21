package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

func TestRecoveryStartupNoNetworkExactSelectionAndAuthority(t *testing.T) {
	for _, kind := range []string{"pool", "message"} {
		t.Run(kind, func(t *testing.T) {
			home := pendingHome(t)
			r := pendingFixture(t, kind)
			if err := persistPending(home, r, false); err != nil {
				t.Fatal(err)
			}
			m := newDashboard(t.Context(), home)
			s := newTestTransport()
			m.session = s
			m.connected = true
			m.connecting = false
			m.data.status = model.Status{Host: "localhost"}
			if len(m.pendingRecords) != 1 || m.pendingMessage != nil || m.poolPending != nil || len(s.operations) != 0 {
				t.Fatal("startup acted on an intent")
			}
			press(m, "u")
			press(m, "enter")
			if len(s.operations) != 0 || m.recovery != nil {
				t.Fatal("selection sent a request")
			}
			if kind == "pool" {
				if m.poolPending.state != "unknown" || !samePending(*m.poolIntent, r) {
					t.Fatal("pool snapshot changed")
				}
				m.data.status.Host = "other"
				if press(m, "i") != nil || press(m, "y") != nil {
					t.Fatal("foreign pool dispatch")
				}
			} else {
				if !m.composer.unknown || !samePending(*m.messageIntent, r) {
					t.Fatal("message snapshot changed")
				}
				m.data.status.Host = "other"
				if press(m, "i") != nil || press(m, "y") != nil {
					t.Fatal("foreign message dispatch")
				}
			}
			if len(s.operations) != 0 {
				t.Fatal(s.operations)
			}
		})
	}
}
func TestRecoveryExplicitMessageLookupSettlesOnlyMatchingDurableResult(t *testing.T) {
	m, s, request := unknownMessageFixture(t)
	s.reply = func(op string, p, out any) error {
		if op != "message_lookup" {
			t.Fatal(op)
		}
		raw, _ := json.Marshal(map[string]any{"found": true, "message": recordedMessage(request)})
		return json.Unmarshal(raw, out)
	}
	cmd := press(m, "i")
	if cmd == nil {
		t.Fatal("explicit lookup missing")
	}
	m.Update(cmd())
	records, err := loadPending(m.home)
	if err != nil || len(records) != 0 || m.pendingMessage != nil {
		t.Fatal("matching result not settled", records, err)
	}
}
func TestRecoveryDurableBeforeDispatchAndSettlementFailure(t *testing.T) {
	m, s := fixtureDashboard(t)
	r := pendingFixture(t, "message")
	var request manager.MessageRequest
	json.Unmarshal(r.Request, &request)
	m.composer = &messageComposer{authority: "localhost", request: request, review: true}
	s.reply = func(op string, p, out any) error {
		records, err := loadPending(m.home)
		if err != nil || len(records) != 1 || !samePending(records[0], r) {
			t.Fatal("dispatch preceded durable exact intent", records, err)
		}
		*out.(*manager.Message) = recordedMessage(request)
		return nil
	}
	cmd := press(m, "y")
	if cmd == nil {
		t.Fatal(m.notice)
	}
	result := cmd()
	if err := os.Chmod(filepath.Join(m.home, pendingDirectory, m.messageIntent.name()), 0644); err != nil {
		t.Fatal(err)
	}
	m.Update(result)
	if m.pendingMessage == nil || !m.composer.unknown || !strings.Contains(m.notice, "storage could not be settled") {
		t.Fatal("known backend outcome silently dropped unsafe recovery record", m.notice)
	}
	if _, err := os.Stat(filepath.Join(m.home, pendingDirectory, r.name())); err != nil {
		t.Fatal("unsafe record purged", err)
	}
}
func TestRecoveryStorageFailureAndConcurrentIntentBlockInitialSend(t *testing.T) {
	m, s := fixtureDashboard(t)
	r := pendingFixture(t, "message")
	var request manager.MessageRequest
	json.Unmarshal(r.Request, &request)
	m.composer = &messageComposer{authority: "localhost", request: request, review: true}
	if err := os.Chmod(filepath.Join(m.home, pendingDirectory), 0755); err != nil {
		t.Fatal(err)
	}
	if cmd := press(m, "y"); cmd != nil || len(s.operations) != 0 || !strings.Contains(m.notice, "nothing sent") {
		t.Fatal("unsafe storage dispatched", m.notice)
	}
	os.Chmod(filepath.Join(m.home, pendingDirectory), 0700)
	if err := persistPending(m.home, r, false); err != nil {
		t.Fatal(err)
	}
	request.Key = "replacement-key"
	m.composer = &messageComposer{authority: "localhost", request: request, review: true}
	if press(m, "y") != nil || len(s.operations) != 0 {
		t.Fatal("another process intent bypassed")
	}
	records, err := loadPending(m.home)
	if err != nil || len(records) != 1 || records[0].key() != r.key() {
		t.Fatal("pending replacement overwrote original")
	}
}
func TestRecoveryAmbiguousRetryCannotClearIntent(t *testing.T) {
	m, s, request := unknownMessageFixture(t)
	s.reply = func(string, any, any) error {
		return model.Err("invalid_request", "definite refusal of this retry only")
	}
	cmd := press(m, "y")
	if cmd == nil {
		t.Fatal(m.notice)
	}
	m.Update(cmd())
	records, err := loadPending(m.home)
	if err != nil || len(records) != 1 || m.pendingMessage == nil || !m.composer.unknown || records[0].key() != request.Key {
		t.Fatal("retry erased earlier uncertainty", records, err)
	}
	s.reply = func(string, any, any) error { return errors.New("offline") }
}
func TestRecoveryMouseRosterRecordAndSmallLayouts(t *testing.T) {
	m, _ := fixtureDashboard(t)
	r := pendingFixture(t, "message")
	persistPending(m.home, r, false)
	m.width, m.height = 140, 40
	m.syncViewport()
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "u" })
	if m.recovery == nil {
		t.Fatal("footer recovery click")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.recovery && r.row == 0 })
	if m.composer == nil || !m.composer.unknown {
		t.Fatal("mouse recovery failed")
	}
	press(m, "u")
	for _, size := range [][2]int{{140, 40}, {70, 24}, {45, 14}} {
		m.width, m.height = size[0], size[1]
		m.syncViewport()
		if !strings.Contains(m.View().Content, "Recovery") {
			t.Fatal("recovery affordance missing", size)
		}
	}
	press(m, "i")
	if m.record == nil {
		t.Fatal("exact private record unavailable")
	}
	press(m, "esc")
	if m.record != nil || m.recovery == nil {
		t.Fatal("record close did not return to roster")
	}
}

func TestRecoverySelectionScrollsAndDoesNotLeakGlobalShortcutIntoInput(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.width, m.height = 45, 14
	var records []pendingRecord
	for i := 0; i < 64; i++ {
		r := pendingFixture(t, "message")
		r.Authority = fmt.Sprintf("authority-%d", i)
		records = append(records, r)
	}
	m.recovery = &recoveryBrowser{records: records}
	for i := 0; i < 63; i++ {
		m.recoveryKey("j")
	}
	if m.recovery.selected != 63 || m.viewport.YOffset() == 0 {
		t.Fatal("bounded roster selection cannot reach last record")
	}
	m.recovery = nil
	m.composer = &messageComposer{authority: "localhost"}
	m.inputMode = "body"
	m.input.SetValue("draft")
	m.syncViewport()
	for _, r := range m.mouseRegions() {
		if r.key == "u" {
			t.Fatal("editing input advertised an active global recovery click")
		}
	}
}

func TestRecoveryHitRegionsDoNotExposeUnderlyingViews(t *testing.T) {
	for _, view := range []int{0, 1, 2, 3, 4, 5} {
		m, _ := fixtureDashboard(t)
		m.width, m.height = 140, 40
		m.view = view
		m.data.status.Leases = []model.Lease{activeLease("hidden-lease")}
		pool := poolFixture()
		m.poolUI = &poolDashboard{mode: "list", views: []model.PoolView{pool}}
		r := pendingFixture(t, "message")
		m.recovery = &recoveryBrowser{records: []pendingRecord{r}}
		m.syncViewport()
		found := false
		for _, region := range m.mouseRegions() {
			if region.recovery {
				found = true
			}
			if region.key == "h" || region.key == "d" || region.key == "n" || region.pool || region.picker || region.row >= 0 && !region.recovery && !region.feed && !region.record {
				t.Fatalf("view%d exposes invisible underlying control: %+v", view, region)
			}
		}
		if !found {
			t.Fatal("recovery row missing", view)
		}
	}
}
