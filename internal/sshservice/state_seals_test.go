package sshservice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func sourceSeal(t *testing.T, r SourceIntent) model.SSHKeyFence {
	t.Helper()
	c, e := r.ControlCommitment()
	if e != nil {
		t.Fatal(e)
	}
	digest, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return model.SSHKeyFence{Schema: 1, Source: r.Source, Target: c.Target, Key: c.Key, State: "sealed", Control: &c, ControlSHA256: digest, Actor: "peer:" + r.Source, Reason: "Explicitly prevent this original invocation", At: time.Now().Add(5 * time.Minute).UTC()}
}
func TestSourceSealRequiresExactDurableNonexecutionReceipt(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "lost-before-exec")
	if e := ReserveSource(t.Context(), home, b, r); e != nil {
		t.Fatal(e)
	}
	seal := sourceSeal(t, r)
	wrong := seal
	c := *seal.Control
	c.CommandSHA256 = strings.Repeat("1", 64)
	wrong.Control = &c
	wrong.ControlSHA256, _ = c.Digest()
	if e := MatchSourceSeal(r, wrong); e == nil {
		t.Fatal("different original control settled source")
	}
	if e := SettleSourceSeal(t.Context(), home, b, r, seal); e == nil {
		t.Fatal("unremoved local artifacts settled")
	}
	if e := os.Remove(r.Directory.Path); e != nil {
		t.Fatal(e)
	}
	if e := SettleSourceSeal(t.Context(), home, b, r, seal); e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(t.Context(), home, b)
	if e != nil || len(s.Intents) != 0 || len(s.Seals) != 1 || len(s.Completions) != 0 {
		t.Fatal("exact seal not durable", e)
	}
	if e = ReserveSource(t.Context(), home, b, r); e == nil {
		t.Fatal("sealed source key reused")
	}
	if e = SettleSourceSeal(t.Context(), home, b, r, seal); e != nil {
		t.Fatal("exact continuation failed", e)
	}
	if e = SettleSource(t.Context(), home, b, r, terminalReceipt(r)); e == nil {
		t.Fatal("nonexecution converted to admitted completion")
	}
	// A local settlement timestamp is independent from destination seal time.
	if e = s.Seals[0].Validate(); e != nil {
		t.Fatal("cross-authority clock ordering required", e)
	}
}
func TestSourceSealInterruptedPairCountsOnceAndPreservesExactReceipt(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "interrupted-seal-settlement")
	if e := ReserveSource(t.Context(), home, b, r); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(r.Directory.Path); e != nil {
		t.Fatal(e)
	}
	seal := sourceSeal(t, r)
	record := SourceSealCompletion{1, r, seal, time.Now().UTC()}
	raw, e := json.Marshal(record)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, "mesh", sealCompletionName(r))
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = SettleSourceSeal(context.Background(), home, b, r, seal); e != nil {
		t.Fatal(e)
	}
	retained, e := os.ReadFile(path)
	if e != nil || string(retained) != string(raw) {
		t.Fatal("continuation rewrote immutable exact seal receipt", e)
	}
	state, e := ReadState(t.Context(), home, b)
	if e != nil || len(state.Intents) != 0 || len(state.Seals) != 1 {
		t.Fatal("interrupted pair did not finish", e)
	}
}
