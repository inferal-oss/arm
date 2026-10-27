package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
)

type nameChange struct {
	Operation        string `json:"operation"`
	Kind             string `json:"kind"`
	AuthorityHost    string `json:"authority_host"`
	ID               string `json:"id"`
	BindingLease     string `json:"binding_lease"`
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type retainedNameChange struct {
	Source  string
	Owner   string
	Request nameChange
}

type nameSnapshot struct {
	customnames.Document
	NamingDiagnostic *model.Error `json:"naming_diagnostic,omitempty"`
}
type nameHostChoice struct{ id, label string }

type nameEditor struct {
	manual                      bool
	hosts                       []nameHostChoice
	hostIndex, kindIndex, field int
	idInput, bindingInput       textinput.Model
	target                      customnames.Entry
	title, source               string
	input                       textinput.Model
	snapshot                    *nameSnapshot
	selected                    int
	loading, busy               bool
	err                         string
	request                     *nameChange
}
type nameReadMsg struct {
	namingSequence uint64
	editor         *nameEditor
	session        transport
	source         string
	value          nameSnapshot
	diagnostic     *model.Error
	err            error
}
type nameResultMsg struct {
	namingSequence uint64
	owner          string
	editor         *nameEditor
	session        transport
	source         string
	request        nameChange
	value          nameSnapshot
	diagnostic     *model.Error
	err            error
}

type namingTransport interface {
	CallWithDiagnostics(context.Context, string, any, any) (*model.Error, error)
}

func callWithNaming(ctx context.Context, session transport, operation string, params, result any) (*model.Error, error) {
	if capable, ok := session.(namingTransport); ok {
		return capable.CallWithDiagnostics(ctx, operation, params, result)
	}
	return nil, session.Call(ctx, operation, params, result)
}
func namingWarning(diagnostic *model.Error) string {
	if diagnostic == nil {
		return ""
	}
	if diagnostic.Code == "name_metadata_invalid" {
		return "Custom name metadata is invalid; exact IDs remain available. Inspect the database without repairing or dropping evidence."
	}
	return "Custom names are unavailable; exact IDs remain available. Read a fresh naming snapshot."
}
func nameResourceReference(host string, r model.ResourceView) (customnames.Entry, error) {
	ref := customnames.Entry{Kind: "resource", AuthorityHost: host, ID: r.ID}
	if r.Host != "" && r.Host != host {
		return ref, errors.New("resource differs from the local authority")
	}
	if r.Kind == "custom" {
		ref.Kind = "pool"
		if r.Custom != nil && r.Custom.Pool.Authority != "" {
			ref.AuthorityHost = r.Custom.Pool.Authority
		}
	}
	if r.Kind == "port" {
		owners := map[string]bool{}
		for _, u := range r.Users {
			if u.Relationship == "reservation" {
				owners[u.Lease] = true
			}
		}
		if len(owners) != 1 {
			return ref, errors.New("port names require one visible reservation owner; infrastructure ports cannot be named")
		}
		for id := range owners {
			ref.BindingLease = id
		}
	}
	if err := customnames.ValidateBinding(ref); err != nil {
		return ref, err
	}
	return ref, nil
}
func (m *dashboardModel) selectedNameReference() (customnames.Entry, string, error) {
	host := m.data.status.Host
	ref := customnames.Entry{Kind: "host", AuthorityHost: host, ID: host}
	title := m.hostDisplayName(host)
	if m.detail != nil {
		ref = customnames.Entry{Kind: "lease", AuthorityHost: m.detail.Host, ID: m.detail.ID}
		title = m.leaseDisplayName(ref.ID, ref.AuthorityHost)
	} else if m.poolUI != nil {
		var pool *model.PoolView
		if m.poolUI.mode == "list" && m.poolUI.selected >= 0 && m.poolUI.selected < len(m.poolUI.views) {
			pool = &m.poolUI.views[m.poolUI.selected]
		} else {
			pool = m.poolUI.current
		}
		if pool != nil {
			ref = customnames.Entry{Kind: "pool", AuthorityHost: pool.Pool.Authority, ID: pool.Pool.ID}
			title = m.poolDisplayName(pool.Pool)
		}
	} else if m.view == 6 && m.meshUI != nil {
		node := m.meshUI.node
		if m.meshUI.mode == "nodes" && m.meshUI.selected >= 0 && m.meshUI.selected < len(m.meshUI.registry.Nodes) {
			node = &m.meshUI.registry.Nodes[m.meshUI.selected]
		}
		if node != nil {
			ref = customnames.Entry{Kind: "host", AuthorityHost: node.Host, ID: node.Host}
			title = nodeDisplayName(*node)
		}
	} else if m.view == 2 {
		leases := m.filteredLeases()
		if m.selected >= 0 && m.selected < len(leases) {
			lease := leases[m.selected]
			ref = customnames.Entry{Kind: "lease", AuthorityHost: lease.Host, ID: lease.ID}
			title = m.leaseDisplayName(lease.ID, lease.Host)
		}
	} else if m.view == 1 {
		var resource *model.ResourceView
		if m.resourceID != "" {
			resource = m.selectedResource()
		} else {
			resources := m.filteredResources()
			if m.selected >= 0 && m.selected < len(resources) {
				resource = &resources[m.selected]
			}
		}
		if resource != nil {
			var err error
			ref, err = nameResourceReference(host, *resource)
			if err != nil {
				return ref, "", err
			}
			title = m.resourceDisplayName(*resource)
		}
	}
	return ref, title, customnames.ValidateBinding(ref)
}
func (m *dashboardModel) nameReferenceVisible(target customnames.Entry) bool {
	if target.Kind == "host" {
		if target.ID == m.data.status.Host {
			return true
		}
		if m.meshUI != nil && m.meshUI.registry.Source == m.data.status.Host {
			for _, n := range m.meshUI.registry.Nodes {
				if n.Host == target.ID {
					return true
				}
			}
		}
		return false
	}
	if target.Kind == "lease" {
		for _, lease := range m.data.status.Leases {
			if lease.ID == target.ID && lease.Host == target.AuthorityHost {
				return true
			}
		}
		return m.detail != nil && m.detail.ID == target.ID && m.detail.Host == target.AuthorityHost
	}
	if target.Kind == "pool" && m.poolUI != nil {
		for _, v := range m.poolUI.views {
			if v.Pool.ID == target.ID && v.Pool.Authority == target.AuthorityHost {
				return true
			}
		}
		if v := m.poolUI.current; v != nil && v.Pool.ID == target.ID && v.Pool.Authority == target.AuthorityHost {
			return true
		}
	}
	matches := 0
	for _, resource := range m.data.status.Resources {
		if resource.ID != target.ID {
			continue
		}
		ref, err := nameResourceReference(m.data.status.Host, resource)
		if err != nil || !customnames.SameBinding(ref, target) {
			return false
		}
		matches++
	}
	return matches > 0
}
func (m *dashboardModel) openNameEditor() tea.Cmd {
	if !m.connected || m.nodeUI != nil || m.recovery != nil || m.modal() || m.record != nil {
		m.notice = "Resource naming requires the local operator context."
		return nil
	}
	ref, title, err := m.selectedNameReference()
	if err != nil {
		m.notice = safe(err.Error())
		return nil
	}
	editor := &nameEditor{target: ref, title: title, source: m.data.status.Host, input: textinput.New()}
	editor.idInput = textinput.New()
	editor.bindingInput = textinput.New()
	editor.idInput.SetStyles(m.input.Styles())
	editor.bindingInput.SetStyles(m.input.Styles())
	editor.idInput.CharLimit = 256
	editor.bindingInput.CharLimit = 32
	editor.input.SetStyles(m.input.Styles())
	editor.input.SetValue("")
	editor.input.CharLimit = 128
	editor.input.Placeholder = "Custom name"
	editor.input.Focus()
	m.nameUI = editor
	m.feedFocus = false
	m.viewport.GotoTop()
	return m.readNames()
}
func (m *dashboardModel) readNames() tea.Cmd {
	editor := m.nameUI
	if editor == nil || editor.busy || editor.loading || !m.connected || m.nodeUI != nil || editor.source != m.data.status.Host {
		return nil
	}
	editor.loading = true
	editor.snapshot = nil
	session, ctx, source := m.session, m.ctx, editor.source
	return func() tea.Msg {
		var value nameSnapshot
		diagnostic, err, sequence := callWithNamingObservation(ctx, session, "name_list", map[string]bool{"all": true}, &value)
		return nameReadMsg{namingSequence: sequence, editor: editor, session: session, source: source, value: value, diagnostic: diagnostic, err: err}
	}
}
func (m *dashboardModel) validNameSnapshot(value nameSnapshot, source string) bool {
	return validNameSnapshotFor(value, source, m.data.status.Owner)
}
func validNameSnapshotFor(value nameSnapshot, source, owner string) bool {
	if value.NamingDiagnostic != nil || value.Version != 1 || value.Host != source || value.Owner != owner || value.Revision < 0 || value.Entries == nil {
		return false
	}
	if value.Revision == 0 {
		return len(value.Entries) == 0
	}
	raw, err := json.Marshal(value.Document)
	if err != nil {
		return false
	}
	_, err = customnames.Decode(raw, value.Owner, source)
	return err == nil
}
func (m *dashboardModel) acceptNameRead(msg nameReadMsg) tea.Cmd {
	editor := m.nameUI
	if editor == nil || editor != msg.editor || msg.session != m.session || msg.source != m.data.status.Host || m.nodeUI != nil {
		return nil
	}
	editor.loading = false
	diagnostic := msg.diagnostic
	if diagnostic == nil {
		diagnostic = msg.value.NamingDiagnostic
	}
	if diagnostic != nil {
		m.observeNaming(msg.namingSequence, diagnostic, false)
		editor.err = namingWarning(diagnostic)
		return nil
	}
	if msg.err != nil {
		editor.err = safe(msg.err.Error())
		return nil
	}
	if !m.validNameSnapshot(msg.value, msg.source) {
		editor.err = "A healthy naming snapshot for this exact source is required."
		return nil
	}
	m.observeNaming(msg.namingSequence, nil, true)
	m.pruneObservedNameIntents(msg.value.Document)
	editor.snapshot = &msg.value
	editor.err = ""
	editor.selected = 0
	editor.input.SetValue(customnames.ActiveName(msg.value.Document, editor.target))
	return nil
}
func (e *nameEditor) reservations() []customnames.Entry {
	target := e.target
	if e.manual {
		target = e.manualDraft()
	}
	result := []customnames.Entry{}
	if e.snapshot != nil {
		for _, entry := range e.snapshot.Entries {
			if customnames.SameBinding(entry, target) || e.manual && target.Kind == "resource" && strings.Contains(target.ID, "/ports/") && target.BindingLease == "" && entry.Kind == target.Kind && entry.AuthorityHost == target.AuthorityHost && entry.ID == target.ID {
				result = append(result, entry)
			}
		}
	}
	return result
}
func (m *dashboardModel) changeName(operation string) tea.Cmd {
	editor := m.nameUI
	if editor == nil || editor.busy || editor.loading || editor.snapshot == nil || !m.connected || editor.source != m.data.status.Host || m.nodeUI != nil {
		return nil
	}
	target := editor.target
	if editor.manual {
		var err error
		target, err = m.manualNameReference(editor)
		if err != nil {
			editor.err = safe(err.Error())
			return nil
		}
	}
	target.Name = editor.input.Value()
	if operation == "clear" {
		entries := editor.reservations()
		if editor.selected >= len(entries) {
			editor.err = "Choose an exact active or retained reservation to clear."
			return nil
		}
		target = entries[editor.selected]
	} else if !editor.manual && !m.nameReferenceVisible(target) {
		editor.err = "The exact binding is no longer in the loaded view. Read its resource again."
		return nil
	}
	if err := customnames.ValidateName(target.Name); err != nil {
		editor.err = safe(err.Error())
		return nil
	}
	if len(m.nameUnknown) >= 16 {
		editor.err = "Retained naming outcomes need inspection before another change. Exit to retain their exact requests."
		return nil
	}
	request := nameChange{operation, target.Kind, target.AuthorityHost, target.ID, target.BindingLease, target.Name, editor.snapshot.Revision}
	editor.busy = true
	editor.err = ""
	editor.request = &request
	session, ctx, source, owner := m.session, m.ctx, editor.source, editor.snapshot.Owner
	return func() tea.Msg {
		var value nameSnapshot
		diagnostic, err, sequence := callWithNamingObservation(ctx, session, "name_mutate", request, &value)
		return nameResultMsg{namingSequence: sequence, owner: owner, editor: editor, session: session, source: source, request: request, value: value, diagnostic: diagnostic, err: err}
	}
}
func (m *dashboardModel) acceptNameResult(msg nameResultMsg) tea.Cmd {
	// Retain a lost mutation response even if its original dialog/session retired.
	if msg.err != nil {
		var definitive *model.Error
		if !errors.As(msg.err, &definitive) {
			m.retainUnknownName(msg.source, msg.owner, msg.request)
		}
	}
	known := msg.err == nil && msg.diagnostic == nil && validNameSnapshotFor(msg.value, msg.source, msg.owner) && nameResultMatches(msg.request, msg.value.Document)
	if msg.err == nil && !known {
		m.retainUnknownName(msg.source, msg.owner, msg.request)
	}
	var definitive *model.Error
	if known || errors.As(msg.err, &definitive) {
		m.resolveNameIntent(msg.source, msg.owner, msg.request)
	}
	if known {
		m.pruneObservedNameIntents(msg.value.Document)
	}
	editor := m.nameUI
	if editor == nil || editor != msg.editor || msg.session != m.session || msg.source != m.data.status.Host || m.nodeUI != nil {
		return nil
	}
	editor.busy = false
	editor.request = nil
	if msg.err != nil {
		editor.snapshot = nil
		var definitive *model.Error
		if errors.As(msg.err, &definitive) {
			editor.err = safe(msg.err.Error()) + ". Read current names before another change."
		} else {
			editor.err = "Outcome may be unknown. Exact request retained; read the current map. No retry was sent."
		}
		return nil
	}
	if msg.diagnostic != nil {
		m.observeNaming(msg.namingSequence, msg.diagnostic, false)
	}
	if !known {
		editor.snapshot = nil
		editor.err = "Outcome may be unknown. The response did not establish this exact source, owner and requested map state; exact intent retained. Read current names."
		return nil
	}
	m.pruneObservedNameIntents(msg.value.Document)
	editor.snapshot = &msg.value
	if editor.manual {
		for _, entry := range msg.value.Entries {
			if entry.Active && entry.Kind == msg.request.Kind && entry.AuthorityHost == msg.request.AuthorityHost && entry.ID == msg.request.ID && entry.Name == msg.request.Name {
				editor.target = entry
				editor.bindingInput.SetValue(entry.BindingLease)
				break
			}
		}
	}
	editor.selected = 0
	editor.input.SetValue(customnames.ActiveName(msg.value.Document, editor.target))
	editor.err = "Name saved; exact resource IDs and resource state are unchanged."
	m.dirty = true
	return nil
}
func (m *dashboardModel) nameKey(key tea.KeyPressMsg) tea.Cmd {
	editor := m.nameUI
	if editor == nil {
		return nil
	}
	switch key.String() {
	case "esc":
		if !editor.busy {
			m.nameUI = nil
		}
		return nil
	case "ctrl+r":
		return m.readNames()
	case "ctrl+s":
		return m.changeName("set")
	case "ctrl+d":
		return m.changeName("clear")
	case "ctrl+t":
		if editor.busy || editor.loading {
			return nil
		}
		editor.manual = !editor.manual
		if editor.manual {
			editor.hosts = m.nameHosts()
			editor.hostIndex = 0
			for i, host := range editor.hosts {
				if host.id == editor.target.AuthorityHost {
					editor.hostIndex = i
				}
			}
			editor.kindIndex = 0
			for i, kind := range []string{"lease", "pool", "resource"} {
				if kind == editor.target.Kind {
					editor.kindIndex = i
				}
			}
			if editor.target.Kind != "host" {
				editor.idInput.SetValue(editor.target.ID)
			}
			editor.bindingInput.SetValue(editor.target.BindingLease)
			editor.field = 0
			m.focusNameField(editor)
		} else {
			editor.idInput.Blur()
			editor.bindingInput.Blur()
			editor.input.Focus()
		}
		return nil
	case "ctrl+g":
		if editor.manual && !editor.busy && len(editor.hosts) > 0 {
			editor.hostIndex = (editor.hostIndex + 1) % len(editor.hosts)
		}
		return nil
	case "ctrl+k":
		if editor.manual && !editor.busy {
			editor.kindIndex = (editor.kindIndex + 1) % 3
		}
		return nil
	case "ctrl+e":
		entries := editor.reservations()
		if len(entries) > 0 {
			editor.selected = (editor.selected + 1) % len(entries)
		}
		return nil
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(key)
		return cmd
	case "tab", "shift+tab":
		if editor.manual {
			direction := 1
			if key.String() == "shift+tab" {
				direction = -1
			}
			editor.field = (editor.field + direction + 3) % 3
			m.focusNameField(editor)
			return nil
		}
		entries := editor.reservations()
		if len(entries) > 0 {
			editor.selected = (editor.selected + 1) % len(entries)
		}
		return nil
	}
	if editor.loading || editor.busy {
		return nil
	}
	var cmd tea.Cmd
	if editor.manual {
		switch editor.field {
		case 0:
			editor.idInput, cmd = editor.idInput.Update(key)
		case 1:
			editor.bindingInput, cmd = editor.bindingInput.Update(key)
		default:
			editor.input, cmd = editor.input.Update(key)
		}
		return cmd
	}
	editor.input, cmd = editor.input.Update(key)
	return cmd
}
func (m *dashboardModel) nameText() string {
	e := m.nameUI
	if e == nil {
		return ""
	}
	var out strings.Builder
	preview := e.target
	title := e.title
	if e.manual {
		preview = e.manualDraft()
		title = "Exact reference · local presentation"
	}
	fmt.Fprintf(&out, "%s\nKind: %s\nID: %s\nAuthority ID: %s\n", safe(title), safe(preview.Kind), safe(preview.ID), safe(preview.AuthorityHost))
	if preview.BindingLease != "" {
		fmt.Fprintf(&out, "Reservation lease ID: %s\n", safe(preview.BindingLease))
	}
	if e.manual {
		draft := e.manualDraft()
		hostLabel := ""
		if e.hostIndex >= 0 && e.hostIndex < len(e.hosts) {
			hostLabel = e.hosts[e.hostIndex].label
		}
		fmt.Fprintf(&out, "\nEXACT REFERENCE · local writer %s\nRegistered target: %s · ID %s\nKind: %s\nExact ID: %s\nReservation lease assertion: %s\n", safe(e.source), safe(hostLabel), safe(draft.AuthorityHost), safe(draft.Kind), e.idInput.View(), e.bindingInput.View())
		out.WriteString("The backend independently reads this exact authorized target before writing a local label. No destination metadata or grants change.\n")
	}
	out.WriteString("\nCustom names change local presentation only. IDs and permissions stay unchanged.\n")
	if e.loading {
		out.WriteString("\nReading the current naming map…\n")
	} else if e.snapshot != nil {
		fmt.Fprintf(&out, "\nMap revision: %d\n\n%s\n", e.snapshot.Revision, e.input.View())
		for i, entry := range e.reservations() {
			state := "retained"
			if entry.Active {
				state = "active"
			}
			fmt.Fprintf(&out, "%s%s · %s\n", selectedPrefix(e.selected, i), safe(entry.Name), state)
			if entry.BindingLease != "" {
				fmt.Fprintf(&out, "  Reservation lease ID: %s\n", safe(entry.BindingLease))
			}
		}
	}
	if e.busy {
		out.WriteString("\nSaving the exact displayed binding. No automatic retry.\n")
	}
	if e.err != "" {
		out.WriteString("\n" + safe(e.err) + "\n")
	}
	for _, request := range m.nameUnknown {
		raw, _ := json.Marshal(request.Request)
		out.WriteString("\nRetained unknown local writer " + safe(request.Source) + " · owner " + safe(request.Owner) + ": " + safe(string(raw)) + "\n")
	}
	return out.String() + "\nCtrl+S set · Ctrl+D clear selected exact reservation\nCtrl+T exact reference · Ctrl+G host · Ctrl+K kind\nTab field/name · Ctrl+E reservation · Ctrl+R read map · Esc closes"
}

func nameResultMatches(request nameChange, doc customnames.Document) bool {
	if doc.Revision < request.ExpectedRevision {
		return false
	}
	target := customnames.Entry{Kind: request.Kind, AuthorityHost: request.AuthorityHost, ID: request.ID, BindingLease: request.BindingLease}
	if request.Operation == "clear" {
		for _, entry := range doc.Entries {
			if customnames.SameBinding(entry, target) && entry.Name == request.Name {
				return false
			}
		}
		return true
	}
	for _, entry := range doc.Entries {
		if entry.Active && entry.Kind == target.Kind && entry.AuthorityHost == target.AuthorityHost && entry.ID == target.ID && entry.Name == request.Name && (target.BindingLease == "" || entry.BindingLease == target.BindingLease) {
			return true
		}
	}
	return false
}
func (m *dashboardModel) retainUnknownName(source, owner string, request nameChange) {
	record := retainedNameChange{source, owner, request}
	for _, old := range m.nameUnknown {
		if old == record {
			return
		}
	}
	if len(m.nameUnknown) < 16 {
		m.nameUnknown = append(m.nameUnknown, record)
	}
}
func (m *dashboardModel) resolveNameIntent(source, owner string, request nameChange) {
	retained := m.nameUnknown[:0]
	for _, record := range m.nameUnknown {
		if record.Source != source || record.Owner != owner || record.Request != request {
			retained = append(retained, record)
		}
	}
	m.nameUnknown = retained
}
func (m *dashboardModel) pruneObservedNameIntents(doc customnames.Document) {
	retained := m.nameUnknown[:0]
	for _, record := range m.nameUnknown {
		if record.Source != doc.Host || record.Owner != doc.Owner || doc.Revision <= record.Request.ExpectedRevision {
			retained = append(retained, record)
		}
	}
	m.nameUnknown = retained
}
func (m *dashboardModel) retireNameEditor() {
	if m.nameUI != nil && m.nameUI.request != nil {
		owner := ""
		if m.nameUI.snapshot != nil {
			owner = m.nameUI.snapshot.Owner
		}
		m.retainUnknownName(m.nameUI.source, owner, *m.nameUI.request)
	}
	m.nameUI = nil
}
func (m *dashboardModel) nameHosts() []nameHostChoice {
	host := m.data.status.Host
	result := []nameHostChoice{{host, m.hostDisplayName(host)}}
	seen := map[string]bool{host: true}
	if m.meshUI != nil && m.meshUI.registry.Source == host && m.meshUI.registry.Error == nil {
		for _, node := range m.meshUI.registry.Nodes {
			ref := customnames.Entry{Kind: "host", AuthorityHost: node.Host, ID: node.Host}
			if !seen[node.Host] && customnames.ValidateBinding(ref) == nil {
				seen[node.Host] = true
				result = append(result, nameHostChoice{node.Host, nodeDisplayName(node)})
			}
		}
	}
	return result
}
func (e *nameEditor) manualDraft() customnames.Entry {
	authority := ""
	if e.hostIndex >= 0 && e.hostIndex < len(e.hosts) {
		authority = e.hosts[e.hostIndex].id
	}
	kinds := []string{"lease", "pool", "resource"}
	kind := ""
	if e.kindIndex >= 0 && e.kindIndex < len(kinds) {
		kind = kinds[e.kindIndex]
	}
	ref := customnames.Entry{Kind: kind, AuthorityHost: authority, ID: e.idInput.Value(), BindingLease: e.bindingInput.Value()}
	if kind == "resource" && strings.HasPrefix(ref.ID, "pool_") {
		ref.Kind = "pool"
	}
	return ref
}
func (m *dashboardModel) manualNameReference(e *nameEditor) (customnames.Entry, error) {
	ref := e.manualDraft()
	registered := false
	for _, host := range m.nameHosts() {
		if host.id == ref.AuthorityHost {
			registered = true
		}
	}
	if !registered {
		return ref, errors.New("choose a host from the currently loaded local registry; open Mesh to load registered hosts")
	}
	if ref.Kind == "resource" && strings.HasPrefix(ref.ID, ref.AuthorityHost+"/ports/") && ref.BindingLease == "" {
		// An absent assertion is not incarnation evidence. The backend owns full
		// canonical locator validation and derives the visible reservation owner.
		parts := strings.SplitN(strings.TrimPrefix(ref.ID, ref.AuthorityHost+"/ports/"), "/", 3)
		printable := true
		for _, c := range ref.ID {
			if c < 33 || c > 126 {
				printable = false
			}
		}
		network := len(parts) == 3 && (parts[1] == "tcp4" || parts[1] == "tcp6" || parts[1] == "udp4" || parts[1] == "udp6")
		if len(ref.ID) <= 256 && printable && network && parts[0] != "" && parts[2] != "" {
			return ref, nil
		}
	}
	return ref, customnames.ValidateBinding(ref)
}
func (m *dashboardModel) focusNameField(e *nameEditor) {
	e.idInput.Blur()
	e.bindingInput.Blur()
	e.input.Blur()
	switch e.field {
	case 0:
		e.idInput.Focus()
	case 1:
		e.bindingInput.Focus()
	default:
		e.input.Focus()
	}
}
