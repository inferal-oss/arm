package kubeprovision

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }

func fixture(t *testing.T) (Provider, Plan, *fake.Clientset) {
	t.Helper()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", UID: types.UID("namespace-uid")}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: types.UID("node-uid"), Labels: map[string]string{corev1.LabelOSStable: "linux", corev1.LabelArchStable: "amd64"}},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	client := fake.NewClientset(namespace, node)
	// The typed fake cannot synthesize generated names/UIDs or SSR status;
	// attach exact reactors for the API behavior under test.
	client.PrependReactor("create", "selfsubjectreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, &authReviewFixture, nil
	})
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
		obj.Name = "arm-node-one"
		obj.UID = types.UID("pod-uid")
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), obj, "default"); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
	cluster := Cluster{Context: "cluster-one", Server: "https://kube.example", CAHash: hash("ca"), AuthSubject: "operator", AuthGroups: []string{"system:authenticated"}}
	api := NewClientGoForTest(cluster.Context, cluster.Server, cluster.CAHash, nil, client)
	review := Review{Key: hash("operation"), Owner: "local-operator", Reason: "disposable test", Cluster: cluster, Namespace: Namespace{Name: "default", UID: "namespace-uid"},
		Node: Node{Name: "worker-a", UID: "node-uid", OS: "linux", Arch: "amd64", Ready: true}, Image: "registry.example/arm-base@sha256:" + hash("image"), BinarySHA256: hash("binary"), BinaryBytes: 4096, CatalogSHA256: hash("catalog"),
		Resources: Resources{CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi", EphemeralRequest: "256Mi", EphemeralLimit: "1Gi"}}
	plan, err := NewPlan(review)
	if err != nil {
		t.Fatal(err)
	}
	return Provider{API: api, Journal: FileJournal{Root: t.TempDir() + "/journal"}}, plan, client
}

var authReviewFixture = authv1.SelfSubjectReview{Status: authv1.SelfSubjectReviewStatus{UserInfo: authv1.UserInfo{Username: "operator", Groups: []string{"system:authenticated"}}}}

func TestCreatePodIsPrivateAndKeyed(t *testing.T) {
	p, plan, client := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != PhaseCreated || record.Pod == nil || record.Pod.UID != "pod-uid" {
		t.Fatal(record)
	}
	created, err := client.CoreV1().Pods("default").Get(t.Context(), "arm-node-one", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if created.Labels[KeyLabel] != plan.Review.Key[:32] || created.Annotations[KeyAnnotation] != plan.Review.Key || created.Annotations[PlanAnnotation] != plan.SHA256 || created.Spec.NodeName != "" || selectedNode(created.Spec) != "worker-a" || created.Spec.AutomountServiceAccountToken == nil || *created.Spec.AutomountServiceAccountToken || created.Spec.HostNetwork || created.Spec.Containers[0].SecurityContext.Privileged != nil || !*created.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem {
		t.Fatalf("unsafe Pod: %+v", created.Spec)
	}
	bootstrap := created.Spec.Containers[0].Command[2]
	if !strings.Contains(bootstrap, " serve --listen 127.0.0.1:9443 --cpu 0.1 --memory 134217728 --disk 268435456") || strings.Contains(bootstrap, "--cpu 0 --memory 0 --disk 0") {
		t.Fatalf("invalid ARM serve bootstrap budgets: %q", bootstrap)
	}
	second, err := p.Apply(t.Context(), plan)
	if err != nil || second.Pod.UID != record.Pod.UID {
		t.Fatalf("same key lookup: %+v %v", second, err)
	}
	count := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "pods" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("create calls: %d", count)
	}
}

func TestLostCreateAcknowledgementNeverReposts(t *testing.T) {
	p, plan, client := fixture(t)
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) { return true, nil, context.DeadlineExceeded })
	_, err := p.Apply(t.Context(), plan)
	if !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	_, err = p.Apply(t.Context(), plan)
	if !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	count := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "pods" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("unknown create replayed %d times", count)
	}
}

func TestLostAcknowledgementFindsExactPodWithoutNewCreate(t *testing.T) {
	p, plan, client := fixture(t)
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
		obj.Name = "arm-node-one"
		obj.UID = types.UID("pod-uid")
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), obj, "default"); err != nil {
			return true, nil, err
		}
		return true, nil, context.DeadlineExceeded
	})
	if _, err := p.Apply(t.Context(), plan); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	recovered, err := p.Reconcile(t.Context(), plan.Review.Key)
	if err != nil || recovered.Phase != PhaseCreated || recovered.Pod == nil || recovered.Pod.UID != "pod-uid" {
		t.Fatalf("original-key recovery: %+v %v", recovered, err)
	}
	creates := 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "create" && a.GetResource().Resource == "pods" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("recovery reposted Pod %d times", creates)
	}
}

