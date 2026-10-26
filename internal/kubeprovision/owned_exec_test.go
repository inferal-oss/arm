package kubeprovision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	remoteconstants "k8s.io/apimachinery/pkg/util/remotecommand"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/streaming/pkg/httpstream"
	"k8s.io/streaming/pkg/httpstream/spdy"
)

func ownedExecServer(t *testing.T, effect func(http.ResponseWriter, *http.Request)) (Plan, Pod, func()) {
	return ownedExecServerWithReview(t, effect, nil)
}

func ownedExecServerWithReview(t *testing.T, effect func(http.ResponseWriter, *http.Request), change func(*Review)) (Plan, Pod, func()) {
	plan, pod, closeServer, _ := ownedExecServerMutable(t, effect, change)
	return plan, pod, closeServer
}

func ownedExecServerMutable(t *testing.T, effect func(http.ResponseWriter, *http.Request), change func(*Review)) (Plan, Pod, func(), func(string)) {
	t.Helper()
	p, oldPlan, fake := fixture(t)
	record, err := p.Apply(t.Context(), oldPlan)
	if err != nil {
		t.Fatal(err)
	}
	var overrideUID atomic.Value
	overrideUID.Store("")
	object, err := fake.CoreV1().Pods("default").Get(t.Context(), record.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews" && r.Method == "POST":
			json.NewEncoder(w).Encode(authv1.SelfSubjectReview{Status: authReviewFixture.Status})
		case r.URL.Path == "/api/v1/namespaces/default" && r.Method == "GET":
			json.NewEncoder(w).Encode(corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", UID: "namespace-uid"}})
		case r.URL.Path == "/api/v1/nodes/worker-a" && r.Method == "GET":
			json.NewEncoder(w).Encode(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: "node-uid", Labels: map[string]string{corev1.LabelOSStable: "linux", corev1.LabelArchStable: "amd64"}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}})
		case r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one" && r.Method == "GET":
			current := object.DeepCopy()
			if uid := overrideUID.Load().(string); uid != "" {
				current.UID = types.UID(uid)
			}
			json.NewEncoder(w).Encode(current)
		case r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one/exec" && r.Method == "POST":
			effect(w, r)
		case r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one/portforward":
			effect(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	server.StartTLS()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	sum := sha256.Sum256(ca)
	review := oldPlan.Review
	review.Cluster.Server = server.URL
	review.Cluster.CAHash = hex.EncodeToString(sum[:])
	if change != nil {
		change(&review)
	}
	plan, err := NewPlan(review)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	object.Annotations[PlanAnnotation] = plan.SHA256
	pod := podFromAPI(object)
	if err := checkPod(plan, pod); err != nil {
		server.Close()
		t.Fatal(err)
	}
	config := clientcmdapi.NewConfig()
	config.Clusters["test"] = &clientcmdapi.Cluster{Server: server.URL, CertificateAuthorityData: ca}
	config.AuthInfos["test"] = &clientcmdapi.AuthInfo{Token: "test-token"}
	config.Contexts["cluster-one"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test", Namespace: "default"}
	config.CurrentContext = "cluster-one"
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	return plan, pod, server.Close, func(uid string) { overrideUID.Store(uid) }
}

func execServerEffect(t *testing.T, output string, started chan<- struct{}, hold bool) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := httpstream.Handshake(r, w, []string{remoteconstants.StreamProtocolV4Name}); err != nil {
			return
		}
		streams := make(chan httpstream.Stream, 4)
		conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(s httpstream.Stream, _ <-chan struct{}) error { streams <- s; return nil })
		if conn == nil {
			return
		}
		defer conn.Close()
		var stdout, status httpstream.Stream
		for i := 0; i < 3; i++ {
			select {
			case stream := <-streams:
				switch stream.Headers().Get(corev1.StreamType) {
				case corev1.StreamTypeStdout:
					stdout = stream
				case corev1.StreamTypeError:
					status = stream
				}
			case <-r.Context().Done():
				return
			}
		}
		if started != nil {
			close(started)
		}
		if hold {
			<-conn.CloseChan()
			return
		}
		if stdout != nil {
			_, _ = stdout.Write([]byte(output))
		}
		if status != nil {
			_ = json.NewEncoder(status).Encode(metav1.Status{Status: metav1.StatusSuccess})
		}
	}
}

