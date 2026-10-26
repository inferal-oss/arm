package pressure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	maxEpochBytes   = 128
	maxIDBytes      = 256
	maxAgentBytes   = 256
	maxKeyBytes     = 8192
	maxJournalBytes = 8 << 20
	maxJournalJobs  = 256
)

func boundedUTF8(s string, max int) bool { return len(s) <= max && utf8.ValidString(s) }

func validJobID(j JobID) bool {
	return boundedUTF8(j.LeaseID, maxIDBytes) && boundedUTF8(j.ExecutionID, maxIDBytes) &&
		boundedUTF8(j.Generation, maxIDBytes) && boundedUTF8(j.OwnershipID, maxIDBytes)
}

func validResource(r Resource) bool { return r == CPU || r == Memory || r == Disk }

func policyDigest(p Policy) string {
	encoded, _ := json.Marshal(p) // Policy contains only JSON-encodable bounded scalar fields.
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func validateStoredState(s State, maxJobs int) error {
	if !boundedUTF8(s.Epoch, maxEpochBytes) || len(s.Pauses) > maxJobs || len(s.Unresolved) > maxJobs || len(s.Resources) > 3 || len(s.JobWarnings) > 3 || s.ControlRevision > ^uint64(0)-uint64(2*maxJobs+3) {
		return errors.New("pressure: invalid or oversized stored state")
	}
	for r := range s.Resources {
		if !validResource(r) {
			return errors.New("pressure: invalid stored resource")
		}
	}
	for jid, record := range s.Pauses {
		if jid != record.Job || !validRecord(record) {
			return errors.New("pressure: invalid stored pause")
		}
	}
	for r, roster := range s.JobWarnings {
		if !validResource(r) || len(roster) > maxJobs {
			return errors.New("pressure: invalid stored warning roster")
		}
		for jid := range roster {
			if !validJobID(jid) {
				return errors.New("pressure: invalid stored warning identity")
			}
		}
	}
	for key, obligation := range s.Unresolved {
		if key != obligation.Key || key != obligation.Record.PauseKey || !boundedUTF8(key, maxKeyBytes) || obligation.SourceEpoch == "" || !boundedUTF8(obligation.SourceEpoch, maxEpochBytes) || !validRecord(obligation.Record) {
			return errors.New("pressure: invalid stored unresolved control")
		}
	}
	return nil
}

func validRecord(record PauseRecord) bool {
	job := record.Job
	if !validJobID(job) || job.LeaseID == "" || job.ExecutionID == "" || job.Generation == "" || job.OwnershipID == "" ||
		!validResource(record.Reason) || record.PauseKey == "" || !boundedUTF8(record.PauseKey, maxKeyBytes) || !boundedUTF8(record.ResumeKey, maxKeyBytes) || len(record.PolicyDigest) != 64 {
		return false
	}
	switch record.Phase {
	case PausePending, CancelPending, PauseApplied, ResumePending, ResumeFailed, NeedsAttention:
	default:
		return false
	}
	_, err := hex.DecodeString(record.PolicyDigest)
	return err == nil
}