func TestConcurrentOriginalKeyHasOneCreate(t *testing.T) {
	p, plan, client := fixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = p.Apply(ctx, plan) }()
	}
	wg.Wait()
	creates := 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "create" && a.GetResource().Resource == "pods" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("concurrent exact key created %d Pods", creates)
	}
}

func TestRestartUsesDurableOriginalKey(t *testing.T) {
	p, plan, client := fixture(t)
	if _, err := p.Apply(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	restarted := Provider{API: p.API, Journal: FileJournal{Root: p.Journal.(FileJournal).Root}}
	if _, err := restarted.Apply(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "create" && a.GetResource().Resource == "pods" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("restart recreated Pod %d times", creates)
	}
}

func TestPreflightCrashCannotAdoptPreexistingPod(t *testing.T) {
	p, plan, _ := fixture(t)
	preflight := Record{Plan: plan, Phase: PhasePreflight, Revision: 1}
	if err := p.Journal.Insert(t.Context(), preflight); err != nil {
		t.Fatal(err)
	}
	if _, err := p.API.CreatePod(t.Context(), plan.Pod); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Reconcile(t.Context(), plan.Review.Key); err == nil {
		t.Fatal("preflight adopted Pod before create dispatch")
	}
	if _, err := p.Apply(t.Context(), plan); !errors.Is(err, ErrIdentity) {
		t.Fatalf("preexisting Pod was adopted: %v", err)
	}
	record, ok, err := p.Journal.Read(t.Context(), plan.Review.Key)
	if err != nil || !ok || record.Phase != PhaseConflict || record.Pod != nil {
		t.Fatalf("preexisting conflict lost: %+v %v", record, err)
	}
}

func TestPreflightReadFailureDoesNotDispatchCreate(t *testing.T) {
	p, plan, client := fixture(t)
	client.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("list denied")
	})
	if _, err := p.Apply(t.Context(), plan); err == nil {
		t.Fatal("preflight read failure accepted")
	}
	record, ok, err := p.Journal.Read(t.Context(), plan.Review.Key)
	if err != nil || !ok || record.Phase != PhasePreflight {
		t.Fatalf("preflight intent lost: %+v %v", record, err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "pods" {
			t.Fatal("create dispatched after failed preflight read")
		}
	}
}

func TestContextNodeAndPodIdentityFences(t *testing.T) {
	p, plan, client := fixture(t)
	_, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ExactPod(t.Context(), plan.Review.Key); err != nil {
		t.Fatal(err)
	}
	obj, _ := client.CoreV1().Pods("default").Get(t.Context(), "arm-node-one", metav1.GetOptions{})
	obj.UID = types.UID("replacement")
	if _, err := client.CoreV1().Pods("default").Update(t.Context(), obj, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ExactPod(t.Context(), plan.Review.Key); !errors.Is(err, ErrIdentity) {
		t.Fatalf("replacement accepted: %v", err)
	}
	if err := p.API.(*ClientGo).Exec(t.Context(), *pRecordPod(t, p, plan.Review.Key), []string{"/bin/true"}, nil, nil, nil); !errors.Is(err, ErrIdentity) {
		t.Fatalf("exec reached replacement: %v", err)
	}
	if _, err := p.API.(*ClientGo).Forward(t.Context(), *pRecordPod(t, p, plan.Review.Key), 9443); !errors.Is(err, ErrIdentity) {
		t.Fatalf("forward reached replacement: %v", err)
	}
	if _, err := p.Apply(t.Context(), func() Plan { changed := plan; changed.Review.Cluster.Context = "other"; return changed }()); err == nil {
		t.Fatal("changed context accepted")
	}
	node, _ := client.CoreV1().Nodes().Get(t.Context(), "worker-a", metav1.GetOptions{})
	node.UID = types.UID("new-node")
	client.CoreV1().Nodes().Update(t.Context(), node, metav1.UpdateOptions{})
	if err := p.Check(t.Context(), plan); !errors.Is(err, ErrIdentity) {
		t.Fatalf("node replacement accepted: %v", err)
	}
}

func pRecordPod(t *testing.T, p Provider, key string) *Pod {
	t.Helper()
	r, ok, err := p.Journal.Read(t.Context(), key)
	if err != nil || !ok || r.Pod == nil {
		t.Fatalf("missing Pod record: %+v %v", r, err)
	}
	return r.Pod
}

func TestDeniedNodeReadAndAdmissionMutation(t *testing.T) {
	p, plan, client := fixture(t)
	client.PrependReactor("get", "nodes", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("forbidden") })
	if _, err := p.Apply(t.Context(), plan); err == nil {
		t.Fatal("denied node read accepted")
	}
	for _, a := range client.Actions() {
		if a.GetVerb() == "create" && a.GetResource().Resource == "pods" {
			t.Fatal("Pod created after RBAC denial")
		}
	}

	p, plan, client = fixture(t)
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
		obj.Name = "arm-node-one"
		obj.UID = types.UID("pod-uid")
		obj.Spec.HostNetwork = true
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), obj, "default"); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
	if _, err := p.Apply(t.Context(), plan); !errors.Is(err, ErrUnknown) {
		t.Fatalf("mutated unsafe Pod accepted: %v", err)
	}
}

