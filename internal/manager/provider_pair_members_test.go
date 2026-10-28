package manager

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/providerpreparation"
)

type providerPairFixture struct {
	m          *Manager
	core       *providerPairCore
	original   *providerPairOriginal
	sample     leaseclock.Sample
	clockCalls int
	cancel     context.CancelFunc
}

// This private fixture supplies an original only to the unused observer. It is
// not an admission publisher, full pair checker or native entitlement producer.
func providerPairOriginalFixture(t *testing.T) *providerPairFixture {
	t.Helper()
	f := &providerPairFixture{sample: leaseclock.Sample{Boot: "pair-test-boot", Tick: int64(100 * time.Second), Wall: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}}
	f.m = &Manager{db: providerPairDB(t), host: strings.Repeat("c", 32), owner: "42"}
	f.core = &providerPairCore{manager: f.m, epoch: providerpreparation.Hex32(strings.Repeat("d", 32)), originals: map[providerpreparation.Hex32]*providerPairOriginal{}}
	life, cancel := context.WithCancel(t.Context())
	f.cancel = cancel
	t.Cleanup(cancel)
	f.original = &providerPairOriginal{core: f.core, id: providerpreparation.Hex32(strings.Repeat("e", 32)), epoch: f.core.epoch, authority: f.m.host, owner: f.m.owner, lifetime: life}
	for i, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
		anchor := f.sample
		anchor.Tick -= int64(i) * int64(10*time.Second)
		d, err := leaseclock.New(anchor, time.Duration(providerpreparation.LifetimeNS))
		if err != nil {
			t.Fatal(err)
		}
		f.original.clocks[i], f.original.expires[i] = *d, d.Wall
		l := model.Lease{ID: id, Host: f.m.host, Owner: f.m.owner, Revision: 1, State: "provisioning", Clock: d, Expires: &f.original.expires[i], Request: model.Request{Key: []string{"pair:worker", "pair:temp"}[i]}}
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		f.original.rows[i] = providerPairLeaseRow{id, l.Request.Key, raw, []byte(`{"path":[]}`)}
		providerPairInsert(t, f.m.db, f.original.rows[i])
	}
	f.core.originals[f.original.id] = f.original
	f.m.leaseClockRead = func() (leaseclock.Sample, error) { f.clockCalls++; return f.sample, nil }
	return f
}

func TestProviderPairOriginalRegistry(t *testing.T) {
	tests := []struct {
		name   string
		damage func(*providerPairFixture)
	}{
		{"foreign manager", func(f *providerPairFixture) { f.core.manager = &Manager{} }},
		{"foreign core", func(f *providerPairFixture) { f.original.core = &providerPairCore{} }},
		{"epoch", func(f *providerPairFixture) { f.original.epoch = providerpreparation.Hex32(strings.Repeat("f", 32)) }},
		{"malformed epoch", func(f *providerPairFixture) { f.core.epoch = "bad"; f.original.epoch = "bad" }},
		{"host", func(f *providerPairFixture) { f.original.authority = strings.Repeat("f", 32) }},
		{"owner", func(f *providerPairFixture) { f.original.owner = "43" }},
		{"unregistered", func(f *providerPairFixture) { delete(f.core.originals, f.original.id) }},
		{"replaced original", func(f *providerPairFixture) { copy := *f.original; f.core.originals[f.original.id] = &copy }},
		{"nil lifetime", func(f *providerPairFixture) { f.original.lifetime = nil }},
		{"cancelled lifetime", func(f *providerPairFixture) { f.cancel() }},
		{"aliased ids", func(f *providerPairFixture) { f.original.rows[1].id = f.original.rows[0].id }},
		{"aliased keys", func(f *providerPairFixture) { f.original.rows[1].requestKey = f.original.rows[0].requestKey }},
		{"closing", func(f *providerPairFixture) { f.m.closing = true }},
		{"draining", func(f *providerPairFixture) { f.m.draining = true }},
		{"sticky core fault", func(f *providerPairFixture) { f.core.fault = errors.New("uncertain publication") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			tt.damage(f)
			got, err := f.m.observeProviderPairMembers(t.Context(), f.core, f.original)
			if err == nil || got.original != nil || f.clockCalls != 0 {
				t.Fatal("foreign original observed", got, err, f.clockCalls)
			}
		})
	}
	t.Run("current", func(t *testing.T) {
		f := providerPairOriginalFixture(t)
		got, err := f.m.observeProviderPairMembers(t.Context(), f.core, f.original)
		if err != nil || got.original != f.original || got.remaining != 290*time.Second || f.clockCalls != 1 {
			t.Fatal(got, err, f.clockCalls)
		}
	})
	t.Run("zero", func(t *testing.T) {
		f := providerPairOriginalFixture(t)
		got, err := f.m.observeProviderPairMembers(t.Context(), nil, nil)
		if err == nil || got.original != nil {
			t.Fatal(got, err)
		}
	})
}

