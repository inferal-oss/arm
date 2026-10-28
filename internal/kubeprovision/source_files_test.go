package kubeprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// The freshly compiled test executable dispatches the existing fixed native
// helper only in this private fixture mode. No new production entrypoint.
func init() {
	if len(os.Args) > 1 && os.Args[1] == ExecHelperArgument && os.Getenv("ARM_KUBE_SOURCE_TEST_HELPER") == "1" {
		os.Exit(ExecHelperMain(os.Args[2:]))
	}
}
func sourceCallFile(t *testing.T, c *NativeExecCall) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "request-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(file.Name()); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = c.WriteRequest(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}
func TestSourceCallAuthorityAtStartAndHello(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	for _, cut := range []string{"cancel-during-verification", "withdraw-before-start", "withdraw-before-grant"} {
		t.Run(cut, func(t *testing.T) {
			var effects atomic.Int32
			plan, pod, closeServer := ownedExecServer(t, func(w http.ResponseWriter, r *http.Request) {
				effects.Add(1)
				execServerEffect(t, "ARM_BINARY_VERIFIED\n", nil, false)(w, r)
			})
			defer closeServer()
			owned, err := NewOwnedExec(plan)
			if err != nil {
				t.Fatal(err)
			}
			call, err := owned.NewSourceCall(pod, "lookup_binary")
			if err != nil {
				t.Fatal(err)
			}
			request := sourceCallFile(t, call)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			if cut == "cancel-during-verification" {
				call.beforeVerify = cancel
			}
			check := func(context.Context) error {
				calls++
				if cut == "withdraw-before-start" && calls == 2 || cut == "withdraw-before-grant" && calls == 3 {
					return errors.New("fixture original native authority withdrawn")
				}
				return nil
			}
			join, err := call.RunOriginal(ctx, request, nil, check)
			if err == nil || !join.Matches(call) || !join.Closed() || effects.Load() != 0 {
				t.Fatal("late source helper released", join, err, effects.Load())
			}
			call.mu.Lock()
			started := call.cmd != nil && call.cmd.Process != nil
			call.mu.Unlock()
			if cut != "withdraw-before-grant" && started {
				t.Fatal("withdrawn authority started a helper")
			}
			if cut == "withdraw-before-grant" && !started {
				t.Fatal("fixture did not reach real hello")
			}
		})
	}
}
func TestSourceCallNativeLoanReceiptIsOriginalAndOneShot(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	plan, pod, closeServer := ownedExecServer(t, execServerEffect(t, "ARM_BINARY_VERIFIED\n", nil, false))
	defer closeServer()
	owned, err := NewOwnedExec(plan)
	if err != nil {
		t.Fatal(err)
	}
	call, err := owned.NewSourceCall(pod, "lookup_binary")
	if err != nil {
		t.Fatal(err)
	}
	request := sourceCallFile(t, call)
	join, err := call.RunOriginal(t.Context(), request, nil, func(context.Context) error { return nil })
	if err != nil || !join.Matches(call) || !join.Closed() {
		t.Fatal(join, err)
	}
	if closed, err := join.RequestClose(); !closed || err != nil {
		t.Fatal("original request first close missing", closed, err)
	}
	if other, _ := owned.NewSourceCall(pod, "lookup_binary"); join.Matches(other) {
		t.Fatal("foreign call adopted join")
	}
	if again, err := call.RunOriginal(t.Context(), request, nil, func(context.Context) error { return nil }); err == nil || again.Matches(call) {
		t.Fatal("call replayed")
	}
	if output, stderr := join.Output(); output != "ARM_BINARY_VERIFIED\n" || stderr != "" {
		t.Fatal("native output differs")
	}
}

func TestSourceCallPanicAfterWaitNeverSignalsReapedProcess(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	plan, pod, closeServer := ownedExecServer(t, execServerEffect(t, "ARM_BINARY_VERIFIED\n", nil, false))
	defer closeServer()
	owned, err := NewOwnedExec(plan)
	if err != nil {
		t.Fatal(err)
	}
	call, err := owned.NewSourceCall(pod, "lookup_binary")
	if err != nil {
		t.Fatal(err)
	}
	call.afterWait = func() { panic("fixture after original Wait") }
	request := sourceCallFile(t, call)
	join, err := call.RunOriginal(t.Context(), request, nil, func(context.Context) error { return nil })
	if !errors.Is(err, ErrHelperCleanupUnknown) || !join.Matches(call) || join.Closed() {
		t.Fatal(join, err)
	}
	call.mu.Lock()
	retired, signaled := call.waitStarted, call.fallbackSignal
	reaped := call.cmd != nil && call.cmd.ProcessState != nil
	call.mu.Unlock()
	if !retired || !reaped || signaled {
		t.Fatal("fallback regained signalability after original reaping", retired, reaped, signaled)
	}
}

// This adapter exists only in the call-map fixture. It verifies the actual
// Provider wrapper chain through real fixed helper processes/TLS/SPDY; native
// Manager resource admission is tested independently in manager's core cohort.
type sourceMapExec struct {
	t     *testing.T
	owned *OwnedExec
	kinds []string
}

