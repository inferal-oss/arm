// Package kubeprovision holds the source-side, keyed Kubernetes Pod creation
// contract. API is deliberately small so the live Kubernetes adapter can be
// reviewed independently of ARM's durable ownership rules.
package kubeprovision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	OwnerLabel           = "app.kubernetes.io/managed-by"
	OwnerValue           = "arm-kubeprovision"
	KeyLabel             = "arm.yrashk.com/provision-key"
	KeyAnnotation        = "arm.yrashk.com/provision-key-full"
	PlanAnnotation       = "arm.yrashk.com/plan-sha256"
	MaxBinaryBytes int64 = 256 << 20
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var dnsPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Cluster is supplied by a live config/auth adapter. AuthSubject is the
// effective, server-reported identity, never a kubeconfig nickname or token.
type Cluster struct {
	Context     string   `json:"context"`
	Server      string   `json:"server"`
	CAHash      string   `json:"ca_sha256"`
	AuthSubject string   `json:"auth_subject"`
	AuthGroups  []string `json:"auth_groups"`
}

type Namespace struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

type Node struct {
	Name                 string            `json:"name"`
	UID                  string            `json:"uid"`
	OS                   string            `json:"os"`
	Arch                 string            `json:"arch"`
	Ready                bool              `json:"ready"`
	Unschedulable        bool              `json:"unschedulable"`
	Taints               []string          `json:"taints,omitempty"`
	AllocatableCPU       string            `json:"allocatable_cpu"`
	AllocatableMemory    string            `json:"allocatable_memory"`
	AllocatableEphemeral string            `json:"allocatable_ephemeral"`
	Allocatable          map[string]string `json:"allocatable,omitempty"`
	Capacity             map[string]string `json:"capacity,omitempty"`
	Zone                 string            `json:"zone,omitempty"`
	InstanceType         string            `json:"instance_type,omitempty"`
	Conditions           []string          `json:"conditions,omitempty"`
}

type Resources struct {
	CPURequest       string `json:"cpu_request"`
	CPULimit         string `json:"cpu_limit"`
	MemoryRequest    string `json:"memory_request"`
	MemoryLimit      string `json:"memory_limit"`
	EphemeralRequest string `json:"ephemeral_request"`
	EphemeralLimit   string `json:"ephemeral_limit"`
}

// Review is the human-visible immutable input. Image is a digest reference,
// not a mutable tag. CatalogHash covers the reviewed carrier manifest.
type Review struct {
	Key           string    `json:"key"`
	Owner         string    `json:"owner"`
	Reason        string    `json:"reason"`
	Cluster       Cluster   `json:"cluster"`
	Namespace     Namespace `json:"namespace"`
	Node          Node      `json:"node"`
	Image         string    `json:"image"`
	BinarySHA256  string    `json:"binary_sha256"`
	BinaryBytes   int64     `json:"binary_bytes"`
	CatalogSHA256 string    `json:"catalog_sha256"`
	Resources     Resources `json:"resources"`
}

// PodSpec is mapped one-for-one by the live adapter. Every security setting
// here is required; an adapter must not silently weaken or omit it.
type PodSpec struct {
	GenerateName                 string    `json:"generate_name"`
	Namespace                    string    `json:"namespace"`
	NodeName                     string    `json:"node_name"`
	Image                        string    `json:"image"`
	OwnerLabel                   string    `json:"owner_label"`
	KeyLabel                     string    `json:"key_label"`
	KeyAnnotation                string    `json:"key_annotation"`
	PlanAnnotation               string    `json:"plan_annotation"`
	Resources                    Resources `json:"resources"`
	VolumeSizeLimit              string    `json:"volume_size_limit"`
	RunAsUser                    int64     `json:"run_as_user"`
	RunAsGroup                   int64     `json:"run_as_group"`
	FSGroup                      int64     `json:"fs_group"`
	RunAsNonRoot                 bool      `json:"run_as_non_root"`
	ReadOnlyRootFilesystem       bool      `json:"read_only_root_filesystem"`
	AllowPrivilegeEscalation     bool      `json:"allow_privilege_escalation"`
	DropAllCapabilities          bool      `json:"drop_all_capabilities"`
	RuntimeDefaultSeccomp        bool      `json:"runtime_default_seccomp"`
	NotReadyTolerationSeconds    int64     `json:"not_ready_toleration_seconds"`
	UnreachableTolerationSeconds int64     `json:"unreachable_toleration_seconds"`
	AutomountServiceAccountToken bool      `json:"automount_service_account_token"`
	HostNetwork                  bool      `json:"host_network"`
	HostPID                      bool      `json:"host_pid"`
	HostIPC                      bool      `json:"host_ipc"`
	EnableServiceLinks           bool      `json:"enable_service_links"`
	RestartPolicy                string    `json:"restart_policy"`
}

type Plan struct {
	Review Review  `json:"review"`
	Pod    PodSpec `json:"pod"`
	SHA256 string  `json:"sha256"`
}

func validDNS(s string) bool { return len(s) <= 63 && dnsPattern.MatchString(s) }
func validNodeName(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !validDNS(label) {
			return false
		}
	}
	return true
}

