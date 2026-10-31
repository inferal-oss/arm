package residentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

// Original-operation DATA. No secret, authority constructor or Ready/Closed bit.
type P1PreparedServiceData struct {
	Home                              string
	HomeID                            NativeID
	DebitID, LaunchKey, ProfileSHA256 string
	EnvironmentSHA256                 string
	Build                             Build
	Resources                         Resources
	Identities                        residentrefs.ServiceIdentities
	BirthJournalBytes                 int64
	BirthJournalSHA256                string
}
type p1PreparedServiceCall struct {
	owner                *owner
	op                   *operation
	custody              *residentrefs.ServiceOriginal
	image                *mesh.RuntimeExecutable
	charge               *ManagerCharge
	birth                *readOperation
	current              residentrefs.ServiceIdentities
	currentJournal       os.FileInfo
	environment          []string
	environmentCanonical []byte
	runtime              mesh.RuntimeProof
	returned             bool
	resultError          error
}

// Before RootBound only. Preserve the ACTUAL initial two-record birth and its
// original read; do not reread/overwrite it or consume the later current-read2.
func (c *ChildPending) ValidateP1PreparedOriginal(ctx context.Context, custody *residentrefs.ServiceOriginal, image *mesh.RuntimeExecutable, charge *ManagerCharge) (data P1PreparedServiceData, resultErr error) {
	if c == nil || c.owner == nil || c.owner.child != c || ctx == nil {
		return data, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	o := c.owner
	o.mu.Lock()
	if o.active != nil {
		o.mu.Unlock()
		return data, ErrPending
	}
	valid := o.firstError == nil && !o.p1PreparedTried && o.prepared && o.runtimeBound && custody != nil && o.p1Child == custody && image != nil && o.image == image && charge != nil && o.charge == charge && charge.owner == o && charge.child == c && o.grant != nil && o.debit != nil && o.debit.validateOriginalLocked(o) == nil && o.append == nil && len(o.reads) == 1 && o.reads[0] != nil && o.reads[0].owner == o && o.reads[0].stage == "p1-receive" && o.reads[0].finished && o.reads[0].firstError == nil && o.reads[0].after != nil
	if !valid {
		o.mu.Unlock()
		return data, ErrInvalid
	}
	o.p1PreparedTried = true
	op := &operation{owner: o, kind: "p1-prepared-validation", phase: "registered_before_actual_prepared_service_checks"}
	call := &p1PreparedServiceCall{owner: o, op: op, custody: custody, image: image, charge: charge, birth: o.reads[0]}
	o.p1PreparedValidation, o.active = call, op
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.validateP1PreparedBody(ctx, call, &data)
	o.mu.Lock()
	call.returned, call.resultError = true, resultErr
	o.mu.Unlock()
	normal = true
	return data, resultErr
}
func (o *owner) validateP1PreparedBody(ctx context.Context, call *p1PreparedServiceCall, data *P1PreparedServiceData) error {
	var err error
	call.current, err = call.custody.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	if call.current != o.p1IDs {
		return ErrInvalid
	}
	call.currentJournal, err = o.p1Journal.StatOriginal(ctx) // metadata only; ZERO journal reads
	if err != nil {
		return err
	}
	birthInfo := call.birth.after
	if !os.SameFile(birthInfo, call.currentJournal) || birthInfo.Size() != call.currentJournal.Size() || !birthInfo.ModTime().Equal(call.currentJournal.ModTime()) || call.currentJournal.Size() != o.journal.bytes {
		return ErrUnresolved
	}
	if o.grant.Intent.Profile.RequireP1Environment() != nil {
		return ErrInvalid
	}
	call.environment = os.Environ()
	if err = o.grant.Intent.Profile.RuntimeEnvironment.MatchesActual(call.environment); err != nil {
		return err
	}
	call.environmentCanonical, err = json.Marshal(o.grant.Intent.Profile.RuntimeEnvironment)
	if err != nil || len(call.environmentCanonical) > 8<<10 {
		return ErrInvalid
	}
	call.runtime, err = call.image.Proof() // SAME actual inherited/current-A owner
	if err != nil {
		return err
	}
	if call.runtime.SHA256 != o.build.SHA256 || call.runtime.Size != o.build.Size || o.build.GOOS != runtime.GOOS || o.build.GOARCH != runtime.GOARCH || o.build.GoBuild != runtime.Version() {
		return ErrInvalid
	}
	profileSHA, err := o.grant.Intent.Profile.SHA256()
	if err != nil || profileSHA != o.debit.record.ProfileSHA256 {
		return ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	environmentSHA := sha256.Sum256(call.environmentCanonical)
	*data = P1PreparedServiceData{Home: o.path, HomeID: o.serviceHomeOriginalLocked(), DebitID: o.debit.record.ID, LaunchKey: o.debit.record.LaunchKey, ProfileSHA256: profileSHA, EnvironmentSHA256: hex.EncodeToString(environmentSHA[:]), Build: o.build, Resources: o.grant.Intent.Profile.Resources, Identities: call.current, BirthJournalBytes: o.journal.bytes, BirthJournalSHA256: hex.EncodeToString(o.journal.last[:])}
	return nil
}