func (a *sourceMapExec) Exec(ctx context.Context, pod Pod, args []string, _ io.Reader, out, stderr io.Writer) error {
	kind := map[string]string{"arm-lookup": "lookup_binary", "arm-catalog-proof": "catalog_proof", "arm-start-lookup": "lookup_start_marker", "arm-runtime-proof": "runtime_proof"}[args[3]]
	if kind == "" {
		return errors.New("unexpected effect in inspection map")
	}
	a.kinds = append(a.kinds, kind)
	call, err := a.owned.NewSourceCall(pod, kind)
	if err != nil {
		return err
	}
	file := sourceCallFile(a.t, call)
	join, err := call.RunOriginal(ctx, file, nil, func(context.Context) error { return nil })
	if !join.Matches(call) || !join.Closed() {
		a.t.Error("inspection original did not close")
	}
	if err != nil {
		return err
	}
	o, e := join.Output()
	if _, err = io.WriteString(out, o); err != nil {
		return err
	}
	_, err = io.WriteString(stderr, e)
	return err
}
func TestSourceCallInspectionWrapperUsesFourNativeRequests(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	var plan Plan
	effect := func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query()["command"]
		if len(name) < 4 {
			http.Error(w, "bad fixed request", 400)
			return
		}
		output := ""
		switch name[3] {
		case "arm-lookup":
			output = "ARM_BINARY_VERIFIED\n"
		case "arm-catalog-proof":
			output = "ARM_CATALOG_VERIFIED " + plan.Review.CatalogSHA256 + "\n"
		case "arm-start-lookup":
			output = "ARM_START_MARKER_VERIFIED\n"
		case "arm-runtime-proof":
			cpu, memory, disk, err := bootstrapBudgets(plan.Review.Resources)
			if err != nil {
				t.Error(err)
				return
			}
			output = "ARM_RUNTIME_VERIFIED " + strings.Repeat("a", 32) + " " + strings.Repeat("b", 32) + " " + cpu + " " + memory + " " + disk + "\n"
		default:
			t.Error("unexpected inspection effect", name[3])
			return
		}
		execServerEffect(t, output, nil, false)(w, r)
	}
	var pod Pod
	var closeServer func()
	plan, pod, closeServer = sourceInspectionServer(t, effect)
	defer closeServer()
	api, err := OpenReviewedContext(plan)
	if err != nil {
		t.Fatal(err)
	}
	journal := FileJournal{Root: filepath.Join(t.TempDir(), "journal")}
	key := plan.Review.Key
	r := Record{Plan: plan, Phase: PhasePreflight, Revision: 1}
	if err = journal.Insert(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	r, err = journal.Update(t.Context(), key, 1, func(r *Record) error { r.Phase = PhaseCreateAttempted; return nil })
	if err != nil {
		t.Fatal(err)
	}
	r, err = journal.Update(t.Context(), key, r.Revision, func(r *Record) error {
		r.Phase = PhaseCreated
		r.Pod = &pod
		r.BinaryPhase = "binary_verified"
		r.StartPhase = "marker_verified"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := NewOwnedExec(plan)
	if err != nil {
		t.Fatal(err)
	}
	transport := &sourceMapExec{t: t, owned: owned}
	p := Provider{API: api, Journal: journal}
	if _, err = p.ReconcileBinary(t.Context(), key, transport); err != nil {
		t.Fatal(err)
	}
	if _, err = p.LookupCatalog(t.Context(), key, transport); err != nil {
		t.Fatal(err)
	}
	if _, err = p.LookupRuntime(t.Context(), key, transport); err != nil {
		t.Fatal(err)
	}
	want := []string{"lookup_binary", "catalog_proof", "lookup_start_marker", "runtime_proof"}
	if !reflect.DeepEqual(transport.kinds, want) {
		t.Fatal("actual wrapper call map differs", transport.kinds)
	}
}

// Same native TLS/SPDY helper pattern as the existing owned-exec fixtures,
// additionally serving an actually Running Pod and its exact-key list route.
func sourceInspectionServer(t *testing.T, effect func(http.ResponseWriter, *http.Request)) (Plan, Pod, func()) {
	t.Helper()
	provider, reviewed, client := fixture(t)
	record, err := provider.Apply(t.Context(), reviewed)
	if err != nil {
		t.Fatal(err)
	}
	object, err := client.CoreV1().Pods("default").Get(t.Context(), record.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	object.Status.Phase = corev1.PodRunning
	object.Spec.NodeName = "worker-a"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews":
			_ = json.NewEncoder(w).Encode(authv1.SelfSubjectReview{Status: authReviewFixture.Status})
		case r.URL.Path == "/api/v1/namespaces/default":
			_ = json.NewEncoder(w).Encode(corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", UID: "namespace-uid"}})
		case r.URL.Path == "/api/v1/nodes/worker-a":
			_ = json.NewEncoder(w).Encode(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: "node-uid", Labels: map[string]string{corev1.LabelOSStable: "linux", corev1.LabelArchStable: "amd64"}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}})
		case r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one/exec" && r.Method == "POST":
			effect(w, r)
		case r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one":
			_ = json.NewEncoder(w).Encode(object)
		case r.URL.Path == "/api/v1/namespaces/default/pods":
			_ = json.NewEncoder(w).Encode(corev1.PodList{Items: []corev1.Pod{*object}})
		default:
			http.NotFound(w, r)
		}
	}))
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	sum := sha256.Sum256(ca)
	reviewed.Review.Cluster.Server = server.URL
	reviewed.Review.Cluster.CAHash = hex.EncodeToString(sum[:])
	plan, err := NewPlan(reviewed.Review)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	object.Annotations[PlanAnnotation] = plan.SHA256
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["fixture"] = &clientcmdapi.Cluster{Server: server.URL, CertificateAuthorityData: ca}
	cfg.AuthInfos["fixture"] = &clientcmdapi.AuthInfo{Token: "fixture-token"}
	cfg.Contexts["cluster-one"] = &clientcmdapi.Context{Cluster: "fixture", AuthInfo: "fixture", Namespace: "default"}
	cfg.CurrentContext = "cluster-one"
	path := filepath.Join(t.TempDir(), "config")
	if err = clientcmd.WriteToFile(*cfg, path); err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	return plan, podFromAPI(object), server.Close
}
