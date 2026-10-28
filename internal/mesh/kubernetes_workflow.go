package mesh

import (
	"fmt"
	"time"

	"getarm.org/internal/kubejournal"
	"getarm.org/internal/model"
)

func validateKubernetesCoordination(op Coordination) error {
	kube := op.Kind == "kubernetes_install" || op.Kind == "kubernetes_connect"
	r := op.KubernetesWorkflow
	if !kube {
		if r != nil {
			return fmt.Errorf("Kubernetes stages mixed with another coordinator kind")
		}
		return nil
	}
	if r == nil || r.Validate() != nil || r.Key != op.Key || r.Kind != op.Kind || r.Owner != op.Actor.UID || r.Digest() != op.Digest || r.Phase != op.Phase || op.Approved || len(op.Request) != 0 || len(op.Review) != 0 || len(op.Result) != 0 || op.ReviewRevision != 0 || op.ReviewHash != "" || !op.ReviewExpires.IsZero() || op.Handoff != nil || op.ResumeFrom != "" || op.ParentKey != "" || op.ResolvedBy != "" {
		return fmt.Errorf("Kubernetes coordination lacks its exact protected stage record")
	}
	return nil
}

// ClaimKubernetesWorkflow writes a known-new closed field in the recognized
// protected coordination namespace. Its caller must ACK the database contract
// first. This record and its worker guard confer no source/native authority.
func ClaimKubernetesWorkflow(home string, record kubejournal.Record) (*CoordinationGuard, error) {
	r, err := record.Snapshot()
	if err != nil {
		return nil, err
	}
	if r.Revision != 1 || r.Phase != "pending" || len(r.Sources) != 0 {
		return nil, fmt.Errorf("only a fresh Kubernetes workflow may be claimed")
	}
	target := "kubernetes:" + r.Plan.Review.Cluster.Server + "/" + r.Plan.Review.Namespace.UID + "/" + r.Plan.Review.Node.UID
	if r.Kind == "kubernetes_connect" {
		target = "kubernetes-pod:" + r.Connection.Runtime.PodUID
	}
	return claimCoordination(home, r.Key, r.Kind, r.Digest(), target, &r)
}

// ChangeKubernetesWorkflow performs one exact durable record CAS. No effect,
// admission, native close/join or human wait may occur inside fn.
func (g *CoordinationGuard) ChangeKubernetesWorkflow(revision int64, fn func(*kubejournal.Record) error) error {
	return g.Change(func(op *Coordination) error {
		if op.KubernetesWorkflow == nil || op.KubernetesWorkflow.Revision != revision {
			return model.Err("review_changed", "Kubernetes stage revision changed")
		}
		if err := fn(op.KubernetesWorkflow); err != nil {
			return err
		}
		op.Phase = op.KubernetesWorkflow.Phase
		return nil
	})
}
func CancelKubernetesWorkflow(home, key string, revision int64, now time.Time) (Coordination, error) {
	unlock, err := coordinationLock(home)
	if err != nil {
		return Coordination{}, err
	}
	defer unlock()
	op, ok, err := ReadCoordination(home, key)
	if err != nil {
		return op, err
	}
	if !ok || op.KubernetesWorkflow == nil || op.Revision != revision {
		return op, model.Err("review_changed", "Kubernetes workflow changed")
	}
	if err = op.KubernetesWorkflow.Cancel(op.KubernetesWorkflow.Revision, now); err != nil {
		return op, err
	}
	op.CancelRequested = true
	op.Phase = op.KubernetesWorkflow.Phase
	op.Revision++
	op.Updated = now.UTC()
	return op, saveCoordination(home, op)
}
