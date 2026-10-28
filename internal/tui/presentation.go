package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *dashboardModel) modal() bool {
	if m.recovery != nil {
		return false
	}
	return m.nameUI != nil || m.meshModal() || m.poolModal() || m.ioPicker != nil || m.checkins != nil || m.review != nil || m.composer != nil || m.inputMode != ""
}
func (m *dashboardModel) monitorLayout() bool {
	return m.nodeUI == nil && m.view == 0 && m.detail == nil && m.record == nil && !m.feedFocus && !m.modal()
}
func (m *dashboardModel) bodyHeight() int { return max(1, m.height-8) }
func (m *dashboardModel) modalGeometry() (int, int) {
	width := min(m.mainWidth(), 88)
	content := ansi.Wrap(m.displayContent(), max(1, width-4), "")
	height := min(m.bodyHeight(), max(9, len(strings.Split(content, "\n"))+2))
	return width, height
}
func (m *dashboardModel) viewportGeometry() (int, int) {
	if m.runFullscreen() {
		return m.width, max(1, m.height-2)
	}
	if m.modal() {
		width, height := m.modalGeometry()
		return width - 4, height - 2
	}
	if m.monitorLayout() {
		return m.mainWidth(), m.bodyHeight()
	}
	return m.mainWidth() - 4, m.bodyHeight() - 2
}

func (m *dashboardModel) editorDisplay() string {
	if strings.HasPrefix(m.inputMode, "mesh:") {
		label := "Reason"
		if m.inputMode == "mesh:field" && m.meshUI != nil && m.meshUI.form != nil {
			label = m.meshUI.form.fields[m.meshUI.form.selected].label
		}
		return strong.Render(safe(label)) + "\n\n" + m.input.View() + "\n\n" + keycap("Enter", "Apply field", "") + "  " + keycap("Esc", "Back", "quiet") + "\n\nNo network change is sent while editing."
	}
	if strings.HasPrefix(m.inputMode, "pool:") {
		return m.poolEditorDisplay()
	}
	if strings.HasPrefix(m.inputMode, "checkin:") {
		return strong.Render("Edit check-in duration") + "\n\n" + m.input.View() + "\n\nExamples: 15s · 10m · 1h. Contention may be 0s.\nEnter keeps this draft field; a separate review precedes saving.\n\n" + keycap("Enter", "Keep draft field", "") + "  " + keycap("Esc", "Back", "quiet")
	}
	field := panelStyle.Background(lipgloss.Color("#24343d")).Padding(0, 1).Render(m.input.View())
	switch m.inputMode {
	case "filter":
		return strong.Render("Filter this view") + "\n\n" + field + "\n\nMatch a resource, lease, owner or purpose.\n\n" + keycap("Enter", "Apply filter", "") + "  " + keycap("Esc", "Back", "quiet")
	case "ttl":
		lease := ""
		if m.detail != nil {
			lease = fmt.Sprintf("Lease %s\nID: %s · revision %d\nCurrent expiry: %s\n\n", m.leaseDisplayName(m.detail.ID, m.detail.Host), safe(m.detail.ID), m.detail.Revision, date(m.detail.Expires))
		}
		return lease + strong.Render("New duration FROM NOW (max 7d)") + "\n\n" + field + "\n\nExamples 15m · 2h · 1d · 1h30m\nDuration starts when accepted; it is not added to the current expiry.\n\n" + keycap("Enter", "Review extension", "") + "  " + keycap("Esc", "Keep lease", "quiet")
	case "recipient":
		var contacts []string
		for _, contact := range m.data.contacts {
			contacts = append(contacts, safe(contact.ID))
		}
		return strong.Render("Recipient ID (or broadcast)") + "\n\n" + field + "\n\nRegistered: " + strings.Join(contacts, ", ") + "\n\nYou will review the exact session and delivery settings before sending.\n\n" + keycap("Enter", "Continue", "") + "  " + keycap("Esc", "Back", "quiet")
	case "body":
		return strong.Render("Message (one line)") + "\n\n" + field + "\n\n" + keycap("Enter", "Review message", "") + "  " + keycap("Esc", "Back", "quiet") + "\n\n" + m.messageReviewText()
	}
	return field
}

