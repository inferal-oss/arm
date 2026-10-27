//go:build arm_devtrace

package providerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"getarm.org/internal/providerproto"
)

type capacityRow struct {
	ID             string `json:"id"`
	Registration   string `json:"registration"`
	Phase          string `json:"phase"`
	Initial        []byte `json:"initial"`
	Terminal       []byte `json:"terminal"`
	Remaining      int    `json:"remaining"`
	NativeClosed   bool   `json:"native_closed"`
	TempRemoved    bool   `json:"temp_removed"`
	WorkerReleased bool   `json:"worker_released"`
}
type capacityState struct {
	Phase    [6]string `json:"phase"`
	Terminal [6]int    `json:"terminal"`
}
type capacityObservation struct {
	State capacityState `json:"ledger"`
	Rows  []capacityRow `json:"rows"`
}

// Read actual SQL bytes and current native/manager receipts. This never applies
// a model action or computes a successor from a prior model state.
func capacitySnapshot(t *testing.T, f *fixture, prepared []Prepared, handles map[string]*Handle) capacityObservation {
	t.Helper()
	observed := capacityObservation{Rows: []capacityRow{}}
	for k := range observed.State.Phase {
		observed.State.Phase[k] = "none"
	}
	rows, err := f.journal.db.Query(`SELECT id,registration,phase,initial,terminal,remaining FROM worker_attempts WHERE authority=? ORDER BY id`, f.journal.authority)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row capacityRow
		if err = rows.Scan(&row.ID, &row.Registration, &row.Phase, &row.Initial, &row.Terminal, &row.Remaining); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		observed.Rows = append(observed.Rows, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := range observed.Rows {
		row := &observed.Rows[i]
		key := -1
		for k, p := range prepared {
			if p.attempt == row.ID {
				key = k
				break
			}
		}
		if key < 0 {
			t.Fatal("unexpected actual SQL attempt", row.ID)
		}
		if h := handles[row.ID]; h != nil {
			row.NativeClosed = h.Snapshot().ScopedNativeClosed
		}
		temp, e := f.m.Inspect(prepared[key].held.temp)
		if e != nil {
			t.Fatal(e)
		}
		worker, e := f.m.Inspect(prepared[key].held.worker)
		if e != nil {
			t.Fatal(e)
		}
		row.TempRemoved = temp.State == "terminal" && temp.Outcome == "released" && temp.Artifact != nil && temp.Artifact.Phase == "removed" && temp.Artifact.NamespaceRemoved
		row.WorkerReleased = worker.State == "terminal" && worker.Outcome == "released"
		phase := "live"
		if row.Phase == "unknown" {
			phase = "unknown"
		}
		if row.NativeClosed && row.TempRemoved && row.WorkerReleased {
			phase = "eligible"
		}
		if row.Phase == "settled" {
			phase = "settled"
			observed.State.Terminal[key] = len(row.Terminal)
		}
		observed.State.Phase[key] = phase
	}
	return observed
}

func TestObservedDurableAuthorityCapacityAndFinalization(t *testing.T) {
	f := newFixture(t)
	prepared := make([]Prepared, 6)
	var initialBytes [6]int
	var ids [6]string
	for k := range prepared {
		reg := fmt.Sprintf("registration-%d", k+1)
		if k == 5 {
			reg = "registration-1"
		}
		prepared[k] = f.prepared(t, fmt.Sprintf("capacity-%d", k+1), reg, providerproto.Validate, "")
		raw, err := initialFixtureBytes(prepared[k])
		if err != nil {
			t.Fatal(err)
		}
		initialBytes[k] = len(raw)
		ids[k] = prepared[k].attempt
	}
	handles := map[string]*Handle{}
	observations := []capacityObservation{}
	capture := func() { observations = append(observations, capacitySnapshot(t, f, prepared, handles)) }
	capture()
	refused := 0
	for k := 0; k < 4; k++ {
		if err := f.journal.reserve(prepared[k]); err != nil {
			t.Fatal(err)
		}
		capture()
		if k == 0 {
			before, _ := json.Marshal(capacitySnapshot(t, f, prepared, handles))
			if err := f.journal.reserve(prepared[5]); err == nil {
				t.Fatal("same-registration second key admitted below global cap")
			}
			after, _ := json.Marshal(capacitySnapshot(t, f, prepared, handles))
			if !bytes.Equal(before, after) {
				t.Fatal("per-registration refusal mutated actual SQL")
			}
			refused++
		}
	}
	for _, k := range []int{4, 3} {
		before, _ := json.Marshal(capacitySnapshot(t, f, prepared, handles))
		if err := f.journal.reserve(prepared[k]); err == nil {
			t.Fatal("fifth/per-registration/replayed-key reservation admitted", k)
		}
		after, _ := json.Marshal(capacitySnapshot(t, f, prepared, handles))
		if !bytes.Equal(before, after) {
			t.Fatal("denied reservation changed actual SQL snapshot")
		}
		refused++
	}
	marker, err := f.journal.startMarker(prepared[0], f.owner.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	h, err := f.owner.BeginStart(context.Background(), prepared[0], marker)
	if err != nil {
		t.Fatal(err)
	}
	handles[prepared[0].attempt] = h
	f.grant(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.ProtocolSuccess || !o.ScopedNativeClosed {
		t.Fatal(o, err)
	}
	f.beforeSettlement = func(t *testing.T, _ *Handle) { capture() }
	f.settle(t, h)
	capture()
	f.beforeSettlement = nil
	// An unknown original keeps its authoritative slot/reserve after reopen;
	// this original marker has no native BeginStart and is never replayed.
	if _, err = f.journal.startMarker(prepared[1], f.owner.OwnerToken()); err != nil {
		t.Fatal(err)
	}
	terminal, old, err := f.journal.terminal(prepared[1].attempt)
	if err != nil {
		t.Fatal(err)
	}
	milestone(&terminal, 5, "unknown")
	if err = f.journal.publish(prepared[1].attempt, terminal, old); err != nil {
		t.Fatal(err)
	}
	capture()
	if err = f.journal.reserve(prepared[5]); err != nil {
		t.Fatal("settled registration did not free its slot", err)
	}
	capture()
	if err = f.journal.reserve(prepared[4]); err == nil {
		t.Fatal("unknown duty disappeared from four-slot cap")
	}
	refused++
	evidence := struct {
		Schema           string                `json:"schema"`
		IDs              [6]string             `json:"ids"`
		InitialBytes     [6]int                `json:"initial_bytes"`
		Observations     []capacityObservation `json:"observations"`
		RefusedUnchanged int                   `json:"refused_unchanged"`
	}{"arm.provider-worker-capacity/v1", ids, initialBytes, observations, refused}
	if path := os.Getenv("ARM_PROVIDER_WORKER_CAPACITY_OUT"); path != "" {
		if err = workerFormalWrite(path, evidence); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual durable authority observations=%d refused_unchanged=%d", len(observations), refused)
}