func TestMergedContextChoices(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	if err := os.WriteFile(first, []byte("apiVersion: v1\nkind: Config\ncurrent-context: first\ncontexts:\n- name: first\n  context:\n    cluster: alpha\n    user: alice\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("apiVersion: v1\nkind: Config\ncontexts:\n- name: second\n  context:\n    cluster: beta\n    user: bob\n    namespace: work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", first+string(os.PathListSeparator)+second)
	choices, err := Contexts()
	if err != nil || len(choices) != 2 || !choices[0].Current || choices[1].Namespace != "work" {
		t.Fatalf("merged contexts: %+v %v", choices, err)
	}
}

func testCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestKubeconfigCAFileIsFrozenAfterReview(t *testing.T) {
	root := t.TempDir()
	caPath := filepath.Join(root, "ca.pem")
	first := testCAPEM(t)
	if err := os.WriteFile(caPath, first, 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config")
	config := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: selected\nclusters:\n- name: cluster\n  cluster:\n    server: https://kube.invalid\n    certificate-authority: %s\nusers:\n- name: user\n  user:\n    token: test-token\ncontexts:\n- name: selected\n  context:\n    cluster: cluster\n    user: user\n", caPath)
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", configPath)
	api, err := OpenContext("selected")
	if err != nil {
		t.Fatal(err)
	}
	if api.config.CAFile != "" || !bytes.Equal(api.config.CAData, first) || api.caHash != hash(string(first)) {
		t.Fatal("reviewed CA bundle was not frozen")
	}
	if err := os.WriteFile(caPath, testCAPEM(t), 0600); err != nil {
		t.Fatal(err)
	}
	if api.config.CAFile != "" || !bytes.Equal(api.config.CAData, first) || api.caHash != hash(string(first)) {
		t.Fatal("replacement CA file changed pinned client transport")
	}
}

func TestNodeTypeaheadIncludesResourceEvidence(t *testing.T) {
	nodes := []Node{{Name: "gpu-west", Arch: "amd64", AllocatableCPU: "8", AllocatableMemory: "32Gi", AllocatableEphemeral: "100Gi", Allocatable: map[string]string{"nvidia.com/gpu": "1"}}, {Name: "arm-east", Arch: "arm64", AllocatableCPU: "4"}}
	selected := FilterNodes(nodes, "arm", 10)
	if len(selected) != 1 || selected[0].Name != "arm-east" {
		t.Fatal(selected)
	}
	selected = FilterNodes(nodes, "gpu", 10)
	if len(selected) != 1 || selected[0].Allocatable["nvidia.com/gpu"] != "1" {
		t.Fatal(selected)
	}
}

func TestDottedKubernetesNodeName(t *testing.T) {
	if !validNodeName("worker-1.zone.example") || validNodeName("worker_1.zone.example") || validNodeName("-worker.example") {
		t.Fatal("Kubernetes DNS subdomain Node name validation changed")
	}
}

func TestAdmissionMutationCannotAddExecutionHooks(t *testing.T) {
	for _, mutate := range []struct {
		name string
		edit func(*corev1.Pod)
	}{
		{"env_from", func(p *corev1.Pod) { p.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{Prefix: "UNREVIEWED_"}} }},
		{"lifecycle", func(p *corev1.Pod) {
			p.Spec.Containers[0].Lifecycle = &corev1.Lifecycle{PostStart: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"/bin/sh"}}}}
		}},
		{"probe", func(p *corev1.Pod) {
			p.Spec.Containers[0].LivenessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"/bin/sh"}}}}
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			p, plan, client := fixture(t)
			client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				obj := action.(ktesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
				obj.Name, obj.UID = "arm-node-one", types.UID("pod-uid")
				mutate.edit(obj)
				if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), obj, "default"); err != nil {
					return true, nil, err
				}
				return true, obj, nil
			})
			if _, err := p.Apply(t.Context(), plan); !errors.Is(err, ErrUnknown) {
				t.Fatalf("admission mutation accepted: %v", err)
			}
		})
	}
}

