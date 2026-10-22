package workspace

import (
	"context"
	"database/sql"
	"fmt"
)

func readRecords(ctx context.Context, tx *sql.Tx, a Authority) (Records, error) {
	v := emptyRecords()
	var host string
	if e := tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='host'`).Scan(&host); e != nil {
		return v, e
	}
	if host != a.Host {
		return v, invalidf("workspace store differs from central ledger authority")
	}
	ok, e := schema(ctx, tx)
	if e != nil {
		return v, e
	}
	if !ok {
		return v, nil
	}
	v.Initialized = true
	total := 0
	read := func(table string, maxRows int, columns string, fn func([]string, []byte) error) error {
		suffix := ""
		if table == "workspace_changes" {
			suffix = " ORDER BY sequence"
		}
		rows, e := tx.QueryContext(ctx, fmt.Sprintf(`SELECT %s,substr(CAST(data AS BLOB),1,%d) FROM %s%s LIMIT %d`, columns, MaxRecordBytes+1, table, suffix, maxRows+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			n++
			if n > maxRows {
				return invalidf("workspace %s row capacity exceeded", table)
			}
			names, e := rows.Columns()
			if e != nil {
				return e
			}
			keys := make([]string, len(names)-1)
			dest := make([]any, len(names))
			for i := range keys {
				dest[i] = &keys[i]
			}
			var raw []byte
			dest[len(dest)-1] = &raw
			if e = rows.Scan(dest...); e != nil {
				return e
			}
			total += len(raw)
			if total > MaxLedgerBytes {
				return invalidf("workspace ledger exceeds complete-read budget")
			}
			if e = fn(keys, raw); e != nil {
				return invalidf("%s: %w", table, e)
			}
		}
		return rows.Err()
	}
	if e = read(tables[0], MaxWorkspaces, `substr(id,1,33)`, func(k []string, b []byte) error {
		var x Instance
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.ID != k[0] {
			return fmt.Errorf("instance key mismatch")
		}
		v.Instances = append(v.Instances, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(tables[1], MaxWorkspaces*MaxMembers, `substr(workspace,1,33),substr(host,1,33)`, func(k []string, b []byte) error {
		var x Member
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.Workspace != k[0] || x.Identity.Host != k[1] {
			return fmt.Errorf("member key mismatch")
		}
		v.Members = append(v.Members, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(tables[2], MaxWorkspaces*MaxMembers, `substr(workspace,1,33),substr(host,1,33)`, func(k []string, b []byte) error {
		var x TrustedMember
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.Workspace != k[0] || x.Identity.Host != k[1] {
			return fmt.Errorf("trusted key mismatch")
		}
		v.Trusted = append(v.Trusted, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(tables[3], MaxWorkspaces, `substr(workspace,1,33)`, func(k []string, b []byte) error {
		var x Observation
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.Workspace != k[0] {
			return fmt.Errorf("observation key mismatch")
		}
		v.Observations = append(v.Observations, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(tables[4], MaxWorkspaces*MaxOperations, `substr(workspace,1,33),substr(principal,1,513),substr(key,1,129),substr(CAST(request AS BLOB),1,1048577)`, func(k []string, b []byte) error {
		var x Operation
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.Workspace != k[0] || x.Principal != k[1] || x.Key != k[2] || string(x.Request) != k[3] {
			return fmt.Errorf("operation key/request mismatch")
		}
		v.Operations = append(v.Operations, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(tables[5], MaxWorkspaces*MaxChanges, `CAST(sequence AS TEXT),substr(workspace,1,33)`, func(k []string, b []byte) error {
		var x Change
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.Workspace != k[1] {
			return fmt.Errorf("journal workspace mismatch")
		}
		v.Changes = append(v.Changes, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(tables[6], MaxWorkspaces*MaxMembers, `substr(workspace,1,33),substr(host,1,33)`, func(k []string, b []byte) error {
		var x Deny
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.Workspace != k[0] || x.Host != k[1] {
			return fmt.Errorf("deny key mismatch")
		}
		v.Denies = append(v.Denies, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(tables[7], MaxWorkspaces, `substr(workspace,1,33)`, func(k []string, b []byte) error {
		var x ClockRecord
		if e := Decode(b, &x); e != nil {
			return e
		}
		if x.Workspace != k[0] {
			return fmt.Errorf("clock key mismatch")
		}
		v.Clocks = append(v.Clocks, x)
		return nil
	}); e != nil {
		return v, e
	}
	if e := ValidateRecords(v, a); e != nil {
		return v, invalidState(e)
	}
	return v, nil
}

// ValidateRecords is pure and time-neutral. Destructive reset proof must still
// independently check canonical raw evidence/schema and settlement; this helper
// alone does not authorize removal or remote retirement.
func ValidateRecords(v Records, a Authority) error {
	if !validAuthority(a) {
		return fmt.Errorf("invalid expected workspace authority")
	}
	if !v.Initialized {
		if len(v.Instances)+len(v.Members)+len(v.Trusted)+len(v.Observations)+len(v.Operations)+len(v.Changes)+len(v.Denies)+len(v.Clocks) != 0 {
			return fmt.Errorf("workspace records without schema")
		}
		return nil
	}
	roots := map[string]Instance{}
	for _, x := range v.Instances {
		if e := validateInstance(x, a); e != nil {
			return e
		}
		if _, ok := roots[x.ID]; ok {
			return fmt.Errorf("duplicate workspace")
		}
		roots[x.ID] = x
	}
	if len(roots) == 0 {
		return fmt.Errorf("initialized workspace group lacks retained origin")
	}
	if len(roots) > MaxWorkspaces {
		return fmt.Errorf("workspace count exceeded")
	}
	for _, r := range roots {
		c, e := clockOf(&v, r.ID)
		if e != nil {
			return e
		}
		if c.Generation != r.Generation || c.LastRevision > r.Revision {
			return fmt.Errorf("workspace clock binding mismatch")
		}
	}
	seen := map[string]bool{}
	for _, x := range v.Clocks {
		if e := validateClock(x); e != nil {
			return e
		}
		if _, ok := roots[x.Workspace]; !ok || seen[x.Workspace] {
			return fmt.Errorf("orphan/duplicate clock")
		}
		seen[x.Workspace] = true
	}
	seen = map[string]bool{}
	for _, x := range v.Operations {
		r, ok := roots[x.Workspace]
		if !ok {
			return fmt.Errorf("orphan operation")
		}
		if e := validateOperation(x); e != nil {
			return e
		}
		k := x.Workspace + "/" + x.Principal + "/" + x.Key
		if seen[k] {
			return fmt.Errorf("duplicate operation")
		}
		seen[k] = true
		if x.Review != nil && x.Review.Generation != r.Generation {
			return fmt.Errorf("operation generation mismatch")
		}
	}
	seen = map[string]bool{}
	for _, x := range v.Members {
		r, ok := roots[x.Workspace]
		if !ok || r.Role != "directory" || x.Generation != r.Generation {
			return fmt.Errorf("member authority mismatch")
		}
		if e := validateMember(x); e != nil {
			return e
		}
		k := x.Workspace + "/" + x.Identity.Host
		if seen[k] {
			return fmt.Errorf("duplicate member")
		}
		seen[k] = true
		intro, ok := memberOf(&v, r.ID, x.IntroducedBy.Host)
		if !ok || intro.MembershipGeneration != x.IntroducedBy.MembershipGeneration || intro.Identity.Fingerprint != x.IntroducedBy.Fingerprint {
			return fmt.Errorf("member introduction lineage missing")
		}
		principal := principalKey(x.IntroducedBy)
		if x.Identity.Host == r.Self.Host {
			principal = nativeKey(a)
		}
		op, ok := operationOf(&v, r.ID, principal, x.AdmissionKey)
		if !ok || !known(op.Kind, "create", "admit") {
			return fmt.Errorf("member admission missing")
		}
		if x.State == "active" && (op.Phase != "completed" || op.Result == nil || op.Result.Identity != x.Identity) {
			return fmt.Errorf("active member lacks committed admission")
		}
		if x.State == "pending" && terminal(op.Phase) {
			return fmt.Errorf("pending member with terminal operation")
		}
	}
	seen = map[string]bool{}
	for _, x := range v.Trusted {
		r, ok := roots[x.Workspace]
		if !ok || x.Generation != r.Generation || x.LatestSeen > r.Revision && r.Role == "directory" {
			return fmt.Errorf("trusted-member scope mismatch")
		}
		if e := validateTrusted(x); e != nil {
			return e
		}
		k := x.Workspace + "/" + x.Identity.Host
		if seen[k] {
			return fmt.Errorf("duplicate trusted pin")
		}
		seen[k] = true
		if r.Role == "directory" {
			m, ok := memberOf(&v, r.ID, x.Identity.Host)
			if !ok || m.Identity != x.Identity || m.MembershipGeneration != x.MembershipGeneration {
				return fmt.Errorf("trusted pin lacks admitted member")
			}
		}
	}
	seen = map[string]bool{}
	for _, x := range v.Denies {
		if e := validateDeny(x, a); e != nil {
			return e
		}
		r, ok := roots[x.Workspace]
		if !ok || x.Revision > r.Revision {
			return fmt.Errorf("deny scope mismatch")
		}
		k := x.Workspace + "/" + x.Host
		if seen[k] {
			return fmt.Errorf("duplicate deny")
		}
		seen[k] = true
		op, ok := operationOf(&v, r.ID, nativeKey(a), x.Key)
		if !ok || op.Kind != "deny" || op.Phase != "completed" {
			return fmt.Errorf("deny mutation evidence missing")
		}
	}
	observationSeen := map[string]bool{}
	for _, x := range v.Observations {
		r, ok := roots[x.Workspace]
		if !ok || r.Role != "member" || x.Schema != Schema || x.Generation != r.Generation || x.DirectoryPin != r.Directory.Fingerprint || x.Received.IsZero() || x.Revision != x.Snapshot.Revision || x.SnapshotHash != x.Snapshot.Hash || observationSeen[x.Workspace] {
			return fmt.Errorf("invalid cached workspace observation")
		}
		if e := validateSnapshot(x.Snapshot); e != nil {
			return e
		}
		if x.Snapshot.Workspace != r.ID || x.Snapshot.Generation != r.Generation || x.Snapshot.Directory != r.Directory || x.Snapshot.Policy != r.Policy || x.Snapshot.PolicyRevision != r.PolicyRevision || x.Snapshot.Requester != r.Self.Host {
			return fmt.Errorf("cached snapshot binding mismatch")
		}
		observationSeen[x.Workspace] = true
	}
	for id, r := range roots {
		members, ops, changes := counts(&v, id)
		if members > r.Policy.MaxMembers || ops > MaxOperations || changes > MaxChanges {
			return fmt.Errorf("workspace record capacity exceeded")
		}
		var previous *Instance
		expected := int64(1)
		for _, c := range v.Changes {
			if c.Workspace != id {
				continue
			}
			if c.Schema != Schema || c.Revision != expected || c.After.ID != id || c.After.Revision != expected || c.After.Generation != r.Generation || c.At.IsZero() || !validKey(c.Key) || !text(c.Actor, 512) || !known(c.Kind, "create_prepared", "join_prepared", "decision", "admission_prepared", "admission_proving", "admission_completed", "admission_cancelled", "deny", "revoke", "retire", "join_proved", "join_finalized", "observation") {
				return fmt.Errorf("invalid workspace journal transition")
			}
			if e := validateInstance(c.After, a); e != nil {
				return e
			}
			if previous == nil && c.Before != nil || previous != nil && (c.Before == nil || hash(previous) != hash(c.Before)) {
				return fmt.Errorf("workspace journal chain mismatch")
			}
			if c.At.Before(c.After.Created) || !c.At.Equal(c.After.Updated) {
				return fmt.Errorf("workspace journal time mismatch")
			}
			after := c.After
			previous = &after
			expected++
		}
		if previous == nil || hash(previous) != hash(r) {
			return fmt.Errorf("workspace root differs from complete journal")
		}
		if r.State == "active" && r.Role == "directory" {
			m, ok := memberOf(&v, id, r.Self.Host)
			if !ok || m.State != "active" || m.Identity != r.Self {
				return fmt.Errorf("directory self admission missing")
			}
		}
		if r.State == "retired" {
			for _, o := range v.Operations {
				if o.Workspace == id && !terminal(o.Phase) {
					return fmt.Errorf("retired workspace has unfinished operation")
				}
			}
		}
	}
	return validateTransitions(v, a)
}
