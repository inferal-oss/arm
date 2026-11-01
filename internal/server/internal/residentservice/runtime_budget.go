package residentservice

// Pure componentwise lower-bound comparison. Counts and bytes are logical
// application requirements, never a native/RSS measurement or a funding grant.
func runtimeBoundsSufficientData(available, required Bounds) error {
	if available.Tasks < required.Tasks || available.Descriptors < required.Descriptors || available.Children < required.Children || available.Sockets < required.Sockets || available.NativePending < required.NativePending || available.IOBytes < required.IOBytes || available.RetainedReceiptBytes < required.RetainedReceiptBytes {
		return ErrInvalid
	}
	return nil
}

// CheckOriginalRuntimeBounds checks this SAME original nonretiring charge. It
// returns no grant, claim, copied profile or native capability. Measured opaque
// costs remain additional; satisfying a finite logical floor cannot prove them.
func (c *ManagerCharge) CheckOriginalRuntimeBounds(required Bounds) error {
	if c == nil || c.owner == nil || c.child == nil || c.child.owner != c.owner {
		return ErrInvalid
	}
	// ObserveOriginal already takes owner.mu and then the original reference
	// census lock. Never invoke it recursively while holding owner.mu.
	observed, err := c.ObserveOriginal()
	if err != nil {
		return err
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.child != c.child || o.charge != c || o.grant == nil || o.debit == nil || o.p1Child == nil || o.active != nil || o.firstError != nil || !o.prepared || !o.runtimeBound || !observed.AdmissionAvailable || o.debit.validateOriginalLocked(o) != nil || observed.DebitID != o.debit.record.ID || observed.ProfileSHA256 != o.debit.record.ProfileSHA256 || observed.Resources != o.grant.Intent.Profile.Resources {
		return ErrInvalid
	}
	if o.parent != nil || o.p1 != nil || len(o.journal.records) != 2 || o.journal.records[0].Grant != o.grant || o.grant.Intent.Home != o.path || o.grant.Intent.HomeID != observed.HomeID || o.debit.record.GrantID != o.grant.ID || o.debit.record.GrantRevision != o.grant.Revision {
		return ErrInvalid
	}
	profile := o.grant.Intent.Profile
	if profile.Schema != 2 || profile.Validate() != nil || profile.RequireP1Environment() != nil {
		return ErrInvalid
	}
	digest, err := profile.SHA256()
	if err != nil || digest != observed.ProfileSHA256 {
		return ErrInvalid
	}
	return runtimeBoundsSufficientData(profile.Bounds, required)
}
