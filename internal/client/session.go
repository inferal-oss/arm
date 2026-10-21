package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

const sessionRequestLimit = 4
const sessionConnectTimeout = 10 * time.Second

type sessionResponse struct {
	raw json.RawMessage
	err error
}

type sessionPending struct {
	operation string
	response  chan sessionResponse
}

// Session multiplexes native requests, durable events and coalesced physical
// telemetry over the same authenticated WebSocket protocol as the browser.
// Calls are never retried: losing a mutation response leaves an unknown outcome.
type Session struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	once        sync.Once
	writeGate   chan struct{}
	mu          sync.Mutex
	pending     map[string]sessionPending
	monitorGate chan struct{}
	monitorID   string
	err         error
	done        chan struct{}
	events      chan model.Event
	telemetry   chan model.MonitorPage
}

func OpenSession(ctx context.Context, home string) (*Session, error) {
	// The endpoint can accept TCP without completing a WebSocket handshake.
	// Bound that dial alone; the established Session keeps its caller's lifetime.
	dialCtx, cancelDial := context.WithTimeout(ctx, sessionConnectTimeout)
	conn, err := Connect(dialCtx, home)
	cancelDial()
	if err != nil {
		return nil, err
	}
	return newSession(ctx, conn), nil
}

func newSession(ctx context.Context, conn *websocket.Conn) *Session {
	ctx, cancel := context.WithCancel(ctx)
	conn.SetReadLimit(4 << 20)
	s := &Session{conn: conn, ctx: ctx, cancel: cancel, pending: make(map[string]sessionPending), writeGate: make(chan struct{}, 1), monitorGate: make(chan struct{}, 1), done: make(chan struct{}), events: make(chan model.Event, 64), telemetry: make(chan model.MonitorPage, 1)}
	go s.read()
	return s
}

func (s *Session) Events() <-chan model.Event          { return s.events }
func (s *Session) Telemetry() <-chan model.MonitorPage { return s.telemetry }
func (s *Session) Done() <-chan struct{}               { return s.done }
func (s *Session) Close()                              { s.fail(errors.New("dashboard connection closed")) }
func (s *Session) Err() error                          { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func (s *Session) fail(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		for id, pending := range s.pending {
			pending.response <- sessionResponse{err: err}
			delete(s.pending, id)
		}
		s.mu.Unlock()
		close(s.done)
		s.cancel()
		_ = s.conn.CloseNow()
	})
}

func (s *Session) read() {
	for {
		var response struct {
			Version      int                `json:"version"`
			ID           string             `json:"id"`
			Result       json.RawMessage    `json:"result"`
			Error        *model.Error       `json:"error"`
			Event        *model.Event       `json:"event"`
			Telemetry    *model.MonitorPage `json:"telemetry"`
			Subscription string             `json:"subscription"`
		}
		if err := wsjson.Read(s.ctx, s.conn, &response); err != nil {
			s.fail(fmt.Errorf("dashboard connection lost: %w", err))
			return
		}
		if response.Version != 1 {
			s.fail(errors.New("unsupported dashboard protocol version"))
			return
		}
		if response.Event != nil {
			select {
			case s.events <- *response.Event:
			default:
				s.fail(errors.New("dashboard event buffer filled; reconnect and load a fresh snapshot"))
				return
			}
			continue
		}
		if response.Telemetry != nil {
			s.mu.Lock()
			matches := response.Subscription != "" && response.Subscription == s.monitorID
			s.mu.Unlock()
			if matches {
				s.offerTelemetry(*response.Telemetry)
			}
			continue
		}
		s.mu.Lock()
		pending, found := s.pending[response.ID]
		delete(s.pending, response.ID)
		if found && response.Error == nil {
			switch pending.operation {
			case "monitor_subscribe":
				s.monitorID = response.ID
				s.clearTelemetry()
			case "monitor_unsubscribe":
				s.monitorID = ""
				s.clearTelemetry()
			}
		}
		s.mu.Unlock()
		if found {
			var err error
			if response.Error != nil {
				err = response.Error
			}
			pending.response <- sessionResponse{raw: response.Result, err: err}
		} else if response.ID == "" && response.Error != nil {
			s.fail(response.Error)
			return
		}
	}
}

func (s *Session) clearTelemetry() {
	select {
	case <-s.telemetry:
	default:
	}
}

func (s *Session) offerTelemetry(page model.MonitorPage) {
	select {
	case s.telemetry <- page:
		return
	default:
	}
	select {
	case <-s.telemetry:
		// A skipped incremental page is an explicit history discontinuity.
		// State remains current; the model never draws a fabricated interval.
		page.Gap = true
	default:
	}
	select {
	case s.telemetry <- page:
	default: // The UI consumed a page concurrently; keep the read loop unblocked.
	}
}

func (s *Session) Call(ctx context.Context, operation string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	monitorControl := operation == "monitor_subscribe" || operation == "monitor_unsubscribe"
	if monitorControl {
		select {
		case s.monitorGate <- struct{}{}:
			defer func() { <-s.monitorGate }()
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return s.Err()
		}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id := local.ID()
	response := make(chan sessionResponse, 1)
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	if len(s.pending) >= sessionRequestLimit {
		s.mu.Unlock()
		return model.Err("too_many_requests", "at most four dashboard requests may be in flight")
	}
	s.pending[id] = sessionPending{operation: operation, response: response}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	select {
	case s.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.Err()
	}
	if err = ctx.Err(); err != nil {
		<-s.writeGate
		return err // Nothing was written; the existing monitor is unchanged.
	}
	err = wsjson.Write(ctx, s.conn, model.WireRequest{Version: 1, ID: id, Operation: operation, Params: raw})
	<-s.writeGate
	if err != nil {
		if monitorControl {
			s.fail(errors.New("monitor control write outcome unknown; reconnect and load a fresh snapshot"))
		}
		return err
	}
	select {
	case got := <-response:
		if got.err != nil || result == nil {
			return got.err
		}
		decoder := json.NewDecoder(bytes.NewReader(got.raw))
		decoder.UseNumber()
		return decoder.Decode(result)
	case <-ctx.Done():
		if monitorControl {
			s.fail(errors.New("monitor control outcome unknown; reconnect and load a fresh snapshot"))
		}
		return ctx.Err()
	case <-s.done:
		return s.Err()
	}
}
