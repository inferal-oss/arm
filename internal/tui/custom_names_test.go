package tui

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func nameFixture(t *testing.T) (*dashboardModel, *testTransport, model.Lease) {
	t.Helper()
	m, s := fixtureDashboard(t)
	host, id := strings.Repeat("a", 32), strings.Repeat("b", 32)
	lease := activeLease(id)
	lease.Host = host
	m.data.status = model.Status{Host: host, Owner: "501", OwnerUsername: "alice", Leases: []model.Lease{lease}}
	m.view = 2
	m.detail = &lease
	s.reply = func(op string, params, result any) error {
		value := nameSnapshot{Document: customnames.Document{Version: 1, Host: host, Owner: "501", Entries: []customnames.Entry{}}}
		if op == "name_mutate" {
			q := params.(nameChange)
			value.Revision = q.ExpectedRevision + 1
			if q.Operation == "set" {
				value.Entries = []customnames.Entry{{Kind: q.Kind, AuthorityHost: q.AuthorityHost, ID: q.ID, BindingLease: q.BindingLease, Name: q.Name, Active: true}}
			}
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, result)
	}
	return m, s, lease
}
func ctrlName(m *dashboardModel, key rune) tea.Cmd {
	return m.key(tea.KeyPressMsg{Code: key, Mod: tea.ModCtrl})
}
func loadNameEditor(t *testing.T, m *dashboardModel) {
	t.Helper()
	cmd := ctrlName(m, 'n')
	if cmd == nil {
		t.Fatal("missing explicit map read")
	}
	m.Update(cmd())
	if m.nameUI == nil || m.nameUI.snapshot == nil {
		t.Fatal("healthy name form not loaded")
	}
}

func TestCustomNamingTUIExplicitCASAndCanonicalActionModels(t *testing.T) {
	m, s, lease := nameFixture(t)
	before, _ := json.Marshal(lease)
	loadNameEditor(t, m)
	if !reflect.DeepEqual(s.operations, []string{"name_list"}) {
		t.Fatal("opening form mutated", s.operations)
	}
	m.nameUI.input.SetValue("Build.Output")
	cmd := ctrlName(m, 's')
	if cmd == nil {
		t.Fatal("set missing")
	}
	if ctrlName(m, 's') != nil {
		t.Fatal("duplicate in-flight set")
	}
	message := cmd().(nameResultMsg)
	m.Update(message)
	request := s.params[1].(nameChange)
	if request.ID != lease.ID || request.AuthorityHost != lease.Host || request.ExpectedRevision != 0 || request.Name != "Build.Output" || request.BindingLease != "" {
		t.Fatal("set lost exact binding", request)
	}
	after, _ := json.Marshal(m.detail)
	if string(before) != string(after) {
		t.Fatal("name control changed lease/revision/request")
	}
	if m.nameUI.snapshot.Revision != 1 || m.nameUI.target.ID != lease.ID {
		t.Fatal("name result retargeted selection")
	}
}

func TestCustomNamingTUIExactRetainedClearAndConflict(t *testing.T) {
	m, s, lease := nameFixture(t)
	loadNameEditor(t, m)
	target := m.nameUI.target
	target.Name = "Old.Cache"
	target.Active = false
	m.nameUI.snapshot.Revision = 7
	m.nameUI.snapshot.Entries = []customnames.Entry{target}
	m.data.status.Leases = nil
	m.detail = nil
	cmd := ctrlName(m, 'd')
	if cmd == nil {
		t.Fatal("dangling exact clear unavailable")
	}
	m.Update(cmd())
	request := s.params[1].(nameChange)
	if request.ID != lease.ID || request.Name != "Old.Cache" || request.Operation != "clear" || request.ExpectedRevision != 7 {
		t.Fatal(request)
	}
	m.nameUI.input.SetValue("Replacement")
	m.data.status.Leases = []model.Lease{lease}
	s.reply = func(string, any, any) error { return model.Err("name_revision_conflict", "changed") }
	cmd = ctrlName(m, 's')
	m.Update(cmd())
	if m.nameUI.snapshot != nil || ctrlName(m, 's') != nil {
		t.Fatal("stale CAS was silently retried")
	}
}

