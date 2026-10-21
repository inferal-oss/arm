package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#71dbc4"))
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("#97a7b8"))
var strong = lipgloss.NewStyle().Bold(true)

func scrollReview(view *viewport.Model, key string) bool {
	switch key {
	case "up", "k":
		view.ScrollUp(1)
	case "down", "j":
		view.ScrollDown(1)
	case "pgup", "ctrl+u":
		view.HalfPageUp()
	case "pgdown", "ctrl+d":
		view.HalfPageDown()
	case "home":
		view.GotoTop()
	case "end":
		view.GotoBottom()
	default:
		return false
	}
	return true
}

func (m *dashboardModel) mainWidth() int {
	if m.width >= 110 {
		return m.width - 35
	}
	return m.width
}
func (m *dashboardModel) resize() {
	m.viewport.SetWidth(max(1, m.mainWidth()-4))
	m.viewport.SetHeight(max(1, m.height-8))
	m.input.SetWidth(max(10, m.mainWidth()-10))
}
func (m *dashboardModel) syncViewport() {
	m.viewport.SetContent(m.displayContent())
}
func (m *dashboardModel) displayContent() string {
	body := m.bodyText()
	if m.feedFocus && m.detail == nil {
		body = m.feedText(max(1, m.mainWidth()-4), true)
	}
	if m.record != nil {
		body = m.record.text(max(1, m.mainWidth()-4))
	}
	if m.review != nil {
		body = m.leaseReviewText()
	}
	if m.composer != nil {
		body = m.messageReviewText()
	}
	return body
}
func (m *dashboardModel) keepSelectionVisible() {
	m.syncViewport()
	row := 0
	width := max(1, m.viewport.Width())
	for _, line := range strings.Split(m.displayContent(), "\n") {
		if strings.HasPrefix(line, "> ") {
			if row < m.viewport.YOffset() {
				m.viewport.SetYOffset(row)
			} else if row >= m.viewport.YOffset()+m.viewport.Height() {
				m.viewport.SetYOffset(max(0, row-m.viewport.Height()+2))
			}
			return
		}
		row += max(1, (ansi.StringWidth(line)+width-1)/width)
	}
}
func (m *dashboardModel) selectionID() string {
	index := m.selected
	if m.resourceID != "" {
		resource := m.selectedResource()
		if resource != nil && index < len(resource.Users) {
			return resource.Users[index].Lease
		}
		return ""
	}
	if m.feedFocus {
		items := m.feedItems()
		if index < len(items) {
			switch record := items[index].record.(type) {
			case model.Event:
				return fmt.Sprint("event:", record.Sequence)
			case manager.Message:
				return "message:" + record.ID
			}
		}
	}
	switch m.view {
	case 1:
		r := m.filteredResources()
		if index < len(r) {
			return r[index].ID
		}
	case 2:
		l := m.filteredLeases()
		if index < len(l) {
			return l[index].ID
		}
	case 3:
		if index < len(m.data.messages) {
			return m.data.messages[index].ID
		}
	case 4:
		if index < len(m.events) {
			return fmt.Sprint(m.events[len(m.events)-1-index].Sequence)
		}
	}
	return ""
}
func (m *dashboardModel) applySnapshot(data snapshot) {
	id := m.selectionID()
	previous := m.selected
	m.data = data
	m.restoreSelection(id, previous)
}
func (m *dashboardModel) restoreSelection(id string, previous int) {
	count := m.itemCount()
	if id != "" {
		for index := 0; index < count; index++ {
			m.selected = index
			if m.selectionID() == id {
				return
			}
		}
	}
	m.selected = max(0, min(previous, count-1))
}
func fitBlock(text string, width, height int) string {
	lines := strings.Split(text, "\n")
	out := make([]string, max(0, height))
	for i := range out {
		if i < len(lines) {
			out[i] = ansi.Truncate(lines[i], max(0, width), "…")
		}
		out[i] += strings.Repeat(" ", max(0, width-ansi.StringWidth(out[i])))
	}
	return strings.Join(out, "\n")
}
func (m *dashboardModel) View() tea.View {
	if m.width < 45 || m.height < 14 {
		view := tea.NewView(fitBlock("ARM dashboard\n\nEnlarge the terminal to at least 45 × 14.\nq or Ctrl+C exits; leases keep running.", m.width, m.height))
		view.AltScreen = true
		return view
	}
	header := accent.Render("◈ ARM") + "  " + strong.Render(short(m.data.status.Host, max(5, m.width-55)))
	state := "○ disconnected · stale"
	if m.connecting {
		state = "◌ connecting"
	}
	if m.connected {
		state = "● connected"
	}
	header += "  " + muted.Render(state)
	nav := make([]string, len(viewNames))
	for i, name := range viewNames {
		text := fmt.Sprintf(" %d %s ", i+1, name)
		if i == m.view {
			text = lipgloss.NewStyle().Background(lipgloss.Color("#183d3b")).Foreground(lipgloss.Color("#a4ffdf")).Bold(true).Render(text)
		}
		nav[i] = text
	}
	if m.width < 80 {
		for i, name := range viewNames {
			nav[i] = fmt.Sprintf("%d%s", i+1, name[:1])
			if i == m.view {
				nav[i] = accent.Render("[" + nav[i] + "]")
			}
		}
	}
	title := viewNames[m.view]
	if m.detail != nil {
		title = "Lease details · " + short(m.detail.ID, 36)
	}
	if m.record != nil {
		title = "Structured record · Enter expands/collapses · Esc returns"
	}
	if m.feedFocus && m.record == nil && m.detail == nil && m.review == nil && m.composer == nil {
		title = "Activity feed · Enter opens lease · Tab returns"
	}
	if m.filter != "" && m.detail == nil {
		title += " · filter: " + short(m.filter, 30)
	}
	body := m.displayContent()
	viewport := m.viewport
	viewport.SetContent(body)
	main := lipgloss.NewStyle().Padding(0, 1).Render(fitBlock(viewport.View(), max(1, m.mainWidth()-2), max(1, m.height-8)))
	if m.width >= 110 {
		feed := accent.Render("LIVE FEED") + "\n" + muted.Render("leases · messages · operations") + "\n\n" + m.feedText(30, false)
		main = lipgloss.JoinHorizontal(lipgloss.Top, main, muted.Render("│")+" "+fitBlock(feed, 32, max(1, m.height-8)))
	} else if m.feedOpen && m.detail == nil && m.record == nil && m.review == nil && m.composer == nil {
		main = fitBlock(accent.Render("LIVE FEED · f closes")+"\n\n"+m.feedText(m.width-4, true), m.width, max(1, m.height-8))
	}
	fresh := "Awaiting snapshot"
	if !m.lastRefresh.IsZero() {
		fresh = fmt.Sprintf("Snapshot %ds ago", max(0, int(m.timeNow.Sub(m.lastRefresh).Seconds())))
	}
	if m.view == 0 && !m.timeMonitor.IsZero() {
		age := m.page.Age + m.timeNow.Sub(m.timeMonitor)
		fresh = fmt.Sprintf("Sample %ds ago · %s cadence · %d/60 points", max(0, int(age.Seconds())), m.page.Cadence, len(m.history))
		if m.page.Stale || !m.connected || age > m.page.StaleAfter {
			fresh = "STALE · " + fresh
		}
	}
	footer := "1–6 views · ↑↓ select · Enter inspect · Tab feed · / filter · ? help · q exit"
	if m.detail != nil {
		footer = "e extend · x release · c cancel · r refresh · i record · Esc return"
	}
	if m.view == 3 && m.detail == nil {
		footer = "n message agent · b broadcast · y reply · Enter record · Tab feed · ? help"
	}
	if m.inputMode != "" {
		footer = strong.Render(map[string]string{"filter": "Filter", "ttl": "New duration FROM NOW (max 7d)", "recipient": "Recipient ID (or broadcast)", "body": "Message (one line)"}[m.inputMode]) + ": " + m.input.View() + " · Enter review · Esc cancel"
	}
	content := fitBlock(header, m.width, 1) + "\n" + fitBlock(strings.Join(nav, " "), m.width, 1) + "\n" + fitBlock(strong.Render(short(title, m.width)), m.width, 1) + "\n" + fitBlock(muted.Render(fresh), m.width, 1) + "\n" + main + "\n" + fitBlock(accent.Render(strings.Repeat("─", m.width)), m.width, 1) + "\n" + fitBlock(short(m.notice, m.width), m.width, 1) + "\n" + fitBlock(footer, m.width, 1) + "\n" + fitBlock(muted.Render("Local operator · closing leaves leases running · f compact feed"), m.width, 1)
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "ARM · " + viewNames[m.view]
	return view
}

