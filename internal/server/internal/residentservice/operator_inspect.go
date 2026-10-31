package residentservice

import (
	"context"
	"errors"

	"golang.org/x/sys/unix"
)

// Inspection is readonly DATA. It never acquires flock, repairs/appends a frame,
// rereads mutable profile/evidence files as new authority, issues a debit/charge,
// resends apply or infers the original writer's fsync result/native closure.
type AuthorizationInspection struct {
	Home               string          `json:"home"`
	Key                string          `json:"key"`
	Expect             string          `json:"expect"`
	ProfilePath        string          `json:"profile"`
	EvidencePath       string          `json:"evidence"`
	Reason             string          `json:"reason"`
	State              string          `json:"state"`
	OriginalCommit     string          `json:"original_commit"`
	GrantID            string          `json:"grant_id,omitempty"`
	GrantRevision      uint64          `json:"grant_revision,omitempty"`
	RecordSequence     uint32          `json:"record_sequence,omitempty"`
	CurrentHomeID      NativeID        `json:"current_home_id"`
	Inputs             *OperatorInputs `json:"recorded_inputs,omitempty"`
	Resources          *Resources      `json:"recorded_resources,omitempty"`
	ReadOnly           bool            `json:"read_only"`
	ExactRecordMatched bool            `json:"exact_record_matched"`
	Detail             string          `json:"detail"`
}

func (p *OperatorPending) InspectAuthorizationOriginal(ctx context.Context, expect string) (result AuthorizationInspection, resultErr error) {
	if !validDigest(expect) {
		return result, ErrInvalid
	}
	op, err := p.begin("operator_inspect")
	if err != nil {
		return result, err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	result, resultErr = p.inspectAuthorizationBody(ctx, expect)
	if resultErr == nil {
		p.owner.mu.Lock()
		p.inspectionComplete = true
		p.owner.mu.Unlock()
	}
	normal = true
	return result, resultErr
}
func (p *OperatorPending) inspectAuthorizationBody(ctx context.Context, expect string) (AuthorizationInspection, error) {
	result := AuthorizationInspection{Home: p.owner.path, Key: p.key, Expect: expect, ProfilePath: p.profilePath, EvidencePath: p.evidencePath, Reason: p.reason, OriginalCommit: "unknown_from_readonly_inspection", ReadOnly: true, Detail: "Read-only observation cannot reconstruct the original fsync result, native custody or closure. Keep the original key/selectors; do not resend apply or choose a new key automatically."}
	if ctx == nil {
		return result, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	o, n := p.owner, p.owner.files
	if err := n.openHome(o.path); err != nil {
		if errors.Is(err, unix.ENOENT) {
			result.State = "home_not_observed"
			return result, nil
		}
		return result, err
	}
	result.CurrentHomeID = n.home.id
	lockExists, err := p.privateEntry(lockName)
	if err != nil {
		return result, err
	}
	journalExists, err := p.privateEntry(journalName)
	if err != nil {
		return result, err
	}
	if !lockExists && !journalExists {
		result.State = "authority_files_not_observed"
		return result, p.validateHome()
	}
	if !lockExists || !journalExists {
		result.State = "partial_authority"
		return result, p.validateHome()
	}
	// Read exact native files without flock/reassertion or writer adoption.
	n.lock, err = n.openPrivateAt(n.home, lockName, unix.O_RDONLY)
	if err != nil {
		return result, err
	}
	n.journal, err = n.openPrivateAt(n.home, journalName, unix.O_RDONLY)
	if err != nil {
		return result, err
	}
	journal, err := o.readOriginalJournal(ctx)
	if err != nil {
		return result, err
	}
	g := journal.records[0].Grant
	if g.PlanSHA != expect || g.Intent.Key != p.key || g.Intent.Home != o.path || g.Intent.HomeID != n.home.id || g.Intent.Inputs.Profile.Path != p.profilePath || g.Intent.Inputs.Evidence.Path != p.evidencePath || g.Intent.Reason != p.reason {
		return result, ErrInvalid
	}
	if err = n.validateCurrent(o.path); err != nil {
		return result, err
	}
	// Only frozen scalar DATA is projected; no underlying owner/record aliases.
	inputs, resources := g.Intent.Inputs, g.Intent.Profile.Resources
	result.Inputs, result.Resources = &inputs, &resources
	result.ExactRecordMatched = true
	result.GrantID, result.GrantRevision = g.ID, g.Revision
	result.RecordSequence = uint32(len(journal.records))
	result.State = journal.records[len(journal.records)-1].Kind
	return result, nil
}
