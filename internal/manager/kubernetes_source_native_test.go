package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == kubeprovision.ExecHelperArgument && os.Getenv("ARM_MANAGER_SOURCE_TEST_HELPER") == "1" {
		os.Exit(kubeprovision.ExecHelperMain(os.Args[2:]))
	}
}
func sourceCorePlan(t *testing.T, m *Manager) (kubeprovision.Plan, kubeprovision.Pod) {
	t.Helper()
	s := sourceCoreScope(kubesource.Inspect)
	plan, err := kubeprovision.NewPlan(kubeprovision.Review{Key: s.Operation, Owner: m.owner, Reason: "fixed source native closure fixture", Cluster: kubeprovision.Cluster{Context: "unavailable-source-fixture", Server: "https://fixture.invalid", CAHash: strings.Repeat("c", 64), AuthSubject: "fixture-principal", AuthGroups: []string{"fixture-group"}}, Namespace: kubeprovision.Namespace{Name: "default", UID: "namespace-uid"}, Node: kubeprovision.Node{Name: "worker", UID: "node-uid", OS: "linux", Arch: "arm64", Ready: true}, Image: "registry.example/bootstrap@sha256:" + strings.Repeat("d", 64), BinarySHA256: strings.Repeat("e", 64), BinaryBytes: s.B, CatalogSHA256: strings.Repeat("f", 64), Resources: kubeprovision.Resources{CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "256Mi", EphemeralRequest: "256Mi", EphemeralLimit: "1Gi"}})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Pod
	return plan, kubeprovision.Pod{Name: "arm-node-fixture", UID: "pod-uid", Namespace: p.Namespace, OwnerLabel: p.OwnerLabel, KeyLabel: p.KeyLabel, KeyAnnotation: p.KeyAnnotation, PlanAnnotation: p.PlanAnnotation, NodeName: p.NodeName, SelectedNode: p.NodeName, Phase: "Running", Image: p.Image, Safe: true, Resources: p.Resources, VolumeSizeLimit: p.VolumeSizeLimit}
}
func TestKubernetesSourceNativeFourRequestsAndFifthRefusal(t *testing.T) {
	t.Setenv("ARM_MANAGER_SOURCE_TEST_HELPER", "1")
	config := filepath.Join(t.TempDir(), "empty-kubeconfig")
	if err := os.WriteFile(config, []byte("apiVersion: v1\nkind: Config\ncontexts: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", config)
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Inspect)
	plan, pod := sourceCorePlan(t, m)
	exec, err := kubeprovision.NewOwnedExec(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"lookup_binary", "catalog_proof", "lookup_start_marker", "runtime_proof"} {
		call, err := exec.NewSourceCall(pod, kind)
		if err != nil {
			t.Fatal(err)
		}
		join, err := owner.Files().RunFixedExec(context.Background(), call, nil)
		// No API endpoint is contacted: an empty fixture kubeconfig makes the
		// target unavailable. Actual original helper/hello/G/Wait/IPC closure must
		// still be positive and distinct from that target failure.
		if err == nil || !join.Matches(call) || !join.Closed() {
			t.Fatal("native original closure lost behind target failure", kind, join, err)
		}
	}
	owner.files.mu.Lock()
	before := len(owner.files.files)
	owner.files.mu.Unlock()
	extra, err := exec.NewSourceCall(pod, "runtime_proof")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Files().RunFixedExec(t.Context(), extra, nil); err == nil {
		t.Fatal("fifth inspection request admitted")
	}
	owner.files.mu.Lock()
	after := len(owner.files.files)
	owner.files.mu.Unlock()
	if before != 4 || after != 4 {
		t.Fatal("fifth request created another file", before, after)
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err != nil || !result.SourceClosed || !result.SourceSettled || result.PromisedDisk != 327680 {
		t.Fatal(result, err)
	}
	parent := sourceCoreDisk(t, m, owner.parent.ID)
	if parent.State != "needs_attention" {
		t.Fatal("written helper storage debt released", parent)
	}
}
