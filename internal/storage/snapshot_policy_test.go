package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func snapshotPolicySource(t *testing.T) (*Provider, model.Artifact, []byte) {
	t.Helper()
	p, a := fixture(t)
	content := bytes.Repeat([]byte("canonical copied contents\n"), 256)
	if err := os.WriteFile(filepath.Join(a.Path, "payload"), content, 0600); err != nil {
		t.Fatal(err)
	}
	return p, a, content
}

func snapshotPolicyPlan(t *testing.T, p *Provider, a model.Artifact, policy SnapshotPolicy) SnapshotPlan {
	t.Helper()
	plan, err := p.PlanSnapshot(deadline(t), a, []model.ExportSelection{{Source: "payload", Path: "payload"}}, MaxExportBytes, policy, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// The child really uses the source as cwd. A bounded pipe exchange, rather
// than PID existence, proves it remains usable before and after the copy.
func snapshotCWDUser(t *testing.T, path string) func() {
	t.Helper()
	in, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output, out, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		_ = input.Close()
		t.Fatal(err)
	}
	child := exec.Command("/bin/cat")
	child.Dir, child.Stdin, child.Stdout = path, in, out
	if err = child.Start(); err != nil {
		_ = in.Close()
		_ = input.Close()
		_ = output.Close()
		_ = out.Close()
		t.Fatal(err)
	}
	if err = in.Close(); err != nil {
		t.Error(err)
	}
	if err = out.Close(); err != nil {
		t.Error(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }() // The creator's one original Wait.
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
		select {
		case err := <-waited:
			if err != nil {
				t.Error("cwd user failed after EOF", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("cwd user did not exit after EOF")
			if err := child.Process.Kill(); err != nil {
				t.Error(err)
			}
			select {
			case <-waited:
			case <-time.After(2 * time.Second):
				t.Error("cwd user's original Wait remains unresolved")
			}
		}
		if err := output.Close(); err != nil {
			t.Error(err)
		}
	})
	return func() {
		t.Helper()
		if err := input.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := output.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		const ping = "source directory remains usable\n"
		if n, err := io.WriteString(input, ping); err != nil || n != len(ping) {
			t.Fatal("cwd user input", n, err)
		}
		got := make([]byte, len(ping))
		if _, err := io.ReadFull(output, got); err != nil || string(got) != ping {
			t.Fatal("cwd user reply", string(got), err)
		}
	}
}

func TestSnapshotVerifiedCopyAllowsRealReadersMappingsAndDirectoryUsers(t *testing.T) {
	for _, kind := range []string{"reader", "private-mapping", "shared-mapping", "cwd"} {
		t.Run(kind, func(t *testing.T) {
			p, source, content := snapshotPolicySource(t)
			var ping func()
			if kind == "cwd" {
				ping = snapshotCWDUser(t, source.Path)
				ping()
			} else {
				f, err := os.Open(filepath.Join(source.Path, "payload"))
				if err != nil {
					t.Fatal(err)
				}
				if kind == "reader" {
					t.Cleanup(func() {
						if err := f.Close(); err != nil {
							t.Error(err)
						}
					})
				} else {
					flags := unix.MAP_PRIVATE
					if kind == "shared-mapping" {
						flags = unix.MAP_SHARED
					}
					mapping, err := unix.Mmap(int(f.Fd()), 0, len(content), unix.PROT_READ, flags)
					if err == nil {
						t.Cleanup(func() {
							if err := unix.Munmap(mapping); err != nil {
								t.Error(err)
							}
						})
					}
					closeErr := f.Close() // The mapping must outlive the original FD.
					if err != nil || closeErr != nil {
						t.Fatal(err, closeErr)
					}
					if !bytes.Equal(mapping, content) {
						t.Fatal("actual mapped bytes differ")
					}
				}
			}
			// A native busy or unavailable survey remains refusal in idle mode;
			// there is no automatic retry under verified-copy.
			if _, err := p.PlanSnapshot(deadline(t), source, []model.ExportSelection{{Source: "payload", Path: "payload"}}, MaxExportBytes, SnapshotIdleNative, func(ctx context.Context) error { return ctx.Err() }); err == nil {
				t.Fatal("idle policy silently accepted a source still in use")
			}
			plan := snapshotPolicyPlan(t, p, source, SnapshotVerifiedCopy)
			phases := []string{}
			result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), snapshotTestCallbacks(t, p, &phases))
			if err != nil || result.Phase != "prepared" || !result.FilesClosed || result.UnknownClose || result.Generation.Policy != SnapshotVerifiedCopy {
				t.Fatal("explicit verified-copy preparation", result, err)
			}
			// Synthetic publication authority here exercises only the internal
			// object primitive, not a Manager publication or public endpoint.
			read, err := p.ReadSnapshotChunk(deadline(t), result.Generation, "payload", 0, func(ctx context.Context) error { return ctx.Err() })
			if err != nil || !bytes.Equal(read.Data, content) {
				t.Fatal("independent canonical copied bytes", err)
			}
			if ping != nil {
				ping()
			}
		})
	}
}

