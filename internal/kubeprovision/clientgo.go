package kubeprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// ContextChoice contains only nonsecret kubeconfig metadata. The user chooses
// a context explicitly when several are available; Current is only a default
// suggestion for a new operation, never an existing operation's identity.
type ContextChoice struct {
	Name         string `json:"name"`
	ClusterName  string `json:"cluster_name"`
	AuthInfoName string `json:"auth_info_name"`
	Namespace    string `json:"namespace"`
	Current      bool   `json:"current"`
}

func Contexts() ([]ContextChoice, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	raw, err := rules.Load()
	if err != nil {
		return nil, err
	}
	out := make([]ContextChoice, 0, len(raw.Contexts))
	for name, c := range raw.Contexts {
		ns := c.Namespace
		if ns == "" {
			ns = "default"
		}
		out = append(out, ContextChoice{Name: name, ClusterName: c.Cluster, AuthInfoName: c.AuthInfo, Namespace: ns, Current: name == raw.CurrentContext})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ClientGo is a live native Kubernetes API, exec and port-forward adapter.
// It holds one resolved explicit context; reopening after restart must compare
// Cluster/Namespace/Node against the persisted reviewed plan.
type ClientGo struct {
	contextName string
	config      *rest.Config
	client      kubernetes.Interface
	server      string
	caHash      string
}

// This starts ARM's local control backend after publication. It does not open
// or authorize the current mesh listener; connection setup is separate.
func bootstrapBudgets(r Resources) (string, string, string, error) {
	q, err := quantities(r)
	if err != nil {
		return "", "", "", err
	}
	cpu := q.Requests.Cpu().AsApproximateFloat64()
	memory := q.Requests.Memory().Value()
	disk := q.Requests.StorageEphemeral().Value()
	if cpu <= 0 || memory <= 0 || disk <= 0 {
		return "", "", "", errors.New("positive reviewed bootstrap budgets required")
	}
	return strconv.FormatFloat(cpu, 'g', -1, 64), strconv.FormatInt(memory, 10), strconv.FormatInt(disk, 10), nil
}

func bootstrapCommand(r Resources) (string, error) {
	cpu, memory, disk, err := bootstrapBudgets(r)
	if err != nil {
		return "", err
	}
	return "umask 077; mkdir -p /arm/private; while [ ! -f /arm/private/verified-start ]; do sleep 1; done; exec /arm/private/arm --home /arm/private/state serve --listen 127.0.0.1:9443 --cpu " + cpu + " --memory " + memory + " --disk " + disk, nil
}

func resolveContext(name string) (string, *rest.Config, string, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	raw, err := rules.Load()
	if err != nil {
		return "", nil, "", "", err
	}
	if name == "" {
		name = raw.CurrentContext
	}
	if name == "" || raw.Contexts[name] == nil {
		return "", nil, "", "", errors.New("explicit Kubernetes context not found")
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: name}).ClientConfig()
	if err != nil {
		return "", nil, "", "", err
	}
	if config.Insecure || config.TLSClientConfig.Insecure || !strings.HasPrefix(config.Host, "https://") {
		return "", nil, "", "", errors.New("verified Kubernetes HTTPS configuration required")
	}
	ca := config.CAData
	if len(ca) == 0 && config.CAFile != "" {
		ca, err = os.ReadFile(config.CAFile)
		if err != nil {
			return "", nil, "", "", err
		}
	}
	if len(ca) == 0 {
		return "", nil, "", "", errors.New("explicit Kubernetes CA bundle required")
	}
	// Freeze the reviewed CA bytes for every later typed, exec and forwarding
	// transport. A mutable CAFile must not silently expand this key's trust.
	config.CAData = append([]byte(nil), ca...)
	config.CAFile = ""
	sum := sha256.Sum256(ca)
	return name, config, strings.TrimSuffix(config.Host, "/"), hex.EncodeToString(sum[:]), nil
}

func OpenContext(name string) (*ClientGo, error) {
	name, config, server, caHash, err := resolveContext(name)
	if err != nil {
		return nil, err
	}
	// Initial discovery also authenticates and must stay on its selected origin.
	config.WrapTransport, err = reviewedOriginGuard(server)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &ClientGo{contextName: name, config: config, client: client, server: server, caHash: caHash}, nil
}

// OpenReviewedContext binds local transport trust before client construction
// or any authenticated request. In particular, an unreviewed endpoint, CA,
// TLS name, proxy or credential plugin never receives the saved credential.
func OpenReviewedContext(plan Plan) (*ClientGo, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	name, config, server, caHash, err := resolveContext(plan.Review.Cluster.Context)
	if err != nil {
		return nil, err
	}
	if name != plan.Review.Cluster.Context || server != plan.Review.Cluster.Server || caHash != plan.Review.Cluster.CAHash {
		return nil, ErrIdentity
	}
	if err := checkOwnedStreamCredentials(config); err != nil {
		return nil, err
	}
	if config.ServerName != "" || config.Proxy != nil || config.Transport != nil || config.WrapTransport != nil || config.Dial != nil || config.CertFile != "" || config.KeyFile != "" || len(config.NextProtos) != 0 {
		return nil, errors.New("unreviewed Kubernetes transport configuration")
	}
	// clientcmd has already loaded a token file into BearerToken. Keep the
	// exact token through SSR, exec and forwarding instead of refreshing it
	// from a mutable path between identity check and effect.
	config.BearerTokenFile = ""
	config.CertData = append([]byte(nil), config.CertData...)
	config.KeyData = append([]byte(nil), config.KeyData...)
	// A nil Proxy would use ambient HTTP(S)_PROXY in client-go. Force direct
	// transport because no proxy identity exists in the reviewed plan.
	config.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	// The typed client and every stream transport reuse this origin fence.
	// HTTP redirect handling must not expand the reviewed endpoint authority.
	config.WrapTransport, err = reviewedOriginGuard(server)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &ClientGo{contextName: name, config: config, client: client, server: server, caHash: caHash}, nil
}

// NewClientGoForTest injects a Kubernetes typed fake or test server. Its caller
// supplies a pinned config identity; it does not load ambient kubeconfig.
func NewClientGoForTest(name, server, caHash string, config *rest.Config, client kubernetes.Interface) *ClientGo {
	return &ClientGo{contextName: name, server: server, caHash: caHash, config: config, client: client}
}

func (c *ClientGo) Cluster(ctx context.Context) (Cluster, error) {
	identity, err := c.client.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return Cluster{}, fmt.Errorf("effective Kubernetes identity unavailable: %w", err)
	}
	user := identity.Status.UserInfo.Username
	if user == "" {
		return Cluster{}, errors.New("Kubernetes did not report effective username")
	}
	groups := append([]string(nil), identity.Status.UserInfo.Groups...)
	sort.Strings(groups)
	return Cluster{Context: c.contextName, Server: c.server, CAHash: c.caHash, AuthSubject: user, AuthGroups: groups}, nil
}

