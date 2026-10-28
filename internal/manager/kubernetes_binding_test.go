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
