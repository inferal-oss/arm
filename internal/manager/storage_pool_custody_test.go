package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
)

func custodyPoolManager(t *testing.T) (*Manager, daemonconfig.Settings, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	location := filepath.Join(base, "location")
	if err = os.Mkdir(location, 0700); err != nil {
		t.Fatal(err)
	}
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 2}, Listen: "127.0.0.1:0", StoragePools: []model.StoragePoolConfig{{Name: "scratch", Path: location}}}
	home := filepath.Join(base, "state")
	m, err := OpenConfigured(home, settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !m.closing {
			if e := m.Close(); e != nil {
				t.Error(e)
			}
		}
	})
	cpuReading(m, 64, 64, 0, 64, 0)
	poolReading(m, "scratch", m.storagePools["scratch"].record.Backing, 10000)
	return m, settings, home
}
func TestStoragePoolFirstProviderRejectsMismatchedFilesystemBeforeMkdir(t *testing.T) {
	m, _, _ := custodyPoolManager(t)
	p := m.storagePools["scratch"]
	trusted := p.location
	trusted.FilesystemID = "fsid:old-controlled-observation"
	m.storagePoolObserve = func(context.Context, string) (model.StorageLocation, error) { return trusted, nil }
	root := m.storagePoolRoot(p)
	l, err := m.Acquire(namedStorage("old-filesystem", 600), nil)
	if err != nil || l.State != "needs_attention" {
		t.Fatalf("old native filesystem observation must refuse creation: state=%s err=%v", l.State, err)
	}
	if _, err = os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("mismatched filesystem caused provider-root side effects", err)
	}
	if p.provider != nil || p.record.Root != nil {
		t.Fatal("unverified provider/root identity published")
	}
	views, err := m.StoragePools()
	if err != nil || len(views) != 1 || views[0].Budget.Reserved != 600 {
		t.Fatal("original promise lost", views, err)
	}
	if _, err = m.End(l.ID, "cancelled"); errorCode(err) != "needs_attention" {
		t.Fatal("ordinary end must retain unresolved custody", err)
	}
	if _, err = m.StorageCleanup(l.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { got, e := m.Inspect(l.ID); return e == nil && got.State == "needs_attention" })
	views, err = m.StoragePools()
	if err != nil || views[0].Budget.Reserved != 600 {
		t.Fatal("uncertain cleanup minted credit", views, err)
	}
}

