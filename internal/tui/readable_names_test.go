package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"github.com/charmbracelet/x/ansi"
)

func setLeaseRenderReply(t *testing.T, result any, lease model.Lease) {
	t.Helper()
	switch target := result.(type) {
	case *model.Lease:
		*target = lease
	case *leaseRenderView:
		target.Lease = lease
	default:
		t.Fatalf("unexpected lease read result %T", result)
	}
}

func TestReadableTUIResponseAdaptersKeepExactModelsAndReadRoster(t *testing.T) {
	const payload = `{"host":"host-a","display_name":"Workstation","leases":[{"id":"lease-a","host":"host-a","revision":7,"display_name":"Build outputs","host_display_name":"Workstation","request":{"resources":{"disk_bytes":9223372036854775807}}}],"resources":[{"id":"pool-a","host":"host-a","kind":"custom","display_name":"Browser slots","budget_total":9223372036854775807,"users":[{"lease":"lease-a","lease_display_name":"Build outputs","quantity":9223372036854775807}]}]}`
	session := newTestTransport()
	session.reply = func(op string, _ any, result any) error {
		if op == "status" {
			return json.Unmarshal([]byte(payload), result)
		}
		return nil
	}
	data, err := readSnapshot(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.operations, []string{"status", "messages", "agents"}) {
		t.Fatal("name rendering added a read", session.operations)
	}
	if data.status.Leases[0].Request.Resources.Disk != int64(9223372036854775807) || data.status.Resources[0].BudgetTotal.String() != "9223372036854775807" || data.status.Resources[0].Users[0].Quantity.String() != "9223372036854775807" {
		t.Fatal("typed name adapter lost integer precision", data.status)
	}
	if data.status.Leases[0].ID != "lease-a" || data.status.Leases[0].Revision != 7 || data.names.leases["lease-a"].name != "Build outputs" || data.names.hostName != "Workstation" || data.names.resources["pool-a"] != "Browser slots" {
		t.Fatal("read labels or canonical model changed", data)
	}
	for _, empty := range []string{`{"host":"h","leases":null,"resources":null}`, `{"host":"h","leases":[],"resources":[]}`} {
		var view statusRenderView
		if err := json.Unmarshal([]byte(empty), &view); err != nil {
			t.Fatal(err)
		}
		status, _ := view.values()
		wantNil := strings.Contains(empty, "null")
		if (status.Leases == nil) != wantNil || (status.Resources == nil) != wantNil {
			t.Fatal("adapter changed null/empty distinction", empty, status)
		}
	}
	var foreign statusRenderView
	if err := json.Unmarshal([]byte(`{"host":"host-a","leases":[{"id":"lease-a","host":"host-b","display_name":"Foreign secret"}],"resources":[{"id":"pool-a","host":"host-b","display_name":"Foreign slots","users":[]}]}`), &foreign); err != nil {
		t.Fatal(err)
	}
	_, labels := foreign.values()
	if len(labels.leases) != 0 || len(labels.resources) != 0 {
		t.Fatal("cross-authority labels entered local cache", labels)
	}
}

