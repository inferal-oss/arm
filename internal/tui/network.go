package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

type transport interface {
	Call(context.Context, string, any, any) error
	Events() <-chan model.Event
	Telemetry() <-chan model.MonitorPage
	Done() <-chan struct{}
	Err() error
	Close()
}

type snapshot struct {
	status   model.Status
	messages []manager.Message
	contacts []agents.Contact
}
type connectedMsg struct {
	session transport
	data    snapshot
	page    model.MonitorPage
	events  []model.Event
	err     error
}
type refreshedMsg struct {
	session transport
	data    snapshot
	err     error
}
type streamMsg struct {
	session transport
	event   *model.Event
	page    *model.MonitorPage
	err     error
}
type tickMsg time.Time
type reconnectMsg struct{}

func readSnapshot(ctx context.Context, session transport) (snapshot, error) {
	var data snapshot
	if err := session.Call(ctx, "status", nil, &data.status); err != nil {
		return data, err
	}
	if err := session.Call(ctx, "messages", nil, &data.messages); err != nil {
		return data, err
	}
	if err := session.Call(ctx, "agents", nil, &data.contacts); err != nil {
		return data, err
	}
	return data, nil
}

func connect(ctx context.Context, home string) tea.Cmd {
	return func() tea.Msg {
		s, err := client.OpenSession(ctx, home)
		if err != nil {
			return connectedMsg{err: err}
		}
		msg := connectedMsg{session: s}
		msg.data, msg.err = readSnapshot(ctx, s)
		if msg.err == nil {
			msg.err = s.Call(ctx, "subscribe", map[string]int64{"after": msg.data.status.Cursor}, nil)
		}
		if msg.err == nil {
			msg.err = s.Call(ctx, "monitor_subscribe", model.MonitorQuery{}, &msg.page)
		}
		if msg.err == nil {
			msg.err = s.Call(ctx, "events", map[string]int64{"after": max(0, msg.data.status.Cursor-200)}, &msg.events)
		}
		if msg.err != nil {
			s.Close()
		}
		return msg
	}
}
func refresh(ctx context.Context, s transport) tea.Cmd {
	return func() tea.Msg { data, err := readSnapshot(ctx, s); return refreshedMsg{s, data, err} }
}
func waitStream(ctx context.Context, s transport) tea.Cmd {
	return func() tea.Msg {
		select {
		case e := <-s.Events():
			return streamMsg{session: s, event: &e}
		case p := <-s.Telemetry():
			return streamMsg{session: s, page: &p}
		case <-s.Done():
			return streamMsg{session: s, err: s.Err()}
		case <-ctx.Done():
			return streamMsg{session: s, err: ctx.Err()}
		}
	}
}
func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func mergeMonitor(previous []model.MonitorPoint, page model.MonitorPage, epoch string) []model.MonitorPoint {
	if page.Gap || epoch != page.Epoch {
		previous = nil
	}
	byID := make(map[int64]model.MonitorPoint, len(previous)+len(page.History))
	for _, point := range previous {
		byID[point.Sequence] = point
	}
	for _, point := range page.History {
		byID[point.Sequence] = point
	}
	// Only the collector's bounded last 60 sequence positions are useful.
	out := make([]model.MonitorPoint, 0, 60)
	for sequence := max(0, page.Cursor-59); sequence <= page.Cursor; sequence++ {
		if point, ok := byID[sequence]; ok {
			out = append(out, point)
		}
		if sequence == page.Cursor {
			break
		}
	}
	return out
}
