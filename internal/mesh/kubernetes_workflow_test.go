package mesh

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/kubejournal"
	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func kubeWorkflowFixture(t *testing.T) kubejournal.Record {
	t.Helper()
	plan, err := kubeprovision.NewPlan(kubeprovision.Review{Key: strings.Repeat("a", 64), Owner: local.CurrentIdentity().UID, Reason: "protected workflow fixture", Cluster: kubeprovision.Cluster{Context: "fixture", Server: "https://fixture.invalid", CAHash: strings.Repeat("b", 64), AuthSubject: "operator"}, Namespace: kubeprovision.Namespace{Name: "default", UID: "ns-uid"}, Node: kubeprovision.Node{Name: "worker", UID: "node-uid", OS: "linux", Arch: "arm64", Ready: true}, Image: "registry.invalid/base@sha256:" + strings.Repeat("c", 64), BinarySHA256: strings.Repeat("d", 64), BinaryBytes: 1024, CatalogSHA256: strings.Repeat("e", 64), Resources: kubeprovision.Resources{CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi", EphemeralRequest: "256Mi", EphemeralLimit: "1Gi"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := kubejournal.New("kubernetes_install", plan.Review.Key, plan.Review.Owner, strings.Repeat("f", 32), plan, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestKubernetesWorkflowProtectedWriterCASAndGenericDecisionRefusal(t *testing.T) {
	home := coordinationHome(t)
	r := kubeWorkflowFixture(t)
	g, err := ClaimKubernetesWorkflow(home, r)
	if err != nil {
		t.Fatal(err)
	}
	path, err := coordinationPath(home, r.Key)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("workflow lost private native mode", st, err)
	}
	if err = g.ChangeKubernetesWorkflow(r.Revision, func(next *kubejournal.Record) error {
		return next.ReviewStage(next.Revision, "create", "", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	op, ok, err := ReadCoordination(home, r.Key)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if op.KubernetesWorkflow == nil || op.KubernetesWorkflow.Revision != 2 || op.Revision != 2 {
		t.Fatal(op)
	}
	review := op.KubernetesWorkflow.Stages[0].Review
	if _, err = DecideCoordination(home, r.Key, review.Revision, review.SHA256, true); err == nil {
		t.Fatal("generic all-stage bit granted Kubernetes authority")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Change(func(next *Coordination) error { next.KubernetesWorkflow = nil; return nil }); err == nil {
		t.Fatal("known-new stage record removed")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed CAS changed original protected bytes")
	}
	if err = g.ChangeKubernetesWorkflow(2, func(next *kubejournal.Record) error {
		return next.Decide(2, "create", review.Revision, review.SHA256, true, time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	op, _, err = ReadCoordination(home, r.Key)
	if err != nil || !op.KubernetesWorkflow.Stages[0].Approved || op.Approved {
		t.Fatal(op, err)
	}
	if err = g.ChangeKubernetesWorkflow(3, func(next *kubejournal.Record) error { next.Stages[0].Approved = false; next.Revision++; return nil }); err == nil {
		t.Fatal("prior exact approval overwritten")
	}
	cancelled, err := CancelCoordination(home, r.Key, op.Revision)
	if err != nil || !cancelled.CancelRequested || cancelled.KubernetesWorkflow.Phase != "cancelled" || !cancelled.KubernetesWorkflow.Stages[0].Approved {
		t.Fatal("generic cancellation did not preserve and stop typed stages", cancelled, err)
	}
}
func TestKubernetesWorkflowProtectedReaderRejectsOuterAliasesAndDuplicates(t *testing.T) {
	for _, mode := range []string{"alias", "duplicate", "missing-stages"} {
		t.Run(mode, func(t *testing.T) {
			home := coordinationHome(t)
			r := kubeWorkflowFixture(t)
			if _, err := ClaimKubernetesWorkflow(home, r); err != nil {
				t.Fatal(err)
			}
			path, _ := coordinationPath(home, r.Key)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(raw)
			switch mode {
			case "alias":
				raw = bytes.Replace(raw, []byte(`"kubernetes_workflow_v1"`), []byte(`"KUBERNETES_WORKFLOW_V1"`), 1)
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"schema": 1`), []byte(`"schema": 1,"schema": 1`), 1)
			case "missing-stages":
				raw = bytes.Replace(raw, []byte(`"stages":`), []byte(`"stage_list":`), 1)
			}
			if bytes.Equal(original, raw) {
				t.Fatal("negative fixture did not alter the protected record")
			}
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err = ReadCoordination(home, r.Key); err == nil {
				t.Fatal("malformed protected workflow accepted", mode)
			}
			if _, err = ListCoordinations(home); err == nil {
				t.Fatal("history reader ignored malformed workflow", mode)
			}
		})
	}
}

func TestKubernetesWorkflowFinalEnvelopeRefusalPreservesProtectedBytes(t *testing.T) {
	for _, mode := range []string{"string", "aggregate", "target-count"} {
		t.Run(mode, func(t *testing.T) {
			home := coordinationHome(t)
			r := kubeWorkflowFixture(t)
			g, err := ClaimKubernetesWorkflow(home, r)
			if err != nil {
				t.Fatal(err)
			}
			path, err := coordinationPath(home, r.Key)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			revision := g.revision
			err = g.Change(func(next *Coordination) error {
				switch mode {
				case "string":
					next.Worker = strings.Repeat("w", 65537)
				case "aggregate":
					// Each string fits, and the nested record stays unchanged.
					// Only the complete outer envelope exceeds 512 KiB.
					part := strings.Repeat("x", 65536)
					next.Targets = []string{part, part, part, part}
					next.Worker = part
					next.Actor.Username = part
					next.Error = &model.Error{Code: part, Message: part}
				case "target-count":
					next.Targets = []string{"a", "b", "c", "d", "e"}
				}
				return nil
			})
			if err == nil || g.revision != revision {
				t.Fatal("unreadable outer envelope advanced original worker", mode, err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatal("failed write replaced protected original", readErr)
			}
			if err = g.ChangeKubernetesWorkflow(r.Revision, func(next *kubejournal.Record) error {
				return next.ReviewStage(next.Revision, "create", "", time.Now().UTC())
			}); err != nil {
				t.Fatal("ordinary transition lost original worker", err)
			}
			if op, ok, err := ReadCoordination(home, r.Key); err != nil || !ok || op.Revision != revision+1 || op.KubernetesWorkflow == nil || op.KubernetesWorkflow.Revision != r.Revision+1 {
				t.Fatal("successful protected write is not strictly readable", op, ok, err)
			}
		})
	}
}
