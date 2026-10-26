package kubeprovision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type redirectObservation struct{ reviewed, unreviewed, authenticated, connections atomic.Int32 }

func redirectFixture(t *testing.T, sameOrigin bool) (Plan, Pod, *redirectObservation) {
	t.Helper()
	observed := &redirectObservation{}
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed.unreviewed.Add(1)
		if r.Header.Get("Authorization") != "" {
			observed.authenticated.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(authv1.SelfSubjectReview{Status: authReviewFixture.Status})
	}))
	other.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			observed.connections.Add(1)
		}
	}
	other.StartTLS()
	t.Cleanup(other.Close)
	reviewed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed.reviewed.Add(1)
		if r.Header.Get("Authorization") == "" {
			t.Error("reviewed request was not authenticated")
		}
		if sameOrigin && r.URL.Path == "/reviewed-self" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(authv1.SelfSubjectReview{Status: authReviewFixture.Status})
			return
		}
		target := other.URL + r.URL.Path
		if sameOrigin {
			target = "/reviewed-self"
		}
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(reviewed.Close)
	// Both TLS identities are trusted by this bundle; only one origin is in
	// the reviewed Plan. The second listener must receive no request at all.
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: reviewed.Certificate().Raw})
	ca = append(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.Certificate().Raw})...)
	sum := sha256.Sum256(ca)
	provider, prior, _ := fixture(t)
	record, err := provider.Apply(t.Context(), prior)
	if err != nil {
		t.Fatal(err)
	}
	review := prior.Review
	review.Cluster.Server = reviewed.URL
	review.Cluster.CAHash = hex.EncodeToString(sum[:])
	plan, err := NewPlan(review)
	if err != nil {
		t.Fatal(err)
	}
	pod := *record.Pod
	pod.PlanAnnotation = plan.SHA256
	config := clientcmdapi.NewConfig()
	config.Clusters["test"] = &clientcmdapi.Cluster{Server: reviewed.URL, CertificateAuthorityData: ca}
	config.AuthInfos["test"] = &clientcmdapi.AuthInfo{Token: "review-only-invented-token"}
	config.Contexts[review.Cluster.Context] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test", Namespace: "default"}
	config.CurrentContext = review.Cluster.Context
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err = clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	return plan, pod, observed
}

func TestReviewedOriginRejectsCredentialRedirect(t *testing.T) {
	for _, kind := range []string{"selected-context", "typed", "exec-helper", "forward-helper"} {
		t.Run(kind, func(t *testing.T) {
			plan, pod, observed := redirectFixture(t, false)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var err error
			switch kind {
			case "selected-context":
				api, e := OpenContext(plan.Review.Cluster.Context)
				if e != nil {
					t.Fatal(e)
				}
				_, err = api.Cluster(ctx)
			case "typed":
				api, e := OpenReviewedContext(plan)
				if e != nil {
					t.Fatal(e)
				}
				_, err = api.Cluster(ctx)
			case "exec-helper":
				executor := realOwnedExec(t, plan)
				command, e := fixedExecCommand(plan, pod, execLookupBinary)
				if e != nil {
					t.Fatal(e)
				}
				err = executor.Exec(ctx, pod, command, nil, &bytes.Buffer{}, &bytes.Buffer{})
			case "forward-helper":
				image := os.Getenv("ARM_KUBE_TEST_BINARY")
				if image == "" {
					t.Skip("set ARM_KUBE_TEST_BINARY to freshly built ARM executable")
				}
				var session *OwnedForwardSession
				session, err = startOwnedForwardImage(ctx, image, plan, pod)
				if session != nil {
					closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer closeCancel()
					if closeErr := session.Close(closeCtx); closeErr != nil {
						t.Fatal(closeErr)
					}
				}
			}
			if err == nil {
				t.Fatal("redirect was accepted")
			}
			if observed.reviewed.Load() != 1 {
				t.Fatalf("did not reach exactly one original reviewed request: %d", observed.reviewed.Load())
			}
			if observed.unreviewed.Load() != 0 || observed.authenticated.Load() != 0 || observed.connections.Load() != 0 {
				t.Fatalf("unreviewed redirect received %d connections/%d requests/%d authenticated", observed.connections.Load(), observed.unreviewed.Load(), observed.authenticated.Load())
			}
		})
	}
}

func TestReviewedOriginAllowsSameOriginRedirect(t *testing.T) {
	for _, kind := range []string{"selected-context", "reviewed-context"} {
		t.Run(kind, func(t *testing.T) {
			plan, _, observed := redirectFixture(t, true)
			var api *ClientGo
			var err error
			if kind == "selected-context" {
				api, err = OpenContext(plan.Review.Cluster.Context)
			} else {
				api, err = OpenReviewedContext(plan)
			}
			if err != nil {
				t.Fatal(err)
			}
			cluster, err := api.Cluster(t.Context())
			if err != nil || !reflect.DeepEqual(cluster, plan.Review.Cluster) {
				t.Fatalf("same-origin identity changed: %v", err)
			}
			if observed.reviewed.Load() != 2 || observed.unreviewed.Load() != 0 || observed.connections.Load() != 0 {
				t.Fatalf("same-origin request roster: reviewed=%d unreviewed=%d connections=%d", observed.reviewed.Load(), observed.unreviewed.Load(), observed.connections.Load())
			}
		})
	}
}

type observedRequestBody struct {
	io.Reader
	closed bool
}

func (b *observedRequestBody) Close() error { b.closed = true; return nil }

func TestOriginRefusalClosesRequestBody(t *testing.T) {
	body := &observedRequestBody{Reader: strings.NewReader("unsent request body")}
	request, err := http.NewRequest(http.MethodPost, "https://different.invalid/api", body)
	if err != nil {
		t.Fatal(err)
	}
	wrap, err := reviewedOriginGuard("https://reviewed.invalid")
	if err != nil {
		t.Fatal(err)
	}
	transport := wrap(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("refused origin reached underlying transport")
		return nil, nil
	}))
	if _, err = transport.RoundTrip(request); !errors.Is(err, ErrIdentity) || !body.closed {
		t.Fatalf("refusal failed to close owned body: closed=%t error=%v", body.closed, err)
	}
}

func TestOriginDNSIdentityUsesIDNA(t *testing.T) {
	wrap, err := reviewedOriginGuard("https://İ.example")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := wrap(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}))
	different, err := http.NewRequest(http.MethodGet, "https://i.example/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.RoundTrip(different); !errors.Is(err, ErrIdentity) || calls != 0 {
		t.Fatalf("different IDNA host reached transport: calls=%d error=%v", calls, err)
	}
	same, err := http.NewRequest(http.MethodGet, "https://xn--i-9bb.example/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(same)
	if err != nil || calls != 1 {
		t.Fatalf("same IDNA origin refused: calls=%d error=%v", calls, err)
	}
	response.Body.Close()
}
