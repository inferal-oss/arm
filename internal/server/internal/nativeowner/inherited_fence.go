package nativeowner

import (
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
)

// Actual P1 constructor: derives the path from the concrete verified inherited
// fence and retains that owner before ANY native issuer open. No caller-provided
// birth, approved bool, FD or metadata-only fence can use this path.
func NewInheritedServePending(destination **ServeIssuer, fence *residentbirth.InheritedFencePending) error {
	if destination == nil || *destination != nil || fence == nil {
		return errNotOriginal
	}
	data, err := fence.NativeSelectorsOriginal() // pure selectors; no native observation
	if err != nil {
		return err
	}
	if err = newServePending(destination, data.Home); err != nil {
		return err
	}
	issuer := *destination
	issuer.owner.inheritedFence = fence
	issuer.owner.inheritedIDs = data.Identities
	return nil
}
func sameInheritedIdentity(actual nativeIdentity, expected residentrefs.Identity) bool {
	return actual.device == expected.Device && actual.inode == expected.Inode && actual.uid == expected.UID && actual.gid == expected.GID && actual.mode == expected.Mode
}
func (o *nativeOwner) matchInheritedDirectoryOriginal() error {
	if o.inheritedFence == nil {
		return nil
	}
	home, ids, err := o.inheritedFence.ValidateForOriginalNative()
	if err != nil {
		return err
	}
	if home != o.state.path || o.state.directory == nil || !sameInheritedIdentity(o.state.directory.identity, ids.Home) {
		return errNotOriginal
	}
	return nil
}
func (o *nativeOwner) matchInheritedOriginal() error {
	if o.inheritedFence == nil {
		return nil
	}
	home, ids, err := o.inheritedFence.ValidateForOriginalNative()
	if err != nil {
		return err
	}
	if home != o.state.path || o.state.directory == nil || o.state.lock == nil || !sameInheritedIdentity(o.state.directory.identity, ids.Home) || !sameInheritedIdentity(o.state.lock.identity, ids.Daemon) {
		return errNotOriginal
	}
	return nil
}

// Called by the SAME original database Connector after its actual attempt is
// registered and before official physical Connect. This is not a new grant and
// does not reopen a ledger, acquire another lock or infer native closure.
func (c *ManagerClaim) ValidateOriginalDatabaseFence() (snapshot Snapshot, resultErr error) {
	if c == nil || c.owner == nil {
		return snapshot, errNotOriginal
	}
	o := c.owner
	o.mu.Lock()
	if c.view == nil || o.manager != c.view || c.view.claim != c {
		o.mu.Unlock()
		return snapshot, errNotOriginal
	}
	op, err := o.beginLocked("validate_actual_inherited_database_fence")
	o.mu.Unlock()
	if err != nil {
		return snapshot, err
	}
	normal := false
	defer o.finishEffect(op, &normal, &resultErr)
	o.state.progress("actual_inherited_ledger_before_official_Connect")
	resultErr = o.state.validateLocked()
	if resultErr == nil {
		resultErr = o.matchInheritedOriginal()
	}
	if resultErr == nil && o.inheritedFence != nil {
		resultErr = o.inheritedFence.ValidateForOriginalDatabase()
	}
	if resultErr == nil {
		o.mu.Lock()
		o.publishNativeLocked()
		snapshot = o.snapshotLocked()
		o.mu.Unlock()
	}
	normal = true
	return snapshot, resultErr
}

// Actual Root linkage before the inherited-view native validation. A matching
// serialized fence/ID cannot stand in for the SAME original inherited owner.
func (h *ServeIssuer) ValidateInheritedOriginalViews(manager *ManagerView, server *ServerView, fence *residentbirth.InheritedFencePending) (Snapshot, error) {
	if h == nil || h.owner == nil || fence == nil {
		return Snapshot{}, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	valid := o.original == h && o.inheritedFence == fence
	o.mu.Unlock()
	if !valid {
		return Snapshot{}, errNotOriginal
	}
	return h.ValidateOriginalViews(manager, server)
}