func (m *dashboardModel) windowTitle() (string, string) {
	if m.nameUI != nil {
		return "RESOURCE NAME · LOCAL PRESENTATION", "accent"
	}
	if m.checkins != nil && m.record == nil {
		return "AGENT CHECK-INS · CONVERSATIONS", "accent"
	}
	if m.nodeUI != nil && m.record == nil {
		return "SELECTED REMOTE NODE · READ ONLY", "blue"
	}
	if m.recovery != nil && m.record == nil {
		return "RECOVERY · UNCERTAIN REQUESTS", "warning"
	}
	if m.ioPicker != nil {
		if m.ioPicker.disk {
			return "DISK I/O · SELECT DEVICES", "blue"
		}
		return "NETWORK · SELECT INTERFACES", "purple"
	}
	if strings.HasPrefix(m.inputMode, "pool:") {
		return "POOL · " + strings.ToUpper(strings.TrimPrefix(m.inputMode, "pool:")), ""
	}
	if m.inputMode != "" {
		return map[string]string{"filter": "FILTER", "ttl": "EXTEND LEASE · DURATION", "recipient": "NEW MESSAGE · RECIPIENT", "body": "NEW MESSAGE · COMPOSE"}[m.inputMode], ""
	}
	if m.review != nil {
		tone := "warning"
		if m.review.request.Action != "renew" {
			tone = "danger"
		}
		return "REVIEW " + strings.ToUpper(m.review.request.Action), tone
	}
	if m.composer != nil {
		if m.messageBusy != "" {
			return strings.ToUpper(m.messageBusy) + " · MESSAGE", "warning"
		}
		if m.composer.unknown {
			return "MESSAGE · OUTCOME UNKNOWN", "warning"
		}
		if m.composer.review {
			return "REVIEW MESSAGE", ""
		}
		return "MESSAGE · DRAFT", ""
	}
	if m.record != nil {
		return "RECORD · Enter expands / collapses", ""
	}
	if m.detail != nil {
		title := "LEASE DETAILS · " + strings.ToUpper(leaseLabel(*m.detail))
		if m.unknownLease != "" {
			return title + " · OUTCOME UNKNOWN", "warning"
		}
		return title, ""
	}
	if m.poolUI != nil {
		return m.poolWindowTitle()
	}
	if m.peerOpen && m.view == 1 {
		return "NEARBY ARM INSTANCES · ADVISORY", "blue"
	}
	if m.view == 6 && m.meshUI != nil {
		if m.meshUI.prompt != nil {
			return m.meshUI.prompt.title, "warning"
		}
		if m.meshUI.form != nil {
			if m.meshUI.form.provisionInstall {
				return "INSTALL ARM · DRAFT", "warning"
			}
			if m.meshUI.form.provisionSourceKey != "" {
				return "MESH CONNECTION · INSTALLED NODE", "warning"
			}
			if m.meshUI.form.uninstallHost != "" {
				return "UNINSTALL ARM · DRAFT", "warning"
			}
			if m.meshUI.form.stopHost != "" {
				return "STOP ARM · DRAFT", "warning"
			}
			return "MESH · CONNECT NODE", ""
		}
		return "MESH · LOCAL OPERATOR", ""
	}
	if m.resourceID != "" {
		return "RESOURCE · CONSUMERS & PURPOSE", ""
	}
	if m.feedFocus {
		return "LIVE FEED · KEYBOARD FOCUS", ""
	}
	if !m.connected && m.data.status.Host == "" {
		return "LOCAL BACKEND", "warning"
	}
	return strings.ToUpper(viewNames[m.view]), ""
}

