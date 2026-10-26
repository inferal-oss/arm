package kubeprovision

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestInventoryReviewRejectsOversizedMetadataBeforeDecode(t *testing.T) {
	// One small projected Node can hide an oversized ignored annotation. Bounding
	// the final dashboard projection cannot bound the preceding API decoder.
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"apiVersion":"v1","kind":"NodeList","items":[{"metadata":{"name":"worker","uid":"node-uid","annotations":{"large":"`)
		for i := 0; i < 129; i++ {
			_, _ = io.WriteString(w, strings.Repeat("x", 64<<10))
		}
		_, _ = io.WriteString(w, `"}},"status":{}}]}`)
	}))
	defer api.Close()
	config := clientcmdapi.NewConfig()
	config.Clusters["test"] = &clientcmdapi.Cluster{Server: api.URL, CertificateAuthorityData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw})}
	config.AuthInfos["test"] = &clientcmdapi.AuthInfo{Token: "invented-bounds-fixture-token"}
	config.Contexts["test"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test", Namespace: "default"}
	config.CurrentContext = "test"
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	client, err := OpenInventoryContext("test")
	if err != nil {
		t.Fatal(err)
	}
	if nodes, err := client.Nodes(context.Background()); err == nil {
		t.Fatalf("oversized API metadata was fully decoded into %d projected node(s)", len(nodes))
	}
}

type inventoryCountedBody struct {
	io.Reader
	read, closed int
}

func (b *inventoryCountedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *inventoryCountedBody) Close() error { b.closed++; return nil }

func TestInventoryReviewMetadataResponseBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		size      int
		declared  int64
		wantError bool
	}{
		{"small", 1024, 1024, false},
		{"exact-unknown", int(maxMetadataResponseBytes), -1, false},
		{"oversized-declared", int(maxMetadataResponseBytes) + 1, maxMetadataResponseBytes + 1, true},
		{"oversized-stream", int(maxMetadataResponseBytes) + 1024, -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &inventoryCountedBody{Reader: strings.NewReader(strings.Repeat("a", tc.size))}
			response, err := boundMetadataResponse(&http.Response{StatusCode: 200, ContentLength: tc.declared, Body: body})
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				if err != nil {
					var buf [4]byte
					_, _ = response.Body.Read(buf[:])
				}
				_ = response.Body.Close()
			}
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if body.read > int(maxMetadataResponseBytes)+1 {
				t.Fatalf("read %d bytes past metadata bound", body.read)
			}
			if body.closed != 1 {
				t.Fatalf("original body closed %d times", body.closed)
			}
			if !tc.wantError && body.read != tc.size {
				t.Fatalf("accepted body truncated: %d != %d", body.read, tc.size)
			}
		})
	}
}

func TestInventoryReviewPreservesUpgradedDuplexBody(t *testing.T) {
	body := &inventoryCountedBody{Reader: strings.NewReader("native stream")}
	response, err := boundMetadataResponse(&http.Response{StatusCode: 101, ContentLength: maxMetadataResponseBytes + 1, Body: body})
	if err != nil || response.Body != body || body.closed != 0 {
		t.Fatalf("upgraded original body replaced or closed: %v", err)
	}
	_ = body.Close()
}