func TestReadableTUIPrimaryLabelsKeepExactIDsAndActions(t *testing.T) {
	m, session := fixtureDashboard(t)
	lease := activeLease("opaque-lease-a")
	lease.Host = "host-a"
	view := statusRenderView{Status: model.Status{Host: "host-a", Owner: "501", OwnerUsername: "alice"}, DisplayName: "Workstation", Leases: []leaseRenderView{{Lease: lease, DisplayName: "Build outputs", HostDisplayName: "Workstation"}}, Resources: []resourceRenderView{{ResourceView: model.ResourceView{ID: "pool-a", Host: "host-a", Kind: "custom", Unit: "permits", BudgetTotal: "2", Available: "1", Reserved: "1", Custom: &model.PoolView{Pool: model.Pool{ID: "pool-a", Name: "Browser slots", Scope: "host", Host: "host-a"}}}, DisplayName: "Browser slots", Users: []useRenderView{{ResourceUse: model.ResourceUse{Lease: lease.ID, Owner: "501", Quantity: "1"}, LeaseDisplayName: "Build outputs"}}}}}
	m.data.status, m.data.names = view.values()
	before, _ := json.Marshal(m.data.status)
	m.detail = &lease
	text := m.leaseDetailText()
	if !strings.HasPrefix(text, "Build outputs\nID: "+lease.ID+"\n") || !strings.Contains(text, "Host: Workstation\nHost ID: host-a") {
		t.Fatal("readable label is not primary with full secondary IDs", text)
	}
	m.startReview("release")
	if m.review == nil || m.review.request.ID != lease.ID || m.review.request.Revision != lease.Revision {
		t.Fatal("label became an action key", m.review)
	}
	request := m.review.request
	_ = m.leaseReviewText()
	if !reflect.DeepEqual(request, m.review.request) || len(session.operations) != 0 {
		t.Fatal("rendering changed or sent a reviewed action")
	}
	m.review = nil
	m.detail = nil
	m.view = 2
	m.width = 140
	m.height = 40
	m.resize()
	if !strings.Contains(ansi.Strip(m.leasesText()), "Build outputs") || m.selectionID() != lease.ID {
		t.Fatal("lease label changed canonical selection")
	}
	m.filter = "Build outputs"
	if len(m.filteredLeases()) != 1 || m.filteredLeases()[0].ID != lease.ID {
		t.Fatal("display-name filter lost identity")
	}
	m.filter = "Browser slots"
	m.view = 1
	if len(m.filteredResources()) != 1 || m.selectionID() != "pool-a" {
		t.Fatal("resource display-name filter lost identity")
	}
	m.filter = ""
	m.resourceID = "pool-a"
	m.selected = 0
	resourceText := m.resourceDetailText()
	if !strings.HasPrefix(resourceText, "Browser slots\nID: pool-a\n") || !strings.Contains(resourceText, "Build outputs · ID "+lease.ID) || m.selectionID() != lease.ID {
		t.Fatal("consumer name hid its exact lease", resourceText)
	}
	after, _ := json.Marshal(m.data.status)
	if !bytes.Equal(before, after) {
		t.Fatal("rendering mutated original status/accounting records")
	}
}

func TestReadableTUIRemoteAndRegistryNamesStayInExactScope(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.data.status.Host = "gateway"
	m.data.names = renderLabels{authority: "gateway", hostName: "Local gateway"}
	m.meshUI = &meshDashboard{registry: model.NodeRegistry{Source: "gateway", Nodes: []model.NodeEntry{{Host: "peer", Name: "Registry peer"}}}}
	var response nodeSummaryRenderView
	if err := json.Unmarshal([]byte(`{"host":"peer","display_name":"Remote worker","leases":[{"id":"shared-id","host":"peer","display_name":"Remote build","revision":9}],"resources":[{"id":"remote-pool","host":"peer","kind":"custom","display_name":"Remote slots","budget_total":9223372036854775807,"reserved":0,"available":1}]}`), &response); err != nil {
		t.Fatal(err)
	}
	summary := response.values()
	m.nodeUI = &nodeDashboard{selection: model.NodeSelection{Host: "peer", Source: "gateway"}, displayName: "Registry peer", summary: summary, connected: true}
	m.view = 2
	selection := m.nodeUI.selection
	text := m.nodeText()
	for _, want := range []string{"Destination: Remote worker\nHost ID: peer", "Through LOCAL gateway: Local gateway\nGateway ID: gateway", "Remote build", "ID: shared-id"} {
		if !strings.Contains(text, want) {
			t.Fatal("remote labels lost context", want, text)
		}
	}
	if summary.Resources[0].Total.String() != "9223372036854775807" {
		t.Fatal("narrow remote DTO lost exact quantity")
	}
	m.view = 1
	if !strings.Contains(m.nodeText(), "Remote slots\nID: remote-pool") {
		t.Fatal("remote resource name missing")
	}
	if m.nodeUI.selection != selection || m.nodeUI.summary.Leases[0].ID != "shared-id" || m.nodeUI.summary.Leases[0].Revision != 9 {
		t.Fatal("render labels changed remote keys")
	}
	m.nodeUI.summary.names.hostName = readablename.FromID(readablename.Host, "peer")
	if m.hostDisplayName("peer") != "Registry peer" {
		t.Fatal("generated response name masked supplied registry name")
	}
	if nodeDisplayName(model.NodeEntry{Host: "peer", Name: "Supplied peer", DisplayName: readablename.FromID(readablename.Host, "peer")}) != "Supplied peer" {
		t.Fatal("generated node view masked supplied identity name")
	}
	m.applySnapshot(snapshot{status: model.Status{Host: "replacement"}})
	if m.nodeUI.displayName != "" || len(m.nodeUI.summary.names.leases) != 0 || m.nodeUI.summary.names.authority != "" || strings.Contains(m.nodeText(), "Remote build") || strings.Contains(m.nodeText(), "Remote worker") {
		t.Fatal("render labels survived a source authority change")
	}
	if m.hostDisplayName("peer") != readablename.FromID(readablename.Host, "peer") {
		t.Fatal("stale registry display name crossed scope")
	}
}

