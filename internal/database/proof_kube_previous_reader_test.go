//go:build arm_kubernetes_previous_reader

package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"getarm.org/internal/kubesource"
)

// This optional native cohort is explicit: a missing retained previous reader
// fails, rather than converting compatibility acceptance into a skipped test.
func TestKubernetesPreviousReaderRefusesPriorContractFence(t *testing.T) {
	path, want := os.Getenv("ARM_KUBE_PREVIOUS_READER"), os.Getenv("ARM_KUBE_PREVIOUS_READER_SHA256")
	if path == "" || len(want) != 64 {
		t.Fatal("explicit previous native reader and SHA256 required")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n, readErr := io.Copy(h, io.LimitReader(file, 128<<20+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || n > 128<<20 || hex.EncodeToString(h.Sum(nil)) != want {
		t.Fatal("previous reader identity differs", readErr, closeErr)
	}
	for _, fenced := range []bool{false, true} {
		name := "before-fence"
		if fenced {
			name = "after-fence"
		}
		t.Run(name, func(t *testing.T) {
			home := proofFixture(t, false)
			if fenced {
				edit(t, home, `INSERT INTO metadata(key,value) VALUES(?,?)`, kubesource.ContractKey, kubesource.ContractValue)
			}
			output, err := exec.CommandContext(t.Context(), path, "--home", home, "database", "remove", "--dry-run", "--json").CombinedOutput()
			if len(output) > 1<<20 {
				t.Fatal("previous reader output exceeded bound")
			}
			if fenced {
				if err == nil || (!strings.Contains(string(output), "unknown metadata key") || !strings.Contains(string(output), kubesource.ContractKey)) {
					t.Fatal("previous reader accepted or failed before the contract fence", err, string(output))
				}
			} else if err != nil {
				t.Fatal("unfenced baseline previous proof failed", err, string(output))
			}
		})
	}
}