func realOwnedExec(t *testing.T, plan Plan) *OwnedExec {
	t.Helper()
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to freshly built ARM executable")
	}
	return &OwnedExec{Plan: plan, image: image}
}

func TestOwnedExecRealChildReceipt(t *testing.T) {
	plan, pod, closeServer := ownedExecServer(t, execServerEffect(t, "ARM_BINARY_VERIFIED\n", nil, false))
	defer closeServer()
	executor := realOwnedExec(t, plan)
	command, _ := fixedExecCommand(plan, pod, execLookupBinary)
	var output, stderr bytes.Buffer
	if err := executor.Exec(t.Context(), pod, command, nil, &output, &stderr); err != nil {
		t.Fatal(err)
	}
	if output.String() != "ARM_BINARY_VERIFIED\n" || stderr.Len() != 0 {
		t.Fatalf("unexpected child receipt %q / %q", output.String(), stderr.String())
	}
}

func TestOwnedExecRealChildCancellation(t *testing.T) {
	started := make(chan struct{})
	plan, pod, closeServer := ownedExecServer(t, execServerEffect(t, "", started, true))
	defer closeServer()
	executor := realOwnedExec(t, plan)
	command, _ := fixedExecCommand(plan, pod, execLookupBinary)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- executor.Exec(ctx, pod, command, nil, &bytes.Buffer{}, &bytes.Buffer{}) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("real helper did not reach exec transport")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned exec did not join after cancellation")
	}
}

func TestOwnedExecRealChildOutputCap(t *testing.T) {
	plan, pod, closeServer := ownedExecServer(t, execServerEffect(t, strings.Repeat("x", 5000), nil, false))
	defer closeServer()
	executor := realOwnedExec(t, plan)
	command, _ := fixedExecCommand(plan, pod, execLookupBinary)
	var output bytes.Buffer
	err := executor.Exec(t.Context(), pod, command, nil, &output, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Kubernetes exec unavailable") || output.Len() != 0 {
		t.Fatalf("oversized remote output was accepted: %v / %d", err, output.Len())
	}
}

func TestOwnedStreamRealChildDoesNotExposeSecretDiagnostic(t *testing.T) {
	const canary = "SECRET_CANARY_TOKEN_18cbe1d1"
	plan, pod, closeServer := ownedExecServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, canary, http.StatusForbidden)
	})
	defer closeServer()
	executor := realOwnedExec(t, plan)
	command, _ := fixedExecCommand(plan, pod, execLookupBinary)
	var output, stderr bytes.Buffer
	err := executor.Exec(t.Context(), pod, command, nil, &output, &stderr)
	if err == nil || strings.Contains(err.Error(), canary) || output.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("exec leaked a remote diagnostic: %v / %q / %q", err, output.String(), stderr.String())
	}
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	session, forwardErr := startOwnedForwardImage(t.Context(), image, plan, pod)
	if session != nil {
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(deadline)
	}
	if forwardErr == nil || strings.Contains(forwardErr.Error(), canary) {
		t.Fatalf("forwarding leaked a remote diagnostic: %v", forwardErr)
	}
}

