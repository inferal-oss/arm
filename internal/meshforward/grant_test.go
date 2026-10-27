package meshforward

import (
	"github.com/inferal-oss/arm/internal/model"
	"strings"
	"testing"
	"time"
)

func testGrant() Grant {
	return Grant{Workspace: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32), Source: strings.Repeat("c", 32), Target: strings.Repeat("d", 32),
		SourceGeneration: 1, TargetGeneration: 1, DirectoryPin: strings.Repeat("1", 64), DirectorySPKI: strings.Repeat("2", 64), SourcePin: strings.Repeat("3", 64), SourceSPKI: strings.Repeat("4", 64),
		TargetPin: strings.Repeat("5", 64), TargetSPKI: strings.Repeat("6", 64), Account: "operator", Endpoint: Endpoint{Name: "web", Network: "tcp", Address: "127.0.0.1:8080"},
		Revision: 1, MaxConnections: 2, BytesPerConnection: 1 << 20, BytesPerSecond: 1 << 20,
		TotalBytes: 4 << 20, CreditNonce: strings.Repeat("e", 32),
		Transit: model.Resources{CPU: .01, Memory: 4 << 20}, MaxTTL: time.Minute, Expires: time.Now().Add(time.Hour)}
}

func TestGrantRequiresExactOriginalAndLiteralService(t *testing.T) {
	g := testGrant()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	q := Request{Version: 1, Workspace: g.Workspace, Generation: g.Generation, Source: g.Source, Target: g.Target, SourcePin: g.SourcePin, TargetPin: g.TargetPin, Service: g.Endpoint.Name, GrantRevision: g.Revision, ConnectionID: strings.Repeat("7", 32), TTL: time.Second, RouteBindingHash: strings.Repeat("8", 64)}
	allow := func(g Grant, q Request, source string) bool {
		return g.Authorize(time.Now(), source, g.SourceSPKI, g.TargetPin, g.TargetSPKI, g.Account, q) == nil
	}
	if !allow(g, q, g.SourcePin) {
		t.Fatal("exact original source denied")
	}
	if allow(g, q, g.TargetPin) {
		t.Fatal("relay borrowed final grant")
	}
	q.Service = "other"
	if allow(g, q, g.SourcePin) {
		t.Fatal("service expansion admitted")
	}
	q.Service = g.Endpoint.Name
	q.GrantRevision++
	if allow(g, q, g.SourcePin) {
		t.Fatal("stale grant admitted")
	}
	q.GrantRevision = g.Revision
	for _, address := range []string{"example.com:8080", "0.0.0.0:8080", "192.0.2.1:8080", "127.0.0.1:0", "127.0.0.1:08080"} {
		x := g
		x.Endpoint.Address = address
		if x.Validate() == nil {
			t.Fatalf("unsafe address %q admitted", address)
		}
	}
}
