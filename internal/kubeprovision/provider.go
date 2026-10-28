package kubeprovision

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"
)

var ErrUnknown = errors.New("Kubernetes outcome unknown; inspect original key without creating again")
var ErrIdentity = errors.New("Kubernetes authority or owned Pod identity changed")

// Pod is the minimum live evidence required to identify one ARM-created Pod.
type Pod struct {
	Name            string    `json:"name"`
	UID             string    `json:"uid"`
	Namespace       string    `json:"namespace"`
	OwnerLabel      string    `json:"owner_label"`
	KeyLabel        string    `json:"key_label"`
	KeyAnnotation   string    `json:"key_annotation"`
	PlanAnnotation  string    `json:"plan_annotation"`
	NodeName        string    `json:"node_name"`
	SelectedNode    string    `json:"selected_node"`
	Phase           string    `json:"phase"`
	Image           string    `json:"image"`
	Safe            bool      `json:"safe"`
	Resources       Resources `json:"resources"`
	VolumeSizeLimit string    `json:"volume_size_limit"`
}

// API must operate with one explicit config/context and namespace. ListByKey is
// read-only and must use an exact label selector. CreatePod must never retry a
// POST internally after an ambiguous transport outcome.
type API interface {
	Cluster(context.Context) (Cluster, error)
	Namespace(context.Context, string) (Namespace, error)
	Nodes(context.Context) ([]Node, error)
	Node(context.Context, string) (Node, error)
	ListByKey(context.Context, string, string) ([]Pod, error)
	CreatePod(context.Context, PodSpec) (Pod, error)
	GetPod(context.Context, string, string) (Pod, error)
}

type Phase string

const (
	PhasePreflight       Phase = "preflight"
	PhaseCreateAttempted Phase = "create_attempted"
	PhaseCreated         Phase = "created"
	PhaseUnknown         Phase = "unknown"
	PhaseConflict        Phase = "conflict"
)

type Record struct {
	SourceCleanupUnknown bool      `json:"source_cleanup_unknown,omitempty"`
	Plan                 Plan      `json:"plan"`
	Phase                Phase     `json:"phase"`
	Pod                  *Pod      `json:"pod,omitempty"`
	BinaryPhase          string    `json:"binary_phase,omitempty"`
	StartPhase           string    `json:"start_phase,omitempty"`
	Revision             uint64    `json:"revision"`
	Updated              time.Time `json:"updated_at"`
}

// Journal serializes an exact key's intent and observations. Insert is durable
// before it returns. Update compares the expected revision and never changes
// Plan; an implementation must fsync/transactionally commit before ACK.
type Journal interface {
	Read(context.Context, string) (Record, bool, error)
	Insert(context.Context, Record) error
	Update(context.Context, string, uint64, func(*Record) error) (Record, error)
}

type Provider struct {
	API     API
	Journal Journal
}

func (p Provider) Check(ctx context.Context, plan Plan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	cluster, err := p.API.Cluster(ctx)
	if err != nil {
		return err
	}
	ns, err := p.API.Namespace(ctx, plan.Review.Namespace.Name)
	if err != nil {
		return err
	}
	node, err := p.API.Node(ctx, plan.Review.Node.Name)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(cluster, plan.Review.Cluster) || ns != plan.Review.Namespace || node.Name != plan.Review.Node.Name || node.UID != plan.Review.Node.UID || node.OS != plan.Review.Node.OS || node.Arch != plan.Review.Node.Arch || !node.Ready || node.Unschedulable || len(node.Taints) != 0 {
		return ErrIdentity
	}
	return nil
}

// Candidates returns a live inventory for both dashboards' typeahead. Node
// selection is not authorization; Apply rechecks the chosen UID and platform.
func (p Provider) Candidates(ctx context.Context, query string, limit int) ([]Node, error) {
	nodes, err := p.API.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	return FilterNodes(nodes, query, limit), nil
}

func checkPod(plan Plan, pod Pod) error {
	if pod.Name == "" || pod.UID == "" || pod.Namespace != plan.Review.Namespace.Name || pod.OwnerLabel != OwnerValue || pod.KeyLabel != plan.Review.Key[:32] || pod.KeyAnnotation != plan.Review.Key || pod.PlanAnnotation != plan.SHA256 || pod.SelectedNode != plan.Review.Node.Name || (pod.NodeName != "" && pod.NodeName != plan.Review.Node.Name) || pod.Image != plan.Review.Image || pod.Resources != plan.Review.Resources || pod.VolumeSizeLimit != plan.Pod.VolumeSizeLimit || !pod.Safe {
		return ErrIdentity
	}
	return nil
}

// Apply performs at most one create attempt per durable key. A crash after
// Insert is conservative: lookup may find the Pod, but no second POST occurs.
func (p Provider) Apply(ctx context.Context, plan Plan) (Record, error) {
	if p.API == nil || p.Journal == nil {
		return Record{}, errors.New("Kubernetes API and durable journal required")
	}
	if err := p.Check(ctx, plan); err != nil {
		return Record{}, err
	}
	if prior, ok, err := p.Journal.Read(ctx, plan.Review.Key); err != nil {
		return Record{}, err
	} else if ok {
		if prior.Plan.SHA256 != plan.SHA256 {
			return prior, ErrIdentity
		}
		if prior.Phase == PhasePreflight {
			return p.finishPreflight(ctx, prior)
		}
		return p.Lookup(ctx, plan.Review.Key)
	}
	record := Record{Plan: plan, Phase: PhasePreflight, Revision: 1, Updated: time.Now().UTC()}
	if err := p.Journal.Insert(ctx, record); err != nil {
		if !errors.Is(err, ErrJournalConflict) {
			return record, err
		}
		// A racing identical caller may finish only the stored preflight. No
		// caller may infer that its own CreatePod request was dispatched.
		prior, ok, readErr := p.Journal.Read(ctx, plan.Review.Key)
		if readErr != nil {
			return record, readErr
		}
		if !ok || prior.Plan.SHA256 != plan.SHA256 {
			return prior, ErrIdentity
		}
		if prior.Phase == PhasePreflight {
			return p.finishPreflight(ctx, prior)
		}
		return p.Lookup(ctx, plan.Review.Key)
	}
	return p.finishPreflight(ctx, record)
}