func TestOwnedExecRealChildDoesNotExposeCredentialPluginDiagnostic(t *testing.T) {
	const canary = "SECRET_PLUGIN_CANARY_56db57ef"
	plan, pod, closeServer := ownedExecServer(t, execServerEffect(t, "ARM_BINARY_VERIFIED\n", nil, false))
	defer closeServer()
	executor := realOwnedExec(t, plan)
	configPath := os.Getenv("KUBECONFIG")
	config, err := clientcmd.LoadFromFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "credential-plugin")
	marker := filepath.Join(t.TempDir(), "plugin-ran")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf x > '"+marker+"'\nprintf '%s\\n' '"+canary+"' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config.AuthInfos["test"].Token = ""
	config.AuthInfos["test"].Exec = &clientcmdapi.ExecConfig{Command: script, APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: clientcmdapi.NeverExecInteractiveMode}
	if err := clientcmd.WriteToFile(*config, configPath); err != nil {
		t.Fatal(err)
	}
	command, _ := fixedExecCommand(plan, pod, execLookupBinary)
	var output, stderr bytes.Buffer
	err = executor.Exec(t.Context(), pod, command, nil, &output, &stderr)
	if err == nil || strings.Contains(err.Error(), canary) || output.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("credential diagnostic escaped child: %v / %q / %q", err, output.String(), stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("credential subprocess ran inside owned exec helper: %v", err)
	}
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	session, forwardErr := startOwnedForwardImage(t.Context(), image, plan, pod)
	if session != nil {
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(deadline)
	}
	if forwardErr == nil || strings.Contains(forwardErr.Error(), canary) {
		t.Fatalf("credential diagnostic escaped forward helper: %v", forwardErr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("credential subprocess ran inside owned forward helper: %v", err)
	}
}

func TestOwnedExecRealChildParentPipeLoss(t *testing.T) {
	started := make(chan struct{})
	plan, pod, closeServer := ownedExecServer(t, execServerEffect(t, "", started, true))
	defer closeServer()
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to freshly built ARM executable")
	}
	request, err := heldExecRequest(reviewedExecRequest{Plan: plan, Pod: pod, Kind: execLookupBinary}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	defer request.Close()
	controlR, controlW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	replyR, replyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(image, ExecHelperArgument)
	cmd.Env = os.Environ()
	cmd.ExtraFiles = []*os.File{request, controlR, replyW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	controlR.Close()
	replyW.Close()
	defer replyR.Close()
	var hello helperPacket
	if err := readBoundedHelperJSON(replyR, &hello, maxHelperPacket); err != nil || hello.PID != cmd.Process.Pid {
		t.Fatalf("real child hello: %+v %v", hello, err)
	}
	if err := writeHelperBytes(controlW, []byte{'G'}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		controlW.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatal("child did not reach held exec stream")
	}
	// EOF on the private parent pipe must terminate the original child even
	// while client-go's remote stream is blocked.
	controlW.Close()
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-ended
		t.Fatal("parent pipe loss did not terminate held exec helper")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Fatalf("lost-pipe child remains after exact Wait: %v", err)
	}
}

func TestOwnedExecRealChildRejectsChangedIdentity(t *testing.T) {
	called := make(chan struct{}, 1)
	plan, pod, closeServer := ownedExecServerWithReview(t, func(http.ResponseWriter, *http.Request) { called <- struct{}{} }, func(review *Review) {
		review.Cluster.AuthSubject = "different-operator"
	})
	defer closeServer()
	executor := realOwnedExec(t, plan)
	command, _ := fixedExecCommand(plan, pod, execLookupBinary)
	err := executor.Exec(t.Context(), pod, command, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Kubernetes exec unavailable") || strings.Contains(err.Error(), "different-operator") {
		t.Fatalf("changed effective identity accepted: %v", err)
	}
	select {
	case <-called:
		t.Fatal("changed identity reached exec effect")
	default:
	}
}

func TestOwnedStreamsRejectChangedTLSServerBeforeAnyRequest(t *testing.T) {
	plan, pod, closeReviewed := ownedExecServer(t, execServerEffect(t, "ARM_BINARY_VERIFIED\n", nil, false))
	defer closeReviewed()
	var requests, authenticated atomic.Int32
	changed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "" {
			authenticated.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer changed.Close()
	path := os.Getenv("KUBECONFIG")
	config, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config.Clusters["test"].Server = changed.URL
	config.Clusters["test"].CertificateAuthorityData = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: changed.Certificate().Raw})
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatal(err)
	}

	executor := realOwnedExec(t, plan)
	command, _ := fixedExecCommand(plan, pod, execLookupBinary)
	if err := executor.Exec(t.Context(), pod, command, nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "Kubernetes exec unavailable") {
		t.Fatalf("changed server exec result: %v", err)
	}
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	session, err := startOwnedForwardImage(t.Context(), image, plan, pod)
	if session != nil {
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(deadline)
	}
	if err == nil || !strings.Contains(err.Error(), "Kubernetes forwarding unavailable") {
		t.Fatalf("changed server forward result: %v", err)
	}
	if requests.Load() != 0 || authenticated.Load() != 0 {
		t.Fatalf("changed server received %d requests, %d authenticated", requests.Load(), authenticated.Load())
	}
}