func TestProviderPairMemberClock(t *testing.T) {
	t.Run("wall jumps do not grant time", func(t *testing.T) {
		f := providerPairOriginalFixture(t)
		for _, jump := range []time.Duration{365 * 24 * time.Hour, -365 * 24 * time.Hour} {
			f.sample.Tick += int64(time.Second)
			f.sample.Wall = f.sample.Wall.Add(jump)
			got, err := f.m.observeProviderPairMembers(t.Context(), f.core, f.original)
			want := time.Duration(f.original.clocks[1].End - f.sample.Tick)
			if err != nil || got.remaining != want || got.sample != f.sample {
				t.Fatal(got, err, want)
			}
		}
	})
	tests := []struct {
		name   string
		damage func(*providerPairFixture)
	}{
		{"expiry", func(f *providerPairFixture) { f.sample.Tick = f.original.clocks[1].End }},
		{"boot change", func(f *providerPairFixture) { f.sample.Boot = "other-boot" }},
		{"regression", func(f *providerPairFixture) { f.m.leaseClockLast = f.sample; f.sample.Tick-- }},
		{"audit mismatch", func(f *providerPairFixture) { f.original.expires[0] = f.original.expires[0].Add(time.Second) }},
		{"invalid duration", func(f *providerPairFixture) { f.original.clocks[0].End++ }},
		{"invalid version", func(f *providerPairFixture) { f.original.clocks[0].Version = 2 }},
		{"negative anchor", func(f *providerPairFixture) { f.original.clocks[0].Anchor = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			tt.damage(f)
			got, err := f.m.observeProviderPairMembers(t.Context(), f.core, f.original)
			if err == nil || got.original != nil {
				t.Fatal(got, err)
			}
		})
	}
	t.Run("clock failure is sticky", func(t *testing.T) {
		f := providerPairOriginalFixture(t)
		f.m.leaseClockRead = func() (leaseclock.Sample, error) { return leaseclock.Sample{}, errors.New("native read failed") }
		if _, err := f.m.observeProviderPairMembers(t.Context(), f.core, f.original); err == nil {
			t.Fatal("failure observed")
		}
		f.m.leaseClockRead = func() (leaseclock.Sample, error) { t.Fatal("sticky failure resampled"); return f.sample, nil }
		if _, err := f.m.observeProviderPairMembers(t.Context(), f.core, f.original); err == nil {
			t.Fatal("failed clock revived")
		}
	})
}

type providerPairWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *providerPairWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
func providerPairWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("original bounded reader did not return")
	}
}

func TestProviderPairReadCancellation(t *testing.T) {
	t.Run("held manager fence", func(t *testing.T) {
		m := &Manager{}
		m.mu.Lock()
		defer m.mu.Unlock()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		c := &providerPairWaitContext{Context: ctx, waiting: make(chan struct{})}
		done := make(chan struct{})
		var err error
		go func() { defer close(done); err = m.lockProviderPairMembers(c) }()
		// Done is consulted only after the real TryLock has refused this held mutex.
		providerPairWait(t, c.waiting)
		cancel()
		providerPairWait(t, done)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	for _, original := range []bool{false, true} {
		name := "caller"
		if original {
			name = "original"
		}
		t.Run("held SQL connection/"+name, func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			conn, err := f.m.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			var got providerPairMemberObservation
			before := f.m.db.Stats().WaitCount
			go func() { defer close(done); got, err = f.m.observeProviderPairMembers(ctx, f.core, f.original) }()
			deadline := time.Now().Add(time.Second)
			for f.m.db.Stats().WaitCount == before {
				if time.Now().After(deadline) {
					t.Fatal("reader never waited on actual sole connection")
				}
				runtime.Gosched()
			}
			if original {
				f.cancel()
			} else {
				cancel()
			}
			providerPairWait(t, done)
			if err == nil || got.original != nil || f.clockCalls != 0 {
				t.Fatal(got, err, f.clockCalls)
			}
			if !f.m.mu.TryLock() {
				t.Fatal("cancelled reader retained Manager fence")
			}
			f.m.mu.Unlock()
		})
	}
}

func TestProviderPairRowReplacement(t *testing.T) {
	for _, tt := range []struct {
		name, sql string
		arg       any
	}{
		{"worker raw", `UPDATE leases SET data=data||' ' WHERE id=?`, strings.Repeat("a", 32)},
		{"temp raw", `UPDATE leases SET data=data||' ' WHERE id=?`, strings.Repeat("b", 32)},
		{"budget raw", `UPDATE lease_budget_bindings SET data=data||' ' WHERE lease=?`, strings.Repeat("a", 32)},
		{"key", `UPDATE leases SET request_key='replacement' WHERE id=?`, strings.Repeat("b", 32)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := providerPairOriginalFixture(t)
			saved := append([]byte(nil), f.original.rows[0].lease...)
			if _, err := f.m.db.Exec(tt.sql, tt.arg); err != nil {
				t.Fatal(err)
			}
			// The altered row still passes the raw reader. Exact comparison,
			// rather than SQL setup/type failure, is what must stop observation.
			index := 0
			if tt.name == "temp raw" || tt.name == "key" {
				index = 1
			}
			row, err := providerPairRead(t, f.m.db, t.Context(), f.original.rows[index].id)
			if err != nil || !row.bounded() || row.same(f.original.rows[index]) {
				t.Fatal("replacement control did not remain readable and distinct", err)
			}
			got, err := f.m.observeProviderPairMembers(t.Context(), f.core, f.original)
			if err == nil || got.original != nil || f.clockCalls != 0 || string(saved) != string(f.original.rows[0].lease) {
				t.Fatal("replacement adopted or fresh clock read before row refusal", got, err, f.clockCalls)
			}
		})
	}
}
