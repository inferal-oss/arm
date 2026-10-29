package resident

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

type Call struct {
	Principal Principal
	Operation string
	Key       string
	Params    json.RawMessage
}

// Dispatch belongs to the resident. Its lifetime is independent of any worker
// or HTTP request. Errors retain an unknown effect, never permission to retry.
type Dispatch func(context.Context, Call) (json.RawMessage, error)

type Authority struct {
	mu       sync.Mutex
	opMu     sync.Mutex // Bounded serialized journal/effects; never a Manager lock.
	identity Identity
	dispatch Dispatch
	journal  *journal
	bindings map[string]*Binding
	active   *Binding
	closed   bool
	fault    error
}

// Binding is an original registration handle, not native exit evidence. This
// prototype retains both registrations forever; a launcher must separately
// retain/positively join the original worker process and all its I/O.
type Binding struct {
	owner       *Authority
	worker      WorkerIdentity
	principal   Principal
	capability  string
	clientToken string
	ready       chan struct{}
	activated   chan struct{}
	sealedCh    chan struct{}
	idle        chan struct{}
	sealed      bool
	handshake   bool
	claimed     bool
	inflight    int
	conn        net.Conn
}

func Open(path string, identity Identity, dispatch Dispatch) (*Authority, error) {
	if !hexID.MatchString(identity.Host) || !hexID.MatchString(identity.Generation) || dispatch == nil {
		return nil, failure("identity_invalid")
	}
	if _, err := strconv.ParseUint(identity.Owner, 10, 32); err != nil {
		return nil, failure("identity_invalid")
	}
	j, err := openJournal(path, identity)
	if err != nil {
		return nil, err
	}
	return &Authority{identity: identity, dispatch: dispatch, journal: j, bindings: map[string]*Binding{}}, nil
}

func validPrincipal(p Principal, id Identity) bool {
	return p.Kind == "local-fixture" && p.Owner == id.Owner
}

func (a *Authority) Stage(worker WorkerIdentity, p Principal) (*Binding, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.fault != nil {
		return nil, failure("authority_unavailable")
	}
	if !hexID.MatchString(worker.Generation) || !digestID.MatchString(worker.SHA256) || !versionID.MatchString(worker.Version) || !validPrincipal(p, a.identity) {
		return nil, failure("worker_invalid")
	}
	if _, ok := a.bindings[worker.Generation]; ok {
		return nil, failure("epoch_reused")
	}
	if len(a.bindings) >= MaxGenerations {
		return nil, failure("generation_limit")
	}
	if err := a.journal.worker(worker, "staged", true); err != nil {
		a.fault = err
		return nil, failure("journal_uncertain")
	}
	idle := make(chan struct{})
	close(idle)
	b := &Binding{owner: a, worker: worker, principal: p, capability: secret(), clientToken: secret(), ready: make(chan struct{}), activated: make(chan struct{}), sealedCh: make(chan struct{}), idle: idle}
	a.bindings[worker.Generation] = b
	return b, nil
}

func (b *Binding) Identity() WorkerIdentity { return b.worker }
func (b *Binding) ClientToken() string      { return b.clientToken }
func (b *Binding) Ready() <-chan struct{}   { return b.ready }

func (a *Authority) original(b *Binding) bool {
	return b != nil && b.owner == a && a.bindings[b.worker.Generation] == b
}

func (a *Authority) Activate(b *Binding) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.fault != nil {
		return failure("authority_unavailable")
	}
	if !a.original(b) || b.sealed || !b.handshake {
		return failure("worker_not_ready")
	}
	if a.active == b {
		return nil
	}
	if a.active != nil && (!a.active.sealed || a.active.inflight != 0) {
		return failure("prior_epoch_unjoined")
	}
	if err := a.journal.worker(b.worker, "active", false); err != nil {
		a.fault = err
		return failure("journal_uncertain")
	}
	a.active = b
	close(b.activated)
	return nil
}

