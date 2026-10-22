package database

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
)

func validSSHHistoryEvidence(n NamespaceEvidence) error {
	if n.Name != sourceHistoryProofName {
		if n.SSHHistory != nil {
			return fmt.Errorf("source history evidence on another namespace")
		}
		return nil
	}
	if !n.Present {
		if n.SSHHistory != nil || len(n.Files) != 0 {
			return fmt.Errorf("absent source history has evidence")
		}
		return nil
	}
	e := n.SSHHistory
	if e == nil || e.Packs == nil || len(e.Packs) > 256 || len(n.Files) != 2 || n.Files[0].Name != ".worker.lock" || n.Files[0].Size != 0 || n.Files[1].Name != "catalog.db" || n.Files[1].Size < 1 || n.Files[1].Size > 64<<20 || e.PacksRoot.Name != "packs" || e.Bytes < 1 || e.Bytes > sourceHistoryProofBytes {
		return fmt.Errorf("source history protected evidence incomplete/bounded")
	}
	owner := fmt.Sprint(n.Identity.Owner)
	if !historyProofIdentity(historyProofID(n.Identity), owner, true) || !historyProofIdentity(historyProofID(e.PacksRoot), owner, true) {
		return fmt.Errorf("source history protected directory unsafe")
	}
	var bytes int64
	for _, v := range n.Files {
		if !v.Present || !historyProofIdentity(historyProofID(v), owner, false) || !validDigest(v.SHA256) {
			return fmt.Errorf("source history protected root file unsafe")
		}
		bytes += v.Size
	}
	if e.Lock.Name != ".historypack.lock" || len(e.Packs) > 0 && !e.Lock.Present || e.Lock.Present && (!historyProofIdentity(historyProofID(e.Lock), owner, false) || e.Lock.Size != 0 || e.Lock.SHA256 != hashEmptyResetFile) {
		return fmt.Errorf("source history protected publication lock unsafe")
	}
	last := ""
	for _, p := range e.Packs {
		name := p.Directory.Name
		if name <= last || !strings.HasPrefix(name, "pack-") || !validHost(strings.TrimPrefix(name, "pack-")) || !historyProofIdentity(historyProofID(p.Directory), owner, true) || len(p.Files) != 2 || p.Files[0].Name != "manifest.json" || p.Files[1].Name != "records.bin" {
			return fmt.Errorf("source history protected pack roster invalid")
		}
		var size int64
		for _, v := range p.Files {
			if !v.Present || !historyProofIdentity(historyProofID(v), owner, false) || v.Size < 1 || v.Size > 3<<20 || !validDigest(v.SHA256) {
				return fmt.Errorf("source history protected pack file invalid")
			}
			size += v.Size
		}
		if size > 3<<20 {
			return fmt.Errorf("source history protected pack combined size exceeds bound")
		}
		bytes += size
		last = name
	}
	if bytes != e.Bytes {
		return fmt.Errorf("source history protected evidence byte count differs")
	}
	return nil
}

// Rebase is allowed only for the exact reviewed whole-directory rename. Both
// paths preserve file identities/bytes; this never adopts replacement history.
func checkSSHHistoryEvidencePath(ctx context.Context, path string, n NamespaceEvidence) error {
	if e := validSSHHistoryEvidence(n); e != nil {
		return e
	}
	d, e := OpenDirectory(path)
	if !n.Present {
		if !os.IsNotExist(e) {
			if d != nil {
				d.Close()
			}
			return fmt.Errorf("unreviewed source history appeared")
		}
		return nil
	}
	if e != nil {
		return e
	}
	defer d.Close()
	if !sameIdentity(d.identity, n.Identity) {
		return fmt.Errorf("protected source history root identity changed")
	}
	m := proofHistoryMarker{Directory: historyProofID(n.Identity), Catalog: historyProofID(n.Files[1]), WorkerLock: historyProofID(n.Files[0]), Packs: historyProofID(n.SSHHistory.PacksRoot)}
	now, files, e := collectSSHHistoryEvidence(ctx, d, m)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(files, n.Files) || now.Bytes != n.SSHHistory.Bytes || !sameIdentity(now.PacksRoot, n.SSHHistory.PacksRoot) || !reflect.DeepEqual(now.Lock, n.SSHHistory.Lock) || len(now.Packs) != len(n.SSHHistory.Packs) {
		return fmt.Errorf("protected source history files/roster changed")
	}
	for i, p := range now.Packs {
		old := n.SSHHistory.Packs[i]
		if p.Directory.Name != old.Directory.Name || !sameIdentity(p.Directory, old.Directory) || !reflect.DeepEqual(p.Files, old.Files) {
			return fmt.Errorf("protected source pack identity/bytes changed")
		}
	}
	return ctx.Err()
}
