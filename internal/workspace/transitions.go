package workspace

import "fmt"

func memberImmutable(a, b Member) bool {
	a.State = ""
	b.State = ""
	a.Revision = 0
	b.Revision = 0
	a.Updated = a.Created
	b.Updated = b.Created
	a.RevokedAt = nil
	b.RevokedAt = nil
	return hash(a) == hash(b)
}
func rootImmutable(a, b Instance) bool {
	a.State = ""
	b.State = ""
	a.Revision = 0
	b.Revision = 0
	a.Updated = a.Created
	b.Updated = b.Created
	a.SelfGeneration = 0
	b.SelfGeneration = 0
	a.ConsentHash = ""
	b.ConsentHash = ""
	return hash(a) == hash(b)
}
func trustedImmutable(a, b TrustedMember) bool {
	a.LatestSeen = 0
	b.LatestSeen = 0
	return hash(a) == hash(b)
}

// Enforce complete journal->member/pin/deny relationships. A current row alone
// cannot manufacture introducer rights or make an unfinished admission settled.
func validateTransitions(v Records, a Authority) error {
	for _, root := range v.Instances {
		members := map[string]Member{}
		trusted := map[string]TrustedMember{}
		creators := 0
		for _, o := range v.Operations {
			if o.Workspace != root.ID {
				continue
			}
			if known(o.Kind, "create", "join") {
				creators++
				if o.Principal != nativeKey(a) || o.Review == nil || o.Review.Source != root.Self || o.Review.Directory != root.Directory || o.Review.Policy != root.Policy || o.Review.PolicyRevision != root.PolicyRevision || o.Review.Name != root.Name || o.Review.Generation != root.Generation || root.Role == "directory" && o.Kind != "create" || root.Role == "member" && o.Kind != "join" {
					return fmt.Errorf("workspace creator/review differs from immutable root")
				}
				if root.State != "prepared" && root.ConsentHash != o.ReviewHash {
					return fmt.Errorf("root consent differs from exact review")
				}
			}
			if o.Kind != "admit" && o.Principal != nativeKey(a) {
				return fmt.Errorf("native mutation principal differs")
			}
			if o.Kind == "admit" && o.Phase == "completed" && (o.Result == nil || o.Result.State != "active") {
				return fmt.Errorf("completed admission lacks exact active result")
			}
		}
		if creators != 1 {
			return fmt.Errorf("workspace lacks exactly one immutable creation/join intent")
		}
		var first *Instance
		for _, c := range v.Changes {
			if c.Workspace != root.ID {
				continue
			}
			if first == nil {
				if !known(c.Kind, "create_prepared", "join_prepared") || c.After.State != "prepared" {
					return fmt.Errorf("workspace journal has no prepared origin")
				}
				copy := c.After
				first = &copy
			}
			if !rootImmutable(*first, c.After) {
				return fmt.Errorf("workspace immutable binding/policy changed without reviewed schema support")
			}
			if c.Before != nil {
				from, to := c.Before.State, c.After.State
				if from != to && !(from == "prepared" && known(to, "active", "joining", "retired") || from == "joining" && known(to, "active", "needs_attention") || known(from, "active", "needs_attention") && to == "retired") {
					return fmt.Errorf("invalid workspace lifecycle transition")
				}
			}
			if c.MemberAfter != nil {
				m := *c.MemberAfter
				if e := validateMember(m); e != nil {
					return e
				}
				old, exists := members[m.Identity.Host]
				if exists {
					if c.MemberBefore == nil || hash(old) != hash(c.MemberBefore) || !memberImmutable(old, m) || m.Revision != old.Revision+1 || !(old.State == "pending" && known(m.State, "active", "revoked") || old.State == "active" && m.State == "revoked") {
						return fmt.Errorf("member transition lacks exact before/immutable identity")
					}
				} else if c.MemberBefore != nil || m.Revision != 1 || !known(c.Kind, "decision", "admission_prepared") {
					return fmt.Errorf("member origin missing")
				}
				members[m.Identity.Host] = m
				_, wasTrusted := trusted[m.Identity.Host]
				if m.State == "active" || m.State == "revoked" && wasTrusted {
					t := TrustedMember{Schema: Schema, Workspace: root.ID, Generation: root.Generation, Identity: m.Identity, MembershipGeneration: m.MembershipGeneration, State: m.State, FirstSeen: c.Revision, LatestSeen: c.Revision}
					if prior, ok := trusted[m.Identity.Host]; ok {
						t.FirstSeen = prior.FirstSeen
					}
					trusted[m.Identity.Host] = t
				}
			} else if c.MemberBefore != nil {
				return fmt.Errorf("member before without after")
			}
			for _, m := range c.Members {
				if c.Kind != "retire" {
					return fmt.Errorf("bulk member settlement outside retirement")
				}
				old, ok := members[m.Identity.Host]
				if !ok || old.State == "revoked" || !memberImmutable(old, m) || m.State != "revoked" || m.Revision != old.Revision+1 || !m.Updated.Equal(c.At) {
					return fmt.Errorf("retirement changed member identity/settlement")
				}
				if e := validateMember(m); e != nil {
					return e
				}
				members[m.Identity.Host] = m
			}
			for _, t := range c.Trusted {
				if !known(c.Kind, "observation", "retire") {
					return fmt.Errorf("trusted pin change outside supported transition")
				}
				if e := validateTrusted(t); e != nil {
					return e
				}
				if t.Workspace != root.ID || t.Generation != root.Generation {
					return fmt.Errorf("trusted journal scope mismatch")
				}
				if old, ok := trusted[t.Identity.Host]; ok && (t.Identity != old.Identity || t.MembershipGeneration != old.MembershipGeneration || old.State != "active" && t.State == "active" || t.FirstSeen != old.FirstSeen) {
					return fmt.Errorf("trusted pin silently replaced/revived")
				}
				trusted[t.Identity.Host] = t
			}
		}
		count := 0
		for _, m := range v.Members {
			if m.Workspace != root.ID {
				continue
			}
			count++
			historical, ok := members[m.Identity.Host]
			if !ok || hash(historical) != hash(m) {
				return fmt.Errorf("member row differs from complete transition history")
			}
			opPrincipal := principalKey(m.IntroducedBy)
			if m.Identity.Host == root.Self.Host {
				opPrincipal = nativeKey(a)
			}
			op, ok := operationOf(&v, root.ID, opPrincipal, m.AdmissionKey)
			if !ok {
				return fmt.Errorf("member operation missing")
			}
			if op.Kind == "admit" {
				var q AdmissionRequest
				if e := Decode(op.Request, &q); e != nil {
					return e
				}
				if q.Candidate != m.Identity || q.ConsentHash != m.ConsentHash || q.Introducer != m.Introducer || q.HostInbox != m.HostInbox || q.Reason != m.Reason {
					return fmt.Errorf("member permissions/consent differ from admitted request")
				}
			}
			if op.Result != nil && !memberImmutable(*op.Result, m) {
				return fmt.Errorf("member immutable policy differs from committed admission")
			}
			if m.State == "active" && (op.Result == nil || hash(*op.Result) != hash(m)) {
				return fmt.Errorf("active member differs from exact committed result")
			}
			if root.State == "retired" && m.State != "revoked" {
				return fmt.Errorf("retired directory retains active member")
			}
		}
		if count != len(members) {
			return fmt.Errorf("member history was removed")
		}
		count = 0
		for _, t := range v.Trusted {
			if t.Workspace != root.ID {
				continue
			}
			count++
			before, ok := trusted[t.Identity.Host]
			if !ok || !trustedImmutable(before, t) {
				return fmt.Errorf("trusted pin differs from audited continuity")
			}
			if root.Role == "directory" {
				if t.LatestSeen != before.LatestSeen {
					return fmt.Errorf("directory trusted revision mismatch")
				}
			} else {
				var observation *Observation
				for _, o := range v.Observations {
					if o.Workspace == root.ID {
						copy := o
						observation = &copy
					}
				}
				if observation == nil || t.LatestSeen != observation.Revision {
					return fmt.Errorf("trusted view lacks latest observation")
				}
				card, ok := cardOf(observation.Snapshot, t.Identity.Host)
				if !ok || t.State == "active" && (card.State != "active" || card.Identity != t.Identity || card.MembershipGeneration != t.MembershipGeneration) {
					return fmt.Errorf("active trusted pin differs from complete cached view")
				}
			}
		}
		if count != len(trusted) {
			return fmt.Errorf("trusted pin evidence removed")
		}
		for _, op := range v.Operations {
			if op.Workspace != root.ID || op.Kind != "deny" || op.Phase != "completed" {
				continue
			}
			var q MutationRequest
			if e := Decode(op.Request, &q); e != nil {
				return e
			}
			found := false
			for _, d := range v.Denies {
				if d.Workspace == root.ID && d.Host == q.Host && d.Key == q.Key && d.Reason == q.Reason && d.Revision == q.Revision+1 {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("completed local deny tombstone removed/altered")
			}
		}
	}
	return nil
}
