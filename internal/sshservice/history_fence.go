package sshservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/inferal-oss/arm/internal/historypack"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

type sourceHistoryVersion struct {
	Identity          historypack.Identity
	Size              int64
	Modified, Changed [2]int64
}

func historyVersion(path, name string) (sourceHistoryVersion, error) {
	return historyVersionBound(path, name, SourceHistoryMaxPackBytes)
}
func historyVersionBound(path, name string, limit int64) (sourceHistoryVersion, error) {
	d, e := privateDirectory(path)
	if e != nil {
		return sourceHistoryVersion{}, e
	}
	defer d.Close()
	v, e := historyVersionAt(d, name, limit)
	if e != nil {
		return sourceHistoryVersion{}, e
	}
	return v, validateDirectory(d, path)
}

// Caller retains and rechecks the directory's exact named binding. This reads
// the same bounded single-link identity and native mtime/ctime evidence without
// reopening its entire absolute ancestor chain for each sibling file.
func historyVersionAt(d *os.File, name string, limit int64) (sourceHistoryVersion, error) {
	id, size, e := sourceFileIdentity(d, name, limit)
	if e != nil {
		return sourceHistoryVersion{}, e
	}
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return sourceHistoryVersion{}, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return sourceHistoryVersion{}, e
	}
	actual := historypack.Identity{Device: uint64(st.Dev), Inode: st.Ino, Owner: st.Uid, Mode: uint32(st.Mode)}
	if actual != id || st.Size != size || st.Nlink != 1 {
		return sourceHistoryVersion{}, fmt.Errorf("source pack version identity changed")
	}
	modified, changed := historyStatTimes(&st)
	return sourceHistoryVersion{id, size, modified, changed}, nil
}

type sourceHistoryFence struct {
	Marker            SourceHistoryMarker
	Ref               SourceHistoryRef
	Pack              SourceHistoryPack
	RequestSHA256     string
	Records, Manifest sourceHistoryVersion
}

// A retained positive tuple is a conservative execution fence. This fast path
// never hashes pack bytes under source locks, and missing/corrupt evidence is
// an error rather than an absent/reusable key or reclaimed capacity.
func sourceFenceMetadata(ctx context.Context, c *historyCatalog, target, key string) (*sourceHistoryFence, error) {
	ref, e := c.ref(ctx, target, key)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	pack, e := c.pack(ctx, ref.Pack)
	if e != nil {
		return nil, e
	}
	op, e := c.lookupOp(ctx, pack.Operation)
	if e != nil {
		return nil, e
	}
	if !((op.Phase == "committed" || op.Phase == "completed") && op.Receipt != nil && *op.Receipt == pack.Receipt && pack.Members == len(op.Request.Plan.Members)) {
		return nil, fmt.Errorf("source key lacks committed archive fence")
	}
	member := false
	for _, m := range op.Request.Plan.Members {
		if m.Source == ref.Source && m.Target == ref.Target && m.Key == ref.Key && m.Kind == ref.Kind && m.SHA256 == ref.SHA256 && m.IntentSHA256 == ref.IntentSHA256 && sourceHistoryKey(m) == ref.Record {
			member = true
		}
	}
	if !member {
		return nil, fmt.Errorf("source fence lost original reviewed member")
	}
	if e = c.checkPackIdentity(pack.Receipt); e != nil {
		return nil, e
	}
	path := filepath.Join(c.path, "packs", "pack-"+pack.Receipt.ID)
	records, e := historyVersion(path, "records.bin")
	if e != nil {
		return nil, e
	}
	manifest, e := historyVersion(path, "manifest.json")
	if e != nil {
		return nil, e
	}
	return &sourceHistoryFence{c.marker, ref, pack, op.RequestSHA256, records, manifest}, c.guard()
}
func sourceStateFence(ctx context.Context, s *State, target, key string) (*sourceHistoryFence, error) {
	if s.historyMarker == nil {
		return nil, nil
	}
	c, e := openHistoryCatalog(ctx, s.home, s.Binding, *s.historyMarker, false, false, nil)
	if e != nil {
		return nil, e
	}
	defer c.close()
	return sourceFenceMetadata(ctx, c, target, key)
}

type verifiedSourceHistory struct {
	Absence *verifiedSourceAbsence
	Fence   sourceHistoryFence
	Lookup  SourceHistoryLookup
	Payload []byte
}

// Full byte/family verification precedes acquiring lifecycle/SSH locks. The
// caller rechecks the exact immutable ref/op and native pack evidence under its
// short mutation lock; it never treats this snapshot as a new execution permit.
func preverifySourceHistory(ctx context.Context, home string, b Binding, target, key string) (*verifiedSourceHistory, error) {
	s, e := ReadState(ctx, home, b)
	if e != nil {
		return nil, e
	}
	if s.historyMarker == nil {
		return &verifiedSourceHistory{Absence: &verifiedSourceAbsence{}}, nil
	}
	c, e := openHistoryCatalog(ctx, home, b, *s.historyMarker, false, false, nil)
	if e != nil {
		return nil, e
	}
	defer c.close()
	before, e := sourceFenceMetadata(ctx, c, target, key)
	if e != nil {
		return nil, e
	}
	if before == nil {
		version, e := verifySourceCatalogAbsence(ctx, c, target, key)
		if e != nil {
			return nil, e
		}
		marker := *s.historyMarker
		return &verifiedSourceHistory{Absence: &verifiedSourceAbsence{Marker: &marker, Version: version}}, nil
	}
	result, raw, e := sourceHistoryLookupPayload(ctx, c, target, key)
	if e != nil {
		return nil, e
	}
	if !result.Archived {
		return nil, fmt.Errorf("positive source fence lost its immutable payload")
	}
	if e = checkSourceHistoryOverlap(s, result, raw); e != nil {
		return nil, e
	}
	f, e := sourceFenceMetadata(ctx, c, target, key)
	if e != nil {
		return nil, e
	}
	if f == nil || *f != *before || f.Ref.SHA256 != stateHash(raw) {
		return nil, fmt.Errorf("verified source payload/fence changed")
	}
	return &verifiedSourceHistory{Fence: *f, Lookup: result, Payload: raw}, nil
}
func recheckVerifiedSourceHistory(ctx context.Context, s *State, v *verifiedSourceHistory) error {
	if v == nil {
		return fmt.Errorf("verified source payload required")
	}
	if v.Absence != nil {
		return v.Absence.check(s)
	}
	now, e := sourceStateFence(ctx, s, v.Fence.Ref.Target, v.Fence.Ref.Key)
	if e != nil {
		return e
	}
	if now == nil || *now != v.Fence {
		return fmt.Errorf("verified source archive namespace/ref/op changed")
	}
	// The lock acquisition loaded the current hot/pending roster. An identical
	// retained copy may have changed after off-lock verification; compare it to
	// the already verified bounded payload, without reading a full pack here.
	return checkSourceHistoryOverlap(s, v.Lookup, v.Payload)
}
