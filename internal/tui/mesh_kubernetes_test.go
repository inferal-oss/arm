package tui

import (
	"strings"
	"testing"

	"getarm.org/internal/kubeprovision"
)

func TestKubernetesTUITypeaheadSelectionAndStaleRead(t *testing.T) {
	m, session := fixtureDashboard(t)
	m.meshUI = &meshDashboard{mode: "nodes"}
	m.view = 6
	session.reply = func(op string, params, result any) error {
		switch op {
		case "kube_contexts":
			p := result.(*[]kubeprovision.ContextChoice)
			*p = []kubeprovision.ContextChoice{{Name: "local", ClusterName: "disposable", Namespace: "default", Current: true}}
		case "kube_nodes":
			p := result.(*struct {
				Cluster   kubeprovision.Cluster   `json:"cluster"`
				Namespace kubeprovision.Namespace `json:"namespace"`
				Nodes     []kubeprovision.Node    `json:"nodes"`
			})
			p.Namespace.Name = "default"
			p.Nodes = []kubeprovision.Node{{Name: "worker-a", UID: "exact-uid", OS: "linux", Arch: "arm64", Ready: true, AllocatableCPU: "1900m", AllocatableMemory: "7Gi", AllocatableEphemeral: "20Gi"}}
		default:
			t.Fatalf("unexpected operation %s", op)
		}
		return nil
	}
	contexts := press(m, "K")
	if contexts == nil || m.meshUI.mode != "kubernetes" {
		t.Fatal("Kubernetes inventory did not open")
	}
	nodes := m.acceptKubeInventory(contexts().(kubeInventoryMsg))
	if nodes == nil {
		t.Fatal("current context did not load Nodes")
	}
	first := nodes().(kubeInventoryMsg)
	if first.result.Nodes[0].Arch != "arm64" {
		t.Fatal("architecture missing")
	}
	m.acceptKubeInventory(first)
	if text := m.meshText(); !strings.Contains(text, "arm64") || !strings.Contains(text, "7Gi") || !strings.Contains(text, "20Gi") || !strings.Contains(text, "not live ARM CPU admission") {
		t.Fatal("resource details missing", text)
	}
	press(m, "enter")
	if m.meshUI.kube.chosen == nil || m.meshUI.kube.chosen.UID != "exact-uid" {
		t.Fatal("exact Node selection missing")
	}
	if text := m.meshText(); !strings.Contains(text, "context local") || !strings.Contains(text, "namespace default") || !strings.Contains(text, "exact UID exact-uid") {
		t.Fatal("selected context, namespace or UID missing", text)
	}
	m.meshUI.kube.nodes[0].Taints = []string{"dedicated=elsewhere:NoSchedule"}
	m.meshUI.kube.chosen = nil
	press(m, "enter")
	if m.meshUI.kube.chosen != nil || !strings.Contains(m.meshText(), "Unavailable for ARM Pod") {
		t.Fatal("tainted Node remained selectable")
	}
	search := press(m, "a")
	if search == nil || m.meshUI.kube.chosen != nil || len(m.meshUI.kube.nodes) != 0 {
		t.Fatal("typeahead left a stale Node selectable")
	}
	press(m, "q")
	press(m, "u")
	if m.meshUI.kube.query != "aqu" {
		t.Fatalf("global shortcuts intercepted typeahead: %q", m.meshUI.kube.query)
	}
	m.acceptKubeInventory(first)
	if m.meshUI.kube.chosen != nil {
		t.Fatal("stale result restored selection")
	}
	press(m, "esc")
	if m.meshUI.mode != "nodes" || m.meshUI.kube != nil {
		t.Fatal("inventory did not close")
	}
}