func TestSnapshotVerifiedCopyPolicyAndDigestsCannotBeSubstituted(t *testing.T) {
	p, source, _ := snapshotPolicySource(t)
	for _, policy := range []SnapshotPolicy{"", "verified-copy-v2", "IDLE-NATIVE-V1"} {
		called := false
		if _, err := p.PlanSnapshot(deadline(t), source, []model.ExportSelection{{Source: "payload", Path: "payload"}}, MaxExportBytes, policy, func(context.Context) error { called = true; return nil }); err == nil || called {
			t.Fatal("unsupported policy reached native/authority preparation", policy, err)
		}
	}
	plan := snapshotPolicyPlan(t, p, source, SnapshotVerifiedCopy)
	changed := plan
	changed.Policy = SnapshotIdleNative // Retain the original canonical digest.
	intentCalled := false
	negativePhases := []string{}
	callbacks := snapshotTestCallbacks(t, p, &negativePhases)
	originalIntent := callbacks.Intent
	callbacks.Intent = func(ctx context.Context, intent SnapshotCopyIntent) error {
		intentCalled = true
		return originalIntent(ctx, intent)
	}
	if _, err := p.CopySnapshot(deadline(t), snapshotTestIntent(changed), callbacks); err == nil || intentCalled {
		t.Fatal("changed plan policy reached allocation intent", err)
	}
	if _, err := os.Lstat(p.Path(snapshotTestDestination)); !os.IsNotExist(err) {
		t.Fatal("policy mismatch created a destination", err)
	}
	phases := []string{}
	result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), snapshotTestCallbacks(t, p, &phases))
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []SnapshotPolicy{SnapshotIdleNative, "", "unknown"} {
		changed := result.Generation
		changed.Policy = policy
		called := false
		if _, err := p.ReadSnapshotChunk(deadline(t), changed, "payload", 0, func(context.Context) error { called = true; return nil }); err == nil || called {
			t.Fatal("changed generation policy reached read authority", policy, err)
		}
	}
}

func TestSnapshotVerifiedCopyKeepsDestructiveCleanupActivityChecks(t *testing.T) {
	p, source, content := snapshotPolicySource(t)
	f, err := os.Open(filepath.Join(source.Path, "payload"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	plan := snapshotPolicyPlan(t, p, source, SnapshotVerifiedCopy)
	phases := []string{}
	if result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), snapshotTestCallbacks(t, p, &phases)); err != nil || result.Phase != "prepared" {
		t.Fatal(result, err)
	}
	if err := p.Quarantine(source); err != nil {
		t.Fatal(err)
	}
	if n, done, err := p.Step(deadline(t), source, 128, nil); err == nil || done || n != 0 {
		t.Fatal("verified-copy weakened destructive cleanup", n, done, err)
	}
	got, err := os.ReadFile(filepath.Join(p.Path(".reclaim-"+source.Name), "payload"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("refused cleanup changed source contents", err)
	}
}

func TestSnapshotVerifiedCopyNewDestinationMayHaveReaders(t *testing.T) {
	p, source, content := snapshotPolicySource(t)
	plan := snapshotPolicyPlan(t, p, source, SnapshotVerifiedCopy)
	phases := []string{}
	callbacks := snapshotTestCallbacks(t, p, &phases)
	var held *os.File
	t.Cleanup(func() {
		if held != nil {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	callbacks.Current = func(ctx context.Context) error {
		if held == nil {
			f, err := os.Open(filepath.Join(p.Path(snapshotTestDestination), "payload"))
			if err == nil {
				held = f
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		return ctx.Err()
	}
	result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), callbacks)
	if err != nil || result.Phase != "prepared" || held == nil {
		t.Fatal("new private destination still required a global idle survey", result, err)
	}
	got := make([]byte, len(content))
	if _, err = held.ReadAt(got, 0); err != nil || !bytes.Equal(got, content) {
		t.Fatal("held new destination lacks canonical contents", err)
	}
}
