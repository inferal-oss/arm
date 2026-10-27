package database_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"golang.org/x/sys/unix"
)

func snapshotTree(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		key, _ := filepath.Rel(home, path)
		out[key] = info.Mode().String()
		if info.Mode().IsRegular() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			out[key] += string(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func review(t *testing.T, home string) (*database.SettledReview, error) {
	t.Helper()
	return database.ReviewSettled(context.Background(), home, strconv.Itoa(os.Geteuid()))
}
func TestReadOnlySettledNamespaceReviewAndStaleSource(t *testing.T) {
	home := proofFixture(t, false)
	before := snapshotTree(t, home)
	r, err := review(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Proof.Complete || !r.Proof.Settled || r.Mesh != nil {
		t.Fatalf("incorrect full review %+v", r)
	}
	if got := snapshotTree(t, home); !reflect.DeepEqual(before, got) {
		t.Fatal("read-only review changed original namespace")
	}
	if _, err = review(t, home); err == nil {
		t.Fatal("second review bypassed stopped authority lock")
	}
	edit(t, home, `INSERT INTO metadata VALUES('future','changed after review')`)
	if err = r.Validate(context.Background()); err == nil {
		t.Fatal("review accepted changed original WAL/database")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestPendingEvidenceBlocksAndLockPreventsConcurrentReview(t *testing.T) {
	for _, kind := range []string{"pending", "unknown", "hardlink", "symlink", "oversize", "busy"} {
		t.Run(kind, func(t *testing.T) {
			home := proofFixture(t, false)
			dir := filepath.Join(home, "dashboard-pending")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(dir, ".lock")
			if err := os.WriteFile(lock, nil, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "op-"+strings.Repeat("a", 64)+".json")
			switch kind {
			case "pending":
				os.WriteFile(path, []byte(`{"authority":"old","unknown":true}`), 0600)
			case "unknown":
				os.WriteFile(filepath.Join(dir, ".intent-crash"), nil, 0600)
			case "hardlink":
				os.WriteFile(filepath.Join(home, "sentinel"), nil, 0600)
				os.Link(filepath.Join(home, "sentinel"), path)
			case "symlink":
				os.Symlink(filepath.Join(home, "absent"), path)
			case "oversize":
				os.WriteFile(path, make([]byte, (64<<10)+1), 0600)
			case "busy":
				f, e := os.Open(lock)
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
					t.Fatal(e)
				}
			}
			before := snapshotTree(t, home)
			r, err := review(t, home)
			if err == nil {
				defer r.Close()
				if r.Proof.Settled {
					t.Fatal("pending evidence was declared safe")
				}
			}
			if got := snapshotTree(t, home); !reflect.DeepEqual(before, got) {
				t.Fatal("pending proof changed evidence")
			}
		})
	}
}
func TestReviewNamespaceMembershipAndStorageIdentity(t *testing.T) {
	home := proofFixture(t, false)
	dir := filepath.Join(home, "dashboard-pending")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, ".lock"), nil, 0600)
	r, err := review(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Proof.Settled {
		t.Fatal("empty pending roster not settled")
	}
	os.WriteFile(filepath.Join(dir, ".intent-raced"), nil, 0600)
	if err = r.Validate(context.Background()); err == nil {
		t.Fatal("changed pending roster accepted")
	}
	r.Close()
	home = proofFixture(t, false)
	os.Mkdir(filepath.Join(home, "storage"), 0700)
	if r, err = review(t, home); err == nil {
		r.Close()
		t.Fatal("unrecorded storage root adopted")
	}
}
func TestMeshNeedsExplicitRetirementAndUnsettledLifecycleBlocks(t *testing.T) {
	home := proofFixture(t, false)
	p, err := prove(t, home)
	if err != nil {
		t.Fatal(err)
	}
	store, err := mesh.Init(home, p.Host, "fixture-node", "127.0.0.1:23456", "127.0.0.1:23456")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	r, err := review(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Proof.Settled || r.Mesh == nil || !r.Mesh.Required || r.Mesh.Host != p.Host {
		t.Fatal("mesh retirement scope missing")
	}
	r.Close()
	lock, err := mesh.LockLifecycle(home)
	if err != nil {
		t.Fatal(err)
	}
	lock()
	if err = os.Mkdir(filepath.Join(home, "mesh-lifecycle"), 0700); err != nil {
		t.Fatal(err)
	}
	op := mesh.LifecycleOperation{Actor: local.CurrentIdentity(), Key: "fixture-connect", Kind: "connect", Digest: strings.Repeat("a", 64), At: time.Now().UTC(), Updated: time.Now().UTC(), Phase: "remote-pending", LocalHost: p.Host, Reason: "private fixture", Transitions: []mesh.LifecycleTransition{{Phase: "remote-pending", At: time.Now().UTC()}}}
	raw, _ := json.Marshal(op)
	path, _ := mesh.LifecycleOperationPath(home, op.Key)
	os.WriteFile(path, raw, 0600)
	r, err = review(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if r.Proof.Settled {
		t.Fatal("uncertain lifecycle declared safe")
	}
	r.Close()
	op.Phase = "connected"
	op.Transitions[0].Phase = "connected"
	raw, _ = json.Marshal(op)
	os.WriteFile(path, raw, 0600)
	r, err = review(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Proof.Settled {
		t.Fatal("settled lifecycle blocked")
	}
	r.Close()
}

func TestHandedOffStorageRootIsVerifiedButChildrenPreserved(t *testing.T) {
	home := proofFixture(t, false)
	m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 4096, Disk: 4096})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1024}, Reason: "retain private artifact", TTL: time.Minute, Key: "storage-handoff"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(lease.ManagedDir, "retained-output")
	if err = os.WriteFile(sentinel, []byte("preserve exactly"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StorageHandoff(lease.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, home)
	r, err := review(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Proof.Settled || r.Proof.StorageRoot == nil {
		t.Fatal("verified handoff root missing")
	}
	r.Close()
	if !reflect.DeepEqual(before, snapshotTree(t, home)) {
		t.Fatal("handoff proof changed retained output")
	}
	if err = os.Rename(filepath.Join(home, "storage"), filepath.Join(home, "old-storage")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(home, "storage"), 0700); err != nil {
		t.Fatal(err)
	}
	if r, err = review(t, home); err == nil {
		r.Close()
		t.Fatal("replacement infrastructure root adopted")
	}
	data, err := os.ReadFile(filepath.Join(home, "old-storage", lease.ID, "retained-output"))
	if err != nil || string(data) != "preserve exactly" {
		t.Fatal("retained output changed")
	}
}

func TestRemovedStorageNamespaceCannotReappearAfterReview(t *testing.T) {
	home := proofFixture(t, false)
	m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 4096, Disk: 4096})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1024}, Reason: "private removed namespace fixture", TTL: time.Minute, Key: "removed-review"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.StorageHandoff(lease.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	// Make a deliberately contradictory private record: reporting removed while
	// its exact namespace remains must refuse, regardless of terminal state.
	edit(t, home, `UPDATE leases SET data=json_set(data,'$.outcome','released','$.artifact.phase','removed','$.artifact.namespace_removed',json('true'),'$.artifact.allocated_before_cleanup',0,'$.artifact.content_observed',json('false')) WHERE id=?`, lease.ID)
	if r, err := review(t, home); err == nil {
		r.Close()
		t.Fatal("removed record with surviving exact namespace accepted")
	}
	// Unknown retained children are preserved, never acquired as cleanup rights.
	if err = os.Rename(lease.ManagedDir, filepath.Join(home, "storage", "retained-unadopted")); err != nil {
		t.Fatal(err)
	}
	r, err := review(t, home)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.Proof.Settled {
		t.Fatal("absent removed namespace was not settled")
	}
	if err = os.Mkdir(lease.ManagedDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = r.Validate(context.Background()); err == nil {
		t.Fatal("removed namespace reappearance bypassed stale-review guard")
	}
}
