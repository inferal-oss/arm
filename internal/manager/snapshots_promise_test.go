package manager

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/model"
)

func snapshotPhysicalPromises(t *testing.T, home string, ids ...string) string {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(home, "state.db")}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := db.Close(); e != nil {
			t.Error(e)
		}
	}()
	var result [][]string
	for _, id := range ids {
		rows, err := db.QueryContext(t.Context(), `SELECT typeof(id),id,typeof(request_key),hex(request_key),typeof(data),hex(data),typeof(run),hex(run) FROM leases WHERE id=? OR id=CAST(? AS BLOB) ORDER BY typeof(id) LIMIT 3`, id, id)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var idType, storedID, keyType, keyHex, dataType, dataHex, runType, runHex string
			if err = rows.Scan(&idType, &storedID, &keyType, &keyHex, &dataType, &dataHex, &runType, &runHex); err != nil {
				break
			}
			count++
			result = append(result, []string{idType, storedID, keyType, keyHex, dataType, dataHex, runType, runHex})
		}
		rowsErr, closeErr := rows.Err(), rows.Close()
		if err != nil || rowsErr != nil || closeErr != nil || count < 1 || count > 2 {
			t.Fatal("bounded physical promise roster", count, err, rowsErr, closeErr)
		}

	}
	raw, e := json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

func TestSnapshotReservationPromiseSQLTypesRefuseEveryReader(t *testing.T) {
	for _, role := range []string{"work", "destination"} {
		t.Run(role, func(t *testing.T) {
			for _, form := range []string{"request-key-blob", "shadow-id-blob"} {
				t.Run(form, func(t *testing.T) {
					for _, entry := range []string{"lookup", "cancel", "startup", "database-proof"} {
						t.Run(entry, func(t *testing.T) {
							f := newSnapshotFixture(t)
							q := snapshotRequest(f, "promise-sql-type")
							r, e := f.m.reserveSnapshot(t.Context(), q)
							if e != nil {
								t.Fatal(e)
							}
							id := r.WorkLease
							if role == "destination" {
								id = r.DestinationLease
							}
							if form == "request-key-blob" {
								if _, e = f.m.db.Exec(`UPDATE leases SET request_key=CAST(request_key AS BLOB) WHERE id=?`, id); e != nil {
									t.Fatal(e)
								}
								var kind string
								if e = f.m.db.QueryRow(`SELECT typeof(request_key) FROM leases WHERE id=?`, id).Scan(&kind); e != nil || kind != "blob" {
									t.Fatal("control did not create actual BLOB key", kind, e)
								}
							} else {
								// The legitimate TEXT row stays valid. An extra BLOB
								// id/key row has identical bytes and a real internal
								// kind/policy; it must not normalize to a claimed id.
								if _, e = f.m.db.Exec(`INSERT INTO leases(id,request_key,data,run) SELECT CAST(id AS BLOB),CAST(request_key AS BLOB),data,run FROM leases WHERE id=?`, id); e != nil {
									t.Fatal(e)
								}
								var count int
								if e = f.m.db.QueryRow(`SELECT count(*) FROM leases WHERE typeof(id)='blob' AND id=CAST(? AS BLOB)`, id).Scan(&count); e != nil || count != 1 {
									t.Fatal("actual shadow BLOB id absent", count, e)
								}
							}

							before := snapshotPhysicalPromises(t, f.home, r.WorkLease, r.DestinationLease)
							copyBefore := snapshotReadonlyCopy(t, f.home, r.ID)
							switch entry {
							case "lookup":
								_, e = f.m.lookupSnapshotRequest(t.Context(), q.Key)
								if e == nil {
									t.Fatal("exact lookup accepted BLOB promise key")
								}
								f.wantCloseError = true
							case "cancel":
								e = f.m.cancelSnapshotReservation(t.Context(), q.Key, 1, "cannot settle SQL type alias")
								if e == nil {
									t.Fatal("cancellation released a promise with BLOB key")
								}
								f.wantCloseError = true
							case "startup", "database-proof":
								if entry == "database-proof" {
									if e = os.WriteFile(filepath.Join(f.home, "daemon.lock"), nil, 0600); e != nil {
										t.Fatal(e)
									}
								}
								e = f.m.Close()
								f.closed = true // This original Close is owned exactly once.
								if e != nil {
									t.Fatal(e)
								}
								if entry == "startup" {
									if m, e := Open(f.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20}); e == nil {
										closeErr := m.Close()
										t.Fatal("startup accepted/repaired BLOB promise key", closeErr)
									}
								} else {
									s, e := database.Offline(t.Context(), f.home)
									if e != nil {
										t.Fatal(e)
									}
									proof, proofErr := database.ProveDatabaseSettled(t.Context(), s, f.m.owner)
									closeErr := s.Close()
									if closeErr != nil {
										t.Fatal(closeErr)
									}
									if proofErr == nil || proof.Complete || proof.Settled {
										t.Fatal("BLOB-key proof was complete rather than refusing", proof, proofErr)
									}
								}
							}
							if after := snapshotPhysicalPromises(t, f.home, r.WorkLease, r.DestinationLease); after != before {
								t.Fatal("refusal changed promise bytes/types/charges")
							}
							if snapshotReadonlyCopy(t, f.home, r.ID) != copyBefore {
								t.Fatal("refusal rewrote copy decision")
							}
							if _, e = os.Lstat(filepath.Join(f.home, "storage", r.DestinationLease)); !os.IsNotExist(e) {
								t.Fatal("refusal allocated destination", e)
							}
						})
					}
				})
			}
		})
	}
}
