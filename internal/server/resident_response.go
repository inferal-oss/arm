package server

import (
	"context"
	"sync"
	"time"

	"getarm.org/internal/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const responseTrackingLimit = 32

type responseState uint8

const (
	responseReserved responseState = iota
	responseNotEnqueued
	responseQueueOwned
	responseWriteAttempt
	responseWriteSucceeded
	responseWriteFailed
	responseAbandoned
)

func (s responseState) terminal() bool {
	return s == responseNotEnqueued || s == responseWriteSucceeded || s == responseWriteFailed || s == responseAbandoned
}

type responseSinkIdentity struct{ marker byte }
type responseRegistryIdentity struct{ marker byte }

// Receipts contain no response/sink/connection/context reference. Released
// handles do not prolong payload or original ingress lifetime.
type responseReceipt struct {
	owner    *responseRegistryIdentity
	identity *responseSinkIdentity
	slot     uint8
}
type responseSettlement struct {
	State          responseState
	TeardownJoined bool
}
type responseRecord struct {
	receipt    *responseReceipt
	settlement responseSettlement
}
type responseTracking struct {
	sync.Mutex
	identity *responseRegistryIdentity
	records  [responseTrackingLimit]responseRecord
}
type responseEnvelope struct {
	response model.WireResponse
	receipt  *responseReceipt
}
type responseCounts struct{ Accepted, Attempted, Written, Failed, Abandoned, Refused uint64 }

type residentResponseSink struct {
	owner                   *Server
	caller                  *authenticatedCaller
	connection              *websocket.Conn
	ctx                     context.Context
	cancel                  context.CancelFunc
	producers               *sync.WaitGroup
	identity                *responseSinkIdentity
	mu                      sync.Mutex
	closed, started, joined bool
	out                     chan responseEnvelope
	done                    chan struct{}
	counts                  responseCounts
}

// openResponseSink is called only by the original WS acceptor, after activation.
// No replacement context or writer function is accepted. The actual connection
// must match the original caller registry and can acquire only one sink.
func (s *Server) openResponseSink(caller *authenticatedCaller, c *websocket.Conn, producers *sync.WaitGroup, cancel context.CancelFunc) (*residentResponseSink, error) {
	s.callers.Lock()
	ingress := s.callers.entries[caller]
	valid := caller != nil && caller.owner == s && ingress != nil && ingress.active && ingress.proof == caller.proof && ingress.connection == c
	s.callers.Unlock()
	if !valid {
		return nil, model.Err("forbidden", "original accepted response connection required")
	}
	if err := s.validateIngress(ingress); err != nil {
		return nil, err
	}
	if producers == nil || cancel == nil {
		return nil, model.Err("forbidden", "original response owners required")
	}
	sink := &residentResponseSink{owner: s, caller: caller, connection: c, ctx: ingress.ctx, cancel: cancel, producers: producers, identity: new(responseSinkIdentity), out: make(chan responseEnvelope, 64), done: make(chan struct{})}
	s.callers.Lock()
	defer s.callers.Unlock()
	current := s.callers.entries[caller]
	if current != ingress || ingress == nil || !ingress.active || ingress.connection != c || ingress.sink != nil || ingress.ctx.Err() != nil {
		return nil, model.Err("forbidden", "original accepted response connection required")
	}
	ingress.sink = sink
	return sink, nil
}
func (s *residentResponseSink) original() bool {
	if s == nil || s.owner == nil || s.caller == nil {
		return false
	}
	s.owner.callers.Lock()
	defer s.owner.callers.Unlock()
	ingress := s.owner.callers.entries[s.caller]
	return ingress != nil && ingress.sink == s && ingress.connection == s.connection
}
func (s *residentResponseSink) reserve() (*responseReceipt, error) {
	if !s.original() {
		return nil, model.Err("forbidden", "original response sink required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return nil, model.Err("response_closed", "response sink closed")
	}
	tracking := &s.owner.responses
	tracking.Lock()
	defer tracking.Unlock()
	if tracking.identity == nil {
		tracking.identity = new(responseRegistryIdentity)
	}
	for i := range tracking.records {
		if tracking.records[i].receipt == nil {
			receipt := &responseReceipt{owner: tracking.identity, identity: s.identity, slot: uint8(i)}
			tracking.records[i] = responseRecord{receipt: receipt, settlement: responseSettlement{State: responseReserved}}
			return receipt, nil
		}
	}
	return nil, model.Err("response_tracking_full", "response settlement tracking capacity unavailable")
}
func (t *responseTracking) record(receipt *responseReceipt) *responseRecord {
	if receipt == nil || t.identity == nil || receipt.owner != t.identity || int(receipt.slot) >= len(t.records) {
		return nil
	}
	record := &t.records[receipt.slot]
	if record.receipt != receipt {
		return nil
	}
	return record
}
func (s *Server) responseSettlement(receipt *responseReceipt) (responseSettlement, bool) {
	s.responses.Lock()
	defer s.responses.Unlock()
	record := s.responses.record(receipt)
	if record == nil {
		return responseSettlement{}, false
	}
	return record.settlement, true
}
func (s *Server) releaseResponseReceipt(receipt *responseReceipt) bool {
	s.responses.Lock()
	defer s.responses.Unlock()
	record := s.responses.record(receipt)
	if record == nil || !record.settlement.State.terminal() {
		return false
	}
	*record = responseRecord{}
	return true
}
func (s *residentResponseSink) cancelReservation(receipt *responseReceipt) bool {
	if !s.original() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &s.owner.responses
	t.Lock()
	defer t.Unlock()
	record := t.record(receipt)
	if record == nil || receipt.identity != s.identity || record.settlement.State != responseReserved {
		return false
	}
	record.settlement.State = responseNotEnqueued
	return true
}

// enqueue and close admission share one cut. The channel is never closed; no
// accepted entry can arrive after the teardown drain. Tracking is optional and
// its finite capacity never changes an ordinary public response's admission.
func (s *residentResponseSink) enqueue(response model.WireResponse, receipt *responseReceipt) bool {
	if !s.original() {
		return false
	}
	s.mu.Lock()
	t := &s.owner.responses
	var record *responseRecord
	if receipt != nil {
		t.Lock()
		record = t.record(receipt)
		if record == nil || receipt.identity != s.identity || record.settlement.State != responseReserved {
			t.Unlock()
			s.mu.Unlock()
			return false
		}
	}
	if s.closed || s.ctx.Err() != nil {
		if record != nil {
			record.settlement.State = responseNotEnqueued
			t.Unlock()
		}
		s.counts.Refused++
		s.mu.Unlock()
		return false
	}
	// Publish QueueOwned under the same lock used by the receiving writer.
	select {
	case s.out <- responseEnvelope{response: response, receipt: receipt}:
		if record != nil {
			record.settlement.State = responseQueueOwned
			t.Unlock()
		}
		s.counts.Accepted++
		s.mu.Unlock()
		return true
	default:
		if record != nil {
			record.settlement.State = responseNotEnqueued
			t.Unlock()
		}
		s.counts.Refused++
		s.closed = true
		s.mu.Unlock()
		s.cancel()
		return false
	}
}

func (s *residentResponseSink) fence() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
}

// runWriter is the only writer/positive completion authority. It uses the
// original socket/encoder/deadline; observers cannot report success or bytes.
func (s *residentResponseSink) runWriter() {
	if !s.original() {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	defer close(s.done)
	defer s.fence()
	for {
		select {
		case <-s.ctx.Done():
			return
		case entry := <-s.out:
			s.mu.Lock()
			s.counts.Attempted++
			if entry.receipt != nil {
				t := &s.owner.responses
				t.Lock()
				t.record(entry.receipt).settlement.State = responseWriteAttempt
				t.Unlock()
			}
			s.mu.Unlock()
			writeCtx, stop := context.WithTimeout(s.ctx, 5*time.Second)
			err := wsjson.Write(writeCtx, s.connection, entry.response)
			stop()
			// Drop the only queue/writer payload reference before publishing settlement.
			entry.response = model.WireResponse{}
			s.mu.Lock()
			state := responseWriteSucceeded
			if err != nil {
				state = responseWriteFailed
				s.counts.Failed++
			} else {
				s.counts.Written++
			}
			if entry.receipt != nil {
				t := &s.owner.responses
				t.Lock()
				t.record(entry.receipt).settlement.State = state
				t.Unlock()
			}
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// closeAndJoin is called by the original WS teardown, after reader exit. Fence
// first, join the original writer/producers, then abandon only never-written
// queued entries. A cancelled/blocked writer remains pending until it returns.
func (s *residentResponseSink) closeAndJoin() {
	s.fence()
	<-s.done
	s.producers.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		select {
		case entry := <-s.out:
			entry.response = model.WireResponse{}
			s.counts.Abandoned++
			if entry.receipt != nil {
				t := &s.owner.responses
				t.Lock()
				t.record(entry.receipt).settlement = responseSettlement{State: responseAbandoned, TeardownJoined: true}
				t.Unlock()
			}
		default:
			t := &s.owner.responses
			t.Lock()
			for i := range t.records {
				record := &t.records[i]
				if record.receipt != nil && record.receipt.identity == s.identity {
					if record.settlement.State == responseReserved {
						record.settlement.State = responseNotEnqueued
					}
					record.settlement.TeardownJoined = true
				}
			}
			t.Unlock()
			s.joined = true
			return
		}
	}
}
func (s *residentResponseSink) snapshot() (responseCounts, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts, s.joined
}
