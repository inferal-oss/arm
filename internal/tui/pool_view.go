package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/yrashk/arm/internal/model"
)

func (m *dashboardModel) poolText() string {
	ui := m.poolUI
	if ui == nil {
		return ""
	}
	if ui.review != nil {
		return m.poolReviewText()
	}
	if ui.form != nil {
		return "Refreshing the complete pool snapshot for review…\n\nEsc keeps the pool unchanged."
	}
	if ui.loading {
		return "Loading current pool state…\n\nEsc goes back. No mutation has been sent."
	}
	if ui.mode == "operation" {
		return m.poolOperationText()
	}
	if ui.mode == "actions" {
		lines := []string{"Select a reviewed operation", ""}
		for i, option := range m.poolActionOptions() {
			lines = append(lines, selectedPrefix(ui.selected, i)+human(option))
		}
		if len(lines) == 2 {
			lines = append(lines, "This retained deleted definition has no mutation controls.")
		}
		return strings.Join(lines, "\n")
	}
	if ui.mode == "list" {
		return m.poolListText()
	}
	if ui.current == nil {
		return "No pool selected. Esc returns to the pool browser."
	}
	if ui.mode == "history" {
		return m.poolHistoryText()
	}
	view := ui.current
	p := view.Pool
	var out strings.Builder
	fmt.Fprintf(&out, "%s\nID: %s\nState: %s · revision %d\nScope: %s\nHost: %s\nAuthority: %s\nOwner: %s\nUnit: %s\n\nCapacity: %d\nReserved: %d\nAvailable: %d\nDeficit: %d\n\n%s\n", safe(p.Name), safe(p.ID), safe(p.State), p.Revision, safe(p.Scope), safe(p.Host), safe(p.Authority), m.owner(p.Owner, p.OwnerUsername, p.Authority), safe(p.Unit), p.Capacity, view.Reserved, view.Available, view.Deficit, safe(p.Description))
	if p.Scope == "global" {
		out.WriteString("Global uses this one authoritative ledger; capacity is not copied per host.\n")
	}
	out.WriteString("\nCURRENT CONSUMERS\n")
	m.poolConsumersText(&out, view.Users, p.Unit, p.Authority, 0)
	out.WriteString("\nQUEUED CLAIMS\n")
	m.poolConsumersText(&out, view.Queued, p.Unit, p.Authority, len(view.Users))
	out.WriteString("\n[a] Actions · [h] Full journal · [i] Complete record\nAbstract permits coordinate work; they do not prove an external activity stopped.")
	return out.String()
}
func (m *dashboardModel) poolConsumersText(out *strings.Builder, users []model.ResourceUse, unit, authority string, offset int) {
	if len(users) == 0 {
		out.WriteString("No claims.\n")
	}
	for i, user := range users {
		fmt.Fprintf(out, "%s%s · %s · %s\n  %s\n  Why: %s\n", selectedPrefix(m.poolUI.selected, offset+i), safe(user.Lease), quantity(user.Quantity, unit), safe(user.State), m.owner(user.Owner, user.OwnerUsername, authority), safe(user.Reason))
	}
}
func (m *dashboardModel) poolListText() string {
	ui := m.poolUI
	head := "CUSTOM POOLS · including retained deleted definitions\n\n"
	if len(ui.views) == 0 {
		return head + "No custom pools. [n] creates a counted resource.\nHost and global scopes share this one local authority."
	}
	width := max(12, m.mainWidth()-4)
	headers := []string{"Pool / scope", "Capacity"}
	widths := []int{max(12, width-23), 23}
	if width >= 100 {
		headers = []string{"Pool / scope", "State", "Capacity", "Reserved", "Available / deficit"}
		widths = []int{width - 12 - 23*3, 12, 23, 23, 23}
	}
	rows := make([][]string, 0, len(ui.views))
	for i, view := range ui.views {
		p := view.Pool
		row := []string{selectedPrefix(ui.selected, i) + short(safe(p.Name), widths[0]-4) + "\n" + safe(p.Scope), fmt.Sprint(p.Capacity)}
		if len(headers) == 5 {
			available := fmt.Sprint(view.Available)
			if view.Deficit > 0 {
				available = "! " + fmt.Sprint(view.Deficit)
			}
			row = []string{row[0], safe(p.State), fmt.Sprint(p.Capacity), fmt.Sprint(view.Reserved), available}
		}
		rows = append(rows, row)
	}
	grid := table.New().Headers(headers...).Rows(rows...).Width(width).Wrap(false).Border(lipgloss.NormalBorder()).BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).BorderColumn(false).BorderRow(false).BorderHeader(true).BorderStyle(lipgloss.NewStyle().Foreground(colorLine)).StyleFunc(func(row, col int) lipgloss.Style {
		style := panelStyle.Padding(0, 1).Width(widths[col]).MaxWidth(widths[col])
		if row == table.HeaderRow {
			return style.Height(1).Bold(true).Foreground(colorBlue)
		}
		style = style.Height(2)
		if row == ui.selected {
			return style.Background(lipgloss.Color("#203630"))
		}
		if row%2 == 1 {
			return style.Background(lipgloss.Color("#182229"))
		}
		return style
	}).String()
	return head + grid + "\n\nCounts are exact; pool details show units, scope, owners and claim purposes."
}
func (m *dashboardModel) poolHistoryText() string {
	ui := m.poolUI
	var out strings.Builder
	fmt.Fprintf(&out, "CAPACITY-CHANGE JOURNAL\nPool: %s\nEntries after cursor: %d\n\n", safe(ui.current.Pool.Name), ui.historyStart)
	for i, entry := range ui.history.Entries {
		before := "new"
		revision := "new"
		if entry.Previous != nil {
			before = fmt.Sprint(entry.Previous.Capacity)
			revision = fmt.Sprint(entry.Previous.Revision)
		}
		fmt.Fprintf(&out, "%s#%d · %s · %s\n  Actor: %s\n  Why: %s\n  Capacity: %s → %d %s · delta %d\n  Revision: %s → %d · state %s\n\n", selectedPrefix(ui.selected, i), entry.Sequence, entry.At.Local().Format("Jan 02 15:04:05"), safe(entry.Operation), m.owner(entry.Actor, entry.ActorUsername, entry.Pool.Authority), safe(entry.Reason), before, entry.Pool.Capacity, safe(entry.Pool.Unit), entry.Delta, revision, entry.Pool.Revision, safe(entry.Pool.State))
	}
	if len(ui.history.Entries) == 0 {
		out.WriteString("No later journal entries.\n")
	}
	fmt.Fprintf(&out, "\nNext page: %t · cursor %d\n[p] Previous · [n] Next · [f] First · [i] Complete page record", ui.history.More, ui.history.Cursor)
	return out.String()
}
func (m *dashboardModel) poolReviewText() string {
	p := m.poolUI.review
	var out strings.Builder
	fmt.Fprintf(&out, "%s · %s\nReason: %s\nScope: %s\nAuthority: %s\nRequest key: %s\n\n", human(p.request.Operation), safe(p.name), safe(p.request.Reason), safe(p.scope), safe(p.request.Reviewed.Authority), safe(p.request.Key))
	beforeCap, beforeState := "new", "new"
	beforeAvailable, beforeDeficit := int64(0), int64(0)
	if p.before != nil {
		beforeCap = fmt.Sprint(p.before.Pool.Capacity)
		beforeState = p.before.Pool.State
		beforeAvailable = p.before.Available
		beforeDeficit = p.before.Deficit
		fmt.Fprintf(&out, "Pool: %s · revision %d\n", safe(p.before.Pool.ID), p.before.Pool.Revision)
	}
	fmt.Fprintf(&out, "Capacity: %s → %d %s\nState: %s → %s\nReserved: %d (unchanged)\nAvailable: %d → %d\nDeficit: %d → %d\n\nExisting leases retain their quantities and deadlines. This does not stop processes or delete files.\n", beforeCap, p.capacity, safe(p.unit), safe(beforeState), safe(p.state), p.reserved, beforeAvailable, p.available, beforeDeficit, p.deficit)
	if p.scope == "global" {
		out.WriteString("Global uses this one authority; capacity is not independently replicated per host.\n")
	}
	if p.deficit > 0 {
		out.WriteString("New grants remain blocked while they would exceed the revised capacity.\n")
	}
	switch p.request.Operation {
	case "drain":
		out.WriteString("Pause new admission; existing and queued claims remain recorded.\n")
	case "resume":
		out.WriteString("Resume normal admission; queued work may start under backend policy.\n")
	case "delete":
		out.WriteString("Retain historical definition and journal. Deletion is refused while queued or consuming claims remain.\n")
	}
	fmt.Fprintf(&out, "\nCOMPLETE REVIEWED OBLIGATIONS · %d\n", len(p.request.Reviewed.Consumers))
	for _, consumer := range p.request.Reviewed.Consumers {
		fmt.Fprintf(&out, "%s · revision %d\n", safe(consumer.ID), consumer.Revision)
	}
	out.WriteString("\n[n/Esc] Keep unchanged · [y] Confirm exact reviewed request")
	return out.String()
}
func (m *dashboardModel) poolEditorDisplay() string {
	ui := m.poolUI
	if ui == nil || ui.form == nil {
		return ""
	}
	form := ui.form
	if form.step >= len(form.fields) {
		return "Refreshing the complete current snapshot…"
	}
	field := form.fields[form.step]
	labels := map[string]string{"name": "Pool name", "scope": "Scope: host or global", "unit": "Integral count unit", "capacity": "Capacity (0..9223372036854775807)", "delta": "Signed nonzero adjustment", "description": "Description (optional)", "reason": "Required reason"}
	return fmt.Sprintf("%s · step %d/%d\n\n%s\n\n%s\n\nHost pools belong to this host. Global pools use this one authority.\nReview follows; nothing changes before explicit confirmation.\n\n%s  %s", human(form.draft.operation), form.step+1, len(form.fields), labels[field], panelStyle.Background(lipgloss.Color("#24343d")).Padding(0, 1).Render(m.input.View()), keycap("Enter", "Continue", ""), keycap("Esc", "Keep unchanged", "quiet"))
}
func (m *dashboardModel) poolOperationText() string {
	op := m.poolPending
	if op == nil {
		return "No retained pool operation. Esc returns to pools."
	}
	text := fmt.Sprintf("POOL OPERATION · %s\nKey: %s\nAuthority: %s\nOperation: %s\nReason: %s\n\n%s\n\nExact request:\n%s\n", safe(op.state), safe(op.request.Key), safe(op.request.Reviewed.Authority), safe(op.request.Operation), safe(op.request.Reason), safe(op.error), safe(poolRequestJSON(op.request)))
	if op.busy != "" {
		return text + "\nRequest in flight; controls wait for it. No automatic retry."
	}
	if op.state == "unknown" {
		return text + "\n[i] Check stored key (read-only) · [y] Retry exact request/key\nA missing result does not prove the original request cannot commit."
	}
	if op.change != nil {
		return text + fmt.Sprintf("\nRecorded journal #%d · capacity %d · revision %d\n", op.change.Sequence, op.change.Pool.Capacity, op.change.Pool.Revision)
	}
	return text
}
func (m *dashboardModel) keepPoolRowVisible() {
	m.syncViewport()
	row := 4 + 2*m.poolUI.selected
	if row < m.viewport.YOffset() {
		m.viewport.SetYOffset(row)
	} else if row+1 >= m.viewport.YOffset()+m.viewport.Height() {
		m.viewport.SetYOffset(max(0, row-m.viewport.Height()+2))
	}
}
func (m *dashboardModel) poolControls() []mouseControl {
	ui := m.poolUI
	if ui.form != nil {
		return []mouseControl{{"esc", "Keep unchanged"}}
	}
	if ui.review != nil {
		return []mouseControl{{"n", "Keep"}, {"y", "Confirm"}, {"pgup", "↑"}, {"pgdown", "↓"}}
	}
	switch ui.mode {
	case "detail":
		return []mouseControl{{"esc", "Pools"}, {"a", "Actions"}, {"h", "History"}, {"o", "Operation"}, {"i", "Record"}}
	case "actions":
		return []mouseControl{{"esc", "Back"}, {"enter", "Choose"}}
	case "history":
		return []mouseControl{{"esc", "Back"}, {"p", "←"}, {"n", "→"}, {"f", "First"}, {"i", "Record"}}
	case "operation":
		controls := []mouseControl{{"esc", "Pools"}}
		if op := m.poolPending; op != nil && op.state == "unknown" && op.busy == "" {
			controls = append(controls, mouseControl{"i", "Check"}, mouseControl{"y", "Retry"})
		}
		return controls
	default:
		return []mouseControl{{"esc", "Budgets"}, {"n", "New"}, {"enter", "Open"}, {"o", "Operation"}, {"r", "Refresh"}}
	}
}
