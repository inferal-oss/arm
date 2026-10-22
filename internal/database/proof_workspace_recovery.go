package database

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
)

// Independent mirrors: changing a producer does not silently expand reset's
// accepted operation families, request fields or nullable evidence.
type proofWSOperationRef struct {
	Principal   string `json:"principal"`
	Key         string `json:"key"`
	RequestHash string `json:"request_sha256"`
	Revision    int64  `json:"revision"`
}
type proofWSRecoveryRequest struct {
	Key                    string              `json:"key"`
	Workspace              string              `json:"workspace"`
	Generation             string              `json:"generation"`
	Original               proofWSOperationRef `json:"original"`
	Previous               string              `json:"previous_recovery_key"`
	Attempt                int                 `json:"attempt"`
	Consent                string              `json:"consent_sha256"`
	DirectoryPin           string              `json:"directory_pin"`
	CandidatePin           string              `json:"candidate_pin"`
	PolicyRevision         int64               `json:"policy_revision"`
	Introducer             bool                `json:"introducer"`
	HostInbox              bool                `json:"host_inbox_enabled"`
	CandidateAuthorization string              `json:"candidate_recovery_sha256"`
	PermitProof            bool                `json:"permit_proof"`
	Reason                 string              `json:"reason"`
}

func proofRecoveryFamilies(enabled bool, roots map[string]proofWSInstance, operations map[string]proofWSOperation, changes []proofWSChange, actor string) error {
	families := map[string]map[int]proofWSOperation{}
	requests := map[string]proofWSRecoveryRequest{}
	for locator, op := range operations {
		if !oneOf(op.Kind, "resume_admission", "resume_join") {
			continue
		}
		if !enabled {
			return fmt.Errorf("workspace recovery requires schema group2")
		}
		var q proofWSRecoveryRequest
		if e := workspaceProofJSON(string(op.Request), &q); e != nil {
			return e
		}
		root, ok := roots[op.Workspace]
		if !ok || op.Principal != actor || op.Key != q.Key || q.Workspace != root.ID || q.Generation != root.Generation || q.PolicyRevision != root.PolicyRevision || q.DirectoryPin != root.Directory.Fingerprint || q.Attempt < 1 || q.Attempt > 4 || op.Review == nil || op.Private == nil {
			return fmt.Errorf("workspace recovery authority/scope differs")
		}
		originalLocator := op.Workspace + "/" + q.Original.Principal + "/" + q.Original.Key
		parent, ok := operations[originalLocator]
		if !ok || parent.RequestHash != q.Original.RequestHash || q.Original.Revision < 1 || parent.Revision <= q.Original.Revision || q.Key == parent.Key {
			return fmt.Errorf("workspace recovery original request/revision differs")
		}
		candidate := root.Self
		if op.Kind == "resume_join" {
			if parent.Kind != "join" || parent.Principal != actor || parent.Review == nil || parent.ReviewHash != q.Consent || q.CandidateAuthorization != "" || q.Introducer != parent.Review.Introducer || q.HostInbox != parent.Review.HostInbox {
				return fmt.Errorf("workspace recovery join consent differs")
			}
		} else {
			var admission proofWSAdmissionRequest
			if parent.Kind != "admit" {
				return fmt.Errorf("workspace recovery admission origin kind differs")
			}
			if e := workspaceProofJSON(string(parent.Request), &admission); e != nil {
				return e
			}
			candidate = admission.Candidate
			if admission.ConsentHash != q.Consent || admission.Introducer != q.Introducer || admission.HostInbox != q.HostInbox || q.PermitProof && !proofWSHashValue(q.CandidateAuthorization) {
				return fmt.Errorf("workspace recovery admission consent differs")
			}
		}
		review := op.Review
		if candidate.Fingerprint != q.CandidatePin || review.Source != root.Self || review.Directory != root.Directory || review.Candidate == nil || *review.Candidate != candidate || review.Policy != root.Policy || review.Generation != root.Generation || review.PolicyRevision != q.PolicyRevision || review.Kind != op.Kind || review.Reason != q.Reason || review.Introducer != q.Introducer || review.HostInbox != q.HostInbox {
			return fmt.Errorf("workspace recovery review differs from immutable family")
		}
		family := families[originalLocator]
		if family == nil {
			family = map[int]proofWSOperation{}
			families[originalLocator] = family
		}
		if _, ok := family[q.Attempt]; ok {
			return fmt.Errorf("workspace recovery family forks")
		}
		family[q.Attempt] = op
		requests[locator] = q
		prepared, decided, proving, completed, reconciled := false, false, false, false, false
		for _, c := range changes {
			if c.Workspace != op.Workspace {
				continue
			}
			if c.Key == op.Key && c.Actor == actor {
				switch c.Kind {
				case "recovery_prepared":
					if prepared || c.OperationPhase != "awaiting_review" || !c.At.Equal(op.Created) {
						return fmt.Errorf("workspace recovery origin event differs")
					}
					prepared = true
				case "recovery_decision":
					if !prepared || !oneOf(c.OperationPhase, "approved", "declined", "cancelled") {
						return fmt.Errorf("workspace recovery decision lacks origin")
					}
					decided = true
				case "recovery_proving":
					if !decided || proving || c.OperationPhase != "proving" || op.Kind != "resume_admission" || !q.PermitProof {
						return fmt.Errorf("workspace recovery dispatch lacks fresh decision")
					}
					proving = true
				case "recovery_reconciled":
					if !decided || q.PermitProof || op.Kind != "resume_admission" || c.OperationPhase != "completed" || !c.At.Equal(op.Updated) {
						return fmt.Errorf("workspace read-only reconciliation differs")
					}
					completed = true
					reconciled = true
				case "recovery_completed":
					if !proving || completed || c.OperationPhase != "completed" || c.MemberAfter == nil || op.Result == nil || !reflect.DeepEqual(*c.MemberAfter, *op.Result) || !c.At.Equal(op.Updated) {
						return fmt.Errorf("workspace recovery completion differs")
					}
					completed = true
				}
			}
			if op.Kind == "resume_join" && c.Kind == "join_finalized" && c.Key == parent.Key && c.Actor == actor && c.OperationPhase == "completed" && c.At.Equal(op.Updated) {
				completed = true
			}
		}
		if !prepared || op.Phase != "awaiting_review" && !decided || oneOf(op.Phase, "proving", "unknown") && !proving {
			return fmt.Errorf("workspace recovery current phase lacks matching audit")
		}
		if op.Phase == "completed" {
			if !completed || parent.Phase != "completed" || op.Kind == "resume_join" && !reconciled && !parent.Updated.Equal(op.Updated) {
				return fmt.Errorf("workspace recovery completed child has unresolved original")
			}
			if op.Kind == "resume_admission" && (op.Result == nil || parent.Result == nil || !reflect.DeepEqual(*parent.Result, *op.Result)) {
				return fmt.Errorf("workspace recovery result differs from original member")
			}
		}
	}
	for original, family := range families {
		previous := ""
		for i := 1; i <= len(family); i++ {
			op, ok := family[i]
			if !ok {
				return fmt.Errorf("workspace recovery family has missing predecessor")
			}
			q := requests[op.Workspace+"/"+op.Principal+"/"+op.Key]
			if q.Previous != previous || i < len(family) && !oneOf(op.Phase, "completed", "declined", "cancelled") {
				return fmt.Errorf("workspace recovery predecessor unresolved or different")
			}
			previous = op.Key
		}
		parent := operations[original]
		if oneOf(parent.Phase, "completed", "cancelled") {
			root := roots[parent.Workspace]
			if root.State == "retired" {
				for _, child := range family {
					if !oneOf(child.Phase, "completed", "declined", "cancelled") {
						return fmt.Errorf("retired workspace has unresolved recovery descendant")
					}
				}
			}
		}
	}
	return nil
}

func proofWSHashValue(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && strings.ToLower(v) == v
}
