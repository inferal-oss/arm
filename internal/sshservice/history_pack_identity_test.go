package sshservice

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceArchiveHeldPackRechecksNativeBindings(t *testing.T) {
	for _, change := range []string{"root-replaced", "pack-replaced", "pack-symlink", "pack-permissions", "record-hardlink", "extra-entry"} {
		t.Run(change, func(t *testing.T) {
			home, binding, _ := settledSourceHistoryFixture(t, 1)
			op, err := ArchiveSourceHistory(t.Context(), home, binding, sourceArchiveRequest(t, home, binding, "held-pack-identity"))
			if err != nil {
				t.Fatal(err)
			}
			rootPath := filepath.Join(home, SourceHistoryName, "packs")
			root, dir, err := openSourcePackIdentity(rootPath, *op.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			defer dir.Close()
			packPath := filepath.Join(rootPath, "pack-"+op.Receipt.ID)
			switch change {
			case "root-replaced":
				if err = os.Rename(rootPath, rootPath+"-retained"); err == nil {
					err = os.Mkdir(rootPath, 0700)
				}
			case "pack-replaced":
				if err = os.Rename(packPath, packPath+"-retained"); err == nil {
					err = os.Mkdir(packPath, 0700)
				}
			case "pack-symlink":
				if err = os.Rename(packPath, packPath+"-retained"); err == nil {
					err = os.Symlink(packPath+"-retained", packPath)
				}
			case "pack-permissions":
				err = os.Chmod(packPath, 0755)
			case "record-hardlink":
				err = os.Link(filepath.Join(packPath, "records.bin"), filepath.Join(home, "retained-extra-link"))
			case "extra-entry":
				err = os.WriteFile(filepath.Join(packPath, "unexpected"), []byte("preserve"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = checkSourcePackIdentityAt(root, dir, rootPath, *op.Receipt); err == nil {
				t.Fatal("held descriptor lent authority to changed native evidence")
			}
		})
	}
}
