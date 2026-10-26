package database_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

func TestResetMissingSeedPointerChild(t *testing.T) {
	if os.Getenv("ARM_TEST_LOST_RESET_SEED") != "1" {
		return
	}
	home := os.Getenv("ARM_TEST_LOST_RESET_HOME")
	if !filepath.IsAbs(home) || !strings.Contains(home, "TestResetMissingSeed") {
		t.Fatal("only a specifically named disposable test fixture is accepted")
	}
	var fence *local.StateFenceError
	if m, err := manager.Open(home, model.Resources{CPU: 2}); err == nil {
		m.Close()
		t.Fatal("missing reset pointer recreated manager authority")
	} else if !errors.As(err, &fence) {
		t.Fatal("expected explicit retained-history fence", err)
	}
	if err := server.Serve(context.Background(), home, "127.0.0.1:0", model.Resources{CPU: 2}); err == nil {
		t.Fatal("missing reset pointer recreated server authority")
	} else if !errors.As(err, &fence) {
		t.Fatal("server did not refuse before startup", err)
	}
}
func TestResetMissingSeedPointerNeverRegeneratesAuthorityAcrossProcesses(t *testing.T) {
	for _, phase := range []string{"uninstalled", "consumed-missing-main", "consumed-main-preserved"} {
		t.Run(phase, func(t *testing.T) {
			home := proofFixture(t, false)
			review, err := database.ReviewReset(context.Background(), home, "private lost-pointer safety regression", false)
			if err != nil {
				t.Fatal(err)
			}
			result, err := review.Apply(context.Background())
			review.Close()
			if err != nil || !result.Completed {
				t.Fatalf("reset %+v %v", result, err)
			}
			if phase != "uninstalled" {
				lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err = database.InstallResetSeed(context.Background(), home, lock); err != nil {
					t.Fatal(err)
				}
				m, err := manager.Open(home, model.Resources{CPU: 2})
				if err != nil {
					t.Fatal(err)
				}
				if err = m.Close(); err != nil {
					t.Fatal(err)
				}
				lock.Close()
				if phase == "consumed-missing-main" {
					if err = os.Remove(filepath.Join(home, "state.db")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = os.Remove(filepath.Join(home, database.ResetSeedName)); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, home)
			command := exec.Command(os.Args[0], "-test.run=^TestResetMissingSeedPointerChild$")
			command.Env = append(os.Environ(), "ARM_TEST_LOST_RESET_SEED=1", "ARM_TEST_LOST_RESET_HOME="+home)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("new-process startup failed safety regression: %s %v", output, err)
			}
			if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("refused new-process startup changed source namespace")
			}
			// Diagnosis reads the retained receipt without repairing/deleting it.
			continuation, err := database.ReviewResetContinuation(context.Background(), home, result.Operation)
			if err != nil {
				t.Fatal(err)
			}
			if continuation.Receipt.Phase != "completed" {
				t.Fatal("lost completed history")
			}
			if _, err = continuation.Continue(context.Background()); err == nil {
				t.Fatal("missing pointer reported successful recovery without authority evidence")
			}
			continuation.Close()
			if phase == "consumed-main-preserved" {
				source, err := database.Offline(context.Background(), home)
				if err != nil {
					t.Fatal(err)
				}
				stats, err := database.ReadStats(context.Background(), source)
				source.Close()
				if err != nil || stats.Host != result.NewHost {
					t.Fatalf("read-only diagnosis unavailable %+v %v", stats, err)
				}
			}
			if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("diagnosis/declined recovery changed retained evidence")
			}
		})
	}
}