func TestPrepareReadsExactCurrentReviewWithoutMutation(t *testing.T) {
	p, expected, client := fixture(t)
	d := Draft{Key: expected.Review.Key, Owner: expected.Review.Owner, Reason: expected.Review.Reason,
		Context: expected.Review.Cluster.Context, Namespace: expected.Review.Namespace.Name,
		SelectedNode: expected.Review.Node.Name, SelectedNodeUID: expected.Review.Node.UID,
		Image: expected.Review.Image, BinarySHA256: expected.Review.BinarySHA256,
		BinaryBytes: expected.Review.BinaryBytes, CatalogSHA256: expected.Review.CatalogSHA256,
		Resources: expected.Review.Resources}
	plan, err := p.Prepare(t.Context(), d)
	if err != nil || !reflect.DeepEqual(plan.Review.Cluster, expected.Review.Cluster) || plan.Review.Namespace != expected.Review.Namespace || plan.Review.Node.UID != expected.Review.Node.UID || plan.Validate() != nil {
		t.Fatalf("review changed: %+v %v", plan, err)
	}
	if _, ok, err := p.Journal.Read(t.Context(), d.Key); err != nil || ok {
		t.Fatalf("preview wrote journal: %v %v", ok, err)
	}
	for _, a := range client.Actions() {
		if a.GetVerb() == "create" && a.GetResource().Resource == "pods" {
			t.Fatal("preview created Pod")
		}
	}
	d.SelectedNodeUID = "other-node"
	if _, err := p.Prepare(t.Context(), d); !errors.Is(err, ErrIdentity) {
		t.Fatalf("changed selection accepted: %v", err)
	}
	d.SelectedNodeUID = expected.Review.Node.UID
	d.Context = "other-context"
	if _, err := p.Prepare(t.Context(), d); !errors.Is(err, ErrIdentity) {
		t.Fatalf("context drift accepted: %v", err)
	}
}

type fakeBinaryExec struct {
	ships         int
	installed     bool
	loseACK       bool
	catalogDigest string
	starts        int
	marker        bool
	loseStartACK  bool
	runtimeReady  bool
	runtimeCPU    string
	runtimeMemory string
	runtimeDisk   string
}

func (f *fakeBinaryExec) Exec(_ context.Context, _ Pod, command []string, input io.Reader, stdout, stderr io.Writer) error {
	if len(command) < 3 {
		return errors.New("bad command")
	}
	if command[2] == shipScript {
		f.ships++
		data, err := io.ReadAll(input)
		if err != nil {
			return err
		}
		if hash(string(data)) != command[5] {
			return errors.New("digest mismatch")
		}
		f.installed = true
		if f.loseACK {
			return context.DeadlineExceeded
		}
		return nil
	}
	if command[2] == lookupBinaryScript && f.installed {
		_, err := io.WriteString(stdout, "ARM_BINARY_VERIFIED\n")
		return err
	}
	if command[2] == catalogProofScript && f.installed {
		_, err := io.WriteString(stdout, "ARM_CATALOG_VERIFIED "+f.catalogDigest+"\n")
		return err
	}
	if command[2] == startMarkerScript && f.installed {
		f.starts++
		f.marker = true
		if f.loseStartACK {
			return context.DeadlineExceeded
		}
		return nil
	}
	if command[2] == lookupStartScript && f.marker {
		_, err := io.WriteString(stdout, "ARM_START_MARKER_VERIFIED\n")
		return err
	}
	if command[2] == runtimeProofScript && f.runtimeReady {
		_, err := io.WriteString(stdout, "ARM_RUNTIME_VERIFIED "+strings.Repeat("a", 32)+" "+strings.Repeat("b", 32)+" "+f.runtimeCPU+" "+f.runtimeMemory+" "+f.runtimeDisk+"\n")
		return err
	}
	return errors.New("not installed")
}