func (c *ClientGo) Namespace(ctx context.Context, name string) (Namespace, error) {
	ns, err := c.client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Namespace{}, err
	}
	return Namespace{Name: ns.Name, UID: string(ns.UID)}, nil
}

func nodeFromAPI(n *corev1.Node) Node {
	out := Node{Name: n.Name, UID: string(n.UID), OS: n.Labels[corev1.LabelOSStable], Arch: n.Labels[corev1.LabelArchStable], Unschedulable: n.Spec.Unschedulable,
		Zone: n.Labels[corev1.LabelTopologyZone], InstanceType: n.Labels[corev1.LabelInstanceTypeStable], Allocatable: map[string]string{}, Capacity: map[string]string{}}
	for k, v := range n.Status.Allocatable {
		out.Allocatable[string(k)] = v.String()
	}
	for k, v := range n.Status.Capacity {
		out.Capacity[string(k)] = v.String()
	}
	out.AllocatableCPU = n.Status.Allocatable.Cpu().String()
	out.AllocatableMemory = n.Status.Allocatable.Memory().String()
	out.AllocatableEphemeral = n.Status.Allocatable.StorageEphemeral().String()
	for _, t := range n.Spec.Taints {
		out.Taints = append(out.Taints, t.Key+"="+t.Value+":"+string(t.Effect))
	}
	for _, condition := range n.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			out.Ready = true
		}
		if condition.Status != corev1.ConditionFalse {
			out.Conditions = append(out.Conditions, string(condition.Type)+"="+string(condition.Status))
		}
	}
	sort.Strings(out.Taints)
	sort.Strings(out.Conditions)
	return out
}

func (c *ClientGo) Nodes(ctx context.Context) ([]Node, error) {
	list, err := c.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	if list.Continue != "" {
		return nil, errors.New("node inventory exceeds one page; selection refused")
	}
	out := make([]Node, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, nodeFromAPI(&list.Items[i]))
	}
	return out, nil
}

func (c *ClientGo) Node(ctx context.Context, name string) (Node, error) {
	n, err := c.client.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Node{}, err
	}
	return nodeFromAPI(n), nil
}

