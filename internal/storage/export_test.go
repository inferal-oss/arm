package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

func exportFixture(t *testing.T) (*Provider, model.Artifact, string) {
	t.Helper()
	p, a := fixture(t)
	if err := os.MkdirAll(filepath.Join(a.Path, "target", "release", "deps"), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(a.Path, "target", "release", "app")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf 'exported-result\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// Real Cargo commonly creates this exact ownership relationship.
	if err := os.Link(source, filepath.Join(a.Path, "target", "release", "deps", "app-hash")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Path, "runtime.dat"), []byte("required runtime data"), 0600); err != nil {
		t.Fatal(err)
	}
	return p, a, filepath.Join(t.TempDir(), "published")
}

func operationFor(plan model.StorageExportPlan) model.StorageExport {
	id := "1234567890abcdef1234567890abcdef"
	return model.StorageExport{ID: id, Request: model.StorageExportRequest{Lease: plan.Source.Name}, Plan: plan, StageName: ".arm-export-" + id, State: "planned"}
}

func exportSelections() []model.ExportSelection {
	return []model.ExportSelection{{Source: "target/release/app", Path: "bin/app"}, {Source: "runtime.dat", Path: "share/runtime.dat"}}
}

func TestExportPublishesIndependentExecutableAndExplicitRuntimeFiles(t *testing.T) {
	p, a, destination := exportFixture(t)
	plan, err := p.PlanExport(deadline(t), a, destination, exportSelections(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("preview created output", err)
	}
	states := []string{}
	op, err := p.Export(deadline(t), operationFor(plan), func(op model.StorageExport) error { states = append(states, op.State); return nil })
	if err != nil || op.State != "published" || strings.Join(states, ",") != "copying,ready,publishing,published" {
		t.Fatal("export failed", op, states, err)
	}
	for _, file := range op.Plan.Files {
		if file.OutputIdentity == nil || *file.OutputIdentity == file.Identity || len(file.SHA256) != 64 {
			t.Fatal("output identity/digest is not independent", file)
		}
		data, err := os.ReadFile(filepath.Join(destination, file.Path))
		if err != nil || int64(len(data)) != file.Size {
			t.Fatal("output incomplete", file.Path, err)
		}
	}
	if err := p.Quarantine(a); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		_, done, err := p.Step(deadline(t), a, 128, nil)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatal("source namespace not removed", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "bin", "app")); err != nil {
		t.Fatal("output did not survive source cleanup", err)
	}
}