func (p Provider) finishPreflight(ctx context.Context, record Record) (Record, error) {
	plan := record.Plan
	if record.Phase != PhasePreflight {
		return record, ErrIdentity
	}
	// The fresh read checks for an earlier externally created Pod with the same
	// key; such a Pod is not adopted as this just-recorded intent.
	preexisting, err := p.API.ListByKey(ctx, plan.Review.Namespace.Name, plan.Review.Key)
	if err != nil {
		return record, fmt.Errorf("preflight Pod lookup unavailable; create was not dispatched: %w", err)
	}
	if len(preexisting) != 0 {
		updated, updateErr := p.Journal.Update(ctx, plan.Review.Key, record.Revision, func(r *Record) error { r.Phase = PhaseConflict; return nil })
		if updateErr != nil {
			return record, updateErr
		}
		return updated, ErrIdentity
	}
	if err := p.Check(ctx, plan); err != nil {
		return record, err
	}
	var updateErr error
	record, updateErr = p.Journal.Update(ctx, plan.Review.Key, record.Revision, func(r *Record) error {
		if r.Phase != PhasePreflight {
			return ErrJournalConflict
		}
		r.Phase = PhaseCreateAttempted
		return nil
	})
	if updateErr != nil {
		return record, fmt.Errorf("create was not dispatched: %w", updateErr)
	}
	pod, err := p.API.CreatePod(ctx, plan.Pod)
	if err != nil {
		return p.markUnknown(ctx, record)
	}
	if err := checkPod(plan, pod); err != nil {
		return p.markUnknown(ctx, record)
	}
	return p.Journal.Update(ctx, plan.Review.Key, record.Revision, func(r *Record) error {
		r.Phase = PhaseCreated
		r.Pod = &pod
		return nil
	})
}

func (p Provider) markUnknown(ctx context.Context, r Record) (Record, error) {
	updated, err := p.Journal.Update(ctx, r.Plan.Review.Key, r.Revision, func(next *Record) error { next.Phase = PhaseUnknown; return nil })
	if err != nil {
		return r, fmt.Errorf("%w: source journal update: %v", ErrUnknown, err)
	}
	return updated, ErrUnknown
}

// Lookup reads original intent and current cluster evidence. It never creates
// or writes. An empty list leaves the original create attempt unknown.
func (p Provider) Lookup(ctx context.Context, key string) (Record, error) {
	if p.API == nil || p.Journal == nil {
		return Record{}, errors.New("Kubernetes API and durable journal required")
	}
	r, ok, err := p.Journal.Read(ctx, key)
	if err != nil {
		return r, err
	}
	if !ok {
		return r, errors.New("unknown Kubernetes operation key")
	}
	if r.Phase == PhaseConflict {
		return r, ErrIdentity
	}
	if r.Phase == PhasePreflight {
		return r, fmt.Errorf("preflight intent has no create dispatch; retry Apply with original reviewed plan")
	}
	if err := p.Check(ctx, r.Plan); err != nil {
		return r, err
	}
	pods, err := p.API.ListByKey(ctx, r.Plan.Review.Namespace.Name, key)
	if err != nil || len(pods) != 1 {
		return r, ErrUnknown
	}
	if err := checkPod(r.Plan, pods[0]); err != nil {
		return r, err
	}
	if r.Pod != nil && (r.Pod.Name != pods[0].Name || r.Pod.UID != pods[0].UID) {
		return r, ErrIdentity
	}
	if r.Pod == nil {
		observed := pods[0]
		r.Pod = &observed
	}
	return r, nil
}

// Reconcile pins a uniquely observed original-key Pod after a lost create ACK.
// It never creates a Pod and refuses any preexisting-key conflict.
func (p Provider) Reconcile(ctx context.Context, key string) (Record, error) {
	r, err := p.Lookup(ctx, key)
	if err != nil {
		return r, err
	}
	if r.Pod == nil {
		return r, ErrUnknown
	}
	if r.Phase == PhaseCreated {
		return r, nil
	}
	return p.updateSourceStage(ctx, key, r, func(next *Record) error {
		if next.Phase == PhaseConflict || next.Pod != nil {
			return ErrIdentity
		}
		next.Phase = PhaseCreated
		pod := *r.Pod
		next.Pod = &pod
		return nil
	})
}

// ExactPod verifies current UID before a later exec/forward. A stopped or
// replaced Pod never inherits the original operation's authority.
func (p Provider) ExactPod(ctx context.Context, key string) (Pod, error) {
	r, err := p.Lookup(ctx, key)
	if err != nil {
		return Pod{}, err
	}
	if r.Pod == nil {
		return Pod{}, ErrUnknown
	}
	pod, err := p.API.GetPod(ctx, r.Plan.Review.Namespace.Name, r.Pod.Name)
	if err != nil {
		return Pod{}, err
	}
	if err := checkPod(r.Plan, pod); err != nil {
		return Pod{}, err
	}
	if pod.UID != r.Pod.UID {
		return Pod{}, ErrIdentity
	}
	return pod, nil
}
