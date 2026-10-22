package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func activePair(t *testing.T) (fixture, fixture, Instance, *Runtime) {
	t.Helper()
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, _, admission := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, m, admission)
	runtime := refreshAndFinalize(t, d, m, root)
	return d, m, root, runtime
}
func TestRoutineIssuanceIsBoundedSeparateEvidenceAndFloorPrecedesReply(t *testing.T) {
	d, _, root, _ := activePair(t)
	before, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	clock, _ := clockOf(&before, root.ID)
	startCount := clock.IssuedCount
	for i := 0; i < 40; i++ {
		d.clock.advance(time.Millisecond, time.Millisecond)
		if _, e = d.s.Issue(d.ctx, RefreshRequest{rootPrincipal(root), hash(i)}); e != nil {
			t.Fatal(e)
		}
	}
	after, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	clock, _ = clockOf(&after, root.ID)
	if len(after.Changes) != len(before.Changes) || len(after.Operations) != len(before.Operations) || clock.IssuedCount != startCount+40 || clock.LastIssued == nil || clock.WallFloor.Before(*clock.LastIssued) {
		t.Fatal("routine issuance exhausted policy audit or lost floor", clock)
	}
	d.s.fault = func(stage string) error {
		if stage == "issuance_floor" {
			return errors.New("disk unavailable")
		}
		return nil
	}
	snap, e := d.s.Issue(d.ctx, RefreshRequest{rootPrincipal(root), hash("failing")})
	if e == nil || snap.Hash != "" {
		t.Fatal("response escaped failed durable floor", snap, e)
	}
	d.s.fault = nil
	after, e = d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := clockOf(&after, root.ID)
	if got.IssuedCount != clock.IssuedCount {
		t.Fatal("failed issuance advanced durable evidence")
	}
}
func TestRequestStartDeadlineCannotBeExtendedByNetworkPagesOrRestart(t *testing.T) {
	d, m, root, r := activePair(t)
	q, e := r.Begin(m.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	d.clock.advance(40*time.Second, 40*time.Second)
	m.clock.advance(40*time.Second, 40*time.Second)
	if e = r.Accept(m.ctx, d.self, snap); e != nil {
		t.Fatal(e)
	}
	access, e := r.Check(m.ctx, root.ID, d.self.Host)
	if e != nil || access.Remaining > 20*time.Second || access.StreamTTL > 20*time.Second {
		t.Fatal("receipt renewed request-start clock", access, e)
	}
	reopened, e := Open(m.ctx, m.db, m.authority, m.self, m.clock)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewRuntime(reopened).Check(m.ctx, root.ID, d.self.Host); code(e) != "membership_unknown" {
		t.Fatal("restart made persisted cache authoritative", e)
	}
	m.clock.advance(21*time.Second, 21*time.Second)
	if _, e = r.Check(m.ctx, root.ID, d.self.Host); code(e) != "membership_stale" {
		t.Fatal("lease outlived original window", e)
	}
	if e = r.Accept(m.ctx, d.self, snap); code(e) != "stale_refresh" {
		t.Fatal("nonce replay refreshed window", e)
	}
}
func TestWallStepsSleepAndRollbackInvalidateRatherThanRefresh(t *testing.T) {
	for _, kind := range []string{"rollback", "forward", "sleep"} {
		t.Run(kind, func(t *testing.T) {
			d, m, root, r := activePair(t)
			switch kind {
			case "rollback":
				m.clock.advance(-time.Second, 0)
			case "forward":
				m.clock.advance(10*time.Second, 0)
			case "sleep":
				m.clock.advance(70*time.Second, time.Second)
			}
			if _, e := r.Check(m.ctx, root.ID, d.self.Host); code(e) != "workspace_clock_unavailable" {
				t.Fatal("unsafe clock retained authority", e)
			}
			if _, e := m.s.Read(m.ctx); e != nil {
				t.Fatal("clock invalidation destroyed inspection", e)
			}
		})
	}
}

func TestPersistedIssuanceFloorSurvivesDirectoryRestart(t *testing.T) {
	d, _, root, _ := activePair(t)
	d.clock.advance(2*time.Second, 2*time.Second)
	if _, e := d.s.Issue(d.ctx, RefreshRequest{rootPrincipal(root), hash("last-fresh-window")}); e != nil {
		t.Fatal(e)
	}
	d.clock.advance(-time.Second, 0)
	restarted, e := Open(d.ctx, d.db, d.authority, d.self, d.clock)
	if e != nil {
		t.Fatal("rollback destroyed historical inspection", e)
	}
	if _, e = restarted.Issue(d.ctx, RefreshRequest{rootPrincipal(root), hash("rollback-after-restart")}); code(e) != "workspace_clock_unavailable" {
		t.Fatal("boot reset discarded durable pre-issuance floor", e)
	}
	if _, e = restarted.Read(d.ctx); e != nil {
		t.Fatal("rollback made historical ledger unreadable", e)
	}
}
func TestPagedSnapshotsAreCompleteBoundedAndExcludePrivateAdmission(t *testing.T) {
	d, m, root, r := activePair(t)
	q, e := r.Begin(m.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(snap)
	var public any
	if e = json.Unmarshal(raw, &public); e != nil {
		t.Fatal(e)
	}
	if forbidden := privateAdmissionField(public); forbidden != "" {
		t.Fatal("directory leaked internal admission data", forbidden)
	}
	single := snap
	single.Members = single.Members[:1]
	one, _ := json.Marshal(Page{Snapshot: single, Offset: 0, Total: len(snap.Members)})
	pageLimit := len(one) + 32
	pages, e := Pages(snap, pageLimit)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range pages {
		raw, _ := json.Marshal(p)
		if len(raw) > pageLimit {
			t.Fatal("page exceeded complete-envelope size", len(raw))
		}
	}
	assembled, e := Assemble(pages)
	if e != nil || assembled.Hash != snap.Hash {
		t.Fatal(assembled, e)
	}
	if len(pages) < 2 {
		t.Fatal("fixture did not require pagination")
	}
	partial := pages[:len(pages)-1]
	if _, e = Assemble(partial); e == nil {
		t.Fatal("incomplete pages became authoritative")
	}
	bad := append([]Page{}, pages...)
	bad[1].Snapshot.Revision++
	if _, e = Assemble(bad); code(e) != "changed_revision" {
		t.Fatal("mixed revision pages accepted", e)
	}
	if e = r.Accept(m.ctx, d.self, assembled); e != nil {
		t.Fatal(e)
	}
}

// Inspect JSON field names rather than certificate/name bytes: random base64
// can legitimately contain "uid" without exposing the private authority UID.
func privateAdmissionField(value any) string {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			for _, forbidden := range []string{"consent_sha256", "admission_key", "proof_nonce", "local_authority", "uid", "reason"} {
				if strings.Contains(key, forbidden) {
					return key
				}
			}
			if forbidden := privateAdmissionField(child); forbidden != "" {
				return forbidden
			}
		}
	case []any:
		for _, child := range v {
			if forbidden := privateAdmissionField(child); forbidden != "" {
				return forbidden
			}
		}
	case string:
		if strings.Contains(v, "reviewed introducer") {
			return "private introducer reason"
		}
	}
	return ""
}

