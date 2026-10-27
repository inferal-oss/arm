package database

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"getarm.org/internal/workspace"
)

// Optional group copied independently, never trusted dynamically from a writer.
var workspaceResetSchema = []string{
	`CREATE TABLE workspace_instances(id TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE workspace_directory_members(workspace TEXT NOT NULL,host TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,host))`,
	`CREATE TABLE workspace_trusted_members(workspace TEXT NOT NULL,host TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,host))`,
	`CREATE TABLE workspace_observations(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE workspace_operations(workspace TEXT NOT NULL,principal TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,principal,key))`,
	`CREATE TABLE workspace_changes(sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace TEXT NOT NULL,data TEXT NOT NULL)`,
	`CREATE TABLE workspace_denies(workspace TEXT NOT NULL,host TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,host))`,
	`CREATE TABLE workspace_clocks(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
}
var workspaceResetTables = []string{"workspace_instances", "workspace_directory_members", "workspace_trusted_members", "workspace_observations", "workspace_operations", "workspace_changes", "workspace_denies", "workspace_clocks"}

func workspaceProofJSON(raw string, out any) error {
	if e := strictEvidence(raw, out); e != nil {
		return e
	}
	// ALL workspace optional fields are omitted, never JSON null. Enforce this
	// independently of the producer decoder, including opaque nested requests.
	d := json.NewDecoder(bytes.NewBufferString(raw))
	depth := 0
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if tok == nil {
			return fmt.Errorf("null workspace evidence is unsupported")
		}
		if b, ok := tok.(json.Delim); ok {
			if b == '{' || b == '[' {
				depth++
			} else {
				depth--
			}
			if depth > 32 {
				return fmt.Errorf("workspace proof depth exceeded")
			}
		}
	}
	return nil
}
func workspaceProofHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func proofConvertWS(raw string, out any) error { return json.Unmarshal([]byte(raw), out) }
func (p *proofReader) readWorkspaces() error {
	present := 0
	for _, table := range workspaceResetTables {
		if _, ok := p.catalog[table]; ok {
			present++
		}
	}
	if present == 0 && !p.workspaceSchema {
		return nil
	}
	if present != len(workspaceResetTables) || !p.workspaceSchema {
		return fmt.Errorf("workspace tables/marker form an incomplete optional group")
	}
	v := workspace.Records{Initialized: true}
	if e := p.walk("metadata", func(row proofRow) error {
		key, e := row.text("key")
		if e != nil {
			return e
		}
		if key == "workspace_schema" {
			value, e := row.text("value")
			if e != nil {
				return e
			}
			if value != "1" && value != "2" {
				return fmt.Errorf("unknown workspace group")
			}
			v.RecoveryEnabled = value == "2"
		}
		return nil
	}); e != nil {
		return e
	}
	roots := map[string]proofWSInstance{}
	members := map[string]proofWSMember{}
	trusted := map[string]proofWSTrustedMember{}
	operations := map[string]proofWSOperation{}
	changes := []proofWSChange{}
	bytesRead := 0
	limits := []int{8, 8 * 128, 8 * 128, 8, 8 * 256, 8 * 1024, 8 * 128, 8}
	for i, table := range workspaceResetTables {
		n := 0
		e := p.walk(table, func(row proofRow) error {
			n++
			if n > limits[i] {
				return fmt.Errorf("complete workspace row bound exceeded")
			}
			raw, e := row.text("data")
			if e != nil {
				return e
			}
			bytesRead += len(raw)
			if bytesRead > 32<<20 {
				return fmt.Errorf("workspace proof evidence exceeds32MiB")
			}
			key := func(field string, expected string) error {
				actual, e := row.text(field)
				if e != nil || actual != expected {
					return fmt.Errorf("workspace embedded/table key differs for %s", field)
				}
				return nil
			}
			switch table {
			case "workspace_instances":
				var x proofWSInstance
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("id", x.ID); e != nil {
					return e
				}
				if _, ok := roots[x.ID]; ok {
					return fmt.Errorf("duplicate workspace root")
				}
				roots[x.ID] = x
				var w workspace.Instance
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Instances = append(v.Instances, w)
			case "workspace_directory_members":
				var x proofWSMember
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("workspace", x.Workspace); e != nil {
					return e
				}
				if e = key("host", x.Identity.Host); e != nil {
					return e
				}
				members[x.Workspace+"/"+x.Identity.Host] = x
				var w workspace.Member
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Members = append(v.Members, w)
			case "workspace_trusted_members":
				var x proofWSTrustedMember
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("workspace", x.Workspace); e != nil {
					return e
				}
				if e = key("host", x.Identity.Host); e != nil {
					return e
				}
				trusted[x.Workspace+"/"+x.Identity.Host] = x
				var w workspace.TrustedMember
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Trusted = append(v.Trusted, w)
			case "workspace_observations":
				var x proofWSObservation
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("workspace", x.Workspace); e != nil {
					return e
				}
				if x.DirectoryPin != x.Snapshot.Directory.Fingerprint || x.SnapshotHash != workspaceProofHash(func() proofWSSnapshot { copy := x.Snapshot; copy.Hash = ""; return copy }()) || x.SnapshotHash != x.Snapshot.Hash || x.Revision != x.Snapshot.Revision {
					return fmt.Errorf("workspace cached snapshot digest/pin differs")
				}
				var w workspace.Observation
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Observations = append(v.Observations, w)
			case "workspace_operations":
				var x proofWSOperation
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("workspace", x.Workspace); e != nil {
					return e
				}
				if e = key("principal", x.Principal); e != nil {
					return e
				}
				if e = key("key", x.Key); e != nil {
					return e
				}
				request, e := row.text("request")
				if e != nil || request != string(x.Request) {
					return fmt.Errorf("workspace raw request column differs")
				}
				var requestValue any
				switch x.Kind {
				case "create":
					requestValue = &proofWSCreateRequest{}
				case "join":
					requestValue = &proofWSJoinRequest{}
				case "admit":
					requestValue = &proofWSAdmissionRequest{}
				case "resume_admission", "resume_join":
					requestValue = &proofWSRecoveryRequest{}
				case "deny", "revoke", "retire":
					requestValue = &proofWSMutationRequest{}
				default:
					return fmt.Errorf("unknown workspace operation")
				}
				if e = workspaceProofJSON(request, requestValue); e != nil {
					return e
				}
				if workspaceProofHash(requestValue) != x.RequestHash {
					return fmt.Errorf("workspace immutable request hash differs")
				}
				if x.Review != nil && workspaceProofHash(x.Review) != x.ReviewHash {
					return fmt.Errorf("workspace review hash differs")
				}
				operations[x.Workspace+"/"+x.Principal+"/"+x.Key] = x
				var w workspace.Operation
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Operations = append(v.Operations, w)
			case "workspace_changes":
				var x proofWSChange
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("workspace", x.Workspace); e != nil {
					return e
				}
				seq, e := row.integer("sequence")
				if e != nil || seq < 1 {
					return fmt.Errorf("invalid workspace journal sequence")
				}
				changes = append(changes, x)
				var w workspace.Change
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Changes = append(v.Changes, w)
			case "workspace_denies":
				var x proofWSDeny
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("workspace", x.Workspace); e != nil {
					return e
				}
				if e = key("host", x.Host); e != nil {
					return e
				}
				var w workspace.Deny
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Denies = append(v.Denies, w)
			case "workspace_clocks":
				var x proofWSClockRecord
				if e = workspaceProofJSON(raw, &x); e != nil {
					return e
				}
				if e = key("workspace", x.Workspace); e != nil {
					return e
				}
				var w workspace.ClockRecord
				if e = proofConvertWS(raw, &w); e != nil {
					return e
				}
				v.Clocks = append(v.Clocks, w)
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	// Independent producer/journal/settlement checks precede the writer helper.
	actor := "native:" + p.result.Host + ":" + p.result.Owner
	if e := proofRecoveryFamilies(v.RecoveryEnabled, roots, operations, changes, actor); e != nil {
		return e
	}
	for id, root := range roots {
		if root.Authority.Host != p.result.Host || root.Authority.UID != p.result.Owner {
			return fmt.Errorf("workspace authority differs from stopped ledger")
		}
		var previous *proofWSChange
		expected := int64(1)
		historyMembers := map[string]proofWSMember{}
		creators := 0
		for _, op := range operations {
			if op.Workspace == id && oneOf(op.Kind, "create", "join") {
				creators++
				if op.Principal != actor || op.Review == nil || op.Review.Source != root.Self || op.Review.Directory != root.Directory || op.Review.Policy != root.Policy || op.Review.Generation != root.Generation {
					return fmt.Errorf("workspace creator/review authority differs")
				}
			}
		}
		if creators != 1 {
			return fmt.Errorf("workspace lacks exactly one retained origin")
		}
		for i := range changes {
			c := &changes[i]
			if c.Workspace != id {
				continue
			}
			if c.Revision != expected || c.After.ID != id || c.After.Generation != root.Generation {
				return fmt.Errorf("workspace journal scope/revision differs")
			}
			if previous == nil {
				if c.Before != nil || !oneOf(c.Kind, "create_prepared", "join_prepared") || c.After.State != "prepared" {
					return fmt.Errorf("workspace journal origin missing")
				}
			} else if c.Before == nil || !reflect.DeepEqual(*c.Before, previous.After) {
				return fmt.Errorf("workspace journal continuity differs")
			}
			expected++
			if c.MemberAfter != nil {
				historyMembers[c.MemberAfter.Identity.Host] = *c.MemberAfter
			}
			for _, m := range c.Members {
				historyMembers[m.Identity.Host] = m
			}
			previous = c
		}
		if previous == nil || !reflect.DeepEqual(previous.After, root) {
			return fmt.Errorf("workspace current root lacks complete producer history")
		}
		for key, m := range members {
			if m.Workspace != id {
				continue
			}
			if root.Role != "directory" || !reflect.DeepEqual(historyMembers[m.Identity.Host], m) {
				return fmt.Errorf("workspace member differs from journal")
			}
			_ = key
		}
		for host, m := range historyMembers {
			if _, ok := members[id+"/"+host]; !ok || m.Workspace != id {
				return fmt.Errorf("workspace retained member was deleted")
			}
		}
		if root.State != "retired" {
			if e := p.block("workspace", id, "Workspace is not explicitly locally retired; certificate expiry does not settle membership"); e != nil {
				return e
			}
			continue
		}
		last := *previous
		op, ok := operations[id+"/"+actor+"/"+last.Key]
		if !ok || !(last.Kind == "retire" && last.OperationPhase == "completed" && op.Kind == "retire" && op.Phase == "completed" || last.Kind == "decision" && last.OperationPhase == "declined" && oneOf(op.Kind, "create", "join") && op.Phase == "declined") {
			return fmt.Errorf("retired workspace lacks explicit native retirement/decline receipt")
		}
		if last.Actor != actor {
			return fmt.Errorf("workspace retirement actor differs")
		}
		for _, m := range members {
			if m.Workspace == id && m.State != "revoked" {
				return fmt.Errorf("retired workspace retains admitted/pending member")
			}
		}
		for _, t := range trusted {
			if t.Workspace != id {
				continue
			}
			if t.State != "revoked" {
				return fmt.Errorf("retired workspace retains accepted live pins")
			}
			found := false
			for _, x := range last.Trusted {
				if reflect.DeepEqual(x, t) {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("workspace retired trust pin lacks exact retirement event")
			}
		}
	}
	for _, op := range operations {
		if !oneOf(op.Phase, "completed", "declined", "cancelled") {
			if e := p.block("workspace_operation", op.Key, "Workspace operation is pending, unknown or unresolved"); e != nil {
				return e
			}
		}
	}
	// Additional pure producer validation is not the schema/raw/retirement proof.
	if e := workspace.ValidateRecords(v, workspace.Authority{Host: p.result.Host, UID: p.result.Owner}); e != nil {
		return fmt.Errorf("workspace complete graph is invalid: %w", e)
	}
	return nil
}
