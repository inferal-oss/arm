package database_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"getarm.org/internal/database"
)

func TestResidentWorkerFormatRefusesStartupProofAndReset(t *testing.T) {
	cases := map[string]string{
		"namespace":  `CREATE TABLE resident_worker_lifetimes(data)`,
		"attachment": `UPDATE leases SET data=json_set(data,'$.resident_worker',NULL)`,
		"worker":     `UPDATE leases SET data=json_set(data,'$.request.kind','resident-worker')`,
		"prepare":    `UPDATE leases SET data=json_set(data,'$.request.kind','resident-worker-prepare')`,
	}
	for name, setup := range cases {
		for _, marker := range []string{"absent", "1", "3"} {
			t.Run(name+"/"+marker, func(t *testing.T) {
				home := proofFixture(t, false)
				edit(t, home, setup)
				if marker != "absent" {
					edit(t, home, `INSERT INTO metadata VALUES('workspace_format',?)`, marker)
				}
				if marker == "3" {
					edit(t, home, `INSERT INTO metadata VALUES('storage_pools','[]')`)
				}
				before := snapshotTree(t, home)
				lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					lock.Close()
					t.Fatal(err)
				}
				read, _, readErr := database.OpenStartupRead(t.Context(), home, lock)
				if read != nil {
					read.Close()
				}
				unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
				closeErr := lock.Close()
				if read != nil || readErr == nil || !strings.Contains(readErr.Error(), "workspace_format_unsupported") || unlockErr != nil || closeErr != nil {
					t.Fatal("reserved state escaped original startup read refusal", readErr, unlockErr, closeErr)
				}
				p, err := prove(t, home)
				if err == nil || !strings.Contains(err.Error(), "workspace_format_unsupported") || p.Complete || p.Settled {
					t.Fatal("reserved state acquired a settled proof", err)
				}
				review, err := database.ReviewReset(t.Context(), home, "reserved worker refusal", false)
				if review != nil {
					review.Close()
				}
				if review != nil || err == nil || !strings.Contains(err.Error(), "workspace_format_unsupported") {
					t.Fatal("reserved state acquired reset review", err)
				}
				// StartupRead explicitly permits private SQLite bookkeeping. No
				// committed WAL data or other namespace change is permitted here.
				after := snapshotTree(t, home)
				for _, name := range []string{"state.db-shm", "state.db-wal"} {
					delete(before, name)
					delete(after, name)
				}
				if info, err := os.Stat(filepath.Join(home, "state.db-wal")); err != nil && !os.IsNotExist(err) || err == nil && info.Size() != 0 {
					t.Fatal("read refusal wrote WAL data", err)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("read/proof/reset refusal changed database or owned namespace bytes")
				}
			})
		}
	}
}
