package manager

import (
	"os"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/kubejournal"
	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/mesh"
)

func managerKubeWorkflow(t *testing.T, m *Manager) kubejournal.Record {
	t.Helper()
	plan, err := kubeprovision.NewPlan(kubeprovision.Review{Key: strings.Repeat("a", 64), Owner: m.owner, Reason: "contract-first fixture", Cluster: kubeprovision.Cluster{Context: "fixture", Server: "https://fixture.invalid", CAHash: strings.Repeat("b", 64), AuthSubject: "operator"}, Namespace: kubeprovision.Namespace{Name: "default", UID: "ns-uid"}, Node: kubeprovision.Node{Name: "worker", UID: "node-uid", OS: "linux", Arch: "arm64", Ready: true}, Image: "registry.invalid/base@sha256:" + strings.Repeat("c", 64), BinarySHA256: strings.Repeat("d", 64), BinaryBytes: 1024, CatalogSHA256: strings.Repeat("e", 64), Resources: kubeprovision.Resources{CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi", EphemeralRequest: "256Mi", EphemeralLimit: "1Gi"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := kubejournal.New("kubernetes_install", plan.Review.Key, m.owner, m.host, plan, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestKubernetesWorkflowContractAcknowledgmentPrecedesProtectedFile(t *testing.T) {
	for _, mode := range []string{"success", "refused", "unknown-contract"} {
		t.Run(mode, func(t *testing.T) {
			m := sourceCoreManager(t)
			r := managerKubeWorkflow(t, m)
			switch mode {
			case "refused":
				if _, err := m.db.Exec(`CREATE TRIGGER refuse_workflow_contract BEFORE INSERT ON metadata WHEN NEW.key='kubernetes_source_contract' BEGIN SELECT RAISE(ABORT,'contract refusal'); END`); err != nil {
					t.Fatal(err)
				}
			case "unknown-contract":
				if _, err := m.db.Exec(`INSERT INTO metadata(key,value) VALUES(?,?)`, kubesource.ContractKey, `{"version":99}`); err != nil {
					t.Fatal(err)
				}
			}
			guard, err := m.PrepareKubernetesWorkflow(r)
			if mode == "success" {
				if err != nil || guard == nil {
					t.Fatal(guard, err)
				}
				var raw string
				if err = m.db.QueryRow(`SELECT value FROM metadata WHERE key=?`, kubesource.ContractKey).Scan(&raw); err != nil || raw != kubesource.ContractValue {
					t.Fatal("workflow lacks prior exact contract ACK", raw, err)
				}
				op, ok, err := mesh.ReadCoordination(m.home, r.Key)
				if err != nil || !ok || op.KubernetesWorkflow == nil {
					t.Fatal(op, ok, err)
				}
			} else {
				if err == nil || guard != nil {
					t.Fatal("unsupported contract wrote workflow", guard, err)
				}
				_, exists, readErr := mesh.ReadCoordination(m.home, r.Key)
				if exists || readErr != nil && !os.IsNotExist(readErr) {
					t.Fatal("failed contract exposed protected record", exists, readErr)
				}
			}
			var count int
			if err = m.db.QueryRow(`SELECT count(*) FROM leases`).Scan(&count); err != nil || count != 0 {
				t.Fatal("workflow claim granted native resources", count, err)
			}
		})
	}
}
