package sshservice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceHistoryAbsenceGuardRechecksClosedNamespaceAfterStage(t *testing.T) {
	for _, change := range []string{"root-member", "packs-member", "pack-member", "sidecar", "packs-replaced", "root-replaced"} {
		t.Run(change, func(t *testing.T) {
			home, b, items := settledSourceHistoryFixture(t, 1)
			op, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "namespace-version"))
			if e != nil {
				t.Fatal(e)
			}
			verified, e := preverifySourceAbsence(t.Context(), home, b, items[0].Request.Pin.Target, "unused-key")
			if e != nil {
				t.Fatal(e)
			}
			next := sourceFixture(t, home, b, items[0].Request.Pin, "unused-key")
			defer os.Remove(next.Directory.Path)
			root := filepath.Join(home, SourceHistoryName)
			fired := false
			e = withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error {
				bindSourceAbsenceGuard(s, verified)
				s.syncFile = func(f *os.File) error {
					if e := f.Sync(); e != nil {
						return e
					}
					fired = true
					switch change {
					case "root-member":
						return os.WriteFile(filepath.Join(root, "unfamiliar-evidence"), []byte("retain"), 0600)
					case "packs-member":
						return os.Mkdir(filepath.Join(root, "packs", "unfamiliar-evidence"), 0700)
					case "pack-member":
						return os.WriteFile(filepath.Join(root, "packs", "pack-"+op.Receipt.ID, "unfamiliar-evidence"), []byte("retain"), 0600)
					case "sidecar":
						return os.WriteFile(filepath.Join(root, SourceHistoryCatalogName+"-journal"), []byte("retain"), 0600)
					case "packs-replaced":
						if e := os.Rename(filepath.Join(root, "packs"), filepath.Join(root, "packs-retained")); e != nil {
							return e
						}
						return os.Mkdir(filepath.Join(root, "packs"), 0700)
					default:
						if e := os.Rename(root, root+"-retained"); e != nil {
							return e
						}
						return os.Mkdir(root, 0700)
					}
				}
				raw, e := json.Marshal(next)
				if e != nil {
					return e
				}
				return atomicStateFile(t.Context(), s, d, intentName(next), raw, nil)
			})
			if !fired || e == nil {
				t.Fatal("changed closed namespace allowed source publication", e)
			}
			if _, e = os.Lstat(filepath.Join(home, "mesh", intentName(next))); !os.IsNotExist(e) {
				t.Fatal("new intent appeared after history namespace changed", e)
			}
			// Failed verification preserves both original pack and unfamiliar evidence.
			retained := root
			if change == "root-replaced" {
				retained += "-retained"
			}
			packRoot := filepath.Join(retained, "packs")
			if change == "packs-replaced" {
				packRoot = filepath.Join(retained, "packs-retained")
			}
			if _, e = os.Stat(filepath.Join(packRoot, "pack-"+op.Receipt.ID, "records.bin")); e != nil {
				t.Fatal("original history evidence disappeared", e)
			}
		})
	}
}
