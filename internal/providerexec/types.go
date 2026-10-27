// Package providerexec owns a bounded, explicitly selected local diagnostic
// worker. It creates no provider registration, external resource or public grant.
package providerexec

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/providerproto"
)

const HelperArgument = "_provider-worker-helper"
const MaxImageBytes = 64 << 20
const MaxRuntimeBytes = 512 << 20
const MaxStderrBytes = 2 << 20
const MaxStderrTail = 16 << 10
const SelectedResultBytes = 16 << 10
const TerminalCeiling = 34816
const InitialCeiling = 65536

// Held is a detached snapshot of ALREADY TRUSTED ordinary manager admissions.
// It is not authorization and is never constructed from provider/wire claims.
// P1b still requires a typed manager adapter retaining unknown-worker holds.
type Held struct {
	authority, owner, worker, temp, path string
	identity                             model.FileIdentity
	cpu                                  float64
	memory, disk                         int64
	expires                              time.Time
}

func HeldFromTrusted(worker, temp model.Lease) (Held, error) {
	var h Held
	if worker.ID == "" || temp.ID == "" || worker.ID == temp.ID || worker.Host == "" || worker.Host != temp.Host || worker.Owner == "" || worker.Owner != temp.Owner || worker.State != "active" || temp.State != "active" || worker.Request.Resources.CPU <= 0 || math.IsNaN(worker.Request.Resources.CPU) || math.IsInf(worker.Request.Resources.CPU, 0) || worker.Request.Resources.Memory <= 0 || temp.Request.Kind != "storage" || temp.Request.Resources.Disk <= 0 || worker.Expires == nil || temp.Expires == nil || temp.Artifact == nil || temp.Artifact.Identity == nil || temp.Artifact.Phase != "owned" {
		return h, fmt.Errorf("trusted active CPU/memory and managed-temp admissions required")
	}
	h = Held{strings.Clone(worker.Host), strings.Clone(worker.Owner), strings.Clone(worker.ID), strings.Clone(temp.ID), strings.Clone(temp.Artifact.Path), *temp.Artifact.Identity, worker.Request.Resources.CPU, worker.Request.Resources.Memory, temp.Request.Resources.Disk, minTime(*worker.Expires, *temp.Expires)}
	return h, nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (h Held) WorkerID() string  { return h.worker }
func (h Held) TempID() string    { return h.temp }
func (h Held) Authority() string { return h.authority }

// Selection pins the two exact native images. Pin functions do bounded native
// reads; call them outside manager/catalogue/SQL bookkeeping locks.
type Selection struct{ runtime, provider Image }

func SelectFromTrusted(runtime, provider Image) (Selection, error) {
	if !runtime.valid || !provider.valid || !imageSizeWithin(runtime.size, MaxRuntimeBytes) || !imageSizeWithin(provider.size, MaxImageBytes) {
		return Selection{}, fmt.Errorf("verified selected images required")
	}
	return Selection{runtime, provider}, nil
}

type Prepared struct {
	attempt, registration string
	selected              Selection
	held                  Held
	request               []byte
	requestDigest         string
	sent                  providerproto.MatchedRequest
	reg                   providerproto.RegistrationSnapshot
	original              providerproto.Original
	timeout               time.Duration
}

func Prepare(attempt, registration string, s Selection, h Held, q providerproto.Request, original providerproto.Original, timeout time.Duration) (Prepared, error) {
	if attempt == "" || len(attempt) > 128 || registration == "" || len(registration) > 128 || !s.runtime.valid || !s.provider.valid || !imageSizeWithin(s.runtime.size, MaxRuntimeBytes) || !imageSizeWithin(s.provider.size, MaxImageBytes) || h.worker == "" || h.temp == "" || q.Context.Authority != providerproto.ID(h.authority) || q.Context.OwnerUID != providerproto.Uint(h.owner) || string(q.Registration.ID) != registration || string(q.Registration.CodeSHA256) != s.provider.sha {
		return Prepared{}, fmt.Errorf("exact trusted attempt, images and held admissions required")
	}
	if timeout < time.Second || timeout > 300*time.Second || !time.Now().Add(timeout).Before(h.expires) {
		return Prepared{}, fmt.Errorf("diagnostic deadline must be 1–300 seconds within held admission")
	}
	switch q.Method {
	case providerproto.Describe, providerproto.Validate, providerproto.Plan:
	default:
		return Prepared{}, fmt.Errorf("only describe, validate and plan diagnostics are supported")
	}
	reg, err := providerproto.RegistrationFromTrusted(q.Registration, q.Configuration)
	if err != nil {
		return Prepared{}, err
	}
	raw, err := providerproto.EncodeRequest(q)
	if err != nil {
		return Prepared{}, err
	}
	decoded, err := providerproto.DecodeRequest(raw)
	if err != nil {
		return Prepared{}, err
	}
	sent, err := providerproto.MatchRequest(decoded, reg, original, nil)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{strings.Clone(attempt), strings.Clone(registration), s, h, raw, hash(raw), sent, reg, original, timeout}, nil
}
func (p Prepared) Attempt() string       { return p.attempt }
func (p Prepared) RequestDigest() string { return p.requestDigest }
func (p Prepared) HeldWorker() Held      { return p.held }

type Exit struct {
	Code   int `json:"code"`
	Signal int `json:"signal"`
}
type Observation struct {
	Attempt             string          `json:"attempt"`
	StartReturned       bool            `json:"start_returned"`
	KnownNotStarted     bool            `json:"known_not_started"`
	StartPending        bool            `json:"start_pending"`
	HelloVerified       bool            `json:"hello_verified"`
	GrantPublished      bool            `json:"grant_published"`
	CancelAccepted      bool            `json:"cancel_accepted"`
	ProviderExit        *Exit           `json:"provider_exit"`
	HelperWaitObserved  bool            `json:"helper_wait_observed"`
	GroupAbsentObserved bool            `json:"group_absent_observed"`
	PumpsJoined         bool            `json:"pumps_joined"`
	ScopedNativeClosed  bool            `json:"scoped_native_closed"`
	StdoutEOF           bool            `json:"stdout_eof"`
	StderrEOF           bool            `json:"stderr_eof"`
	ProtocolMatched     bool            `json:"protocol_matched"`
	ProtocolSuccess     bool            `json:"protocol_success"`
	Selected            json.RawMessage `json:"selected_result"`
	ResponseSHA256      string          `json:"response_sha256"`
	StdoutBytes         int64           `json:"stdout_bytes"`
	StderrBytes         int64           `json:"stderr_bytes"`
	Failure             string          `json:"failure"`
	// Explicit immediate detail only; never ordinary catalogue audit.
	StderrTail []byte `json:"-"`
}

func (o Observation) clone() Observation {
	if o.ProviderExit != nil {
		v := *o.ProviderExit
		o.ProviderExit = &v
	}
	o.Selected = append(json.RawMessage(nil), o.Selected...)
	o.StderrTail = append([]byte(nil), o.StderrTail...)
	return o
}

// No contextual callback or provider-supplied flag establishes this marker.
// The trusted durable coordinator must commit one exact start marker before
// BeginStart; its fixture is real SQLite and rejects unknown/restart replay.
type StartMarker struct{ Attempt, RequestDigest, Authority, Worker, Temp, OwnerProcess string }

func (m StartMarker) matches(p Prepared) bool {
	return m.Attempt == p.attempt && m.RequestDigest == p.requestDigest && m.Authority == p.held.authority && m.Worker == p.held.worker && m.Temp == p.held.temp && m.OwnerProcess != ""
}

// WaitObservation only waits on the existing retained handle; it never starts,
// grants, signals a recovered PID, creates a replacement or asserts a join.
func (h *Handle) WaitObservation(ctx context.Context) (Observation, error) {
	select {
	case <-h.done:
		return h.Snapshot(), nil
	case <-ctx.Done():
		return h.Snapshot(), ctx.Err()
	}
}

// DispatchMarker is a detached trusted durable coordinator receipt, not a wire
// request. It binds the actual hello identity and original launch record.
type DispatchMarker struct {
	Start                                  StartMarker
	WorkerProcessSHA256, PublicationSHA256 string
}
