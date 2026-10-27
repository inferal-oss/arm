package server

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
	"golang.org/x/sys/unix"
)

func TestWorkspaceFormatServeAndRestartRefuseBeforeStoppedMutation(t *testing.T) {
	for _, change := range []string{"format-2", "blob-worker", "blob-ref", "blob-ordinary"} {
		for _, pathKind := range []string{"serve", "restart-cas"} {
			t.Run(change+"/"+pathKind, func(t *testing.T) {
				home, record, state := stoppedLedger(t)
				path := filepath.Join(home, "state.db")
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(map[string]string{
					"format-2":      `INSERT INTO metadata VALUES('workspace_format','2')`,
					"blob-worker":   `INSERT INTO leases VALUES('held','original',CAST('{"request":{"kind":"provider-prepare-worker"},"state":"active"}' AS BLOB),NULL)`,
					"blob-ref":      `INSERT INTO leases VALUES('held','original',CAST('{"request":{"kind":"storage"},"state":"active","provider_preparation":null}' AS BLOB),NULL)`,
					"blob-ordinary": `INSERT INTO leases VALUES('held','original',CAST('{"request":{"kind":"execution"},"state":"released"}' AS BLOB),NULL)`,
				}[change]); err != nil {
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if pathKind == "serve" {
					err = Serve(t.Context(), home, "127.0.0.1:0", model.Resources{CPU: 4})
				} else {
					lock, e := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0600)
					if e != nil {
						t.Fatal(e)
					}
					defer lock.Close()
					if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
						t.Fatal(e)
					}
					defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
					q, e := nodestop.ReviewRestart(state, record, "reviewed restart refusal fixture", time.Now())
					if e != nil {
						t.Fatal(e)
					}
					// A genuinely reviewed restart under the actual authority lock
					// must refuse before CAS, not just fail a malformed request.
					err = applyNodeStopRestart(t.Context(), home, state, q)
				}
				after, readErr := os.ReadFile(path)
				if err == nil || !strings.Contains(err.Error(), "workspace_format_unsupported") {
					t.Fatalf("format guard did not precede stopped-state interpretation: state_file_changed=%t error=%v", readErr == nil && !bytes.Equal(before, after), err)
				}
				if readErr != nil || !bytes.Equal(before, after) {
					t.Fatal("unsupported database changed", readErr)
				}
				if _, err := os.Lstat(filepath.Join(home, "endpoint.json")); !os.IsNotExist(err) {
					t.Fatal("unsupported authority published an endpoint", err)
				}
			})
		}
	}
}