// Seal fences new dispatch but neither cancels admitted effects nor certifies
// process exit. The returned channel joins admitted dispatch only.
func (a *Authority) Seal(b *Binding) (<-chan struct{}, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.original(b) {
		return nil, failure("worker_invalid")
	}
	if a.closed {
		return b.idle, nil
	}
	if !b.sealed {
		b.sealed = true
		close(b.sealedCh)
		if err := a.journal.worker(b.worker, "sealed", false); err != nil {
			a.fault = err
			return b.idle, failure("journal_uncertain")
		}
	}
	return b.idle, nil
}

func (a *Authority) begin(b *Binding, r Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.fault != nil {
		return failure("authority_unavailable")
	}
	if !a.original(b) || a.active != b || b.sealed {
		return failure("stale_epoch")
	}
	if r.Protocol != Protocol || r.Resident != a.identity || r.Worker != b.worker || subtle.ConstantTimeCompare([]byte(r.Capability), []byte(b.capability)) != 1 || r.ID == 0 {
		return failure("binding_changed")
	}
	if b.inflight != 0 {
		return failure("call_limit")
	}
	b.inflight++
	b.idle = make(chan struct{})
	return nil
}

func (a *Authority) end(b *Binding) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b.inflight--
	if b.inflight == 0 {
		close(b.idle)
	}
}

func receiptName(p Principal, key string) string {
	h := sha256.Sum256([]byte(p.Kind + "\x00" + p.Owner + "\x00" + key))
	return "receipt-" + hex.EncodeToString(h[:]) + ".json"
}

func requestHash(r Request) string {
	h := sha256.Sum256(append([]byte(r.Operation+"\x00"), r.Params...))
	return hex.EncodeToString(h[:])
}

func (a *Authority) call(b *Binding, r Request) Reply {
	reply := Reply{Protocol: Protocol, Resident: a.identity, Worker: b.worker, ID: r.ID, Key: r.Key, Outcome: "rejected"}
	if err := a.begin(b, r); err != nil {
		reply.Error = err.(*Failure)
		return reply
	}
	defer a.end(b)
	if len(r.Params) > MaxRequest || !json.Valid(r.Params) || len(r.Key) > 128 {
		reply.Error = failure("request_invalid")
		return reply
	}
	mutate := r.Operation == "run" || r.Operation == "renew" || r.Operation == "end"
	if !mutate && r.Operation != "status" && r.Operation != "run_inspect" && r.Operation != "run_output" && r.Operation != "lookup" {
		reply.Error = failure("operation_forbidden")
		return reply
	}
	if (mutate || r.Operation == "lookup") && r.Key == "" {
		reply.Error = failure("key_required")
		return reply
	}
	if !mutate && r.Operation != "lookup" && r.Key != "" {
		reply.Error = failure("request_invalid")
		return reply
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	// Fence validation and admission occurred before Seal's serialized cut.
	// This original admitted call may finish after sealing, without worker ctx.
	name := receiptName(b.principal, r.Key)
	if r.Operation == "lookup" {
		rec, ok := a.journal.receipts[name]
		if !ok {
			reply.Error = failure("not_found")
			return reply
		}
		reply.Outcome = "complete"
		reply.Result, _ = json.Marshal(rec)
		return reply
	}
	var rec receipt
	if mutate {
		if old, ok := a.journal.receipts[name]; ok {
			if old.Operation != r.Operation || old.SHA256 != requestHash(r) {
				reply.Error = failure("idempotency_conflict")
				return reply
			}
			return receiptReply(reply, old)
		}
		rec = receipt{Resident: a.identity, Principal: b.principal, Key: r.Key, Operation: r.Operation, SHA256: requestHash(r), State: "pending"}
		if err := a.journal.put(rec, true); err != nil {
			a.markFault(err)
			reply.Outcome = "unknown"
			reply.Error = failure("journal_uncertain")
			return reply
		}
	}
	result, err := a.invoke(Call{Principal: b.principal, Operation: r.Operation, Key: r.Key, Params: append(json.RawMessage(nil), r.Params...)})
	if err != nil || len(result) > MaxResponse/2 || !json.Valid(result) {
		reply.Outcome = "unknown"
		reply.Error = failure("effect_unknown")
	} else {
		reply.Outcome = "complete"
		reply.Result = append(json.RawMessage(nil), result...)
	}
	if mutate {
		rec.State = reply.Outcome
		rec.Result = reply.Result
		rec.Error = reply.Error
		if err := a.journal.put(rec, false); err != nil {
			a.markFault(err)
			reply.Outcome = "unknown"
			reply.Result = nil
			reply.Error = failure("journal_uncertain")
		}
	}
	return reply
}

func receiptReply(reply Reply, rec receipt) Reply {
	reply.Outcome = rec.State
	reply.Result = rec.Result
	reply.Error = rec.Error
	if rec.State != "complete" {
		reply.Outcome = "unknown"
		reply.Result = nil
		reply.Error = failure("effect_unknown")
	}
	return reply
}

func (a *Authority) markFault(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fault = errors.Join(a.fault, err)
}

func (a *Authority) invoke(call Call) (result json.RawMessage, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = failure("dispatch_panicked")
		}
	}()
	return a.dispatch(context.Background(), call)
}

