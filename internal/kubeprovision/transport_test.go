package kubeprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	remoteconstants "k8s.io/apimachinery/pkg/util/remotecommand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/streaming/pkg/httpstream"
	"k8s.io/streaming/pkg/httpstream/spdy"
)

// An HTTP SPDY server exercises the actual client-go exec stream, including
// stdin, stdout and status, without touching a configured cluster.
func TestNativeExecTransport(t *testing.T) {
	p, plan, fakeClient := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	object, err := fakeClient.CoreV1().Pods("default").Get(t.Context(), record.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var observedPath, observedCommand, input string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(object)
			return
		}
		observedPath = r.URL.Path
		observedCommand = strings.Join(r.URL.Query()["command"], " ")
		if _, err := httpstream.Handshake(r, w, []string{remoteconstants.StreamProtocolV4Name}); err != nil {
			t.Errorf("handshake: %v", err)
			return
		}
		streams := make(chan httpstream.Stream, 4)
		conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(s httpstream.Stream, _ <-chan struct{}) error { streams <- s; return nil })
		if conn == nil {
			t.Error("upgrade failed")
			return
		}
		defer conn.Close()
		var stdin, stdout, status httpstream.Stream
		for i := 0; i < 4; i++ {
			s := <-streams
			switch s.Headers().Get(corev1.StreamType) {
			case corev1.StreamTypeStdin:
				stdin = s
			case corev1.StreamTypeStdout:
				stdout = s
			case corev1.StreamTypeError:
				status = s
			}
		}
		if stdin == nil || stdout == nil || status == nil {
			t.Error("missing streams")
			return
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			t.Errorf("stdin: %v", err)
			return
		}
		input = string(data)
		io.WriteString(stdout, "verified\n")
		json.NewEncoder(status).Encode(metav1.Status{Status: metav1.StatusSuccess})
	}))
	defer server.Close()
	config := &rest.Config{Host: server.URL}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	api := NewClientGoForTest("test", server.URL, hash("ca"), config, client)
	var stdout, stderr bytes.Buffer
	if err := api.Exec(t.Context(), *record.Pod, []string{"/bin/cat", "/arm/private/receipt"}, strings.NewReader("payload"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if observedPath != "/api/v1/namespaces/default/pods/arm-node-one/exec" || observedCommand != "/bin/cat /arm/private/receipt" || input != "payload" || stdout.String() != "verified\n" || stderr.Len() != 0 {
		t.Fatalf("transport mismatch path=%q command=%q input=%q stdout=%q stderr=%q", observedPath, observedCommand, input, stdout.String(), stderr.String())
	}
}

type heldExecInput struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	reads   atomic.Int32
}

func (r *heldExecInput) Read(p []byte) (int, error) {
	r.reads.Add(1)
	r.once.Do(func() { close(r.started) })
	<-r.release
	p[0] = 'x'
	return 1, nil
}

func TestNativeExecCancellationSealsHeldInput(t *testing.T) {
	p, plan, fakeClient := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	object, err := fakeClient.CoreV1().Pods("default").Get(t.Context(), record.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(object)
			return
		}
		if _, err := httpstream.Handshake(r, w, []string{remoteconstants.StreamProtocolV4Name}); err != nil {
			return
		}
		conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(httpstream.Stream, <-chan struct{}) error { return nil })
		if conn == nil {
			return
		}
		defer conn.Close()
		<-conn.CloseChan()
	}))
	defer server.Close()
	config := &rest.Config{Host: server.URL}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	api := NewClientGoForTest("test", server.URL, hash("ca"), config, client)
	held := &heldExecInput{started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- api.Exec(ctx, *record.Pod, []string{"/bin/cat"}, held, io.Discard, io.Discard) }()
	select {
	case <-held.started:
	case <-time.After(3 * time.Second):
		cancel()
		close(held.release)
		t.Fatal("stdin copy did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("exec returned before held source was sealed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(held.release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel result: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec did not seal released input")
	}
	if got := held.reads.Load(); got != 1 {
		t.Fatalf("caller source read %d times", got)
	}
}

func TestNativePortForwardBindsLoopbackAndCarriesBytes(t *testing.T) {
	p, plan, fakeClient := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	object, err := fakeClient.CoreV1().Pods("default").Get(t.Context(), record.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(object)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "websocket unsupported", http.StatusBadRequest)
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
			case s := <-streams:
				if s.Headers().Get(corev1.StreamType) == corev1.StreamTypeData {
					go func() { io.Copy(s, s); s.Close() }()
				}
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer server.Close()
	config := &rest.Config{Host: server.URL}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	api := NewClientGoForTest("test", server.URL, hash("ca"), config, client)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := api.Forward(ctx, *record.Pod, 9443)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", session.LocalPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("forwarded %q", buf)
	}
	other, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.2:%d", session.LocalPort), 100*time.Millisecond)
	if err == nil {
		other.Close()
		t.Fatal("forward accepted non-loopback binding")
	}
	first := session.Close()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("forward worker did not join")
	}
	if second := session.Close(); (first == nil) != (second == nil) || first != nil && second.Error() != first.Error() {
		t.Fatalf("repeat Close changed terminal error: %v / %v", first, second)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("owned forward left local connection active after Close")
	}
}

func TestPortForwardBoundsRemoteErrorStream(t *testing.T) {
	p, plan, fakeClient := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	object, err := fakeClient.CoreV1().Pods("default").Get(t.Context(), record.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(object)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "websocket unsupported", http.StatusBadRequest)
			return
		}
		if _, err := httpstream.Handshake(r, w, []string{portforward.PortForwardProtocolV1Name}); err != nil {
			return
		}
		conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(s httpstream.Stream, _ <-chan struct{}) error {
			if s.Headers().Get(corev1.StreamType) == corev1.StreamTypeError {
				go func() { io.WriteString(s, strings.Repeat("x", 8192)); s.Close() }()
			}
			return nil
		})
		if conn == nil {
			return
		}
		defer conn.Close()
		<-conn.CloseChan()
	}))
	defer server.Close()
	config := &rest.Config{Host: server.URL}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	api := NewClientGoForTest("test", server.URL, hash("ca"), config, client)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := api.Forward(ctx, *record.Pod, 9443)
	if err != nil {
		t.Fatal(err)
	}
	local, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", session.LocalPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	select {
	case <-session.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("oversized error stream did not stop forwarding")
	}
	if err := session.Err(); err == nil || !strings.Contains(err.Error(), "exceeded bound") {
		t.Fatalf("unbounded remote error: %v", err)
	}
}

func TestPortForwardStalledUpgradeCancels(t *testing.T) {
	p, plan, fakeClient := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	object, err := fakeClient.CoreV1().Pods("default").Get(t.Context(), record.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/namespaces/default/pods/arm-node-one" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(object)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	config := &rest.Config{Host: server.URL}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	api := NewClientGoForTest("test", server.URL, hash("ca"), config, client)
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := api.Forward(ctx, *record.Pod, 9443); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled upgrade result: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("stalled upgrade outlived context")
	}
}

func TestGenerateNameCreateDisablesClientGoRetries(t *testing.T) {
	_, plan, _ := fixture(t)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("unexpected %s", r.Method)
			return
		}
		posts.Add(1)
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"TooManyRequests","code":429}`)
	}))
	defer server.Close()
	config := &rest.Config{Host: server.URL}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	api := NewClientGoForTest("test", server.URL, hash("ca"), config, client)
	if _, err := api.CreatePod(t.Context(), plan.Pod); err == nil {
		t.Fatal("retryable ambiguous create reported success")
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("non-idempotent Pod POST retried %d times", got)
	}
}