func (m *dashboardModel) renderMain() string {
	width, height := m.mainWidth(), m.bodyHeight()
	view := m.viewport
	view.SetContent(m.viewportContent())
	if m.monitorLayout() {
		return fitBlock(view.View(), width, height)
	}
	title, tone := m.windowTitle()
	if !m.modal() {
		return scrollFrame(window(title, view.View(), width, height, !m.feedFocus, tone), view, width)
	}
	dialogWidth, dialogHeight := m.modalGeometry()
	content := view.View()
	dialog := scrollFrame(window(title, content, dialogWidth, dialogHeight, true, tone), view, dialogWidth)
	backgroundContent := m.bodyText()
	if m.poolModal() {
		backgroundContent = m.poolListText()
	}
	background := window(strings.ToUpper(viewNames[m.view]), fitBlock(backgroundContent, max(1, width-4), max(1, height-2)), width, height, false, "")
	dimmed := lipgloss.NewStyle().Foreground(lipgloss.Color("#52616c")).Background(colorBackground).Render(ansi.Strip(background))
	x, y := max(0, (width-dialogWidth)/2), max(0, (height-dialogHeight)/2)
	shadow := lipgloss.NewStyle().Background(lipgloss.Color("#080d10")).Render(fitBlock("", dialogWidth, dialogHeight))
	return fitBlock(lipgloss.NewCompositor(lipgloss.NewLayer(dimmed), lipgloss.NewLayer(shadow).X(x+1).Y(y+1).Z(1), lipgloss.NewLayer(dialog).X(x).Y(y).Z(2)).Render(), width, height)
}
func (m *dashboardModel) renderFeed() string {
	if m.nodeUI != nil {
		ui := m.nodeUI
		body := "REMOTE · READ ONLY\n\n" + m.hostDisplayName(ui.selection.Host) + "\nHost ID: " + safe(ui.selection.Host) + "\n\nThrough LOCAL gateway\n" + m.hostDisplayName(ui.selection.Source) + "\nGateway ID: " + safe(ui.selection.Source) + "\n\nLocal activity is hidden.\nRemote monitor/event streams, contacts, messages and mutations are unavailable.\n\n[7/Esc] Return to LOCAL Mesh"
		return window("DESTINATION CONTEXT", ansi.Wrap(body, 29, ""), 33, m.bodyHeight(), false, "blue")
	}
	width, height := 33, m.bodyHeight()
	lines := []string{muted.Render("Leases & messages · scroll"), ""}
	items := m.feedItems()
	if len(items) == 0 {
		lines = append(lines, muted.Render("No activity yet."), "", muted.Render("New events and agent messages"), muted.Render("will appear here."))
	}
	offset := max(0, min(m.feedOffset, max(0, len(items)-1)))
	for _, item := range items[offset:] {
		label, detail := m.feedCaption(item)
		icon := "◇"
		tone := accent
		if strings.Contains(item.label, "→") {
			icon = "✉"
			tone = tone.Foreground(colorBlue)
		}
		lines = append(lines, align(tone.Render(icon+" "+short(label, 19)), muted.Render(item.at.Local().Format("15:04")), width-4), muted.Render(short(detail, width-6)), muted.Foreground(colorLine).Render(strings.Repeat("─", width-4)))
	}
	if !m.connected {
		lines = append([]string{strong.Foreground(colorWarning).Render("STALE · reconnecting"), ""}, lines...)
	}
	return window("LIVE FEED", strings.Join(lines, "\n"), width, height, m.feedFocus, "")
}