func TestOpenReviewedContextRejectsAuthProviderBeforeFactory(t *testing.T) {
	plan, _, closeServer := ownedExecServer(t, execServerEffect(t, "", nil, false))
	defer closeServer()
	const plugin = "arm-owned-stream-factory-guard"
	var factories atomic.Int32
	if err := rest.RegisterAuthProviderPlugin(plugin, func(string, map[string]string, rest.AuthProviderConfigPersister) (rest.AuthProvider, error) {
		factories.Add(1)
		return nil, errors.New("factory ran")
	}); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("KUBECONFIG")
	config, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config.AuthInfos["test"].Token = ""
	config.AuthInfos["test"].AuthProvider = &clientcmdapi.AuthProviderConfig{Name: plugin}
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReviewedContext(plan); err == nil || !strings.Contains(err.Error(), "credential helper unsupported") {
		t.Fatalf("auth provider was accepted: %v", err)
	}
	if factories.Load() != 0 {
		t.Fatalf("auth provider factory ran %d times before rejection", factories.Load())
	}
}

func TestOwnedExecRealChildShipsHeldFile(t *testing.T) {
	payload := bytes.Repeat([]byte("z"), 4096)
	sum := sha256.Sum256(payload)
	received := make(chan []byte, 1)
	plan, pod, closeServer := ownedExecServerWithReview(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := httpstream.Handshake(r, w, []string{remoteconstants.StreamProtocolV4Name}); err != nil {
			return
		}
		streams := make(chan httpstream.Stream, 4)
		conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(s httpstream.Stream, _ <-chan struct{}) error { streams <- s; return nil })
		if conn == nil {
			return
		}
		defer conn.Close()
		var stdin, status httpstream.Stream
		for i := 0; i < 4; i++ {
			select {
			case stream := <-streams:
				switch stream.Headers().Get(corev1.StreamType) {
				case corev1.StreamTypeStdin:
					stdin = stream
				case corev1.StreamTypeError:
					status = stream
				}
			case <-r.Context().Done():
				return
			}
		}
		if stdin == nil || status == nil {
			return
		}
		data, err := io.ReadAll(io.LimitReader(stdin, 4097))
		if err != nil {
			return
		}
		received <- data
		_ = json.NewEncoder(status).Encode(metav1.Status{Status: metav1.StatusSuccess})
	}, func(review *Review) {
		review.BinarySHA256 = hex.EncodeToString(sum[:])
	})
	defer closeServer()
	executor := realOwnedExec(t, plan)
	file, err := spoolBinary(t.Context(), bytes.NewReader(payload), int64(len(payload)), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	command, _ := fixedExecCommand(plan, pod, execShip)
	if err := executor.Exec(t.Context(), pod, command, file, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-received:
		if !bytes.Equal(data, payload) {
			t.Fatalf("held payload changed: %d bytes", len(data))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("held payload was not received")
	}
}

func forwardServerEffect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "websocket unavailable", http.StatusBadRequest)
		return
	}
	if _, err := httpstream.Handshake(r, w, []string{portforward.PortForwardProtocolV1Name}); err != nil {
		return
	}
	streams := make(chan httpstream.Stream, 2)
	conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(s httpstream.Stream, _ <-chan struct{}) error { streams <- s; return nil })
	if conn == nil {
		return
	}
	defer conn.Close()
	for {
		select {
		case stream := <-streams:
			if stream.Headers().Get(corev1.StreamType) == corev1.StreamTypeData {
				go func() { _, _ = io.Copy(stream, stream); _ = stream.Close() }()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func TestOwnedForwardRealChildActiveConnectionCancellation(t *testing.T) {
	plan, pod, closeServer := ownedExecServer(t, forwardServerEffect)
	defer closeServer()
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to freshly built ARM executable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session, err := startOwnedForwardImage(ctx, image, plan, pod)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", session.LocalPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatal(err)
	}
	var echo [4]byte
	if _, err := io.ReadFull(conn, echo[:]); err != nil || string(echo[:]) != "ping" {
		t.Fatalf("child forward echo %q: %v", echo, err)
	}
	cancel()
	deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := session.Close(deadline); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled forward result: %v", err)
	}
	select {
	case <-session.Done():
	default:
		t.Fatal("forward returned before exact child Wait")
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection survived helper cancellation")
	}
	if next, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", session.LocalPort), 100*time.Millisecond); err == nil {
		next.Close()
		t.Fatal("helper left local listener open")
	}
}