// Serve accepts exactly one inherited channel per original registration. The
// caller owns the channel and the original worker process's positive join.
func (a *Authority) Serve(b *Binding, c net.Conn) error {
	a.mu.Lock()
	if !a.original(b) || b.claimed || a.closed {
		a.mu.Unlock()
		return failure("worker_invalid")
	}
	b.claimed = true
	b.conn = c
	a.mu.Unlock()
	defer c.Close()
	defer func() { _, _ = a.Seal(b) }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	boot := bootstrap{Protocol: Protocol, Resident: a.identity, Worker: b.worker, Capability: b.capability, ClientToken: b.clientToken}
	if err := writeFrame(c, 4096, boot); err != nil {
		return err
	}
	var h hello
	if err := readFrame(c, 4096, &h); err != nil {
		return err
	}
	if h.Protocol != Protocol || h.Resident != a.identity || h.Worker != b.worker || subtle.ConstantTimeCompare([]byte(h.Capability), []byte(b.capability)) != 1 {
		return failure("handshake_invalid")
	}
	a.mu.Lock()
	b.handshake = true
	close(b.ready)
	a.mu.Unlock()
	select {
	case <-b.activated:
	case <-b.sealedCh:
		return failure("stale_epoch")
	}
	a.mu.Lock()
	active := a.active == b && !b.sealed && !a.closed
	a.mu.Unlock()
	if !active {
		return failure("stale_epoch")
	}
	if err := writeFrame(c, 4096, map[string]int{"protocol": Protocol}); err != nil {
		return err
	}
	_ = c.SetDeadline(time.Time{})
	var previous uint64
	for {
		var r Request
		if err := readFrame(c, MaxRequest, &r); err != nil {
			return err
		}
		if r.ID <= previous {
			return failure("request_sequence")
		}
		previous = r.ID
		reply := a.call(b, r)
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := writeFrame(c, MaxResponse, reply); err != nil {
			return err
		}
		_ = c.SetWriteDeadline(time.Time{})
	}
}

// Close only closes the private boundary. It has no Manager.Close/cancel hook.
// Busy dispatch must be positively joined by its resident caller first.
func (a *Authority) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	for _, b := range a.bindings {
		if b.inflight != 0 {
			a.mu.Unlock()
			return failure("dispatch_busy")
		}
	}
	a.closed = true
	var conns []net.Conn
	for _, b := range a.bindings {
		if !b.sealed {
			b.sealed = true
			close(b.sealedCh)
		}
		if b.conn != nil {
			conns = append(conns, b.conn)
		}
	}
	a.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	return a.journal.close()
}
