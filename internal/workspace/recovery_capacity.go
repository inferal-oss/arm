package workspace

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"time"
)

// capacityProblem distinguishes a writer's refused precommit projection from
// arbitrary SQL failure. A capacity-invalid on-disk ledger is still corruption.
type capacityProblem struct{ detail string }

func (e *capacityProblem) Error() string { return e.detail }

func recordBytes(v any) int64 {
	raw, e := json.Marshal(v)
	if e != nil {
		return int64(MaxLedgerBytes) + 1
	}
	return int64(len(raw))
}
func completeBytes(v Records) int64 {
	var n int64
	for _, group := range []any{v.Instances, v.Members, v.Trusted, v.Observations, v.Operations, v.Changes, v.Denies, v.Clocks} {
		rows := reflect.ValueOf(group)
		for i := 0; i < rows.Len(); i++ {
			n += recordBytes(rows.Index(i).Interface())
		}
	}
	return max(n, v.rawBytes)
}

// familyRecordBounds deliberately bound future reasons/revisions/timestamps
// above their producer maxima using exact immutable identity/request sizes.
// Each phase write is checked against these same bounds by the recovery writer.
func boundedRootBytes(root Instance) int64 {
	root.Revision = math.MaxInt64
	root.State = "needs_attention"
	root.Created = time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	root.Updated = root.Created
	return recordBytes(root)
}
func familyRecordBounds(root Instance, parent Operation, candidate Identity) (operation, change int64) {
	operation = 3*boundedRootBytes(root) + 2*int64(len(parent.Request)) + 16384
	change = 2*boundedRootBytes(root) + 2*recordBytes(candidate) + 16384
	return
}

type recoveryCredit struct {
	operations, changes int
	bytes               int64
}

func familyCredits(v *Records, root Instance, parent Operation) (credit recoveryCredit, err error) {
	if !known(parent.Kind, "join", "admit") {
		return credit, nil
	}
	ref := OperationRef{Principal: parent.Principal, Key: parent.Key, RequestHash: parent.RequestHash, Revision: parent.Revision}
	children, e := familyChildren(v, root.ID, ref)
	if e != nil {
		return credit, e
	}
	pending := !terminal(parent.Phase)
	for _, child := range children {
		if !terminal(child.Phase) {
			pending = true
		}
	}
	if !pending {
		return credit, nil
	}
	candidate := root.Self
	if parent.Kind == "admit" {
		var q AdmissionRequest
		if e = Decode(parent.Request, &q); e != nil {
			return credit, e
		}
		candidate = q.Candidate
	}
	operation, change := familyRecordBounds(root, parent, candidate)
	remaining := MaxRecoveryAttempts - len(children)
	credit.operations = remaining
	credit.changes = remaining * 8
	credit.bytes = int64(remaining) * (operation + 8*change)
	// Existing unfinished children have their row already, but still need phase
	// and result growth. Count no extra operation slot for that same child.
	for _, child := range children {
		if terminal(child.Phase) {
			continue
		}
		usedChanges := 0
		var usedBytes int64
		for _, event := range v.Changes {
			if event.Workspace == root.ID && event.Key == child.Key && event.Actor == child.Principal {
				usedChanges++
				usedBytes += recordBytes(event)
			}
		}
		if usedChanges > 8 || usedBytes > 8*change || recordBytes(child) > operation {
			return credit, fmt.Errorf("recovery consumed more than its reserved evidence bounds")
		}
		credit.changes += 8 - usedChanges
		credit.bytes += operation - recordBytes(child) + 8*change - usedBytes
	}
	// A phase already durably written consumes, rather than duplicates, its
	// original settlement reservation. Original row/result growth is a delta.
	future := reserved(parent)
	credit.changes += future
	credit.bytes += max(int64(0), operation-recordBytes(parent)) + int64(future)*change + recordBytes(candidate) + 2048
	return credit, nil
}

// validateRecoveryCapacity is global for bytes and per-workspace for row/audit
// counts. It never deletes old evidence or spends another family's reserves.
func validateRecoveryCapacity(v Records) error {
	if !v.RecoveryEnabled {
		return nil
	}
	bytes := completeBytes(v)
	for _, root := range v.Instances {
		_, operations, changes := counts(&v, root.ID)
		if root.State != "retired" {
			operations++ // one explicit local-retirement receipt
			changes++
			bytes += 2*MaxRecordBytes + boundedRootBytes(root) - recordBytes(root) // retirement plus remaining root growth
		}
		for _, parent := range v.Operations {
			if parent.Workspace != root.ID {
				continue
			}
			credit, e := familyCredits(&v, root, parent)
			if e != nil {
				return e
			}
			operations += credit.operations
			changes += credit.changes
			bytes += credit.bytes
			if !known(parent.Kind, "join", "admit") && !recoveryKind(parent.Kind) {
				changes += reserved(parent)
			}
		}
		if operations > MaxOperations || changes > MaxChanges {
			return &capacityProblem{fmt.Sprintf("workspace audit reserve exhausted (%d operation slots, %d changes)", operations, changes)}
		}
	}
	if bytes > MaxLedgerBytes {
		return &capacityProblem{fmt.Sprintf("workspace complete-ledger reserve exhausted (%d bytes)", bytes)}
	}
	return nil
}