func TestCustomNamingTUIPortIncarnationAndSourceFences(t *testing.T) {
	m, s, _ := nameFixture(t)
	m.detail = nil
	m.view = 1
	host := m.data.status.Host
	owner, consumer := strings.Repeat("c", 32), strings.Repeat("d", 32)
	resource := model.ResourceView{ID: host + "/ports/native/tcp4/127.0.0.1:8080", Host: host, Kind: "port", Users: []model.ResourceUse{{Lease: consumer, Relationship: "activation"}, {Lease: owner, Relationship: "reservation"}}}
	m.data.status.Resources = []model.ResourceView{resource}
	loadNameEditor(t, m)
	if m.nameUI.target.BindingLease != owner {
		t.Fatal("attached user selected as owner")
	}
	m.nameUI.input.SetValue("preview-http")
	m.data.status.Resources[0].Users[1].Lease = consumer
	if ctrlName(m, 's') != nil || len(s.operations) != 1 {
		t.Fatal("new port incarnation inherited old binding")
	}
	m.retireNameEditor()
	m.data.status.Resources[0].Users = []model.ResourceUse{{Lease: consumer, Relationship: "activation"}}
	if ctrlName(m, 'n') != nil {
		t.Fatal("infrastructure/non-owner row was nameable")
	}
	m.nodeUI = &nodeDashboard{}
	if ctrlName(m, 'n') != nil {
		t.Fatal("remote scoped window gained name writes")
	}
}

func TestCustomNamingTUIUnknownResultRetainsExactIntentAndNoRetry(t *testing.T) {
	m, s, lease := nameFixture(t)
	loadNameEditor(t, m)
	m.nameUI.input.SetValue("unknown-cache")
	s.reply = func(string, any, any) error { return errors.New("lost response") }
	cmd := ctrlName(m, 's')
	m.Update(cmd())
	if len(m.nameUnknown) != 1 || m.nameUnknown[0].Request.ID != lease.ID || m.nameUnknown[0].Request.Name != "unknown-cache" || m.nameUI.snapshot != nil {
		t.Fatal("unknown request lost", m.nameUnknown)
	}
	if ctrlName(m, 's') != nil || ctrlName(m, 'd') != nil || len(s.operations) != 2 {
		t.Fatal("unknown change retried")
	}
	m.retireNameEditor()
	if len(m.nameUnknown) != 1 {
		t.Fatal("closing lost exact unknown intent")
	}
}

type nameDiagnosticTransport struct {
	*testTransport
	diagnostic *model.Error
}

