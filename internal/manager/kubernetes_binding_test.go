package manager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

func TestKubernetesSourceBindingIsCachedObservationAfterClose(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "state"), model.Resources{CPU: 1, Memory: 512 << 20, Disk: 512 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	joined := make(chan struct{})
	var once sync.Once
	stopped := false
	t.Cleanup(func() {
		once.Do(func() { close(joined) })
		if !stopped {
			_ = m.Close()
		}
	})
	owner, err := m.PrepareKubernetesSource(t.Context(), sourceCoreScope(kubesource.Start), joined)
	if err != nil {
		t.Fatal(err)
	}
	first, err := owner.Binding()
	if err != nil || first.SourceLeaseID == "" || first.StorageLeaseID == "" || first.Stage != "t" {
		t.Fatal(first, err)
	}
	files := owner.files
	once.Do(func() { close(joined) })
	if _, err = owner.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	stopped = true
	after, err := owner.Binding()
	if err != nil || after != first {
		t.Fatal("cached binding depended on closed SQL", after, err)
	}
	if err = owner.CheckActive(context.Background()); err == nil {
		t.Fatal("binding revived authority")
	}
	if owner.Files() != nil {
		t.Fatal("finished observation exposed factory")
	}
	if file, err := files.create(context.Background(), "catalog_proof"); err == nil || file != nil {
		t.Fatal("cached binding revived file authority", file, err)
	}
}

func TestKubernetesSourceNewContractRefusalPrecedesAdmission(t *testing.T) {
	m := sourceCoreManager(t)
	if _, err := m.db.Exec(`CREATE TRIGGER refuse_kube_contract BEFORE INSERT ON metadata WHEN NEW.key='kubernetes_source_contract' BEGIN SELECT RAISE(ABORT,'fixture compatibility refusal'); END`); err != nil {
		t.Fatal(err)
	}
	if owner, err := m.PrepareKubernetesSource(t.Context(), sourceCoreScope(kubesource.Start), make(chan struct{})); err == nil || owner != nil {
		t.Fatal(owner, err)
	}
	var count int
	if err := m.db.QueryRow(`SELECT count(*) FROM leases`).Scan(&count); err != nil || count != 0 {
		t.Fatal("compatibility refusal admitted source", count, err)
	}
	if m.storage != nil {
		t.Fatal("compatibility refusal created storage")
	}
}

func TestKubernetesSourcePortRoleReservedIndependentlyOfProject(t *testing.T) {
	m := sourceCoreManager(t)
	scope := sourceCoreScope(kubesource.Forward)
	request := scope.PortRequest(m.host, "", m.portNamespace)
	request.Project = "unrelated"
	if _, err := m.Acquire(request, nil); err == nil {
		t.Fatal("generic admission accepted reserved port role with another project")
	}
	// A historical row is not a native attachment capability. This deliberately
	// forged fixture has enough ordinary parent fields to reach the reserved-key
	// check without creating or adopting a kernel socket.
	parent := model.Lease{ID: strings.Repeat("a", 32), Host: m.host, Owner: m.owner, Request: request, State: "active", Revision: 1}
	raw, err := json.Marshal(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`INSERT INTO leases(id,request_key,data) VALUES(?,?,?)`, parent.ID, parent.Request.Key, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.portParent(model.Request{PortLease: parent.ID, TTL: time.Second}, ""); err == nil || !strings.Contains(err.Error(), "native source ports cannot") {
		t.Fatal("generic parent validation ignored reserved role", err)
	}
	if len(m.portSockets) != 0 {
		t.Fatal("negative fixture allocated or adopted sockets")
	}
}

// The request, artifact and revision remain unchanged. The current durable
// parent cannot acquire named-root authority behind the original pair owner.
func TestKubernetesSourceSameRevisionStorageBindingMutationRefused(t *testing.T) {
	for _, role := range []string{"source", "storage", "port"} {
		t.Run(role, func(t *testing.T) {
			var m *Manager
			var owner *KubernetesSourcePreparation
			var finish func()
			purpose := "start_marker"
			if role == "port" {
				m, owner, finish = forwardCoreOwner(t)
				purpose = "mesh_forward"
			} else {
				m = sourceCoreManager(t)
				owner, finish = sourceCoreOwner(t, m, kubesource.Start)
			}
			if err := owner.CheckActive(t.Context()); err != nil {
				t.Fatal("default-root original inactive", err)
			}
			id := owner.transit.id
			if role == "storage" {
				id = owner.parent.ID
			}
			if role == "port" {
				id = owner.port.ID
			}
			m.mu.Lock()
			original, err := m.get(id)
			m.mu.Unlock()
			if err != nil || original.StorageBinding != nil {
				t.Fatal("default-root fixture differs", original, err)
			}
			raw, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			// Restore only for fixture cleanup; the actual check observes the mutated
			// durable row while its original native owner/context is still live.
			defer func() {
				if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), id); err != nil {
					t.Error("restore fixture for original cleanup", err)
				}
				finish()
				result, err := owner.FinishAfterJoin()
				if err != nil || !result.SourceClosed || !result.SourceSettled || role == "port" && (!result.PortClosed || !result.PortSettled) {
					t.Error("exact original cleanup did not join and settle native owners", result, err)
				}
			}()
			changed := original
			changed.StorageBinding = &model.StoragePoolBinding{Name: "named-root", Volume: "fixture-volume", Backing: "fixture-backing", SharesDefault: true}
			mutated, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(mutated), id); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			current, err := m.get(id)
			m.mu.Unlock()
			if err != nil || current.Revision != original.Revision || current.StorageBinding == nil || owner.Context().Err() != nil {
				t.Fatal("fixture did not reach same-revision live binding mutation", current, err)
			}
			if err := owner.CheckActive(t.Context()); err == nil {
				t.Fatal("same-revision non-default storage binding retained authority", role)
			}
			opened := false
			owner.files.beforeOpen = func(string) { opened = true }
			file, err := owner.files.create(t.Context(), purpose)
			if file != nil {
				_ = file.Close()
			}
			owner.files.mu.Lock()
			files, consumed, lane := len(owner.files.files), len(owner.files.consumed), owner.files.requestLane
			owner.files.mu.Unlock()
			if err == nil || file != nil || opened || files != 0 || consumed != 0 || lane {
				t.Fatal("mutated binding reached file create/unlink/write authority", role, file, opened, files, consumed, lane, err)
			}
			select {
			case <-owner.privateJoined:
				t.Fatal("binding refusal substituted for the original caller join")
			default:
			}
			m.mu.Lock()
			source, sourceErr := m.get(owner.transit.id)
			parent, parentErr := m.get(owner.parent.ID)
			m.mu.Unlock()
			if sourceErr != nil || parentErr != nil || source.State != "active" || parent.State != "active" {
				t.Fatal("binding refusal dropped original charges before join", source, parent, sourceErr, parentErr)
			}
		})
	}
}