func (m *dashboardModel) freshness() string {
	if m.nodeUI != nil {
		ui := m.nodeUI
		if !ui.connected || ui.err != "" || !ui.lastRead.IsZero() && m.timeNow.Sub(ui.lastRead) > 5*time.Second {
			return "DESTINATION STALE"
		}
		if ui.lastRead.IsZero() {
			return "Reading destination"
		}
		return fmt.Sprintf("Destination snapshot %ds ago", max(0, int(m.timeNow.Sub(ui.lastRead).Seconds())))
	}
	fresh := "Awaiting snapshot"
	if !m.lastRefresh.IsZero() {
		fresh = fmt.Sprintf("State %ds ago", max(0, int(m.timeNow.Sub(m.lastRefresh).Seconds())))
	}
	if m.view == 0 && !m.timeMonitor.IsZero() {
		age := m.page.Age + m.timeNow.Sub(m.timeMonitor)
		fresh = fmt.Sprintf("Sample %ds ago · %s", max(0, int(age.Seconds())), m.page.Cadence)
		if m.monitorStale() {
			fresh = "STALE · " + fresh
		}
	}
	return fresh
}
func (m *dashboardModel) footer() string {
	if m.inputMode != "" {
		return "Enter continue / review · Esc back · Ctrl+C exit"
	}
	if m.review != nil {
		return "[y] Confirm reviewed action   [n/Esc] Keep unchanged   PgUp/PgDn scroll"
	}
	if m.composer != nil {
		if m.messageBusy != "" {
			return "Request in flight · no automatic retry · q exits and prints unresolved key"
		}
		if m.composer.unknown {
			return "[i] Inspect key   [y] Retry same request/key   q exit"
		}
		return "[y] Send reviewed message   [n/Esc] Keep unsent   PgUp/PgDn scroll"
	}
	if m.record != nil {
		return "↑↓ select branch · Enter / Space expand · PgUp/PgDn scroll · Esc back"
	}
	if m.detail != nil {
		return "[e] Extend   [x] Release   [c] Cancel   [r] Refresh   [i] Record   Esc back"
	}
	if m.resourceID != "" {
		return "↑↓ select consumer · Enter open lease · [i] Record · Esc budgets"
	}
	if m.peerOpen && m.view == 1 {
		return "↑↓ scroll · [r] Refresh · [v/Esc] Back to budgets · q exit"
	}
	if m.view == 1 {
		return "↑↓ select resource · Enter inspect · [v] Nearby peers · [p] Pools · q exit"
	}
	if m.view == 0 {
		return "[d] Select disks   [n] Select interfaces   [?] Help   [i] Record   PgDn scroll   q exit"
	}
	if m.view == 3 {
		return "[n] Message agent   [b] Broadcast   [y] Reply   Enter inspect   Tab feed"
	}
	return "↑↓ select · Enter inspect · [/] Filter · Tab feed · [?] Help · q exit"
}
func (m *dashboardModel) View() tea.View {
	if m.runFullscreen() {
		return m.runFullscreenView()
	}
	if m.width < 45 || m.height < 14 {
		view := tea.NewView(fitBlock("ARM dashboard\n\nEnlarge the terminal to at least 45 × 14.\nq or Ctrl+C exits; leases keep running.", m.width, m.height))
		view.AltScreen = true
		return view
	}
	status := muted.Render("○ disconnected · stale")
	if m.connecting {
		status = strong.Foreground(colorWarning).Render("◌ connecting")
	}
	if m.connected {
		status = strong.Foreground(colorAccent).Render("● connected")
	}
	host := m.hostDisplayName(m.data.status.Host)
	if m.nodeUI != nil {
		host = "REMOTE " + m.hostDisplayName(m.nodeUI.selection.Host)
		status = strong.Foreground(colorWarning).Render("read-only · stale")
		if m.nodeUI.connected && m.nodeUI.err == "" && (m.nodeUI.lastRead.IsZero() || m.timeNow.Sub(m.nodeUI.lastRead) <= 5*time.Second) {
			status = strong.Foreground(colorBlue).Render("remote · read-only")
		}
	}
	host = short(host, max(12, min(48, m.width-52)))
	if host == "" {
		host = "local host"
	}
	brand := brandMark()
	if m.width >= 110 {
		brand += muted.Render("agent resource manager") + muted.Foreground(colorLine).Render("  /  ")
	} else {
		brand += muted.Render(" / ")
	}
	header := align(brand+strong.Render(safe(host)), status+" ", m.width)
	var nav []string
	for index := range viewNames {
		label := m.navigationLabel(index)
		if m.view == index {
			nav = append(nav, lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Background(lipgloss.Color("#203630")).Render("["+label+"]"))
		} else {
			if m.width < 65 {
				nav = append(nav, muted.Render(label))
			} else {
				nav = append(nav, muted.Render(" "+label+" "))
			}
		}
	}
	section := viewNames[m.view]
	if m.view == 0 {
		section = "Host monitor · physical usage"
	}
	if m.view == 1 {
		section = "Resources · live budgets"
	}
	if m.filter != "" {
		section += " · filter " + short(m.filter, 20)
	}
	main := m.renderMain()
	if m.width >= 110 {
		main = lipgloss.JoinHorizontal(lipgloss.Top, main, "  ", m.renderFeed())
	}
	notice := m.notice
	if m.nameDiagnostic != nil && m.nodeUI == nil {
		notice = namingWarning(m.nameDiagnostic)
	}
	if m.nodeUI != nil {
		notice = "Read-only destination context · [7/Esc] returns to LOCAL Mesh · no automatic retry or localhost fallback"
	}
	if notice == "" {
		notice = "Ready · inspection is read-only; changes require a concrete review."
		if m.review != nil || m.composer != nil {
			notice = "Review the exact scope before confirming. No action is sent until you confirm."
		}
	}
	noticeStyle := muted
	if !m.connected || m.nameDiagnostic != nil || m.unknownLease != "" || m.composer != nil && m.composer.unknown {
		noticeStyle = strong.Foreground(colorWarning)
	}
	content := fitBlock(header, m.width, 1) + "\n" + fitBlock(strings.Join(nav, " "), m.width, 1) + "\n" + fitBlock(align(strong.Render(" "+section), muted.Render(m.freshness()+" "), m.width), m.width, 1) + "\n" + fitBlock(muted.Foreground(colorLine).Render(strings.Repeat("─", m.width)), m.width, 1) + "\n" + main + "\n" + fitBlock(noticeStyle.Render(" "+short(notice, m.width-2)), m.width, 1) + "\n" + fitBlock(func() string { bar, _ := m.controlBar(); return bar }(), m.width, 1) + "\n" + fitBlock(muted.Foreground(colorLine).Render(strings.Repeat("─", m.width)), m.width, 1) + "\n" + m.exitFooter()
	view := tea.NewView(canvasStyle.Render(fitBlock(content, m.width, m.height)))
	view.AltScreen = true
	view.WindowTitle = "ARM · " + viewNames[m.view]
	view.MouseMode = tea.MouseModeCellMotion
	view.OnMouse = m.mouseHandler()
	return view
}

