package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/yrashk/arm/internal/model"
)

// Each lease owns two aligned lines: identity and account remain adjacent. The
// complete ID, unabridged purpose, claims and timestamps live in lease details.
func (m *dashboardModel) leaseTable(leases []model.Lease) string {
	width := max(12, m.mainWidth()-4)
	headers := []string{"Lease / Owner", "Status"}
	widths := []int{max(16, width-19), 19}
	if width >= 58 {
		widths = []int{22, 17, width - 39}
		headers = append(headers, "Reason")
	}
	if width >= 88 {
		widths = []int{24, 17, width - 24 - 17 - 17 - 17, 17, 17}
		headers = []string{"Lease / Owner", "Status", "Reason", "Claims", "Expires"}
	}
	rows := make([][]string, 0, len(leases))
	hasCurrentAccount := false
	for i, l := range leases {
		ownerName := l.OwnerUsername
		currentAccount := false
		if ownerName == "" && l.Host == m.data.status.Host && l.Owner == m.data.status.Owner {
			ownerName = m.data.status.OwnerUsername
			currentAccount = ownerName != ""
			hasCurrentAccount = hasCurrentAccount || currentAccount
		}
		account := "UID " + safe(l.Owner)
		if ownerName != "" {
			suffix := ""
			if currentAccount {
				suffix = "*"
			}
			account = short(safe(ownerName), max(1, widths[0]-ansiWidth(account)-4-len(suffix))) + suffix + " · " + account
		}
		row := []string{selectedPrefix(m.selected, i) + short(safe(l.ID), widths[0]-4) + "\n" + account, leaseLabel(l)}
		if len(headers) >= 3 {
			row = append(row, short(safe(l.Request.Reason), widths[2]-2))
		}
		if len(headers) == 5 {
			var parts []string
			if l.Request.Resources.CPU > 0 {
				parts = append(parts, fmt.Sprintf("%g CPU", l.Request.Resources.CPU))
			}
			if l.Request.Resources.Memory > 0 {
				parts = append(parts, byteCount(fmt.Sprint(l.Request.Resources.Memory))+" RAM")
			}
			if l.Request.Resources.Disk > 0 {
				parts = append(parts, byteCount(fmt.Sprint(l.Request.Resources.Disk))+" disk")
			}
			if len(l.Request.Ports) > 0 {
				parts = append(parts, fmt.Sprintf("%d port claims", len(l.Request.Ports)))
			}
			if len(l.Request.Pools) > 0 {
				parts = append(parts, fmt.Sprintf("%d custom claims", len(l.Request.Pools)))
			}
			claims := "—"
			if len(parts) > 0 {
				claims = strings.Join(parts[:min(2, len(parts))], "\n")
			}
			if len(parts) > 2 {
				claims = parts[0] + fmt.Sprintf("\n+%d more claims", len(parts)-1)
			}

			expiry := "—"
			if l.Expires != nil {
				expiry = l.Expires.Local().Format("Jan 02 15:04")
			}
			row = append(row, claims, expiry)
		}
		rows = append(rows, row)
	}
	legend := ""
	if hasCurrentAccount {
		legend = "\n* Current account lookup; historical name unknown. Details explain attribution.\n"
	}
	return table.New().Headers(headers...).Rows(rows...).Width(width).Wrap(false).
		Border(lipgloss.NormalBorder()).BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).BorderColumn(false).BorderRow(false).BorderHeader(true).
		BorderStyle(lipgloss.NewStyle().Foreground(colorLine)).StyleFunc(func(row, col int) lipgloss.Style {
		style := panelStyle.Padding(0, 1).Width(widths[col]).MaxWidth(widths[col])
		if row == table.HeaderRow {
			return style.Height(1).Bold(true).Foreground(colorBlue)
		}
		style = style.Height(2)
		if row == m.selected {
			return style.Background(lipgloss.Color("#203630")).Foreground(colorText)
		}
		if row%2 == 1 {
			style = style.Background(lipgloss.Color("#182229"))
		}
		if col == 1 {
			style = style.Foreground(colorAccent)
		}
		return style
	}).String() + legend + "\n\n" + muted.Render("Click a row to select · click selected row / Enter for full details")
}
func ansiWidth(value string) int                      { return lipgloss.Width(value) }
func (m *dashboardModel) leaseTableSelectionRow() int { return 4 + 2*m.selected }