func TestExportRefusesActiveSharedUnmanagedAndExistingTargets(t *testing.T) {
	for _, scenario := range []string{"active", "external-hardlink", "symlink-source", "managed-destination", "existing-target", "parent-writeable"} {
		t.Run(scenario, func(t *testing.T) {
			p, a, destination := exportFixture(t)
			switch scenario {
			case "active":
				f, err := os.Open(filepath.Join(a.Path, "runtime.dat"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
			case "external-hardlink":
				if err := os.Link(filepath.Join(a.Path, "runtime.dat"), filepath.Join(t.TempDir(), "outside-link")); err != nil {
					t.Fatal(err)
				}
			case "symlink-source":
				if err := os.Remove(filepath.Join(a.Path, "runtime.dat")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "unrelated"), filepath.Join(a.Path, "runtime.dat")); err != nil {
					t.Fatal(err)
				}
			case "managed-destination":
				destination = filepath.Join(a.Path, "not-an-export")
			case "existing-target":
				if err := os.WriteFile(destination, []byte("unrelated-user-file"), 0600); err != nil {
					t.Fatal(err)
				}
			case "parent-writeable":
				if err := os.Chmod(filepath.Dir(destination), 0777); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.PlanExport(deadline(t), a, destination, exportSelections(), 1<<20, nil); err == nil {
				t.Fatal("unsafe export accepted")
			}
			if scenario == "existing-target" {
				if data, err := os.ReadFile(destination); err != nil || string(data) != "unrelated-user-file" {
					t.Fatal("existing target changed", err)
				}
			}
		})
	}
}

func TestExportSourceChangeAndPublishCollisionPreserveBothSides(t *testing.T) {
	for _, scenario := range []string{"changed-source", "changed-parent", "publish-collision", "changed-stage", "ready-root-permissions", "publishing-root-permissions", "cancelled-context"} {
		t.Run(scenario, func(t *testing.T) {
			p, a, destination := exportFixture(t)
			plan, err := p.PlanExport(deadline(t), a, destination, exportSelections(), 1<<20, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := deadline(t)
			if scenario == "changed-source" {
				if err := os.WriteFile(filepath.Join(a.Path, "runtime.dat"), []byte("new version"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "changed-parent" {
				if err := os.Rename(filepath.Dir(destination), filepath.Dir(destination)+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Dir(destination), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "cancelled-context" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			_, err = p.Export(ctx, operationFor(plan), func(op model.StorageExport) error {
				if scenario == "publish-collision" && op.State == "publishing" {
					return os.WriteFile(destination, []byte("preserve the late arrival"), 0600)
				}
				if scenario == "changed-stage" && op.State == "publishing" {
					return os.WriteFile(filepath.Join(filepath.Dir(destination), op.StageName, "share", "runtime.dat"), []byte("tampered after checkpoint"), 0600)
				}
				if scenario == "ready-root-permissions" && op.State == "ready" || scenario == "publishing-root-permissions" && op.State == "publishing" {
					return os.Chmod(filepath.Join(filepath.Dir(destination), op.StageName), 0777)
				}
				return nil
			})
			if err == nil {
				t.Fatal("changed export target/source accepted")
			}
			if _, err := os.Stat(a.Path); err != nil {
				t.Fatal("export failure removed source", err)
			}
			if scenario == "publish-collision" {
				if data, err := os.ReadFile(destination); err != nil || string(data) != "preserve the late arrival" {
					t.Fatal("late target was overwritten", err)
				}
			}
		})
	}
}

func TestExportInterruptedPublicationRequiresVerifiedRecovery(t *testing.T) {
	p, a, destination := exportFixture(t)
	plan, err := p.PlanExport(deadline(t), a, destination, exportSelections(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	var last model.StorageExport
	_, err = p.Export(deadline(t), operationFor(plan), func(op model.StorageExport) error {
		if op.State == "published" {
			return errors.New("simulated final journal failure")
		}
		last = op
		return nil
	})
	if err == nil || last.State != "publishing" {
		t.Fatal("missing interrupted publication", err, last.State)
	}
	recovered, err := p.ReconcileExport(deadline(t), last)
	if err != nil || recovered.State != "published" {
		t.Fatal("could not verify already-published output", recovered, err)
	}
	if err := os.WriteFile(filepath.Join(destination, "share", "runtime.dat"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReconcileExport(deadline(t), last); err == nil {
		t.Fatal("changed output was adopted during reconciliation")
	}
}

func TestExportSelectionsRejectTraversalAndAmbiguousOutputPaths(t *testing.T) {
	for _, files := range [][]model.ExportSelection{nil, {{Source: "../source"}}, {{Source: "/source"}}, {{Source: "source", Path: "../out"}}, {{Source: "first", Path: "bin"}, {Source: "second", Path: "bin/app"}}, {{Source: "same"}, {Source: "same", Path: "other"}}} {
		if _, err := NormalizeExportSelections(files); err == nil {
			t.Fatal("unsafe selections accepted", files)
		}
	}
}

// A side effect in Err deterministically changes the namespace during native
// digest verification without introducing production test hooks.
type exportMutationContext struct {
	context.Context
	mutate func()
}

func (c *exportMutationContext) Err() error {
	if c.mutate != nil {
		c.mutate()
	}
	return c.Context.Err()
}

func TestExportFinalVerificationRejectsRootAndParentChanges(t *testing.T) {
	for _, scenario := range []string{"export-root-mode", "export-parent", "reconcile-root-mode", "reconcile-parent"} {
		t.Run(scenario, func(t *testing.T) {
			p, a, destination := exportFixture(t)
			plan, err := p.PlanExport(deadline(t), a, destination, exportSelections(), 1<<20, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &exportMutationContext{Context: deadline(t)}
			mutated := false
			ctx.mutate = func() {
				if mutated {
					return
				}
				if _, err := os.Stat(destination); err != nil {
					return
				}
				mutated = true
				if strings.HasSuffix(scenario, "root-mode") {
					if err := os.Chmod(destination, 0777); err != nil {
						t.Fatal(err)
					}
				} else {
					parent := filepath.Dir(destination)
					if err := os.Rename(parent, parent+"-moved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(parent, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if strings.HasPrefix(scenario, "export-") {
				_, err = p.Export(ctx, operationFor(plan), func(model.StorageExport) error { return nil })
			} else {
				var operation model.StorageExport
				operation, err = p.Export(deadline(t), operationFor(plan), func(model.StorageExport) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				_, err = p.ReconcileExport(ctx, operation)
			}
			if !mutated || err == nil {
				t.Fatal("final verification accepted changed bundle root/parent", mutated, err)
			}
			if _, err := os.Stat(a.Path); err != nil {
				t.Fatal("verification failure removed source", err)
			}
		})
	}
}

func TestExportReconcileRejectsUnprotectedBundleRoot(t *testing.T) {
	p, a, destination := exportFixture(t)
	plan, err := p.PlanExport(deadline(t), a, destination, exportSelections(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := p.Export(deadline(t), operationFor(plan), func(model.StorageExport) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReconcileExport(deadline(t), operation); err == nil {
		t.Fatal("reconcile accepted unprotected bundle root")
	}
}