func TestStoragePoolFirstProviderMatchingFilesystemAndReopen(t *testing.T) {
	m, settings, home := custodyPoolManager(t)
	p := m.storagePools["scratch"]
	l, err := m.Acquire(namedStorage("matching-filesystem", 600), nil)
	if err != nil || l.State != "active" || p.provider == nil || p.record.Root == nil || !p.record.Root.BirthKnown {
		t.Fatal("matching held parent did not publish original root", l, err)
	}
	rootIdentity := *p.record.Root
	if p.provider.Identity() != rootIdentity {
		t.Fatal("published root differs from opened provider")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenConfigured(home, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	pool := next.storagePools["scratch"]
	if pool.providerErr != nil || pool.provider == nil || pool.provider.Identity() != rootIdentity || *pool.record.Root != rootIdentity {
		t.Fatal("filesystem-bound reopen failed", pool.providerErr)
	}
	views, err := next.StoragePools()
	if err != nil || views[0].Budget.Reserved != 600 {
		t.Fatal("reopen lost original obligation", views, err)
	}
}
func TestStoragePoolObservationParentReplacementCannotRedirectMkdir(t *testing.T) {
	m, _, _ := custodyPoolManager(t)
	p := m.storagePools["scratch"]
	trusted := p.location
	original := p.record.Path
	moved := original + "-moved"
	replacement := original + "-replacement"
	if err := os.Mkdir(replacement, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(replacement, "unrelated")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.storagePoolObserve = func(context.Context, string) (model.StorageLocation, error) {
		calls++
		if err := os.Rename(original, moved); err != nil {
			return model.StorageLocation{}, err
		}
		if err := os.Symlink(replacement, original); err != nil {
			return model.StorageLocation{}, err
		}
		return trusted, nil
	}
	l, err := m.Acquire(namedStorage("replaced-parent", 600), nil)
	if err != nil || l.State != "needs_attention" || calls != 1 {
		t.Fatal("replaced parent did not refuse", l, err, calls)
	}
	if _, err = os.Lstat(filepath.Join(replacement, filepath.Base(m.storagePoolRoot(p)))); !os.IsNotExist(err) {
		t.Fatal("creation followed replacement parent", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "preserve" {
		t.Fatal("unrelated namespace changed", err)
	}
	if p.provider != nil || p.record.Root != nil || l.StorageBinding == nil || l.StorageBinding.Volume != p.record.Volume || l.StorageBinding.Backing != p.record.Backing {
		t.Fatal("original binding/root publication changed", l)
	}
	views, err := m.StoragePools()
	if err != nil || views[0].Budget.Reserved != 600 {
		t.Fatal("replacement failure minted capacity", views, err)
	}
}
func TestStoragePoolExistingRootMismatchNeverAdopts(t *testing.T) {
	m, settings, home := custodyPoolManager(t)
	p := m.storagePools["scratch"]
	l, err := m.Acquire(namedStorage("known-root", 600), nil)
	if err != nil || l.State != "active" {
		t.Fatal(l, err)
	}
	root := m.storagePoolRoot(p)
	sentinel := filepath.Join(root, "preserve")
	if err = os.WriteFile(sentinel, []byte("original root"), 0600); err != nil {
		t.Fatal(err)
	}
	wrong := *p.record.Root
	wrong.Inode++
	p.record.Root = &wrong
	if err = m.saveStoragePools(); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenConfigured(home, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	pool := next.storagePools["scratch"]
	if pool.provider != nil || pool.providerErr == nil || *pool.record.Root != wrong {
		t.Fatal("mismatched persisted root adopted")
	}
	views, err := next.StoragePools()
	if err != nil || views[0].Budget.Reserved != 600 || views[0].Budget.Known {
		t.Fatal("mismatch lost promise/refusal", views, err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "original root" {
		t.Fatal("existing root changed", err)
	}
}
func TestStoragePoolProviderPublicationFailurePreservesUnknownNamespaceAndPromise(t *testing.T) {
	m, settings, home := custodyPoolManager(t)
	p := m.storagePools["scratch"]
	root := m.storagePoolRoot(p)
	_, err := m.db.Exec(`CREATE TRIGGER custody_publication_refusal BEFORE UPDATE ON metadata WHEN NEW.key='storage_pools' BEGIN SELECT RAISE(ABORT,'controlled root publication refusal'); END`)
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(namedStorage("unpublished-root", 600), nil)
	if err != nil || l.State != "needs_attention" || p.provider != nil || p.record.Root != nil {
		t.Fatal("failed publication did not retain uncertainty", l, err)
	}
	if info, e := os.Lstat(root); e != nil || !info.IsDir() {
		t.Fatal("uncertain original creation was removed", e)
	}
	var raw string
	if err = m.db.QueryRow(`SELECT value FROM metadata WHERE key='storage_pools'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var records []model.StoragePoolRecord
	if err = json.Unmarshal([]byte(raw), &records); err != nil || len(records) != 1 || records[0].Root != nil {
		t.Fatal("uncommitted identity published", records, err)
	}
	sentinel := filepath.Join(root, "unpublished-sentinel")
	if err = os.WriteFile(sentinel, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`DROP TRIGGER custody_publication_refusal`); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenConfigured(home, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	cpuReading(next, 64, 64, 0, 64, 0)
	poolReading(next, "scratch", next.storagePools["scratch"].record.Backing, 10000)
	retry, err := next.Acquire(namedStorage("do-not-adopt", 100), nil)
	if err != nil || retry.State != "needs_attention" || next.storagePools["scratch"].provider != nil || next.storagePools["scratch"].record.Root != nil {
		t.Fatal("unpublished existing name adopted", retry, err)
	}
	views, err := next.StoragePools()
	if err != nil || views[0].Budget.Reserved != 700 {
		t.Fatal("unpublished/retry promises lost", views, err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "retain" {
		t.Fatal("unpublished namespace deleted", err)
	}
}