func TestOwnedForwardRealChildSetupCancellation(t *testing.T) {
	entered := make(chan struct{})
	plan, pod, closeServer := ownedExecServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "websocket unavailable", http.StatusBadRequest)
			return
		}
		close(entered)
		<-r.Context().Done()
	})
	defer closeServer()
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to freshly built ARM executable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := startOwnedForwardImage(ctx, image, plan, pod); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("forward child did not reach setup")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled setup result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forward helper setup did not join after cancellation")
	}
}

func TestOwnedForwardRealChildRevokedPodClosesActiveConnection(t *testing.T) {
	plan, pod, closeServer, replaceUID := ownedExecServerMutable(t, forwardServerEffect, nil)
	defer closeServer()
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to freshly built ARM executable")
	}
	session, err := startOwnedForwardImage(t.Context(), image, plan, pod)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", session.LocalPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	replaceUID("replacement-pod-uid")
	select {
	case <-session.Done():
	case <-time.After(7 * time.Second):
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(deadline)
		t.Fatal("changed Pod UID did not close owned forwarding helper")
	}
	if session.Err() == nil {
		t.Fatal("revoked forwarding session reported success")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection survived Pod UID replacement")
	}
}

func TestOwnedForwardRealChildConnectionCapRecovery(t *testing.T) {
	plan, pod, closeServer := ownedExecServer(t, forwardServerEffect)
	defer closeServer()
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to freshly built ARM executable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session, err := startOwnedForwardImage(ctx, image, plan, pod)
	if err != nil {
		t.Fatal(err)
	}
	address := fmt.Sprintf("127.0.0.1:%d", session.LocalPort)
	var active []net.Conn
	defer func() {
		for _, conn := range active {
			conn.Close()
		}
	}()
	echo := func(conn net.Conn) error {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.WriteString(conn, "x"); err != nil {
			return err
		}
		var reply [1]byte
		if _, err := io.ReadFull(conn, reply[:]); err != nil {
			return err
		}
		if reply[0] != 'x' {
			return fmt.Errorf("unexpected echo %q", reply[:])
		}
		return nil
	}
	for i := 0; i < MaxForwardConnections; i++ {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := echo(conn); err != nil {
			conn.Close()
			t.Fatalf("active slot %d: %v", i, err)
		}
		active = append(active, conn)
	}
	excess, err := net.DialTimeout("tcp", address, time.Second)
	if err == nil {
		if err := echo(excess); err == nil {
			excess.Close()
			t.Fatal("connection above reviewed forwarding cap was served")
		}
		excess.Close()
	}
	active[0].Close()
	active = active[1:]
	recovered := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			if err := echo(conn); err == nil {
				active = append(active, conn)
				recovered = true
				break
			}
			conn.Close()
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("forwarding slot did not recover after original stream workers closed")
	}
	cancel()
	stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := session.Close(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capped forward: %v", err)
	}
}
