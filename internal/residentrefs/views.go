package residentrefs

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

const maxJournalIO = 1 << 20

type JournalView struct {
	self      *JournalView
	recipient *recipientState
	entry     *originalEntry
}
type ImageView struct {
	self      *ImageView
	recipient *recipientState
	entry     *originalEntry
}
type ServiceIdentities struct{ Home, Lock, Journal Identity }
type ImageIdentities struct{ Image, Images, Directory Identity }
type FenceIdentities struct{ Launch, Intent, Home, Daemon, Directory, Ledger Identity }
type RecipientInspection struct {
	Registered, Materialized                          int
	Delegated, MaterializeAttempted, OperationPending bool
	FirstError                                        error
	NativeOutcome                                     string
}

func (r *ServiceOriginal) ClaimJournalOriginalView() (*JournalView, error) {
	if r == nil || r.self != r || r.state == nil {
		return nil, ErrOriginal
	}
	return r.state.claimJournal(r, 2)
}
func (r *FenceOriginal) ClaimIntentOriginalView() (*JournalView, error) {
	if r == nil || r.self != r || r.state == nil {
		return nil, ErrOriginal
	}
	return r.state.claimJournal(r, 1)
}
func (r *recipientState) claimJournal(original any, index int) (*JournalView, error) {
	s := r.census
	if s == nil {
		return nil, ErrOriginal
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.original != original || r.firstError != nil || s.firstError != nil || s.active != nil || r.journal != nil || index >= r.count || r.entries[index].reservedRecipient != r {
		return nil, ErrOriginal
	}
	view := &JournalView{recipient: r, entry: r.entries[index]}
	view.self = view
	r.journal = view
	return view, nil
}
func (r *ImageOriginal) ClaimImageOriginalView() (*ImageView, error) {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil {
		return nil, ErrOriginal
	}
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.state.original != r || r.state.firstError != nil || s.firstError != nil || s.active != nil || r.state.imageView != nil {
		return nil, ErrOriginal
	}
	view := &ImageView{recipient: r.state, entry: r.state.entries[0]}
	view.self = view
	r.state.imageView = view
	return view, nil
}
func (v *JournalView) original() bool {
	return v != nil && v.self == v && v.recipient != nil && v.recipient.journal == v && v.entry != nil && v.entry.reservedRecipient == v.recipient
}
func (v *ImageView) original() bool {
	return v != nil && v.self == v && v.recipient != nil && v.recipient.imageView == v && v.entry != nil && v.entry.reservedRecipient == v.recipient
}
func (r *recipientState) beginView(ctx context.Context, kind string, entry *originalEntry) (*originalOperation, error) {
	if r == nil || r.census == nil || entry == nil {
		return nil, ErrOriginal
	}
	s := r.census
	s.mu.Lock()
	valid := entry.recipient == r && entry.reservedRecipient == r && entry.file != nil && !entry.closeTried
	s.mu.Unlock()
	if !valid {
		return nil, ErrOriginal
	}
	op, err := s.self.begin(ctx, kind, r, r.original)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	op.entry = entry
	s.mu.Unlock()
	return op, nil
}
func (r *recipientState) stat(ctx context.Context, entry *originalEntry) (info os.FileInfo, resultErr error) {
	op, err := r.beginView(ctx, "stat", entry)
	if err != nil {
		return nil, err
	}
	s := r.census
	normal := false
	defer s.finish(op, &normal, &resultErr)
	resultErr = s.before(op, "original_borrowed_stat_pending")
	if resultErr == nil {
		info, resultErr = entry.file.Stat()
	}
	if resultErr == nil && !os.SameFile(entry.info, info) {
		resultErr = ErrOriginal
	}
	normal = true
	return info, resultErr
}
func (v *JournalView) StatOriginal(ctx context.Context) (os.FileInfo, error) {
	if !v.original() {
		return nil, ErrOriginal
	}
	return v.recipient.stat(ctx, v.entry)
}
func (v *ImageView) StatOriginal(ctx context.Context) (os.FileInfo, error) {
	if !v.original() {
		return nil, ErrOriginal
	}
	return v.recipient.stat(ctx, v.entry)
}
func (r *recipientState) readAt(ctx context.Context, entry *originalEntry, buffer []byte, offset int64) (read int, resultErr error) {
	op, err := r.beginView(ctx, "read_at", entry)
	if err != nil {
		return 0, err
	}
	s := r.census
	normal := false
	defer s.finish(op, &normal, &resultErr)
	if len(buffer) == 0 || len(buffer) > maxJournalIO || offset < 0 {
		resultErr = ErrOriginal
	} else {
		op.bytes, op.offset, op.requested = buffer, offset, len(buffer)
		resultErr = s.before(op, "original_borrowed_read_pending")
		if resultErr == nil {
			read, resultErr = entry.file.ReadAt(buffer, offset)
			s.mu.Lock()
			op.returned, op.ioError = read, resultErr
			s.mu.Unlock()
		}
	}
	normal = true
	return read, resultErr
}
func (v *JournalView) ReadAtOriginal(ctx context.Context, buffer []byte, offset int64) (int, error) {
	if !v.original() {
		return 0, ErrOriginal
	}
	return v.recipient.readAt(ctx, v.entry, buffer, offset)
}
func (v *ImageView) ReadAtOriginal(ctx context.Context, buffer []byte, offset int64) (int, error) {
	if !v.original() {
		return 0, ErrOriginal
	}
	return v.recipient.readAt(ctx, v.entry, buffer, offset)
}
func (v *JournalView) WriteAtOriginal(ctx context.Context, buffer []byte, offset int64) (written int, resultErr error) {
	if !v.original() {
		return 0, ErrOriginal
	}
	r := v.recipient
	s := r.census
	op, err := r.beginView(ctx, "write_at", v.entry)
	if err != nil {
		return 0, err
	}
	normal := false
	defer s.finish(op, &normal, &resultErr)
	if len(buffer) == 0 || len(buffer) > maxJournalIO || offset < 0 || offset > maxJournalIO-int64(len(buffer)) {
		resultErr = ErrOriginal
	} else {
		// Own the exact write bytes before the original native call. An interrupted
		// or partial write retains that same operation and bytes, never a retry.
		op.bytes, op.offset, op.requested = append([]byte(nil), buffer...), offset, len(buffer)
		resultErr = s.before(op, "original_borrowed_write_pending")
		if resultErr == nil {
			written, resultErr = v.entry.file.WriteAt(op.bytes, offset)
			s.mu.Lock()
			op.returned, op.ioError = written, resultErr
			s.mu.Unlock()
		}
	}
	normal = true
	return written, resultErr
}
func (v *JournalView) SyncOriginal(ctx context.Context) (resultErr error) {
	if !v.original() {
		return ErrOriginal
	}
	r := v.recipient
	s := r.census
	op, err := r.beginView(ctx, "sync", v.entry)
	if err != nil {
		return err
	}
	normal := false
	defer s.finish(op, &normal, &resultErr)
	resultErr = s.before(op, "original_borrowed_sync_pending")
	if resultErr == nil {
		resultErr = v.entry.file.Sync()
	}
	normal = true
	return resultErr
}
func (r *recipientState) identities(ctx context.Context, original any) (values [6]Identity, resultErr error) {
	if r == nil || r.census == nil {
		return values, ErrOriginal
	}
	s := r.census
	op, err := s.self.begin(ctx, "validate", r, original)
	if err != nil {
		return values, err
	}
	normal := false
	defer s.finish(op, &normal, &resultErr)
	for at, entry := range r.entries[:r.count] {
		if resultErr = s.before(op, "original_recipient_identity_validation"); resultErr != nil {
			break
		}
		var st unix.Stat_t
		if resultErr = unix.Fstat(entry.number, &st); resultErr != nil {
			break
		}
		values[at] = Identity{uint64(st.Dev), uint64(st.Ino), st.Uid, st.Gid, uint32(st.Mode)}
		if values[at] != entry.identity {
			resultErr = ErrOriginal
			break
		}
		if resultErr = validateFixedKind(entry.number, &st); resultErr != nil {
			break
		}
	}
	normal = true
	return values, resultErr
}
func (r *ServiceOriginal) ValidateOriginal(ctx context.Context) (value ServiceIdentities, err error) {
	if r == nil || r.self != r {
		return value, ErrOriginal
	}
	ids, err := r.state.identities(ctx, r)
	return ServiceIdentities{ids[0], ids[1], ids[2]}, err
}
func (r *FenceOriginal) ValidateOriginal(ctx context.Context) (value FenceIdentities, err error) {
	if r == nil || r.self != r {
		return value, ErrOriginal
	}
	ids, err := r.state.identities(ctx, r)
	return FenceIdentities{ids[0], ids[1], ids[2], ids[3], ids[4], ids[5]}, err
}
func (r *ImageOriginal) ValidateOriginal(ctx context.Context) (value ImageIdentities, err error) {
	if r == nil || r.self != r {
		return value, ErrOriginal
	}
	ids, err := r.state.identities(ctx, r)
	return ImageIdentities{ids[0], ids[1], ids[2]}, err
}
func (r *recipientState) inspect(original any) RecipientInspection {
	if r == nil || r.census == nil {
		return RecipientInspection{FirstError: ErrOriginal, NativeOutcome: "unknown"}
	}
	s := r.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.original != original {
		return RecipientInspection{FirstError: ErrOriginal, NativeOutcome: "unknown"}
	}
	failure := r.firstError
	if failure == nil {
		failure = s.firstError
	}
	value := RecipientInspection{Registered: r.count, MaterializeAttempted: r.materializeTried, FirstError: failure, NativeOutcome: "unknown", Delegated: s.delegated, OperationPending: s.active != nil}
	for _, entry := range r.entries[:r.count] {
		if entry.file != nil {
			value.Materialized++
		}
	}
	return value
}
func (r *ServiceOriginal) InspectOriginal() RecipientInspection {
	if r == nil || r.self != r {
		return RecipientInspection{FirstError: ErrOriginal, NativeOutcome: "unknown"}
	}
	return r.state.inspect(r)
}