func podFromAPI(p *corev1.Pod) Pod {
	image := ""
	resources := Resources{}
	if len(p.Spec.Containers) == 1 {
		image = p.Spec.Containers[0].Image
		c := p.Spec.Containers[0]
		resources = Resources{CPURequest: c.Resources.Requests.Cpu().String(), CPULimit: c.Resources.Limits.Cpu().String(), MemoryRequest: c.Resources.Requests.Memory().String(), MemoryLimit: c.Resources.Limits.Memory().String(), EphemeralRequest: c.Resources.Requests.StorageEphemeral().String(), EphemeralLimit: c.Resources.Limits.StorageEphemeral().String()}
	}
	volumeLimit := ""
	if len(p.Spec.Volumes) == 1 && p.Spec.Volumes[0].EmptyDir != nil && p.Spec.Volumes[0].EmptyDir.SizeLimit != nil {
		volumeLimit = p.Spec.Volumes[0].EmptyDir.SizeLimit.String()
	}
	selected := selectedNode(p.Spec)
	return Pod{Name: p.Name, UID: string(p.UID), Namespace: p.Namespace, OwnerLabel: p.Labels[OwnerLabel], KeyLabel: p.Labels[KeyLabel], KeyAnnotation: p.Annotations[KeyAnnotation], PlanAnnotation: p.Annotations[PlanAnnotation], NodeName: p.Spec.NodeName, SelectedNode: selected, Phase: string(p.Status.Phase), Image: image, Safe: safePod(p), Resources: resources, VolumeSizeLimit: volumeLimit}
}

func selectedNode(s corev1.PodSpec) string {
	if s.Affinity == nil || s.Affinity.NodeAffinity == nil || s.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return ""
	}
	terms := s.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 0 || len(terms[0].MatchFields) != 1 {
		return ""
	}
	f := terms[0].MatchFields[0]
	if f.Key != "metadata.name" || f.Operator != corev1.NodeSelectorOpIn || len(f.Values) != 1 {
		return ""
	}
	return f.Values[0]
}

func safePod(p *corev1.Pod) bool {
	s := p.Spec
	if len(s.Containers) != 1 || len(s.InitContainers) != 0 || len(s.EphemeralContainers) != 0 || len(s.Volumes) != 1 || s.Volumes[0].Name != "arm-private" || s.Volumes[0].EmptyDir == nil || s.Volumes[0].EmptyDir.SizeLimit == nil || s.Volumes[0].EmptyDir.SizeLimit.Sign() <= 0 || selectedNode(s) == "" || len(s.Tolerations) != 0 || len(s.NodeSelector) != 0 || (s.SchedulerName != "" && s.SchedulerName != "default-scheduler") || s.Affinity.PodAffinity != nil || s.Affinity.PodAntiAffinity != nil || len(s.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution) != 0 ||
		s.HostNetwork || s.HostPID || s.HostIPC || s.RestartPolicy != corev1.RestartPolicyNever || s.AutomountServiceAccountToken == nil || *s.AutomountServiceAccountToken || s.EnableServiceLinks == nil || *s.EnableServiceLinks || s.SecurityContext == nil || s.SecurityContext.RunAsNonRoot == nil || !*s.SecurityContext.RunAsNonRoot || s.SecurityContext.RunAsUser == nil || *s.SecurityContext.RunAsUser != 65532 || s.SecurityContext.RunAsGroup == nil || *s.SecurityContext.RunAsGroup != 65532 || s.SecurityContext.FSGroup == nil || *s.SecurityContext.FSGroup != 65532 || s.SecurityContext.SeccompProfile == nil || s.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		return false
	}
	c := s.Containers[0]
	observedResources := Resources{CPURequest: c.Resources.Requests.Cpu().String(), CPULimit: c.Resources.Limits.Cpu().String(), MemoryRequest: c.Resources.Requests.Memory().String(), MemoryLimit: c.Resources.Limits.Memory().String(), EphemeralRequest: c.Resources.Requests.StorageEphemeral().String(), EphemeralLimit: c.Resources.Limits.StorageEphemeral().String()}
	command, commandErr := bootstrapCommand(observedResources)
	if len(c.EnvFrom) != 0 || c.Lifecycle != nil || c.LivenessProbe != nil || c.ReadinessProbe != nil || c.StartupProbe != nil || len(c.Ports) != 0 || len(c.VolumeDevices) != 0 || c.WorkingDir != "" || c.Stdin || c.StdinOnce || c.TTY || len(s.HostAliases) != 0 {
		return false
	}
	if c.Name != "arm" || !regexp.MustCompile(`^[^@[:space:]]+@sha256:[a-f0-9]{64}$`).MatchString(c.Image) || commandErr != nil || !reflect.DeepEqual(c.Command, []string{"/bin/sh", "-ec", command}) || len(c.Args) != 0 || len(c.Env) != 1 || c.Env[0].Name != "ARM_POD_UID" || c.Env[0].ValueFrom == nil || c.Env[0].ValueFrom.FieldRef == nil || c.Env[0].ValueFrom.FieldRef.FieldPath != "metadata.uid" || len(c.VolumeMounts) != 1 || c.VolumeMounts[0].Name != "arm-private" || c.VolumeMounts[0].MountPath != "/arm/private" || c.SecurityContext == nil || (c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged) || c.SecurityContext.RunAsNonRoot == nil || !*c.SecurityContext.RunAsNonRoot || c.SecurityContext.RunAsUser == nil || *c.SecurityContext.RunAsUser != 65532 || c.SecurityContext.RunAsGroup == nil || *c.SecurityContext.RunAsGroup != 65532 || c.SecurityContext.ReadOnlyRootFilesystem == nil || !*c.SecurityContext.ReadOnlyRootFilesystem || c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.SeccompProfile == nil || c.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault || c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Add) != 0 || len(c.SecurityContext.Capabilities.Drop) != 1 || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		return false
	}
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourceEphemeralStorage} {
		request, ok := c.Resources.Requests[name]
		if !ok || request.Sign() <= 0 {
			return false
		}
		limit, ok := c.Resources.Limits[name]
		if !ok || limit.Sign() <= 0 || request.Cmp(limit) > 0 {
			return false
		}
	}
	return true
}

