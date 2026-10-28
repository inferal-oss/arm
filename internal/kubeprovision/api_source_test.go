package kubeprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/kubesource"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// This is an owned fake API, not cluster or Manager-admission evidence.
type apiSourceFixture struct {
	mu       sync.Mutex
	posts    int
	pod      *corev1.Pod
	nodeLive bool
	mode     string
}

func apiSourceServer(t *testing.T, mode string) (Plan, *apiSourceFixture) {
	t.Helper()
	_, base, _ := fixture(t)
	state := &apiSourceFixture{nodeLive: true, mode: mode}
	var plan Plan
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews":
			_ = json.NewEncoder(w).Encode(authv1.SelfSubjectReview{Status: authReviewFixture.Status})
		case r.URL.Path == "/api/v1/namespaces/default":
			_ = json.NewEncoder(w).Encode(corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", UID: "namespace-uid"}})
		case r.URL.Path == "/api/v1/nodes/worker-a":
			ready := corev1.ConditionFalse
			if state.nodeLive {
				ready = corev1.ConditionTrue
			}
			_ = json.NewEncoder(w).Encode(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: "node-uid", Labels: map[string]string{corev1.LabelOSStable: "linux", corev1.LabelArchStable: "amd64"}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}}})
		case r.URL.Path == "/api/v1/namespaces/default/pods" && r.Method == "GET":
			if r.URL.Query().Get("labelSelector") != (labels.Set{OwnerLabel: OwnerValue, KeyLabel: plan.Review.Key[:32]}).AsSelector().String() || r.URL.Query().Get("limit") != "50" {
				t.Error("API helper did not use the exact bounded original selector")
			}
			list := corev1.PodList{Items: []corev1.Pod{}}
			if state.pod != nil {
				list.Items = append(list.Items, *state.pod.DeepCopy())
			}
			if mode == "node-changed-during-list" {
				state.nodeLive = false
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.URL.Path == "/api/v1/namespaces/default/pods" && r.Method == "POST":
			state.posts++
			var pod corev1.Pod
			raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
			if err != nil || len(raw) > 65536 {
				t.Error("bounded fixture Pod body required")
				w.WriteHeader(400)
				return
			}
			if _, _, err := scheme.Codecs.UniversalDeserializer().Decode(raw, nil, &pod); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			pod.Name, pod.UID = "arm-node-api-original", types.UID("api-original-uid")
			state.pod = pod.DeepCopy()
			if mode == "lost-create-reply" {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			_ = json.NewEncoder(w).Encode(pod)
		case r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-api-original" && r.Method == "GET":
			if state.pod == nil {
				w.WriteHeader(404)
				return
			}
			pod := state.pod.DeepCopy()
			if mode == "replacement-at-get" {
				pod.UID = "replacement-uid"
			}
			_ = json.NewEncoder(w).Encode(pod)
		default:
			t.Error("unexpected API helper route", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	sum := sha256.Sum256(ca)
	base.Review.Cluster.Server, base.Review.Cluster.CAHash = server.URL, hex.EncodeToString(sum[:])
	var err error
	plan, err = NewPlan(base.Review)
	if err != nil {
		t.Fatal(err)
	}
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["fixture"] = &clientcmdapi.Cluster{Server: server.URL, CertificateAuthorityData: ca}
	cfg.AuthInfos["fixture"] = &clientcmdapi.AuthInfo{Token: "fixture-token"}
	cfg.Contexts["cluster-one"] = &clientcmdapi.Context{Cluster: "fixture", AuthInfo: "fixture", Namespace: "default"}
	cfg.CurrentContext = "cluster-one"
	path := filepath.Join(t.TempDir(), "api-fixture-config")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	return plan, state
}

func runAPISourceFixture(t *testing.T, plan Plan, kind reviewedExecKind) (OriginalExecJoin, error) {
	t.Helper()
	owned, err := NewOwnedExec(plan)
	if err != nil {
		t.Fatal(err)
	}
	// Controlled intent callback only: no durable pre-Pod provider-journal or
	// actual Manager admission is established by this helper-only fixture.
	call, err := owned.newAPISourceCall(kind, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	key, size, purpose, err := call.Scope()
	scope := kubesource.Scope{Operation: key, Attempt: "11111111111111111111111111111111", Stage: kubesource.Bootstrap, B: size}
	limit, limitErr := scope.Limit(purpose)
	if err != nil || limitErr != nil || purpose != "mesh_bootstrap" || limit != 65536 {
		t.Fatal("API call escaped the one e request-file lane", purpose, err, limitErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	join, err := call.RunOriginal(ctx, sourceCallFile(t, call), nil, func(context.Context) error { return nil })
	if !join.Matches(call) || !join.Closed() {
		t.Fatal("original API helper did not positively join", join, err)
	}
	return join, err
}

func TestAPISourceNativeCreateAndFreshLookupNeverResend(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	for _, mode := range []string{"ordinary", "lost-create-reply"} {
		t.Run(mode, func(t *testing.T) {
			plan, state := apiSourceServer(t, mode)
			join, err := runAPISourceFixture(t, plan, execCreateOriginalPod)
			if (err != nil) != (mode == "lost-create-reply") {
				t.Fatal("create response class differs", err)
			}
			if mode == "ordinary" {
				var pod Pod
				out, _ := join.Output()
				if jsonUnmarshalStrict([]byte(out), &pod) != nil || pod.UID != "api-original-uid" || checkPod(plan, pod) != nil {
					t.Fatal("native original create observation differs", out)
				}
			}
			lookup, err := runAPISourceFixture(t, plan, execLookupOriginalPod)
			out, _ := lookup.Output()
			var pod Pod
			if err != nil || jsonUnmarshalStrict([]byte(out), &pod) != nil || pod.UID != "api-original-uid" {
				t.Fatal("fresh original lookup failed", out, err)
			}
			// Even a private duplicate create call may not adopt a preexisting
			// matching object or make another POST. The provider adapter must
			// additionally fence dispatch durably before constructing the call.
			if _, err := runAPISourceFixture(t, plan, execCreateOriginalPod); err == nil {
				t.Fatal("preexisting Pod was silently adopted by create action")
			}
			state.mu.Lock()
			posts := state.posts
			state.mu.Unlock()
			if posts != 1 {
				t.Fatal("helper resent original create", posts)
			}
		})
	}
}

func TestAPISourceRefusesChangedNodeAfterList(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	plan, state := apiSourceServer(t, "node-changed-during-list")
	if _, err := runAPISourceFixture(t, plan, execCreateOriginalPod); err == nil {
		t.Fatal("pre-list Node readiness authorized later create")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.posts != 0 {
		t.Fatal("changed reviewed Node received a create POST")
	}
}

func TestAPISourceLookupRefusesReplacementUID(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	plan, state := apiSourceServer(t, "replacement-at-get")
	if _, err := runAPISourceFixture(t, plan, execCreateOriginalPod); err != nil {
		t.Fatal(err)
	}
	if _, err := runAPISourceFixture(t, plan, execLookupOriginalPod); err == nil {
		t.Fatal("lookup adopted a replacement UID between list and Get")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.posts != 1 {
		t.Fatal("lookup created another Pod", state.posts)
	}
}

func TestAPISourcePrivateBuilderDoesNotWidenPublicCommands(t *testing.T) {
	_, plan, _ := fixture(t)
	owned, err := NewOwnedExec(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owned.newAPISourceCall(execCreateOriginalPod, nil); err == nil {
		t.Fatal("API call lost its original provider current check")
	}
	if _, err := owned.newAPISourceCall("arbitrary", func(context.Context) error { return nil }); err == nil {
		t.Fatal("arbitrary API action accepted")
	}
	if _, err := owned.NewSourceCall(Pod{}, string(execCreateOriginalPod)); err == nil {
		t.Fatal("public Pod-call builder minted an API action")
	}
	if err := owned.Exec(t.Context(), Pod{}, []string{string(execCreateOriginalPod)}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("public exec command selected API creation")
	}
}