func TestBinaryLostACKLookupNeverResends(t *testing.T) {
	p, plan, client := fixture(t)
	plan.Review.BinaryBytes = int64(len("payload"))
	plan.Review.BinarySHA256 = hash("payload")
	var err error
	plan, err = NewPlan(plan.Review)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	obj, _ := client.CoreV1().Pods("default").Get(t.Context(), "arm-node-one", metav1.GetOptions{})
	obj.Status.Phase = corev1.PodRunning
	obj.Spec.NodeName = "worker-a"
	if _, err := client.CoreV1().Pods("default").Update(t.Context(), obj, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	transport := &fakeBinaryExec{loseACK: true, catalogDigest: plan.Review.CatalogSHA256}
	if _, err := p.ShipBinary(t.Context(), plan.Review.Key, strings.NewReader("payload"), transport); !errors.Is(err, ErrUnknown) {
		t.Fatalf("lost ACK: %v", err)
	}
	if transport.ships != 1 {
		t.Fatal("missing original ship")
	}
	result, err := p.ShipBinary(t.Context(), plan.Review.Key, strings.NewReader("must-not-send"), transport)
	if err != nil || result.BinaryPhase != "binary_verified" || transport.ships != 1 {
		t.Fatalf("recovery resent or failed: %+v %v ships=%d", result, err, transport.ships)
	}
	transport.catalogDigest = hash("wrong-catalog")
	if _, err := p.RequestStart(t.Context(), plan.Review.Key, transport); !errors.Is(err, ErrUnknown) || transport.starts != 0 {
		t.Fatalf("unverified carrier catalog admitted start: %v starts=%d", err, transport.starts)
	}
	transport.catalogDigest = plan.Review.CatalogSHA256
	transport.loseStartACK = true
	if _, err := p.RequestStart(t.Context(), plan.Review.Key, transport); !errors.Is(err, ErrUnknown) {
		t.Fatalf("lost start ACK: %v", err)
	}
	result, err = p.RequestStart(t.Context(), plan.Review.Key, transport)
	if err != nil || result.StartPhase != "marker_verified" || transport.starts != 1 {
		t.Fatalf("start marker replayed or lost: %+v %v starts=%d", result, err, transport.starts)
	}
	if _, err := p.LookupRuntime(t.Context(), plan.Review.Key, transport); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unready backend gained runtime proof: %v", err)
	}
	transport.runtimeReady = true
	transport.runtimeCPU, transport.runtimeMemory, transport.runtimeDisk, err = bootstrapBudgets(plan.Review.Resources)
	if err != nil {
		t.Fatal(err)
	}
	transport.runtimeCPU = "1"
	if _, err := p.LookupRuntime(t.Context(), plan.Review.Key, transport); !errors.Is(err, ErrIdentity) {
		t.Fatalf("changed runtime CPU ceiling gained proof: %v", err)
	}
	transport.runtimeCPU, _, _, _ = bootstrapBudgets(plan.Review.Resources)
	proof, err := p.LookupRuntime(t.Context(), plan.Review.Key, transport)
	if err != nil || proof.PodUID != result.Pod.UID || proof.Host != strings.Repeat("a", 32) || proof.Generation != strings.Repeat("b", 32) {
		t.Fatalf("exact runtime proof unavailable: %+v %v", proof, err)
	}
}

func TestChangedBinaryDoesNotOpenExecOrConsumeAttempt(t *testing.T) {
	p, plan, client := fixture(t)
	if _, err := p.Apply(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	obj, err := client.CoreV1().Pods("default").Get(t.Context(), "arm-node-one", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	obj.Status.Phase = corev1.PodRunning
	obj.Spec.NodeName = plan.Review.Node.Name
	if _, err := client.CoreV1().Pods("default").Update(t.Context(), obj, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	transport := &fakeBinaryExec{}
	if _, err := p.ShipBinary(t.Context(), plan.Review.Key, strings.NewReader("changed"), transport); err == nil {
		t.Fatal("changed binary accepted")
	}
	if transport.ships != 0 {
		t.Fatal("changed binary opened exec")
	}
	r, ok, err := p.Journal.Read(t.Context(), plan.Review.Key)
	if err != nil || !ok || r.BinaryPhase != "" {
		t.Fatalf("changed binary consumed attempt: %+v %v", r, err)
	}
}
