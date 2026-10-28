package server

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"getarm.org/internal/model"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestKubernetesInventoryTypeaheadReturnsCurrentResources(t *testing.T) {
	var requests atomic.Int32
	var denied atomic.Value
	denied.Store("")
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == denied.Load().(string) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(metav1.Status{Status: "Failure", Reason: metav1.StatusReasonForbidden, Code: http.StatusForbidden, Message: "private API credential diagnostic must not cross UI boundary"})
			return
		}
		switch r.URL.Path {
		case "/apis/authentication.k8s.io/v1/selfsubjectreviews":
			_ = json.NewEncoder(w).Encode(authv1.SelfSubjectReview{Status: authv1.SelfSubjectReviewStatus{UserInfo: authv1.UserInfo{Username: "operator"}}})
		case "/api/v1/namespaces/default":
			_ = json.NewEncoder(w).Encode(corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", UID: "namespace-uid"}})
		case "/api/v1/nodes":
			_ = json.NewEncoder(w).Encode(corev1.NodeList{Items: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "arm-worker", UID: "node-uid", Labels: map[string]string{corev1.LabelOSStable: "linux", corev1.LabelArchStable: "arm64"}}, Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1900m"), corev1.ResourceMemory: resource.MustParse("7Gi"), corev1.ResourceEphemeralStorage: resource.MustParse("20Gi")}, Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw})
	config := clientcmdapi.NewConfig()
	config.Clusters["disposable"] = &clientcmdapi.Cluster{Server: api.URL, CertificateAuthorityData: certificate}
	config.AuthInfos["operator"] = &clientcmdapi.AuthInfo{Token: "fixture-token"}
	config.Contexts["local"] = &clientcmdapi.Context{Cluster: "disposable", AuthInfo: "operator", Namespace: "default"}
	config.CurrentContext = "local"
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	server := &Server{}
	if _, err := server.kubernetesInventory(context.Background(), "kube_nodes", json.RawMessage(`{"context":"local","query":"arm","limit":0}`)); err == nil || requests.Load() != 0 {
		t.Fatalf("invalid request touched API: %v, %d", err, requests.Load())
	}
	result, err := server.kubernetesInventory(context.Background(), "kube_nodes", json.RawMessage(`{"context":"local","query":"arm64","limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`"name":"arm-worker"`, `"uid":"node-uid"`, `"arch":"arm64"`, `"allocatable_cpu":"1900m"`, `"allocatable_memory":"7Gi"`, `"allocatable_ephemeral":"20Gi"`} {
		if !strings.Contains(string(raw), part) {
			t.Fatalf("missing %s in %s", part, raw)
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("expected SSR, Namespace and Nodes reads, got %d", requests.Load())
	}
	if !strings.Contains(string(raw), `"inventory":{"total":1,"matched":1,"truncated":false}`) {
		t.Fatal("missing unfiltered inventory counts")
	}
	filtered, err := server.kubernetesInventory(t.Context(), "kube_nodes", json.RawMessage(`{"context":"local","namespace":"default","query":"absent","limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(filtered)
	if err != nil || !strings.Contains(string(raw), `"nodes":[]`) || !strings.Contains(string(raw), `"inventory":{"total":1,"matched":0,"truncated":false}`) {
		t.Fatalf("filtered inventory confused with empty cluster: %s, %v", raw, err)
	}
	for _, path := range []string{"/api/v1/nodes", "/api/v1/namespaces/default", "/apis/authentication.k8s.io/v1/selfsubjectreviews"} {
		denied.Store(path)
		_, err := server.kubernetesInventory(t.Context(), "kube_nodes", json.RawMessage(`{"context":"local","query":"","limit":50}`))
		var projected *model.Error
		if !errors.As(err, &projected) || projected.Code != "kubernetes_forbidden" || strings.Contains(err.Error(), "private API") {
			t.Fatalf("denied inventory became empty or leaked an API diagnostic: %v", err)
		}
	}
}

func TestKubernetesInventoryErrorClassesDoNotExposeServerDiagnostics(t *testing.T) {
	private := "private-endpoint-and-credential-diagnostic"
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"forbidden", apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, private, errors.New(private)), "kubernetes_forbidden"},
		{"unauthorized", apierrors.NewUnauthorized(private), "kubernetes_unauthorized"},
		{"missing", apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, private), "kubernetes_not_found"},
		{"deadline", context.DeadlineExceeded, "kubernetes_timeout"},
		{"server-timeout", apierrors.NewServerTimeout(schema.GroupResource{Resource: "nodes"}, "list", 1), "kubernetes_timeout"},
		{"cancelled", context.Canceled, "kubernetes_cancelled"},
		{"transport-or-decode", errors.New(private), "kubernetes_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := kubernetesInventoryError("Nodes", tc.err)
			var projected *model.Error
			if !errors.As(err, &projected) || projected.Code != tc.code || strings.Contains(err.Error(), private) {
				t.Fatalf("unsafe or incorrect inventory diagnostic: %v", err)
			}
		})
	}
}

func TestKubernetesInventoryIsNotAdmittedDuringBackendStop(t *testing.T) {
	for _, operation := range []string{"kube_contexts", "kube_nodes"} {
		if stopInspection(operation) {
			t.Fatalf("%s was classified as a Stop-safe inspection", operation)
		}
		s := &Server{nodeStopClosing: true}
		if _, err := s.dispatch(t.Context(), model.WireRequest{Operation: operation}); err == nil || !strings.Contains(err.Error(), "backend_stopping") {
			t.Fatalf("%s reached kubeconfig work during Stop: %v", operation, err)
		}
	}
}

func TestKubernetesInventoryEmbeddedAndBrowserScope(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	r := httptest.NewRequest("GET", "http://"+s.authority+"/assets/arm.mjs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "kube_nodes") {
		t.Fatalf("embedded Kubernetes inventory unavailable: %d", w.Code)
	}
	for _, operation := range []string{"kube_contexts", "kube_nodes"} {
		if !browserAllowedOperation(operation) {
			t.Fatalf("read-only %s unavailable", operation)
		}
	}
	for _, operation := range []string{"kube_apply", "kube_ship", "kube_start"} {
		if browserAllowedOperation(operation) {
			t.Fatalf("unreviewed %s exposed", operation)
		}
	}
}