func (c Cluster) Validate() error {
	u, err := url.Parse(c.Server)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Path != "" || !digestPattern.MatchString(c.CAHash) || strings.TrimSpace(c.Context) == "" || strings.TrimSpace(c.AuthSubject) == "" {
		return errors.New("cluster requires explicit HTTPS server, CA pin, context and effective auth identity")
	}
	return nil
}

func (r Review) Validate() error {
	if !digestPattern.MatchString(r.Key) || !digestPattern.MatchString(r.BinarySHA256) || !digestPattern.MatchString(r.CatalogSHA256) || r.BinaryBytes < 1 || r.BinaryBytes > MaxBinaryBytes || strings.TrimSpace(r.Owner) == "" || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 4096 {
		return errors.New("invalid keyed owner, reason or bounded payload")
	}
	if err := r.Cluster.Validate(); err != nil {
		return err
	}
	if !validDNS(r.Namespace.Name) || r.Namespace.UID == "" || !validNodeName(r.Node.Name) || r.Node.UID == "" || r.Node.OS != "linux" || (r.Node.Arch != "amd64" && r.Node.Arch != "arm64") || !r.Node.Ready || r.Node.Unschedulable || len(r.Node.Taints) != 0 {
		return errors.New("namespace/selected ready untainted Linux Node identity required")
	}
	if !regexp.MustCompile(`^[^@[:space:]]+@sha256:[a-f0-9]{64}$`).MatchString(r.Image) {
		return errors.New("image must be an exact sha256 digest reference")
	}
	for _, q := range []string{r.Resources.CPURequest, r.Resources.CPULimit, r.Resources.MemoryRequest, r.Resources.MemoryLimit, r.Resources.EphemeralRequest, r.Resources.EphemeralLimit} {
		if q == "" || len(q) > 32 || strings.ContainsAny(q, " \t\n") {
			return errors.New("all resource requests and limits must be explicit")
		}
	}
	if _, err := quantities(r.Resources); err != nil {
		return err
	}
	volume, _ := resource.ParseQuantity(r.Resources.EphemeralLimit)
	if volume.Value() < r.BinaryBytes+(64<<20) {
		return errors.New("private volume limit must cover binary plus 64 MiB startup allowance")
	}
	return nil
}

func NewPlan(r Review) (Plan, error) {
	if err := r.Validate(); err != nil {
		return Plan{}, err
	}
	r.Resources.CPURequest = canonicalQuantity(r.Resources.CPURequest)
	r.Resources.CPULimit = canonicalQuantity(r.Resources.CPULimit)
	r.Resources.MemoryRequest = canonicalQuantity(r.Resources.MemoryRequest)
	r.Resources.MemoryLimit = canonicalQuantity(r.Resources.MemoryLimit)
	r.Resources.EphemeralRequest = canonicalQuantity(r.Resources.EphemeralRequest)
	r.Resources.EphemeralLimit = canonicalQuantity(r.Resources.EphemeralLimit)
	r.Cluster.AuthGroups = append([]string(nil), r.Cluster.AuthGroups...)
	sort.Strings(r.Cluster.AuthGroups)
	r.Node.Taints = append([]string(nil), r.Node.Taints...)
	r.Node.Conditions = append([]string(nil), r.Node.Conditions...)
	r.Node.Allocatable = cloneMap(r.Node.Allocatable)
	r.Node.Capacity = cloneMap(r.Node.Capacity)
	p := PodSpec{GenerateName: "arm-node-", Namespace: r.Namespace.Name, NodeName: r.Node.Name, Image: r.Image,
		OwnerLabel: OwnerValue, KeyLabel: r.Key[:32], KeyAnnotation: r.Key, Resources: r.Resources, VolumeSizeLimit: r.Resources.EphemeralLimit,
		RunAsUser: 65532, RunAsGroup: 65532, FSGroup: 65532, RunAsNonRoot: true, ReadOnlyRootFilesystem: true, DropAllCapabilities: true,
		RuntimeDefaultSeccomp: true, NotReadyTolerationSeconds: 300, UnreachableTolerationSeconds: 300, RestartPolicy: "Never"}
	plan := Plan{Review: r, Pod: p}
	raw, err := json.Marshal(plan)
	if err != nil {
		return Plan{}, err
	}
	sum := sha256.Sum256(raw)
	plan.SHA256 = hex.EncodeToString(sum[:])
	plan.Pod.PlanAnnotation = plan.SHA256
	return plan, nil
}

func canonicalQuantity(s string) string { q, _ := resource.ParseQuantity(s); return q.String() }

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// FilterNodes gives both dashboards a stable typeahead source. Selection must
// still use the displayed UID and be revalidated live before Pod creation.
func FilterNodes(nodes []Node, query string, limit int) []Node {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]Node, 0, min(len(nodes), limit))
	sorted := append([]Node(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, n := range sorted {
		if q != "" && !strings.Contains(strings.ToLower(n.Name+" "+n.Arch+" "+n.Zone+" "+n.InstanceType), q) {
			continue
		}
		out = append(out, n)
		if len(out) == limit {
			break
		}
	}
	return out
}

func (p Plan) Validate() error {
	canonical, err := NewPlan(p.Review)
	if err != nil {
		return err
	}
	if canonical.SHA256 != p.SHA256 || canonical.Pod != p.Pod {
		return fmt.Errorf("reviewed Pod plan changed")
	}
	return nil
}
