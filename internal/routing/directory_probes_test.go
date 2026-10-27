package routing

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"getarm.org/internal/workspace"
	"getarm.org/internal/workspacegraph"
)

func TestActualDirectoryProbeSurvivesSparseBundleAndRuntimeRestart(t *testing.T) {
	id, d, b, c := messageFixture(t)
	path := []string{b.identity.Host, c.identity.Host, d.identity.Host}
	accept := func(actualPath []string) {
		q, r := ownGraphReport(t, id, b, d, nil)
		m, g, _ := issueGraphReport(t, d, q, r, nil, nil)
		if len(g.Edges) != 0 {
			t.Fatal("fixture must be sparse")
		}
		if e := b.runtime.AcceptChecked(t.Context(), d.identity, m, func(ctx context.Context, tx *sql.Tx, a workspace.DirectoryAcceptance) (workspace.DirectoryDependent, error) {
			return AcceptGraphDirectoryPath(ctx, tx, a, g, actualPath)
		}); e != nil {
			t.Fatal(e)
		}
	}
	accept(path)
	accept([]string{b.identity.Host, d.identity.Host})
	b.runtime.Close()
	next := workspace.NewRuntime(b.store)
	defer next.Close()
	var hints GraphHints
	_, e := next.RouteTransaction(t.Context(), id, b.identity.Host, d.identity.Host, workspace.RouteDirectory, func(tx *sql.Tx, a workspace.RouteAccess) error {
		var e error
		hints, e = ReadGraphHints(t.Context(), tx, a, true)
		return e
	})
	if e != nil || !hints.ReachabilityOnly || len(hints.Paths) != 0 || len(hints.DirectoryProbes) != 2 {
		t.Fatal("sparse update erased historical probe", hints, e)
	}
	found := false
	for _, p := range hints.DirectoryProbes {
		if reflect.DeepEqual(probePath(p), path) {
			found = true
		}
	}
	if !found {
		t.Fatal("actual prior multihop path lost")
	}
	if _, e = next.RouteAccess(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage); e == nil {
		t.Fatal("historical path activated message membership")
	}
	before := readMessageRecords(t, b)
	// A changed first-hop generation/pin cannot borrow an older saved path.
	if _, e = next.DirectoryNextHopPinned(t.Context(), id, c.identity.Host, 2, c.identity.Fingerprint); e == nil {
		t.Fatal("saved path lent another member generation")
	}
	if !reflect.DeepEqual(before, readMessageRecords(t, b)) {
		t.Fatal("probe read changed ledger")
	}
}

func TestDirectoryProbeChangedMiddlePinGenerationDoesNotBlockFreshWriter(t *testing.T) {
	id, d, b, c := messageFixture(t)
	accept := func(m workspace.Snapshot, g workspacegraph.Snapshot, path []string) error {
		return b.runtime.AcceptChecked(t.Context(), d.identity, m, func(ctx context.Context, tx *sql.Tx, a workspace.DirectoryAcceptance) (workspace.DirectoryDependent, error) {
			return AcceptGraphDirectoryPath(ctx, tx, a, g, path)
		})
	}
	q, r := ownGraphReport(t, id, b, d, nil)
	m, g, _ := issueGraphReport(t, d, q, r, nil, nil)
	if e := accept(m, g, []string{b.identity.Host, c.identity.Host, d.identity.Host}); e != nil {
		t.Fatal(e)
	}
	q, r = ownGraphReport(t, id, b, d, nil)
	m, g, _ = issueGraphReport(t, d, q, r, nil, nil)
	// An authenticated directory may report changed remote lineage. Runtime
	// already records this as identity_change_pending and refuses promotion. The
	// actual local writer must still accept the complete view while discarding an
	// unusable old reachability hint; no database corruption/insertion is used.
	m.Revision++
	for i := range m.Members {
		if m.Members[i].Identity.Host == c.identity.Host {
			m.Members[i].MembershipGeneration++
		}
	}
	m.Hash = ""
	m.Hash = hash(m)
	next, e := workspacegraph.Build(GraphScope(m, g.Revision), nil, "12345678123456781234567812345678", 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = accept(m, next, []string{b.identity.Host, d.identity.Host}); e != nil {
		t.Fatal("historical middle generation blocked legitimate complete refresh", e)
	}
	v := readMessageRecords(t, b)
	if len(v.Graph.Observations[0].DirectoryProbes) != 1 || !reflect.DeepEqual(probePath(v.Graph.Observations[0].DirectoryProbes[0]), []string{b.identity.Host, d.identity.Host}) {
		t.Fatal("changed historical hint survived", v.Graph.Observations[0].DirectoryProbes)
	}
	records, e := b.store.Read(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	pending := false
	for _, member := range records.Trusted {
		if member.Identity.Host == c.identity.Host && member.State == "identity_change_pending" {
			pending = true
		}
	}
	if !pending {
		t.Fatal("new directory lineage silently promoted")
	}
	if _, e = b.runtime.RouteAccess(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage); e == nil {
		t.Fatal("changed middle generation gained message authority")
	}
}