func TestReadableTUINamesEmptyUnsafeNarrowAndResize(t *testing.T) {
	m, _ := fixtureDashboard(t)
	lease := activeLease("lease-id-with-full-detail")
	for _, name := range []string{"", strings.Repeat("工作🧪 long readable label ", 30), "build\x1b]52;c;secret\a\nnext"} {
		m.data.status = model.Status{Host: lease.Host, Owner: "501", OwnerUsername: "alice", Leases: []model.Lease{lease}}
		m.data.names = renderLabels{authority: lease.Host, hostName: name}
		m.data.names.addLease(lease, name, name)
		for _, size := range [][2]int{{45, 14}, {70, 24}, {140, 42}} {
			m.width, m.height = size[0], size[1]
			m.view = 2
			m.detail = nil
			m.resize()
			m.syncViewport()
			frame := m.View().Content
			if strings.Contains(frame, "\x1b]52;") {
				t.Fatal("custom name injected a terminal OSC")
			}
			for _, line := range strings.Split(ansi.Strip(frame), "\n") {
				if ansi.StringWidth(line) > m.width {
					t.Fatal("name escaped narrow viewport", m.width, line)
				}
			}
			if m.selectionID() != lease.ID {
				t.Fatal("resize retargeted lease")
			}
			m.detail = &lease
			if !strings.Contains(m.leaseDetailText(), "ID: "+lease.ID) {
				t.Fatal("narrow view discarded full ID")
			}
		}
	}
	if readableLabel(readablename.Lease, "", "") != "Unknown lease" || nodeDisplayName(model.NodeEntry{}) != "Unknown host" {
		t.Fatal("empty identities invented a name")
	}
	if nodeDisplayName(model.NodeEntry{Host: "host", Name: "Friendly node"}) != "Friendly node" {
		t.Fatal("supplied node name lost")
	}
	if nodeDisplayName(model.NodeEntry{Host: "host", Name: "localhost"}) != readablename.FromID(readablename.Host, "host") {
		t.Fatal("localhost convenience alias became a stable primary name")
	}
	if got := m.resourceDisplayName(model.ResourceView{ID: "port", Kind: "port", Port: &model.PortBinding{PortClaim: model.PortClaim{Name: "Preview service"}}}); got != "Preview service" {
		t.Fatal("supplied port name lost", got)
	}
}

