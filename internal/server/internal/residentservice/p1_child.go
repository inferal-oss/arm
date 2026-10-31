package residentservice

import (
	"context"
	"encoding/hex"
	"os"
	"runtime"

	"getarm.org/internal/residentrefs"
)

// DATA on the original bounded private channel only. No caller success/Ready/
// birth/closure flag. Its secret must match the REAL inherited debit commitment.
type P1BootstrapData struct {
	ChannelID     NativeID `json:"channel_id"`
	Schema        uint32   `json:"schema"`
	Home          string   `json:"home"`
	Build         Build    `json:"build"`
	DebitID       string   `json:"debit_id"`
	LaunchKey     string   `json:"launch_key"`
	ProfileSHA256 string   `json:"profile_sha256"`
	JournalBytes  int64    `json:"journal_bytes"`
	JournalSHA256 string   `json:"journal_sha256"`
	Secret        [32]byte `json:"secret"`
}

// Pure concrete child construction. Root retains this actual holder BEFORE
// same-entry delegation. The legacy registry stays EMPTY: P1 never registerRaw,
// wraps inherited files again, calls receiveBody or reacquires an inherited lock.
func NewP1ChildPending(custody *residentrefs.ServiceOriginal) (*ChildPending, error) {
	if custody == nil {
		return nil, ErrInvalid
	}
	c := &ChildPending{}
	o := &owner{child: c, p1Child: custody}
	c.owner = o
	o.files = &nativeFiles{owner: o}
	journal, err := custody.ClaimJournalOriginalView()
	if err != nil {
		return nil, err
	}
	o.p1Journal = journal
	return c, nil
}
func (p *P1ServiceGroup) BootstrapOriginalData() (data P1BootstrapData, resultErr error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return data, ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.active != nil || o.firstError != nil || !p.bound || p.debit.validateOriginalLocked(o) != nil || o.append == nil || !o.append.committed || o.channel != nil {
		return data, ErrInvalid
	}
	return P1BootstrapData{ChannelID: o.files.childChannel.id, Schema: 2, Home: o.path, Build: o.build, DebitID: o.debit.record.ID, LaunchKey: o.debit.record.LaunchKey, ProfileSHA256: o.debit.record.ProfileSHA256, JournalBytes: o.journal.bytes, JournalSHA256: hex.EncodeToString(o.journal.last[:]), Secret: o.secret}, nil
}
func (c *ChildPending) ReceiveP1Original(ctx context.Context, data P1BootstrapData) (resultErr error) {
	if c == nil || c.owner == nil || c.owner.child != c || c.owner.p1Child == nil {
		return ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	if o.active != nil {
		o.mu.Unlock()
		return ErrPending
	}
	if o.firstError != nil || o.receiveTried {
		o.mu.Unlock()
		return ErrInvalid
	}
	o.receiveTried = true
	op := &operation{owner: o, kind: "p1-receive", phase: "registered_before_actual_delegated_service_materialization"}
	o.active = op
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.receiveP1Body(ctx, data)
	normal = true
	return resultErr
}
func (o *owner) receiveP1Body(ctx context.Context, data P1BootstrapData) error {
	if ctx == nil || data.Schema != 2 || !boundedText(data.Home, 4096) || data.JournalBytes <= 0 || data.JournalBytes > MaxJournalBytes || !validDigest(data.JournalSHA256) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.p1Child.MaterializeOriginal(ctx); err != nil {
		return err
	}
	identities, err := o.p1Child.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.p1IDs = identities
	o.mu.Unlock()
	journal, err := o.readOriginalJournal(ctx)
	if err != nil {
		return err
	}
	if len(journal.records) != 2 || journal.bytes != data.JournalBytes || hex.EncodeToString(journal.last[:]) != data.JournalSHA256 {
		return ErrUnresolved
	}
	grant, record := journal.records[0].Grant, journal.records[1].Debit
	if grant.Intent.Home != data.Home || grant.Intent.HomeID != sharedID(identities.Home) || grant.Intent.Profile.Build != data.Build || record.ID != data.DebitID || record.LaunchKey != data.LaunchKey || record.ProfileSHA256 != data.ProfileSHA256 || record.SecretCommitment != digest(data.Secret[:]) || record.LockID != sharedID(identities.Lock) || record.JournalID != sharedID(identities.Journal) {
		return ErrInvalid
	}
	// Exact actual grant-bound schema2 environment, not bootstrap-only data.
	// Read/retain the real child process snapshot; errors reveal no values.
	if grant.Intent.Profile.RequireP1Environment() != nil {
		return ErrInvalid
	}
	environment := os.Environ()
	o.mu.Lock()
	o.p1Environment = append([]string(nil), environment...)
	o.mu.Unlock()
	if err = grant.Intent.Profile.RuntimeEnvironment.MatchesActual(environment); err != nil {
		return ErrInvalid
	}
	if data.Build.GOOS != runtime.GOOS || data.Build.GOARCH != runtime.GOARCH || data.Build.GoBuild != runtime.Version() {
		return ErrInvalid
	}
	// Path/current-native authority and all13 image/fence identity matching still
	// belong to actual Root, before native/Manager/DB/Server claims and its ACK.
	o.mu.Lock()
	o.path, o.build, o.journal, o.grant, o.secret = data.Home, data.Build, journal, grant, data.Secret
	o.debit = &OriginalDebit{owner: o, record: record}
	o.prepared, o.opened = true, true
	o.mu.Unlock()
	return ctx.Err()
}
func sharedID(id residentrefs.Identity) NativeID {
	return NativeID{Device: id.Device, Inode: id.Inode, UID: id.UID}
}
func (o *owner) serviceHomeOriginalLocked() NativeID {
	if o.p1Child != nil {
		return sharedID(o.p1IDs.Home)
	}
	return o.files.home.id
}
func (o *owner) journalStatOriginal(ctx context.Context) (os.FileInfo, error) {
	if o.p1Child != nil {
		return o.p1Journal.StatOriginal(ctx)
	}
	return o.files.journal.file.Stat()
}
func (o *owner) journalReadOriginal(ctx context.Context, data []byte, offset int64) (int, error) {
	if o.p1Child != nil {
		return o.p1Journal.ReadAtOriginal(ctx, data, offset)
	}
	return o.files.journal.file.ReadAt(data, offset)
}
func (o *owner) journalWriteOriginal(ctx context.Context, data []byte, offset int64) (int, error) {
	if o.p1Child != nil {
		return o.p1Journal.WriteAtOriginal(ctx, data, offset)
	}
	return o.files.journal.file.WriteAt(data, offset)
}
func (o *owner) journalSyncOriginal(ctx context.Context) error {
	if o.p1Child != nil {
		return o.p1Journal.SyncOriginal(ctx)
	}
	return o.files.journal.file.Sync()
}

// Fresh actual-process equivalence for the original root before ACK/activation.
// A request cannot substitute a digest, env slice, approved bool or another grant.
func (c *ChildPending) ValidateP1EnvironmentOriginal(ctx context.Context) (resultErr error) {
	if c == nil || c.owner == nil || c.owner.child != c {
		return ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	if o.p1Child == nil || o.active != nil || o.firstError != nil || !o.prepared || o.grant == nil {
		o.mu.Unlock()
		return ErrInvalid
	}
	op := &operation{owner: o, kind: "p1-current-environment", phase: "registered_before_actual_environment_snapshot"}
	o.active = op
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	if ctx == nil {
		resultErr = ErrInvalid
	} else if resultErr = ctx.Err(); resultErr == nil {
		if o.grant.Intent.Profile.RequireP1Environment() != nil {
			resultErr = ErrInvalid
		} else {
			actual := os.Environ()
			o.mu.Lock()
			o.p1Environment = append([]string(nil), actual...)
			o.mu.Unlock()
			resultErr = o.grant.Intent.Profile.RuntimeEnvironment.MatchesActual(actual)
			if resultErr == nil {
				resultErr = ctx.Err()
			}
		}
	}
	normal = true
	return resultErr
}
