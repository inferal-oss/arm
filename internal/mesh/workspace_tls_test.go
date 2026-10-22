package mesh

import (
	"context"
	"crypto/tls"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func workspaceTLSFixture(t *testing.T, id, name string) *Store {
	t.Helper()
	s, e := InitPrepared(filepath.Join(t.TempDir(), "state"), strings.Repeat(id, 32), name, "127.0.0.1:40001", "127.0.0.1:40001")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func innerHandshake(client, server *tls.Config) (error, error) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { left.Close(); right.Close() })
	defer stop()
	result := make(chan error, 1)
	go func() { result <- tls.Server(right, server).HandshakeContext(ctx) }()
	first := tls.Client(left, client).HandshakeContext(ctx)
	if first != nil {
		left.Close()
		right.Close()
	}
	return first, <-result
}
func TestWorkspaceInnerTLSNormalPinsNoPairPromotion(t *testing.T) {
	a := workspaceTLSFixture(t, "a", "inner-a")
	b := workspaceTLSFixture(t, "b", "inner-b")
	ai, bi := publicWorkspaceIdentity(a.Config().Identity), publicWorkspaceIdentity(b.Config().Identity)
	client, e := a.WorkspaceTLS(ai, bi, false)
	if e != nil {
		t.Fatal(e)
	}
	server, e := b.WorkspaceTLS(bi, ai, true)
	if e != nil {
		t.Fatal(e)
	}
	if client.InsecureSkipVerify || client.RootCAs == nil || len(client.RootCAs.Subjects()) != 1 || server.ClientAuth != tls.RequireAndVerifyClientCert || client.MinVersion != tls.VersionTLS13 || client.MaxVersion != tls.VersionTLS13 || client.ClientSessionCache != nil || !client.SessionTicketsDisabled {
		t.Fatal("workspace TLS weakened normal verification")
	}
	if c, s := innerHandshake(client, server); c != nil || s != nil {
		t.Fatal(c, s)
	}
	if len(a.Config().Peers) != 0 || len(b.Config().Peers) != 0 {
		t.Fatal("workspace TLS promoted membership into direct pairing")
	}
	for _, protocol := range []string{"", "http/1.1", "arm.mesh.ssh.v1"} {
		wrong := client.Clone()
		wrong.NextProtos = []string{protocol}
		if c, s := innerHandshake(wrong, server); c == nil && s == nil {
			t.Fatal("accepted wrong inner protocol", protocol)
		}
	}
	low := client.Clone()
	low.MinVersion = tls.VersionTLS12
	low.MaxVersion = tls.VersionTLS12
	if c, s := innerHandshake(low, server); c == nil && s == nil {
		t.Fatal("accepted TLS1.2")
	}
	missing := client.Clone()
	missing.Certificates = nil
	if c, s := innerHandshake(missing, server); c == nil && s == nil {
		t.Fatal("accepted missing candidate certificate")
	}
}
func TestWorkspaceInnerTLSRefusesCurrentIdentityReplacementAndClosedStore(t *testing.T) {
	a := workspaceTLSFixture(t, "a", "inner-a")
	b := workspaceTLSFixture(t, "b", "inner-b")
	ai, bi := publicWorkspaceIdentity(a.Config().Identity), publicWorkspaceIdentity(b.Config().Identity)
	client, e := a.WorkspaceTLS(ai, bi, false)
	if e != nil {
		t.Fatal(e)
	}
	server, e := b.WorkspaceTLS(bi, ai, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Renew("explicit local certificate change invalidates workspace signing context"); e != nil {
		t.Fatal(e)
	}
	if e = a.WorkspaceIdentityCurrent(ai); e == nil {
		t.Fatal("old signing identity remained current")
	}
	if _, e = a.WorkspaceTLS(ai, bi, false); e == nil {
		t.Fatal("built config for replaced identity")
	}
	if c, s := innerHandshake(client, server); c == nil && s == nil {
		t.Fatal("cached config authorized replaced identity")
	}
	fresh := publicWorkspaceIdentity(a.Config().Identity)
	client, e = a.WorkspaceTLS(fresh, bi, false)
	if e != nil {
		t.Fatal(e)
	}
	server, e = b.WorkspaceTLS(bi, fresh, true)
	if e != nil {
		t.Fatal(e)
	}
	a.Close()
	if c, s := innerHandshake(client, server); c == nil && s == nil {
		t.Fatal("cached config authorized closed signer")
	}
}