func TestReadableTUIConsumerMouseRowsKeepCanonicalID(t *testing.T) {
	for _, mode := range []string{"resource", "pool", "subtree"} {
		for _, width := range []int{45, 140} {
			for _, scenario := range []string{"duplicate", "spoof"} {
				t.Run(fmt.Sprintf("%s/%d/%s", mode, width, scenario), func(t *testing.T) {
					m, session := fixtureDashboard(t)
					m.width, m.height, m.view = width, 20, 1
					host := m.data.status.Host
					name := "Same display name" + strings.Repeat(" long", 24)
					reason := "ordinary purpose"
					if scenario == "spoof" {
						name = "Why: foo" + strings.Repeat(" long", 24)
						reason = strings.TrimPrefix(name, "Why: ") + " · ID second"
						if mode == "pool" {
							reason += " · 1 permits · active"
						}
						if mode == "subtree" {
							reason += " · active"
						}
					}
					users := []model.ResourceUse{{Lease: "first", Quantity: "1", State: "active", Reason: reason}, {Lease: "second", Quantity: "1", State: "active"}}
					m.data.names = renderLabels{authority: host}
					m.data.names.addLease(model.Lease{ID: "first", Host: host}, name, "")
					m.data.names.addLease(model.Lease{ID: "second", Host: host}, name, "")
					var rendered textRows
					if mode == "resource" {
						m.resourceID = "resource"
						m.data.status.Resources = []model.ResourceView{{ID: "resource", Host: host, Kind: "custom", Unit: "permits", BudgetTotal: "2", Available: "0", Reserved: "2", Users: users}}
						rendered = m.resourceDetailRender()
					} else {
						view := &model.PoolView{Pool: model.Pool{ID: "pool", Name: "Global slots", Scope: "global", Authority: host, Unit: "permits", Capacity: 2}, Users: users}
						m.poolUI = &poolDashboard{mode: "detail", current: view}
						if mode == "subtree" {
							m.poolUI.mode = "consumers"
							m.poolUI.consumers.Entries = []model.PoolConsumer{{Use: users[0]}, {Use: users[1]}}
							rendered = m.poolSubtreeRender()
						} else {
							rendered = m.poolDetailRender()
						}
					}
					first, second := -1, -1
					for source, index := range rendered.rows {
						if index == 0 {
							first = source
						}
						if index == 1 {
							second = source
						}
					}
					if first < 0 || second <= first || len(rendered.rows) != 2 {
						t.Fatal("renderer lost exact row ownership", rendered.rows)
					}
					if _, selectable := rendered.rows[first+2]; selectable {
						t.Fatal("untrusted reason became selectable")
					}
					m.resize()
					m.syncViewport()
					visualRow := 0
					for index, line := range strings.Split(m.displayContent(), "\n") {
						if index == second {
							break
						}
						visualRow += max(1, (ansi.StringWidth(line)+m.viewport.Width()-1)/m.viewport.Width())
					}
					m.viewport.SetYOffset(visualRow)
					var hit *mouseRegion
					for _, region := range m.mouseRegions() {
						if region.row == 1 && region.key == "" && !region.feed && region.pool == (mode != "resource") {
							copy := region
							hit = &copy
							break
						}
					}
					if hit == nil || hit.y != 5+visualRow-m.viewport.YOffset() {
						t.Fatal("wrapped/scrolled target differs from rendered row", hit, visualRow, m.viewport.YOffset())
					}
					frame := strings.Split(ansi.Strip(m.View().Content), "\n")
					if hit.y >= len(frame) || !strings.Contains(frame[hit.y], name[:8]) {
						t.Fatal("hit rectangle does not cover the actual name", hit, frame)
					}
					session.reply = func(op string, params, result any) error {
						if op != "inspect" || params.(map[string]string)["id"] != "second" {
							t.Fatal("display name retargeted read", op, params)
						}
						setLeaseRenderReply(t, result, activeLease("second"))
						return nil
					}
					if mode == "resource" {
						m.selected = hit.row
						if m.selectionID() != "second" {
							t.Fatal("resource row selected a name instead of ID")
						}
						cmd := press(m, "enter")
						if cmd == nil {
							t.Fatal("missing exact inspect")
						}
						cmd()
					} else {
						cmd := m.acceptPoolMouse(*hit)
						if cmd == nil {
							t.Fatal("missing pool exact inspect")
						}
						cmd()
					}
					if !reflect.DeepEqual(session.operations, []string{"inspect"}) {
						t.Fatal("row click added other effects", session.operations)
					}
				})
			}
		}
	}
}