func (c *ClientGo) ListByKey(ctx context.Context, namespace, key string) ([]Pod, error) {
	if !digestPattern.MatchString(key) {
		return nil, errors.New("invalid operation key")
	}
	selector := labels.Set{OwnerLabel: OwnerValue, KeyLabel: key[:32]}.AsSelector().String()
	list, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 50})
	if err != nil {
		return nil, err
	}
	if list.Continue != "" {
		return nil, errors.New("too many Pods for exact operation key")
	}
	out := make([]Pod, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, podFromAPI(&list.Items[i]))
	}
	return out, nil
}

func quantities(r Resources) (corev1.ResourceRequirements, error) {
	stringsByName := map[corev1.ResourceName][2]string{
		corev1.ResourceCPU: {r.CPURequest, r.CPULimit}, corev1.ResourceMemory: {r.MemoryRequest, r.MemoryLimit},
		corev1.ResourceEphemeralStorage: {r.EphemeralRequest, r.EphemeralLimit},
	}
	out := corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}}
	for name, pair := range stringsByName {
		req, err := resource.ParseQuantity(pair[0])
		if err != nil || req.Sign() <= 0 {
			return out, fmt.Errorf("invalid %s request", name)
		}
		limit, err := resource.ParseQuantity(pair[1])
		if err != nil || limit.Sign() <= 0 || req.Cmp(limit) > 0 {
			return out, fmt.Errorf("invalid %s limit", name)
		}
		out.Requests[name] = req
		out.Limits[name] = limit
	}
	return out, nil
}

