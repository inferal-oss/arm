package sshservice

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceHistoryRecoveryRechecksAfterCompletionStageFsync(t *testing.T) {
	for _, change := range []string{"pack-content", "namespace", "worker"} {
		t.Run(change, func(t *testing.T) {
			home, b, _ := settledSourceHistoryFixture(t, 1)
			op, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "recovery-stage"))
			if e != nil {
				t.Fatal(e)
			}
			crashSourceHistoryCatalog(t, home)
			q, e := PlanSourceHistoryRecovery(t.Context(), home, b, "recovery-stage", "Inspect real crashed journal and preserve current history")
			if e != nil {
				t.Fatal(e)
			}
			fired := false
			_, e = recoverSourceHistoryCatalog(t.Context(), home, b, q, func(stage string) error {
				if stage != "after_recovery_stage_fsync" {
					return nil
				}
				fired = true
				root := filepath.Join(home, SourceHistoryName)
				switch change {
				case "pack-content":
					path := filepath.Join(root, "packs", "pack-"+op.Receipt.ID, "records.bin")
					before, e := os.Stat(path)
					if e != nil {
						return e
					}
					raw, e := os.ReadFile(path)
					if e != nil {
						return e
					}
					raw[len(raw)-1] ^= 1
					if e = os.WriteFile(path, raw, 0600); e != nil {
						return e
					}
					return os.Chtimes(path, before.ModTime(), before.ModTime())
				case "namespace":
					return os.Mkdir(filepath.Join(root, "packs", "unfamiliar-evidence"), 0700)
				default:
					path := filepath.Join(root, sourceHistoryWorkerLockName)
					if e := os.Rename(path, path+"-retained"); e != nil {
						return e
					}
					return os.WriteFile(path, nil, 0600)
				}
			})
			if !fired {
				t.Fatal("actual recovery completion stage fsync was not reached", e)
			}
			if e == nil {
				t.Error("changed evidence published a completed recovery result")
			}
			saved, _, readErr := readSourceRecovery(t.Context(), home, q.Marker)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if saved == nil || !saved.Pending() || saved.Attempts[len(saved.Attempts)-1].Completed != nil {
				t.Fatal("recovery no longer preserves its pending obligation")
			}
		})
	}
}
