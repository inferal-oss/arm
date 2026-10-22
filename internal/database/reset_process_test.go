package database

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResetPrivateSubprocessHelper(t *testing.T) {
	if os.Getenv("ARM_TEST_RESET_PROCESS") != "1" {
		return
	}
	home, id, stage := os.Getenv("ARM_TEST_RESET_HOME"), os.Getenv("ARM_TEST_RESET_ID"), os.Getenv("ARM_TEST_RESET_STAGE")
	if !strings.HasPrefix(filepath.Base(home), "arm-reset-store-") || !validHost(id) {
		t.Fatal("helper accepts only owned disposable reset fixtures")
	}
	c, err := ReviewResetContinuation(context.Background(), home, id)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	result, err := c.continueReset(context.Background(), func(at string) error {
		if stage != "finish" && at == stage {
			os.Exit(73)
		}
		return nil
	})
	if err != nil || !result.Completed {
		t.Fatalf("continuation %+v %v", result, err)
	}
}
func TestResetActualProcessExitAndNewProcessRecovery(t *testing.T) {
	for _, stage := range []string{"fence-intent", "after-move:state.db-wal", "after-erase:state.db", "completed-receipt"} {
		t.Run(stage, func(t *testing.T) {
			s, r, _ := resetFileEngineFixture(t)
			run := func(checkpoint string) error {
				command := exec.Command(os.Args[0], "-test.run=^TestResetPrivateSubprocessHelper$")
				command.Env = append(os.Environ(), "ARM_TEST_RESET_PROCESS=1", "ARM_TEST_RESET_HOME="+s.home.Path, "ARM_TEST_RESET_ID="+r.Plan.ID, "ARM_TEST_RESET_STAGE="+checkpoint)
				output, err := command.CombinedOutput()
				if checkpoint == "finish" && err != nil {
					t.Fatalf("new process failed: %s %v", output, err)
				}
				return err
			}
			var status *exec.ExitError
			if err := run(stage); !errors.As(err, &status) || status.ExitCode() != 73 {
				t.Fatalf("did not interrupt at exact durable boundary: %v", err)
			}
			if err := run("finish"); err != nil {
				t.Fatal(err)
			}
			receipt, _, err := s.readReceipt()
			if err != nil || receipt.Phase != "completed" {
				t.Fatalf("retained receipt %+v %v", receipt, err)
			}
			if _, err = os.Lstat(filepath.Join(s.home.Path, "database-reset-pending.json")); !os.IsNotExist(err) {
				t.Fatal("fence left after explicitly recovered reset", err)
			}
		})
	}
}