func (c *ClientGo) CreatePod(ctx context.Context, p PodSpec) (Pod, error) {
	if p.GenerateName != "arm-node-" || !validNodeName(p.NodeName) || p.OwnerLabel != OwnerValue || !digestPattern.MatchString(p.KeyAnnotation) || p.KeyLabel != p.KeyAnnotation[:32] || !digestPattern.MatchString(p.PlanAnnotation) || p.RunAsUser != 65532 || p.RunAsGroup != 65532 || p.FSGroup != 65532 || !p.RunAsNonRoot || !p.ReadOnlyRootFilesystem || !p.DropAllCapabilities || !p.RuntimeDefaultSeccomp || p.AllowPrivilegeEscalation || p.AutomountServiceAccountToken || p.HostNetwork || p.HostPID || p.HostIPC || p.EnableServiceLinks || p.RestartPolicy != "Never" {
		return Pod{}, errors.New("unsafe Pod spec")
	}
	requests, err := quantities(p.Resources)
	if err != nil {
		return Pod{}, err
	}
	volumeSize, err := resource.ParseQuantity(p.VolumeSizeLimit)
	if err != nil || volumeSize.Sign() <= 0 {
		return Pod{}, errors.New("invalid private volume size")
	}
	falseValue := false
	trueValue := true
	uid := p.RunAsUser
	command, err := bootstrapCommand(p.Resources)
	if err != nil {
		return Pod{}, err
	}
	gid := p.RunAsGroup
	fsGroup := p.FSGroup
	object := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: p.GenerateName, Namespace: p.Namespace, Labels: map[string]string{OwnerLabel: p.OwnerLabel, KeyLabel: p.KeyLabel}, Annotations: map[string]string{PlanAnnotation: p.PlanAnnotation, KeyAnnotation: p.KeyAnnotation}},
		Spec: corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{p.NodeName}}}}}}}}, RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: &falseValue,
			EnableServiceLinks: &falseValue, HostNetwork: false, HostPID: false, HostIPC: false,
			SecurityContext: &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &gid, FSGroup: &fsGroup, RunAsNonRoot: &trueValue, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Volumes:         []corev1.Volume{{Name: "arm-private", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &volumeSize}}}},
			Containers: []corev1.Container{{Name: "arm", Image: p.Image, ImagePullPolicy: corev1.PullIfNotPresent,
				Env:       []corev1.EnvVar{{Name: "ARM_POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}}},
				Command:   []string{"/bin/sh", "-ec", command},
				Resources: requests, VolumeMounts: []corev1.VolumeMount{{Name: "arm-private", MountPath: "/arm/private"}},
				SecurityContext: &corev1.SecurityContext{RunAsUser: &uid, RunAsGroup: &gid, RunAsNonRoot: &trueValue, ReadOnlyRootFilesystem: &trueValue, AllowPrivilegeEscalation: &falseValue, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			}}}}
	var created *corev1.Pod
	if restClient, ok := c.client.CoreV1().RESTClient().(*rest.RESTClient); ok && restClient != nil {
		created = &corev1.Pod{}
		// GenerateName is non-idempotent. The typed Create helper inherits
		// client-go's retry-on-429 policy; this exact POST must never retry.
		err = restClient.Post().Namespace(p.Namespace).Resource("pods").VersionedParams(&metav1.CreateOptions{}, scheme.ParameterCodec).Body(object).MaxRetries(0).Do(ctx).Into(created)
	} else {
		// Typed fake clients have no RESTClient; production never uses this path.
		created, err = c.client.CoreV1().Pods(p.Namespace).Create(ctx, object, metav1.CreateOptions{})
	}
	if err != nil {
		return Pod{}, err
	}
	return podFromAPI(created), nil
}

func (c *ClientGo) GetPod(ctx context.Context, namespace, name string) (Pod, error) {
	p, err := c.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Pod{}, err
	}
	return podFromAPI(p), nil
}

// Exec streams only a caller-constructed fixed command to an exact Pod. The
// provider's higher layer must verify UID immediately before and inspect the
// destination receipt afterward. The adapter seals caller-owned stream I/O
// before returning because client-go's stdin copier may outlive cancellation.
func (c *ClientGo) Exec(ctx context.Context, pod Pod, command []string, input io.Reader, output, stderr io.Writer) error {
	if pod.Name == "" || pod.UID == "" || len(command) == 0 {
		return ErrIdentity
	}
	current, err := c.GetPod(ctx, pod.Namespace, pod.Name)
	if err != nil {
		return err
	}
	if current.UID != pod.UID {
		return ErrIdentity
	}
	restClient, ok := c.client.CoreV1().RESTClient().(*rest.RESTClient)
	if !ok || restClient == nil || c.config == nil {
		return errors.New("native Kubernetes exec transport unavailable")
	}
	request := restClient.Post().Resource("pods").Namespace(pod.Namespace).Name(pod.Name).SubResource("exec").VersionedParams(&corev1.PodExecOptions{Container: "arm", Command: command, Stdin: input != nil, Stdout: output != nil, Stderr: stderr != nil, TTY: false}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(c.config, "POST", request.URL())
	if err != nil {
		return err
	}
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var guardedInput *streamInput
	var guardedOutput, guardedStderr *streamOutput
	if input != nil {
		guardedInput = &streamInput{reader: input}
		input = guardedInput
	}
	if output != nil {
		guardedOutput = &streamOutput{writer: output, cancel: cancel}
		output = guardedOutput
	}
	if stderr != nil {
		guardedStderr = &streamOutput{writer: stderr, cancel: cancel}
		stderr = guardedStderr
	}
	err = executor.StreamWithContext(execCtx, remotecommand.StreamOptions{Stdin: input, Stdout: output, Stderr: stderr})
	if guardedInput != nil {
		guardedInput.seal()
	}
	return streamError(err, guardedOutput, guardedStderr)
}