func (m *dashboardModel) bodyText() string {
	if m.detail != nil {
		return m.leaseDetailText()
	}
	if m.resourceID != "" {
		return m.resourceDetailText()
	}
	if !m.connected && m.data.status.Host == "" {
		return "Connecting to the local backend…\n\nStart it with arm start if it is not running.\nThe dashboard will reconnect without retrying any mutation."
	}
	switch m.view {
	case 0:
		return m.monitorText()
	case 1:
		return m.resourcesText()
	case 2:
		return m.leasesText()
	case 3:
		return m.messagesText()
	case 4:
		return m.eventsText()
	case 5:
		return m.hostText()
	}
	return ""
}
func (m *dashboardModel) filteredLeases() []model.Lease {
	out := make([]model.Lease, 0)
	filter := strings.ToLower(m.filter)
	for _, l := range m.data.status.Leases {
		if filter == "" || strings.Contains(strings.ToLower(l.ID+" "+l.Request.Reason+" "+l.State+" "+l.Request.Agent+" "+l.Request.Project+" "+l.OwnerUsername), filter) {
			out = append(out, l)
		}
	}
	return out
}
func (m *dashboardModel) filteredResources() []model.ResourceView {
	out := make([]model.ResourceView, 0)
	filter := strings.ToLower(m.filter)
	for _, r := range m.data.status.Resources {
		if filter == "" || strings.Contains(strings.ToLower(r.ID+" "+r.Kind+" "+r.Pool), filter) {
			out = append(out, r)
		}
	}
	return out
}
func selectedPrefix(selected, index int) string {
	if selected == index {
		return "> "
	}
	return "  "
}
func (m *dashboardModel) leasesText() string {
	var out strings.Builder
	leases := m.filteredLeases()
	if len(leases) == 0 {
		return "No matching leases."
	}
	for i, l := range leases {
		fmt.Fprintf(&out, "%s%s · %s · %s\n  %s\n  %s · expires %s\n\n", selectedPrefix(m.selected, i), safe(l.ID), safe(l.State), safe(l.Request.Kind), safe(l.Request.Reason), owner(l.Owner, l.OwnerUsername), date(l.Expires))
	}
	return out.String()
}
func (m *dashboardModel) resourcesText() string {
	var out strings.Builder
	for i, r := range m.filteredResources() {
		fmt.Fprintf(&out, "%s%s · %s\n  Total %s   Reserved %s   Available %s\n", selectedPrefix(m.selected, i), safe(r.Kind), safe(r.Pool), quantity(r.BudgetTotal, r.Unit), quantity(r.Reserved, r.Unit), quantity(r.Available, r.Unit))
		if r.Deficit != "" && r.Deficit != "0" {
			out.WriteString("  Deficit " + quantity(r.Deficit, r.Unit) + "\n")
		}
		if len(r.Users) == 0 {
			out.WriteString("  No lease consumers\n")
		}
		for _, u := range r.Users {
			fmt.Fprintf(&out, "  ↳ %s · %s · %s\n    Why: %s\n", safe(u.Lease), owner(u.Owner, u.OwnerUsername), quantity(u.Quantity, r.Unit), safe(u.Reason))
		}
		out.WriteString("\n")
	}
	return out.String()
}
func (m *dashboardModel) leaseDetailText() string {
	l := m.detail
	var out strings.Builder
	fmt.Fprintf(&out, "%s\nState: %s · revision %d\nWhy: %s\nOwner: %s\nAgent: %s\nProject: %s\nHost: %s\n\nCPU: %g cores\nMemory: %s\nDisk: %s\nExpires: %s\n", safe(l.ID), safe(l.State), l.Revision, safe(l.Request.Reason), owner(l.Owner, l.OwnerUsername), safe(l.Request.Agent), safe(l.Request.Project), safe(l.Host), l.Request.Resources.CPU, byteCount(fmt.Sprint(l.Request.Resources.Memory)), byteCount(fmt.Sprint(l.Request.Resources.Disk)), date(l.Expires))
	if l.Artifact != nil {
		fmt.Fprintf(&out, "\nManaged files: %s\nPhase: %s · policy %s\n", safe(l.Artifact.Path), safe(l.Artifact.Phase), safe(l.Artifact.Policy))
	}
	if l.PID != 0 {
		fmt.Fprintf(&out, "Supervised PID: %d\n", l.PID)
	}
	for _, c := range l.Request.Pools {
		fmt.Fprintf(&out, "Custom: %s\n", safe(fmt.Sprint(c)))
	}
	out.WriteString("\nControls: e extend · x release · c cancel\nOnly active leases can extend/release; queued requests can cancel.\nAll actions require a review tied to this exact revision.\n\ni opens the collapsible complete record.")
	return out.String()
}
func (m *dashboardModel) leaseReviewText() string {
	r := m.review
	if r == nil {
		return ""
	}
	out := fmt.Sprintf("REVIEW %s\n\nLease: %s\nRevision: %d\nWhy: %s\n\n%s\n", strings.ToUpper(r.request.Action), safe(r.request.ID), r.request.Revision, safe(r.lease.Request.Reason), r.text)
	if r.request.Action == "renew" {
		now := m.timeNow
		if now.IsZero() {
			now = time.Now()
		}
		out += fmt.Sprintf("\nCurrent expiry: %s\nDuration from acceptance: %s\nApproximate expiry if accepted now: %s\n", date(r.lease.Expires), r.request.TTL, now.Add(r.request.TTL).Local().Format(time.RFC1123))
	}
	for _, consumer := range r.request.Consumers {
		out += fmt.Sprintf("\nAffected attached lease: %s · revision %d", safe(consumer.ID), consumer.Revision)
	}
	if m.pending {
		return out + "\n\nREQUEST IN FLIGHT · no retries · closing may leave outcome unknown"
	}
	if !m.connected {
		return out + "\n\nDisconnected. Refresh and review again after reconnect."
	}
	return out + "\n\ny confirm this exact request · n/Esc keep unchanged"
}
func (m *dashboardModel) messagesText() string {
	var out strings.Builder
	out.WriteString("Registered recipients: ")
	for i, c := range m.data.contacts {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(safe(c.ID) + " (" + safe(c.Delivery) + ")")
	}
	out.WriteString("\n\nn new · b broadcast · y reply to selected operator message\n\n")
	for i, message := range m.data.messages {
		fmt.Fprintf(&out, "%s%s → %s · %s\n  %s\n  %s\n", selectedPrefix(m.selected, i), safe(message.Sender), safe(message.Audience), message.Created.Local().Format("Jan 02 15:04:05"), safe(message.Body), safe(message.ID))
		ids := make([]string, 0, len(message.Deliveries))
		for id := range message.Deliveries {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			delivery := message.Deliveries[id]
			fmt.Fprintf(&out, "  %s: %s", safe(id), safe(delivery.State))
			if delivery.Receipt != nil && delivery.Receipt.Acknowledged != nil {
				out.WriteString(" · acknowledged")
			}
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	return out.String()
}
func (m *dashboardModel) eventsText() string {
	var out strings.Builder
	for i := len(m.events) - 1; i >= 0; i-- {
		event := m.events[i]
		fmt.Fprintf(&out, "%s%s · %s\n  %s · #%d\n\n", selectedPrefix(m.selected, len(m.events)-1-i), event.At.Local().Format("15:04:05"), safe(event.Type), safe(event.Entity), event.Sequence)
	}
	if len(m.events) == 0 {
		return "No recorded events."
	}
	return out.String()
}
func (m *dashboardModel) hostText() string {
	s := m.data.status
	var out strings.Builder
	fmt.Fprintf(&out, "Host: %s\nOwner: %s\nDraining: %t\nJournal cursor: %d\n\nLocal authority\nNative authenticated operator connection. No remote-host transport yet.\nClosing the dashboard never stops work.\n\nCapabilities\n", safe(s.Host), owner(s.Owner, s.OwnerUsername), s.Draining, s.Cursor)
	for _, capability := range s.Capabilities {
		out.WriteString("  • " + safe(capability) + "\n")
	}
	out.WriteString("\nARM infrastructure ports\n")
	for _, port := range s.Infrastructure {
		out.WriteString("  " + safe(fmt.Sprint(port)) + "\n")
	}
	out.WriteString("\ni opens the structured complete status.")
	return out.String()
}

type feedItem struct {
	at                   time.Time
	label, detail, lease string
	record               any
}

func (m *dashboardModel) feedItems() []feedItem {
	out := make([]feedItem, 0, len(m.events)+len(m.data.messages))
	for _, event := range m.events {
		lease := ""
		for _, l := range m.data.status.Leases {
			if l.ID == event.Entity {
				lease = l.ID
				break
			}
		}
		out = append(out, feedItem{event.At, event.Type, event.Entity, lease, event})
	}
	for _, message := range m.data.messages {
		out = append(out, feedItem{message.Created, message.Sender + " → " + message.Audience, message.Body, message.Lease, message})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.After(out[j].at) })
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}
func (m *dashboardModel) feedText(width int, selectable bool) string {
	items := m.feedItems()
	if len(items) == 0 {
		return "No activity yet."
	}
	var out strings.Builder
	for i, item := range items {
		prefix := ""
		if selectable {
			prefix = selectedPrefix(m.selected, i)
		}
		fmt.Fprintf(&out, "%s%s %s\n%s\n\n", prefix, item.at.Local().Format("15:04:05"), short(item.label, max(1, width-12)), short(item.detail, width))
	}
	return out.String()
}
func (m *dashboardModel) itemCount() int {
	if m.resourceID != "" {
		resource := m.selectedResource()
		if resource != nil {
			return len(resource.Users)
		}
		return 0
	}
	if m.feedFocus {
		return len(m.feedItems())
	}
	switch m.view {
	case 1:
		return len(m.filteredResources())
	case 2:
		return len(m.filteredLeases())
	case 3:
		return len(m.data.messages)
	case 4:
		return len(m.events)
	}
	return 1
}
func (m *dashboardModel) currentRecord() any {
	index := m.selected
	if m.feedFocus {
		items := m.feedItems()
		if index < len(items) {
			return items[index].record
		}
	}
	switch m.view {
	case 0:
		return m.page
	case 1:
		r := m.filteredResources()
		if index < len(r) {
			return r[index]
		}
	case 2:
		l := m.filteredLeases()
		if index < len(l) {
			return l[index]
		}
	case 3:
		if index < len(m.data.messages) {
			return m.data.messages[index]
		}
	case 4:
		if index < len(m.events) {
			return m.events[len(m.events)-1-index]
		}
	}
	return m.data.status
}
func (m *dashboardModel) openSelected() tea.Cmd {
	if m.feedFocus {
		items := m.feedItems()
		if m.selected < len(items) && items[m.selected].lease != "" {
			m.feedFocus = false
			return m.loadDetail(items[m.selected].lease)
		}
	}
	if m.view == 2 {
		leases := m.filteredLeases()
		if m.selected < len(leases) {
			return m.loadDetail(leases[m.selected].ID)
		}
	}
	if m.view == 1 {
		resources := m.filteredResources()
		if m.selected < len(resources) {
			m.resourceID = resources[m.selected].ID
			m.selected = 0
			m.viewport.GotoTop()
			return nil
		}
	}
	if m.view == 4 && m.selected < len(m.events) {
		event := m.events[len(m.events)-1-m.selected]
		for _, lease := range m.data.status.Leases {
			if lease.ID == event.Entity {
				return m.loadDetail(lease.ID)
			}
		}
	}
	m.record, _ = newRecord(m.currentRecord(), viewNames[m.view])
	m.viewport.GotoTop()
	return nil
}

func (m *dashboardModel) selectedResource() *model.ResourceView {
	for i := range m.data.status.Resources {
		if m.data.status.Resources[i].ID == m.resourceID {
			return &m.data.status.Resources[i]
		}
	}
	return nil
}
func (m *dashboardModel) resourceDetailText() string {
	r := m.selectedResource()
	if r == nil {
		return "Resource no longer exists. Esc returns to budgets."
	}
	out := fmt.Sprintf("%s · %s\nHost: %s\n\nTotal: %s\nReserved: %s\nAvailable: %s\n\nCONSUMERS · Enter opens selected lease\n", safe(r.Kind), safe(r.Pool), safe(r.Host), quantity(r.BudgetTotal, r.Unit), quantity(r.Reserved, r.Unit), quantity(r.Available, r.Unit))
	if len(r.Users) == 0 {
		out += "No lease consumers.\n"
	}
	for i, u := range r.Users {
		out += fmt.Sprintf("\n%s%s\n  %s · %s · %s\n  Why: %s\n", selectedPrefix(m.selected, i), safe(u.Lease), owner(u.Owner, u.OwnerUsername), quantity(u.Quantity, r.Unit), safe(u.State), safe(u.Reason))
	}
	return out + "\n↑↓ selects consumer · Enter inspects lease · i record · Esc budgets"
}
