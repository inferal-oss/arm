package rollout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCheckContextMatchesLegacyWithoutWrites(t *testing.T) {
	f := setup(t)
	f.project("all")
	// Both interfaces use the same retained policy and clock. Reading must not
	// create identities, refresh liveness or rewrite policy/audit bytes.
	path := filepath.Join(f.Home, "rollout.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	q := Query{CWD: f.root, Adapter: "codex"}
	legacy, err := f.Check(q)
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.CheckContext(t.Context(), q)
	if err != nil || !reflect.DeepEqual(legacy, current) {
		t.Fatal(legacy, current, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("check changed policy", err)
	}
}
func TestCheckContextCanceledBeforeStateOrScope(t *testing.T) {
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		want := context.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		_, err := (Store{Home: filepath.Join(t.TempDir(), "absent-state")}).CheckContext(ctx, Query{CWD: "missing-scope"})
		cancel()
		if !errors.Is(err, want) {
			t.Fatal("did not preserve caller cancellation", err)
		}
	}
}
func TestCheckContextUnknownOriginalStartRemainsIneligible(t *testing.T) {
	f := setup(t)
	f.project("new-sessions")
	d, err := f.CheckContext(t.Context(), Query{CWD: f.root, Adapter: "codex"})
	if err != nil || d.Eligible || d.Coordinate || d.Session != nil || d.Why != "new-session eligibility requires a valid, self-reported original session start" {
		t.Fatal(d, err)
	}
}
