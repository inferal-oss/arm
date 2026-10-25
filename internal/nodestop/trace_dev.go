//go:build arm_devtrace

package nodestop

// This recorder retains observations only. It grants no admission, clock,
// teardown, native-inspection or restart authority. No wire/environment API
// enables it. Tests explicitly select one private home before starting Serve.
import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yrashk/arm/internal/model"
	"sync"
	"sync/atomic"
)

type DevFact struct {
	Sequence int             `json:"sequence"`
	Kind     string          `json:"kind"`
	Home     string          `json:"home"`
	Data     json.RawMessage `json:"data"`
}
type DevCapture struct {
	mu      sync.Mutex
	home    string
	events  []DevFact
	bytes   int
	failure error
}

var devCapture DevCapture
var devSQLReads, devElapsedReads atomic.Uint64

func DevObserverReadCounts() (uint64, uint64) { return devSQLReads.Load(), devElapsedReads.Load() }

func DevBegin(home string) {
	devCapture.mu.Lock()
	defer devCapture.mu.Unlock()
	devCapture.home = home
	devCapture.events = nil
	devCapture.bytes = 0
	devCapture.failure = nil
}
func DevEnabled(home string) bool {
	devCapture.mu.Lock()
	defer devCapture.mu.Unlock()
	return devCapture.home == home && home != ""
}
func DevEmit(home, kind string, data any) {
	devCapture.mu.Lock()
	defer devCapture.mu.Unlock()
	if home == "" || home != devCapture.home || devCapture.failure != nil {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > 256<<10 || len(devCapture.events) >= 2048 || devCapture.bytes+len(raw) > 8<<20 {
		devCapture.failure = fmt.Errorf("bounded node-stop development capture failed: %v", err)
		return
	}
	devCapture.events = append(devCapture.events, DevFact{len(devCapture.events) + 1, kind, home, raw})
	devCapture.bytes += len(raw)
}
func DevEvents() ([]DevFact, error) {
	devCapture.mu.Lock()
	defer devCapture.mu.Unlock()
	raw, err := json.Marshal(devCapture.events)
	if err != nil {
		return nil, err
	}
	var out []DevFact
	err = json.Unmarshal(raw, &out)
	if devCapture.failure != nil {
		return out, devCapture.failure
	}
	return out, err
}

type DevClock struct {
	Last    int64            `json:"last"`
	Have    bool             `json:"have"`
	Failure string           `json:"failure"`
	Issued  map[string]int64 `json:"issued"`
}

// DevClockSnapshot copies retained observations. In particular it never calls
// nativeReviewTick (or the injected clock) a second time to infer a past fact.
func (b *Boundary) DevClockSnapshot() DevClock {
	if b == nil {
		return DevClock{Issued: map[string]int64{}}
	}
	r := &b.elapsed
	r.mu.Lock()
	defer r.mu.Unlock()
	out := DevClock{Last: r.last, Have: r.have, Issued: map[string]int64{}}
	if r.failed != nil {
		out.Failure = r.failed.Error()
	}
	for k, v := range r.issued {
		out.Issued[k] = v
	}
	return out
}

type DevElapsed struct {
	First   int64  `json:"first"`
	Last    int64  `json:"last"`
	Failure string `json:"failure"`
}

func (r *ElapsedReview) DevSnapshot() DevElapsed {
	devElapsedReads.Add(1)
	if r == nil {
		return DevElapsed{Failure: "missing retained review"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := DevElapsed{First: r.first, Last: r.last}
	if r.failure != nil {
		out.Failure = r.failure.Error()
	}
	return out
}

// Controlled clock installation is explicitly a development input. Subsequent
// issue/check calls still execute real permit code and retain its actual ticks.
// These controls do not establish that a native machine suspended.
func (b *Boundary) DevClockControl(tick int64, failure error) {
	b.elapsed.mu.Lock()
	defer b.elapsed.mu.Unlock()
	b.elapsed.now = func() (int64, error) { return tick, failure }
}
func (r *ElapsedReview) DevClockControl(tick int64, failure error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = func() (int64, error) { return tick, failure }
}
func traceBoundary(b *Boundary, kind string, p *Plan, err error) {
	if b == nil || !DevEnabled(b.target.Home) {
		return
	}
	out := struct {
		Target Target   `json:"target"`
		Plan   *Plan    `json:"plan,omitempty"`
		Clock  DevClock `json:"clock"`
		Error  string   `json:"error"`
		Code   string   `json:"code"`
	}{Target: b.target, Plan: p, Clock: b.DevClockSnapshot()}
	if err != nil {
		out.Error = err.Error()
		var api *model.Error
		if errors.As(err, &api) {
			out.Code = api.Code
		}
	}
	DevEmit(b.target.Home, "boundary."+kind, out)
}
func traceClosure(c *Closure, closed *Closed, err error) {
	if c == nil || !DevEnabled(c.record.Plan.Target.Home) {
		return
	}
	out := struct {
		Record     Record  `json:"record"`
		Closed     *Closed `json:"closed,omitempty"`
		StageCount int     `json:"stage_count"`
		Error      string  `json:"error"`
	}{Record: c.record, Closed: closed, StageCount: c.next}
	if err != nil {
		out.Error = err.Error()
	}
	DevEmit(c.record.Plan.Target.Home, "closure.write", out)
}

type DevLedger struct {
	State   *State            `json:"state"`
	Records []Record          `json:"records"`
	Rows    map[string]string `json:"rows"`
	Error   string            `json:"error"`
}

func DevReadLedger(q Queryer) (out DevLedger) {
	devSQLReads.Add(1)
	out = DevLedger{Records: []Record{}, Rows: map[string]string{}}
	state, present, err := ReadState(q)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if !present {
		return out
	}
	out.State = &state
	out.Records, err = ReadRecords(q)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	var raw string
	if err = q.QueryRow(`SELECT data FROM node_stop_state WHERE singleton=1`).Scan(&raw); err != nil {
		out.Error = err.Error()
		return out
	}
	out.Rows["state"] = raw
	rows, err := q.Query(`SELECT key,data FROM node_stop_operations ORDER BY key LIMIT 129`)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() {
		if e := rows.Close(); e != nil {
			out.Error += " SQL rows close: " + e.Error()
		}
	}()
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			out.Error = err.Error()
			return out
		}
		out.Rows["record:"+k] = v
	}
	if err = rows.Err(); err != nil {
		out.Error = err.Error()
	}
	return out
}

// DevFailureIdentity never invokes Check or the clock. It ties a real API's
// first returned failure to this same retained owner, including the original
// native observations. Classification is from retained arithmetic, not text.
func (r *ElapsedReview) DevFailureIdentity(err error) (string, bool) {
	if r == nil || err == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure == nil || !errors.Is(err, r.failure) {
		return "", false
	}
	if r.last-r.first >= int64(ReviewTTL) {
		return "retained-review-expired", true
	}
	return "retained-clock-fault", true
}