func TestSnapshotPrivacyCheckUsesFieldsInsteadOfOpaqueCertificateBytes(t *testing.T) {
	public := map[string]any{"certificate": "-----BEGIN CERTIFICATE-----\nuidReasonUID\n-----END CERTIFICATE-----", "name": "uid reason admission_key"}
	if got := privateAdmissionField(public); got != "" {
		t.Fatal("opaque public text mistaken for private fields", got)
	}
	for _, field := range []string{"uid", "owner_uid", "consent_sha256", "admission_key", "proof_nonce", "local_authority", "reason"} {
		nested := map[string]any{"members": []any{map[string]any{"nested": map[string]any{field: "private"}}}}
		if got := privateAdmissionField(nested); got == "" {
			t.Fatal("nested private field accepted", field)
		}
	}
	if got := privateAdmissionField(map[string]any{"name": "reviewed introducer"}); got == "" {
		t.Fatal("private introducer reason accepted")
	}
}
func TestLocalDenyImmediatelyOverridesFreshCachedMembership(t *testing.T) {
	d, m, root, r := activePair(t)
	v, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.s.Deny(m.ctx, root.ID, MutationRequest{Key: "deny-directory", Host: d.self.Host, Revision: v.Instances[0].Revision, Reason: "local immediate block"}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Check(m.ctx, root.ID, d.self.Host); code(e) != "forbidden" {
		t.Fatal("cached scope overrode deny", e)
	}
}

func TestRevokedTombstoneDoesNotConsumeActiveSnapshotCapacity(t *testing.T) {
	d := newStoreFixture(t)
	policy := DefaultPolicy()
	policy.MaxMembers = 2
	prepared, e := d.s.PrepareCreate(d.ctx, CreateRequest{Key: "create-two", Name: "two active members", Policy: policy, Reason: "separate active capacity from retained tombstones"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.s.Decide(d.ctx, prepared.Workspace, prepared.Key, prepared.Revision, prepared.ReviewHash, true); e != nil {
		t.Fatal(e)
	}
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	b, _, admissionB := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, b, admissionB)
	refreshAndFinalize(t, d, b, root)
	v, e = d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.s.Revoke(d.ctx, root.ID, MutationRequest{Key: "revoke-b", Host: b.self.Host, Revision: v.Instances[0].Revision, Reason: "retain B tombstone while allowing a replacement member"}); e != nil {
		t.Fatal(e)
	}
	c, _, admissionC := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, c, admissionC)
	r := refreshAndFinalize(t, d, c, root)
	if _, e = r.Check(c.ctx, root.ID, d.self.Host); e != nil {
		t.Fatal("replacement member could not authorize fresh directory", e)
	}
	if _, e = r.Check(c.ctx, root.ID, b.self.Host); code(e) != "identity_changed" {
		t.Fatal("retained revoked member regained authority", e)
	}
	q, e := r.Begin(c.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if len(snapshot.Members) != 3 {
		t.Fatal("actual directory writer omitted revoked tombstone", snapshot.Members)
	}
	if e = validateSnapshot(snapshot); e != nil {
		t.Fatal("complete roster incorrectly bounded by active policy limit", e)
	}
	for _, kind := range []string{"over-active", "over-hard-roster", "pending"} {
		t.Run(kind, func(t *testing.T) {
			bad := snapshot
			bad.Members = append([]MemberCard{}, snapshot.Members...)
			switch kind {
			case "over-active", "pending":
				for i := range bad.Members {
					if bad.Members[i].Identity.Host == b.self.Host {
						bad.Members[i].State = "active"
						if kind == "pending" {
							bad.Members[i].State = "pending"
						}
					}
				}
			case "over-hard-roster":
				for len(bad.Members) <= MaxMembers {
					bad.Members = append(bad.Members, MemberCard{Identity: identityFixture(t, d.clock.Wall()), MembershipGeneration: 1, State: "revoked", Revision: 1})
				}
			}
			bad.Hash = snapshotHash(bad)
			if e := validateSnapshot(bad); e == nil {
				t.Fatal("invalid active/pending/retained roster accepted", kind)
			}
		})
	}
}
