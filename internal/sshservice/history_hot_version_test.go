package sshservice

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceHistoryHotVersionLossBeforeUnlinkPreservesChangedEvidence(t *testing.T) {
	for _, kind := range []string{"same-inode-restored-mtime", "replaced-inode"} {
		t.Run(kind, func(t *testing.T) {
			home, b, items := settledSourceHistoryFixture(t, 1)
			q := sourceArchiveRequest(t, home, b, "hot-changed")
			path := filepath.Join(home, "mesh", completionName(items[0]))
			fired := false
			var changed []byte
			_, e := archiveSourceHistory(t.Context(), home, b, q, func(point string) error {
				if point == "before_unlink" && !fired {
					fired = true
					if kind == "same-inode-restored-mtime" {
						corruptHistoryFileSameIdentity(t, path, true)
					} else {
						raw, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						if err = os.Rename(path, path+".retained-original"); err != nil {
							t.Fatal(err)
						}
						raw[0] ^= 255
						if err = os.WriteFile(path, raw, 0600); err != nil {
							t.Fatal(err)
						}
					}
					var err error
					changed, err = os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
				}
				return nil
			})
			if !fired {
				t.Fatal("actual hot unlink boundary not reached")
			}
			if e == nil {
				t.Fatal("changed hot receipt was deleted against stale verification")
			}
			retained, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(retained, changed) {
				t.Fatal("changed hot evidence not retained", err)
			}
		})
	}
}
