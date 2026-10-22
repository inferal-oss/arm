package workspace

import "time"

const MaxRecoveryAttempts = 4

// OperationRef binds a recovery to the original immutable request. Revision is
// the operator's reviewed precondition, not permission to reuse an old worker.
type OperationRef struct {
	Principal   string `json:"principal"`
	Key         string `json:"key"`
	RequestHash string `json:"request_sha256"`
	Revision    int64  `json:"revision"`
}

// RecoveryRequest describes one explicit attempt, never an implicit resend.
// Identity/capability fields must exactly equal the original approved family.
type RecoveryRequest struct {
	Key                    string       `json:"key"`
	Workspace              string       `json:"workspace"`
	Generation             string       `json:"generation"`
	Original               OperationRef `json:"original"`
	Previous               string       `json:"previous_recovery_key"`
	Attempt                int          `json:"attempt"`
	Consent                string       `json:"consent_sha256"`
	DirectoryPin           string       `json:"directory_pin"`
	CandidatePin           string       `json:"candidate_pin"`
	PolicyRevision         int64        `json:"policy_revision"`
	Introducer             bool         `json:"introducer"`
	HostInbox              bool         `json:"host_inbox_enabled"`
	CandidateAuthorization string       `json:"candidate_recovery_sha256"`
	PermitProof            bool         `json:"permit_proof"`
	Reason                 string       `json:"reason"`
}

// RecoveryBinding is additional proof context. Original consent and identity
// remain unchanged; the new candidate review and directory attempt are bound
// alongside them. Initial challenges/proofs omit this structure.
type RecoveryBinding struct {
	OriginalKey            string `json:"original_admission_key"`
	AttemptKey             string `json:"recovery_attempt_key"`
	CandidateAuthorization string `json:"candidate_recovery_sha256"`
}

// AdmissionStatus is a TLS-derived own-candidate observation, not a transferable
// grant or an authorizing cached receipt. It never contains another candidate's
// state, operator reasons, private nonce or worker boot.
type AdmissionStatus struct {
	Schema              int       `json:"schema_version"`
	Workspace           string    `json:"workspace"`
	Generation          string    `json:"generation"`
	Source              string    `json:"source_host"`
	Target              string    `json:"target_host"`
	Nonce               string    `json:"nonce"`
	OriginalPrincipal   string    `json:"original_principal"`
	OriginalKey         string    `json:"original_key"`
	OriginalRequestHash string    `json:"original_request_sha256"`
	Consent             string    `json:"consent_sha256"`
	DirectoryPin        string    `json:"directory_pin"`
	CandidatePin        string    `json:"candidate_pin"`
	Membership          int64     `json:"membership_generation"`
	Introducer          bool      `json:"introducer"`
	HostInbox           bool      `json:"host_inbox_enabled"`
	State               string    `json:"state"`
	CompletingAttempt   string    `json:"completing_attempt"`
	ChallengeHash       string    `json:"challenge_sha256"`
	DirectoryRevision   int64     `json:"directory_revision"`
	Observed            time.Time `json:"observed_at"`
}

func recoveryKind(kind string) bool { return known(kind, "resume_admission", "resume_join") }

func validRecoveryRequest(q RecoveryRequest) bool {
	return validKey(q.Key) && q.Key != q.Original.Key && validHex(q.Workspace, 16) && validHex(q.Generation, 16) &&
		text(q.Original.Principal, 512) && validKey(q.Original.Key) && validHex(q.Original.RequestHash, 32) && q.Original.Revision > 0 &&
		(q.Previous == "" || validKey(q.Previous)) && q.Attempt >= 1 && q.Attempt <= MaxRecoveryAttempts &&
		validHex(q.Consent, 32) && validHex(q.DirectoryPin, 32) && validHex(q.CandidatePin, 32) && q.PolicyRevision > 0 &&
		(q.CandidateAuthorization == "" || validHex(q.CandidateAuthorization, 32)) && reason(q.Reason)
}

func recoveryRequest(o Operation) (RecoveryRequest, error) {
	var q RecoveryRequest
	if !recoveryKind(o.Kind) {
		return q, problem("invalid_request", "operation is not a recovery child")
	}
	if e := Decode(o.Request, &q); e != nil || !validRecoveryRequest(q) {
		return q, problem("workspace_state_invalid", "recovery child request is invalid")
	}
	return q, nil
}

type AdmissionStatusRequest struct {
	Workspace  string `json:"workspace"`
	Generation string `json:"generation"`
	Source     string `json:"source_host"`
	Consent    string `json:"consent_sha256"`
	Nonce      string `json:"nonce"`
}