func (s *nameDiagnosticTransport) CallWithDiagnostics(ctx context.Context, op string, params, result any) (*model.Error, error) {
	return s.diagnostic, s.Call(ctx, op, params, result)
}
func TestCustomNamingTUIDiagnosticIsCosmeticAndHealthySnapshotClears(t *testing.T) {
	m, s, lease := nameFixture(t)
	transport := &nameDiagnosticTransport{s, &model.Error{Code: "name_metadata_invalid", Message: "private raw bytes"}}
	m.session = transport
	s.reply = func(op string, _ any, result any) error {
		if op == "status" {
			value := statusRenderView{Status: m.data.status}
			raw, _ := json.Marshal(value)
			return json.Unmarshal(raw, result)
		}
		if op == "release" {
			*(result.(*model.Lease)) = lease
		}
		return nil
	}
	data, err := readSnapshot(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	m.applySnapshot(data)
	if m.nameDiagnostic == nil || strings.Contains(namingWarning(m.nameDiagnostic), "private raw bytes") {
		t.Fatal("missing fixed cosmetic diagnostic")
	}
	var result model.Lease
	diagnostic, err := callWithNaming(t.Context(), transport, "release", nil, &result)
	if err != nil || diagnostic == nil || result.ID != lease.ID {
		t.Fatal("warning discarded successful cleanup")
	}
	m.acceptDetail(detailMsg{generation: m.detailGeneration, lease: lease})
	if m.nameDiagnostic == nil {
		t.Fatal("unrelated inspect cleared diagnostic")
	}
	transport.diagnostic = nil
	data, err = readSnapshot(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	m.applySnapshot(data)
	if m.nameDiagnostic != nil {
		t.Fatal("healthy local naming snapshot did not clear")
	}
}

func TestCustomNamingTUIFreshPoolLabelPreservesExactReview(t *testing.T) {
	m, _, _ := nameFixture(t)
	payload := `{"display_name":"Custom slots","pool":{"id":"pool_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","authority":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"Original supplied name","scope":"global","capacity":9223372036854775807},"review":{"authority":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","consumers":[]},"users":[],"queued":[]}`
	var wire poolRenderView
	if err := json.Unmarshal([]byte(payload), &wire); err != nil {
		t.Fatal(err)
	}
	view, names := wire.values()
	m.poolUI = &poolDashboard{current: &view, names: names}
	if m.poolDisplayName(view.Pool) != "Custom slots" || view.Pool.Name != "Original supplied name" || view.Pool.Capacity != 9223372036854775807 || view.Review == nil || view.Review.Consumers == nil {
		t.Fatal("pool label changed review/model", view)
	}
	before, _ := json.Marshal(view)
	_ = m.poolText()
	after, _ := json.Marshal(view)
	if string(before) != string(after) {
		t.Fatal("pool rendering mutated review")
	}
}

func TestCustomNamingTUIMouseAndNarrowFormKeepExactTarget(t *testing.T) {
	for _, width := range []int{45, 70, 140} {
		t.Run(fmtWidth(width), func(t *testing.T) {
			m, s, lease := nameFixture(t)
			m.width, m.height = width, 24
			loadNameEditor(t, m)
			m.nameUI.title = "host\x1b]52;c;secret\a\nlabel"
			m.nameUI.input.SetValue("Build.Output")
			m.resize()
			m.syncViewport()
			frame := m.mouseFrame()
			var save mouseRegion
			found := false
			for _, region := range m.mouseRegions() {
				if region.key == "ctrl+s" {
					save = region
					found = true
					break
				}
			}
			if !found {
				t.Fatal("name set not mouse accessible")
			}
			text := m.View().Content
			if strings.Contains(text, "\x1b]52;") {
				t.Fatal("terminal escape injected")
			}
			for _, line := range strings.Split(ansi.Strip(text), "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatal("form escaped viewport")
				}
			}
			m.selected = 9
			if stale := m.acceptMouse(dashboardMouseMsg{frame: frame, region: save}, m.mouseFrame()); stale != nil {
				t.Fatal("stale mouse frame sent a naming request")
			}
			frame = m.mouseFrame()
			cmd := m.acceptMouse(dashboardMouseMsg{frame: frame, region: save}, m.mouseFrame())
			if cmd == nil {
				t.Fatal("trusted name click missing")
			}
			m.Update(cmd())
			if s.params[1].(nameChange).ID != lease.ID {
				t.Fatal("selection drift retargeted name")
			}
		})
	}
}
func fmtWidth(width int) string { return map[int]string{45: "45", 70: "70", 140: "140"}[width] }

func TestCustomNamingTUIManualRegisteredReferenceUsesLocalCASOnly(t *testing.T) {
	m, s, _ := nameFixture(t)
	peer := strings.Repeat("e", 32)
	m.meshUI = &meshDashboard{registry: model.NodeRegistry{Source: m.data.status.Host, Nodes: []model.NodeEntry{{Host: m.data.status.Host, Local: true}, {Host: peer, Name: "Peer"}}}}
	loadNameEditor(t, m)
	ctrlName(m, 't')
	ctrlName(m, 'g')
	m.nameUI.idInput.SetValue(strings.Repeat("f", 32))
	m.nameUI.input.SetValue("remote-work")
	if len(s.operations) != 1 {
		t.Fatal("manual form opening probed or mutated")
	}
	cmd := ctrlName(m, 's')
	if cmd == nil {
		t.Fatal("manual explicit set missing")
	}
	message := cmd().(nameResultMsg)
	request := s.params[1].(nameChange)
	if request.AuthorityHost != peer || request.ID != strings.Repeat("f", 32) || request.Kind != "lease" || request.ExpectedRevision != 0 || s.operations[1] != "name_mutate" {
		t.Fatal("manual writer changed authority/CAS", request, s.operations)
	}
	message.err = model.Err("forbidden", "scope revoked")
	m.Update(message)
	if m.nameUI.snapshot != nil || ctrlName(m, 's') != nil {
		t.Fatal("revoked read became naming authority")
	}
}
func TestCustomNamingTUIManualPortAssertionAndMalformedReference(t *testing.T) {
	m, s, _ := nameFixture(t)
	loadNameEditor(t, m)
	ctrlName(m, 't')
	m.nameUI.kindIndex = 2
	m.nameUI.idInput.SetValue(m.data.status.Host + "/ports/native/tcp4/127.0.0.1:8080")
	m.nameUI.bindingInput.SetValue("")
	m.nameUI.input.SetValue("preview-http")
	cmd := ctrlName(m, 's')
	if cmd == nil {
		t.Fatal("optional assertion denied before backend observation")
	}
	message := cmd().(nameResultMsg)
	if message.request.BindingLease != "" {
		t.Fatal("UI invented reservation evidence")
	}
	m.nameUI.busy = false
	m.nameUI.request = nil
	m.nameUI.idInput.SetValue("bad ID")
	if ctrlName(m, 's') != nil || len(s.operations) != 2 {
		t.Fatal("malformed manual resource submitted")
	}
}

func TestCustomNamingTUIDiagnosticCompletionOrderIsFenced(t *testing.T) {
	m, s, _ := nameFixture(t)
	transport := &nameDiagnosticTransport{testTransport: s}
	m.session = transport
	s.reply = func(op string, _ any, result any) error {
		switch op {
		case "status":
			value := statusRenderView{Status: m.data.status}
			raw, _ := json.Marshal(value)
			return json.Unmarshal(raw, result)
		case "name_list":
			value := nameSnapshot{Document: customnames.Document{Version: 1, Host: m.data.status.Host, Owner: "501", Entries: []customnames.Entry{}}, NamingDiagnostic: &model.Error{Code: "name_metadata_invalid"}}
			raw, _ := json.Marshal(value)
			return json.Unmarshal(raw, result)
		}
		return nil
	}
	older, err := readSnapshot(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	cmd := ctrlName(m, 'n')
	if cmd == nil {
		t.Fatal("missing current naming read")
	}
	m.Update(cmd())
	if m.nameDiagnostic == nil {
		t.Fatal("new diagnostic absent")
	}
	observed := m.namingObserved
	m.applySnapshot(older)
	if m.nameDiagnostic == nil || m.namingObserved != observed {
		t.Fatal("older healthy snapshot hid newer corruption diagnostic")
	}
	fresh, err := readSnapshot(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	m.applySnapshot(fresh)
	if m.nameDiagnostic != nil {
		t.Fatal("new healthy snapshot did not clear")
	}
}

func TestCustomNamingTUIBadSuccessRetainsOriginalWriterAndOwner(t *testing.T) {
	for _, kind := range []string{"wrong-host", "wrong-owner", "wrong-state", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			m, s, _ := nameFixture(t)
			loadNameEditor(t, m)
			source := m.data.status.Host
			m.nameUI.input.SetValue("lost-cache")
			cmd := ctrlName(m, 's')
			msg := cmd().(nameResultMsg)
			switch kind {
			case "wrong-host":
				msg.value.Host = strings.Repeat("c", 32)
			case "wrong-owner":
				msg.value.Owner = "502"
			case "wrong-state":
				msg.value.Entries = []customnames.Entry{}
			case "malformed":
				msg.value = nameSnapshot{}
			}
			m.Update(msg)
			if len(m.nameUnknown) != 1 || m.nameUnknown[0].Source != source || m.nameUnknown[0].Owner != "501" || m.nameUnknown[0].Request.Name != "lost-cache" || m.nameUI.snapshot != nil {
				t.Fatal("bad success lost exact source-bound intent", m.nameUnknown)
			}
			if ctrlName(m, 's') != nil || len(s.operations) != 2 {
				t.Fatal("bad success replayed")
			}
		})
	}
	m, s, _ := nameFixture(t)
	source := m.data.status.Host
	peer := strings.Repeat("e", 32)
	m.meshUI = &meshDashboard{registry: model.NodeRegistry{Source: source, Nodes: []model.NodeEntry{{Host: source, Local: true}, {Host: peer}}}}
	loadNameEditor(t, m)
	ctrlName(m, 't')
	ctrlName(m, 'g')
	m.nameUI.idInput.SetValue(strings.Repeat("f", 32))
	m.nameUI.input.SetValue("peer-cache")
	cmd := ctrlName(m, 's')
	msg := cmd().(nameResultMsg)
	msg.err = errors.New("lost old source response")
	m.retireNameEditor()
	m.session = newTestTransport()
	m.data.status = model.Status{Host: strings.Repeat("d", 32), Owner: "502"}
	m.Update(msg)
	if len(m.nameUnknown) != 1 || m.nameUnknown[0].Source != source || m.nameUnknown[0].Owner != "501" || m.nameUnknown[0].Request.AuthorityHost != peer {
		t.Fatal("destination confused with original local writer", m.nameUnknown)
	}
	_ = s
}

func TestCustomNamingTUIRetiredKnownRepliesResolveOnlyExactIntent(t *testing.T) {
	for _, outcome := range []string{"no-op", "definitive"} {
		t.Run(outcome, func(t *testing.T) {
			m, s, _ := nameFixture(t)
			loadNameEditor(t, m)
			source, owner := m.nameUI.source, m.nameUI.snapshot.Owner
			target := m.nameUI.target
			target.Name = "existing-cache"
			target.Active = true
			m.nameUI.snapshot.Entries = []customnames.Entry{target}
			m.nameUI.snapshot.Revision = 7
			m.nameUI.input.SetValue(target.Name)
			s.reply = func(_ string, _ any, result any) error {
				value := nameSnapshot{Document: customnames.Document{Version: 1, Host: source, Owner: owner, Revision: 7, Entries: []customnames.Entry{target}}}
				raw, _ := json.Marshal(value)
				return json.Unmarshal(raw, result)
			}
			cmd := ctrlName(m, 's')
			msg := cmd().(nameResultMsg)
			m.retireNameEditor()
			other := msg.request
			other.Name = "another-intent"
			m.retainUnknownName(source, owner, other)
			if outcome == "definitive" {
				msg.err = model.Err("name_revision_conflict", "changed")
			}
			m.Update(msg)
			if len(m.nameUnknown) != 1 || m.nameUnknown[0].Request.Name != "another-intent" {
				t.Fatal("known exact reply left stale intent or removed another same-CAS request", m.nameUnknown)
			}
		})
	}
}
func TestCustomNamingTUIUnhealthyMutationReplyRemainsUnknown(t *testing.T) {
	m, _, _ := nameFixture(t)
	loadNameEditor(t, m)
	m.nameUI.input.SetValue("cache-work")
	cmd := ctrlName(m, 's')
	msg := cmd().(nameResultMsg)
	msg.diagnostic = &model.Error{Code: "name_metadata_invalid"}
	m.Update(msg)
	if len(m.nameUnknown) != 1 || m.nameUI.snapshot != nil || m.nameDiagnostic == nil {
		t.Fatal("unhealthy mutation reply became known success")
	}
}