func (m *dashboardModel) feedCaption(item feedItem) (string, string) {
	label := strings.ReplaceAll(strings.ReplaceAll(item.label, ".", " "), "_", " ")
	captions := map[string]string{"lease.created": "Lease requested", "lease.granted": "Lease granted", "lease.renewed": "Lease extended", "lease.released": "Lease released", "lease.cancelled": "Lease cancelled", "lease.expired": "Lease expired", "lease.completed": "Work completed", "lease.failed": "Work failed", "storage.created": "Storage created"}
	if caption, ok := captions[item.label]; ok {
		label = caption
	} else {
		label = human(label)
	}
	detail := item.detail
	for _, lease := range m.data.status.Leases {
		if lease.ID == item.lease {
			who := lease.Request.Agent
			if who == "" {
				who = lease.OwnerUsername
			}
			if who != "" {
				detail = who + " · " + lease.Request.Reason
			} else {
				detail = lease.Request.Reason
			}
			break
		}
	}
	return label, detail
}

func (m *dashboardModel) exitFooter() string {
	if m.nodeUI != nil {
		return " [q] Exit · [7/Esc] LOCAL Mesh · leases keep running"
	}
	right := "Local operator · [u] Recovery · [f] Feed "
	if m.width < 82 {
		right = "Local operator · [u] Recovery"
	}
	if m.meshOwnsUninstallShortcut() {
		right = strings.Replace(right, " · [u] Recovery", "", 1)
	}
	return fitBlock(align(keycap("q", "Exit", "quiet")+muted.Render(" leases keep running"), muted.Render(right), m.width), m.width, 1)
}

func (m *dashboardModel) runFullscreenView() tea.View {
	ui := m.runsUI
	mode := "paused"
	if ui.following {
		mode = "following"
	}
	title := "RUN LOG · " + safe(ui.selected.Name) + " · " + mode
	if !m.connected {
		title = "DISCONNECTED · " + title
	}
	// This is a full terminal reading surface: only a one-line title and
	// controls remain around the same viewport and transcript buffer.
	content := fitBlock(title, m.width, 1) + "\n" + fitBlock(m.viewport.View(), m.width, max(1, m.height-2)) + "\n" + fitBlock("[z/Esc] Back [f] Follow [o] Fold [q] Exit", m.width, 1)
	view := tea.NewView(canvasStyle.Render(fitBlock(content, m.width, m.height)))
	view.AltScreen = true
	view.WindowTitle = "ARM · Run log"
	view.MouseMode = tea.MouseModeCellMotion
	view.OnMouse = m.mouseHandler()
	return view
}
