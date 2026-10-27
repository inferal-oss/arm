package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

var accent = lipgloss.NewStyle().Foreground(colorAccent)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("#95a8b3"))
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
	width, height := m.viewportGeometry()
	m.viewport.SetWidth(max(1, width))
	m.viewport.SetHeight(max(1, height))
	m.input.SetWidth(max(10, width-4))
}
func (m *dashboardModel) syncViewport() {
	m.resize()
	m.viewport.SetContent(m.viewportContent())
}
func (m *dashboardModel) viewportContent() string {
	content := m.displayContent()
	if m.modal() {
		return ansi.Wrap(content, max(1, min(m.mainWidth(), 88)-4), "")
	}
	return content
}
func (m *dashboardModel) displayContent() string {
	if m.nodeUI != nil && m.record == nil {
		return fieldLabels(m.nodeText())
	}
	if m.ioPicker != nil {
		return fieldLabels(m.pickerText())
	}
	if m.inputMode != "" {
		return fieldLabels(m.editorDisplay())
	}
	body := m.bodyText()
	if m.feedFocus && m.detail == nil {
		body = m.feedText(max(1, m.mainWidth()-4), true)
	}
	if m.checkins != nil {
		body = m.checkinText()
	}
	if m.record != nil {
		body = m.record.text(max(1, m.mainWidth()-4))
	}
	if m.review != nil {
		body = m.leaseReviewText()
		body = strings.TrimPrefix(body, "REVIEW "+strings.ToUpper(m.review.request.Action)+"\n\n")
		if !m.pending && m.connected {
			body = strings.TrimSuffix(body, "\n\ny confirm this exact request · n/Esc keep unchanged")
			label := "Extend lease"
			tone := ""
			if m.review.request.Action == "release" {
				label = "Release lease"
				tone = "danger"
			}
			if m.review.request.Action == "cancel" {
				label = "Cancel lease"
				tone = "danger"
			}
			body += "\n\n" + keycap("y", label, tone) + "  " + keycap("n/Esc", "Keep lease", "quiet")
		}
	}
	if m.composer != nil && m.recovery == nil {
		body = m.messageReviewText()
	}
	return fieldLabels(body)
}
func (m *dashboardModel) keepSelectionVisible() {
	m.syncViewport()
	if m.view == 2 && m.detail == nil && m.record == nil && m.resourceID == "" && !m.feedFocus && !m.modal() {
		row := m.leaseTableSelectionRow()
		if row < m.viewport.YOffset() {
			m.viewport.SetYOffset(row)
		} else if row+1 >= m.viewport.YOffset()+m.viewport.Height() {
			m.viewport.SetYOffset(max(0, row-m.viewport.Height()+2))
		}
		return
	}
	row := 0
	width := max(1, m.viewport.Width())
	for _, line := range strings.Split(m.displayContent(), "\n") {
		plain := ansi.Strip(line)
		if strings.HasPrefix(plain, "> ") || strings.HasPrefix(plain, "╔═ > ") || strings.HasPrefix(plain, "╭─ > ") {
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

func (m *dashboardModel) bodyText() string {
	if m.nodeUI != nil {
		return m.nodeText()
	}
	if m.recovery != nil {
		return m.recoveryText()
	}
	if m.detail != nil {
		return m.leaseDetailText()
	}
	if m.poolUI != nil {
		return m.poolText()
	}
	if m.resourceID != "" {
		return m.resourceDetailText()
	}
	if m.peerOpen && m.view == 1 {
		return m.peerDetailsText()
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
	case 6:
		return m.meshText()
	}
	return ""
}
func (m *dashboardModel) filteredLeases() []model.Lease {
	out := make([]model.Lease, 0)
	filter := strings.ToLower(m.filter)
	for _, l := range m.data.status.Leases {
		if m.hideInactive && l.State == "terminal" {
			continue
		}
		if filter == "" || strings.Contains(strings.ToLower(l.ID+" "+l.Request.Reason+" "+l.State+" "+leaseLabel(l)+" "+l.Request.Agent+" "+l.Request.Project+" "+l.OwnerUsername), filter) {
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
	mark := " "
	if m.hideInactive {
		mark = "x"
	}
	fmt.Fprintf(&out, "[%s] Hide inactive leases · [h] toggle · %d visible\n\n", mark, len(leases))
	if len(leases) == 0 {
		return out.String() + "◇ No matching leases\n\nClear the filter or wait for an agent to reserve resources.\nNew claims will appear here automatically."
	}
	out.WriteString(m.leaseTable(leases))
	return out.String()
}
func (m *dashboardModel) resourcesText() string {
	var cards []string
	width := max(12, m.mainWidth()-4)
	for i, r := range m.filteredResources() {
		totalLabel := "Capacity"
		availableLabel := "Available"
		title := strings.ToUpper(safe(r.Kind))
		if r.Kind == "cpu" {
			totalLabel = "Live headroom"
			availableLabel = "To lease"
			title = "CPU · LIVE HEADROOM"
		}
		if r.Kind == "memory" {
			totalLabel = "Live pool"
			availableLabel = "To lease"
			title = "MEMORY · LIVE BUDGET"
		}
		if r.Kind == "disk" {
			totalLabel = "Live pool"
			availableLabel = "To lease"
			title = "DISK · LIVE HEADROOM"
		}
		if r.Custom != nil {
			title = safe(r.Custom.Pool.Name) + " · " + strings.ToUpper(r.Custom.Pool.Scope)
		}
		if i == m.selected {
			title = "> " + title
		}
		inner := width - 4
		totalText, promisedText, availableText, deficitText := m.resourceCapacityTexts(&r)
		lines := []string{align(muted.Render(availableLabel), strong.Foreground(colorAccent).Render(availableText), inner), align(muted.Render(totalLabel), strong.Render(totalText), inner), align(muted.Render("Promised"), promisedText, inner)}
		if r.Kind == "cpu" || r.Kind == "memory" || r.Kind == "disk" {
			lines = append(lines, align(muted.Render("Shortfall"), deficitText, inner))
		} else if r.Deficit != "" && r.Deficit != "0" {
			lines = append(lines, align(strong.Foreground(colorWarning).Render("SHORTFALL"), strong.Foreground(colorWarning).Render(deficitText), inner))
		}
		if r.Kind == "memory" {
			lines = append(lines, muted.Render("Changes with current host headroom · not physical RAM"))
			if !memoryBudgetKnown(m.memoryBudgetForView()) {
				lines = append(lines, muted.Render("No fresh reading · new memory grants wait"))
			}
		}
		if r.Kind == "cpu" {
			lines = append(lines, muted.Render(ansi.Wrap("Other apps share this CPU · full promises stay charged", inner, "")))
			if b := m.cpuBudgetForView(); cpuBudgetKnown(b) {
				lines = append(lines, align(muted.Render("Managed use credited"), cpuQuantity(model.FloatQuantity(b.ManagedCredit)), inner))
			}
			if b := m.data.status.CPU; b != nil && b.Ceiling != nil {
				lines = append(lines, align(muted.Render("Operator ceiling"), cpuQuantity(model.FloatQuantity(*b.Ceiling)), inner))
			}
			if !cpuBudgetKnown(m.cpuBudgetForView()) {
				lines = append(lines, muted.Render("No fresh reading · new CPU grants wait"))
			}
		}
		if r.Kind == "disk" {
			lines = append(lines, muted.Render("State filesystem · full promises remain charged"))
			if b := m.data.status.Disk; b != nil && b.Ceiling != nil {
				ceiling := b.Ceiling
				lines = append(lines, align(muted.Render("Operator ceiling"), quantity(model.CountQuantity(*ceiling), "bytes"), inner))
			}
			if !diskBudgetKnown(m.diskBudgetForView()) {
				lines = append(lines, muted.Render("No fresh reading · new disk grants wait"))
			}
		}
		lines = append(lines, muted.Foreground(colorLine).Render(strings.Repeat("─", inner)))
		if len(r.Users) == 0 {
			lines = append(lines, muted.Render("No lease consumers"))
		}
		for _, u := range r.Users[:min(3, len(r.Users))] {
			lines = append(lines, align(safe(u.Agent), quantity(u.Quantity, r.Unit), inner), muted.Render(m.owner(u.Owner, u.OwnerUsername, r.Host)), ansi.Wrap("Why: "+safe(u.Reason), inner, ""))
		}
		if len(r.Users) > 3 {
			lines = append(lines, muted.Render(fmt.Sprintf("+%d more consumers · Enter to browse", len(r.Users)-3)))
		}
		cards = append(cards, window(title, strings.Join(lines, "\n"), width, 0, i == m.selected, ""))
	}
	if len(cards) == 0 {
		return m.peerSummaryText() + "\n\nNo matching resources.\n\nClear the filter or refresh the backend snapshot."
	}
	if m.mainWidth() < 70 {
		return strings.Join(cards, "\n") + "\n" + m.peerSummaryText()
	}
	return m.peerSummaryText() + "\n" + strings.Join(cards, "\n")
}

func (m *dashboardModel) leaseDetailText() string {
	l := m.detail
	var out strings.Builder
	fmt.Fprintf(&out, "%s\nState: %s · revision %d\nWhy: %s\nOwner: %s\nAgent: %s\nProject: %s\nHost: %s\n\nCPU: %g cores\nMemory: %s\nDisk: %s\nExpires: %s\n", safe(l.ID), leaseLabel(*l), l.Revision, safe(l.Request.Reason), m.owner(l.Owner, l.OwnerUsername, l.Host), safe(l.Request.Agent), safe(l.Request.Project), safe(l.Host), l.Request.Resources.CPU, byteCount(fmt.Sprint(l.Request.Resources.Memory)), byteCount(fmt.Sprint(l.Request.Resources.Disk)), date(l.Expires))
	if l.State == "terminal" {
		fmt.Fprintf(&out, "Lifecycle: terminal · recorded outcome: %s\n", safe(l.Outcome))
	}
	if l.ExitCode != nil {
		fmt.Fprintf(&out, "Exit code: %d\n", *l.ExitCode)
	}
	if l.Artifact != nil {
		fmt.Fprintf(&out, "\nManaged files: %s\nPhase: %s · policy %s\n", safe(l.Artifact.Path), safe(l.Artifact.Phase), safe(l.Artifact.Policy))
	}
	if l.PID != 0 {
		fmt.Fprintf(&out, "Supervised PID: %d\n", l.PID)
	}
	for _, c := range l.Request.Pools {
		fmt.Fprintf(&out, "Custom: %s\n", safe(fmt.Sprint(c)))
	}
	out.WriteString("\nControls: e extend · x release · c cancel · h agent check-ins\nOnly active leases can extend/release; queued requests can cancel.\nAll actions require a review tied to this exact revision.\n\ni opens the collapsible complete record.")
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
	if hasLeaseCheckins(m.data.status) {
		out.WriteString(strong.Render("Agent check-ins") + "\n")
		if m.data.reviewError != "" {
			out.WriteString("Check-ins unavailable: " + safe(m.data.reviewError) + "\n")
		} else {
			fmt.Fprintf(&out, "%d awaiting replies on this page", len(m.data.reviews.Items))
			if m.data.reviews.More {
				out.WriteString(" · more pending records")
			}
			out.WriteString("\n")
			for _, r := range m.data.reviews.Items {
				for _, c := range r.Context {
					fmt.Fprintf(&out, "  %s · lease %s · reply by %s\n", safe(r.Agent), safe(c.Lease), date(&r.Deadline))
				}
			}
			out.WriteString("Open the lease in Leases [3], then h for context, explanations and settings.\nReplies and silence never change entitlement.\n\n")
		}
	}
	out.WriteString("Registered recipients: ")
	for i, c := range m.data.contacts {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(safe(c.ID) + " (" + safe(c.Delivery) + ")")
	}
	out.WriteString("\n\nn new · b broadcast · y reply to selected operator message\n\n")
	for i, message := range m.data.messages {
		fmt.Fprintf(&out, "%s%s → %s · %s\n  %s\n  %s\n", selectedPrefix(m.selected, i), strong.Foreground(colorBlue).Render(safe(message.Sender)), accent.Render(safe(message.Audience)), message.Created.Local().Format("Jan 02 15:04:05"), safe(message.Body), safe(message.ID))
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
		fmt.Fprintf(&out, "%s%s · %s\n  %s · #%d\n\n", selectedPrefix(m.selected, len(m.events)-1-i), muted.Render(event.At.Local().Format("15:04:05")), accent.Render(safe(event.Type)), safe(event.Entity), event.Sequence)
	}
	if len(m.events) == 0 {
		return "No recorded events."
	}
	return out.String()
}
func (m *dashboardModel) hostText() string {
	s := m.data.status
	var out strings.Builder
	fmt.Fprintf(&out, "Host: %s\nOwner: %s\nDraining: %t\nJournal cursor: %d\n\nLocal authority\nNative authenticated operator connection. This Host page describes the local authority. Mesh [7] opens explicitly selected read-only peer contexts through this gateway.\nClosing the dashboard never stops work.\n\nCapabilities\n", safe(s.Host), m.owner(s.Owner, s.OwnerUsername, s.Host), s.Draining, s.Cursor)
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
			return m.resourceRecord(&r[index])
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
	total, promised, available, deficit := m.resourceCapacityTexts(r)
	out := fmt.Sprintf("%s · %s\nHost: %s\n\nAvailable: %s\nCapacity: %s\nPromised: %s\n\nCONSUMERS · Enter opens selected lease\n", safe(r.Kind), safe(r.Pool), safe(r.Host), available, total, promised)
	if r.Kind == "cpu" {
		out = cpuBudgetDetail(r, m.cpuBudgetForView())
	}
	if r.Kind == "memory" {
		out = fmt.Sprintf("Memory · live host budget\nHost: %s\n\nTo lease: %s\nLive pool: %s\nPromised: %s\nShortfall: %s\n", safe(r.Host), available, total, promised, deficit)
		if !memoryBudgetKnown(m.memoryBudgetForView()) {
			out += "\nNew memory grants wait for a fresh availability estimate.\n"
		}
		out += "\nPhysical RAM and actual usage remain separate from lease promises.\n\nCONSUMERS · Enter opens selected lease\n"
	}
	if r.Kind == "disk" {
		out = fmt.Sprintf("Disk · live state-filesystem headroom\nHost: %s\n\nTo lease: %s\nLive pool: %s\nPromised: %s\nShortfall: %s\n", safe(r.Host), available, total, promised, deficit)
		if !diskBudgetKnown(m.diskBudgetForView()) {
			out += "\nNew disk grants wait for fresh availability.\n"
		}
		out += "\nCONSUMERS · Enter opens selected lease\n"
		out += "\nFull outstanding promises stay charged. Shared files and snapshots do not prove exclusive reclamation. [?] help explains scope.\n"
	}
	if len(r.Users) == 0 {
		out += "No lease consumers.\n"
	}
	for i, u := range r.Users {
		out += fmt.Sprintf("\n%s%s\n  %s · %s · %s\n  Why: %s\n", selectedPrefix(m.selected, i), safe(u.Lease), m.owner(u.Owner, u.OwnerUsername, r.Host), quantity(u.Quantity, r.Unit), safe(u.State), safe(u.Reason))
	}
	return out + "\n↑↓ selects consumer · Enter inspects lease · i record · Esc budgets"
}
