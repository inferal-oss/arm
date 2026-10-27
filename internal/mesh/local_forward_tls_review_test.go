package mesh

import (
	"crypto/tls"
	"strings"
	"testing"
)

func TestLocalForwardTLSUsesExactCurrentIdentityAndProtocol(t *testing.T) {
	store := workspaceTLSFixture(t, "a", "local-tls")
	server, err := store.LocalForwardServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	client, err := LocalForwardClientTLS(store.Config().Identity)
	if err != nil {
		t.Fatal(err)
	}
	if client.InsecureSkipVerify || client.RootCAs == nil || len(client.RootCAs.Subjects()) != 1 || client.ClientSessionCache != nil || !client.SessionTicketsDisabled {
		t.Fatal("normal exact-root verification weakened")
	}
	if c, s := innerHandshake(client, server); c != nil || s != nil {
		t.Fatal(c, s)
	}
	for _, protocol := range []string{"", "http/1.1", "arm.mesh.v1"} {
		t.Run("protocol="+protocol, func(t *testing.T) {
			wrong := server.Clone()
			wrong.NextProtos = []string{protocol}
			if c, s := innerHandshake(client, wrong); c == nil && s == nil {
				t.Fatal("wrong source ALPN accepted")
			}
		})
	}
	low := server.Clone()
	low.MinVersion = tls.VersionTLS12
	low.MaxVersion = tls.VersionTLS12
	if c, s := innerHandshake(client, low); c == nil && s == nil {
		t.Fatal("TLS1.2 source accepted")
	}
	identity := store.Config().Identity
	identity.SPKI = strings.Repeat("0", 64)
	if _, err = LocalForwardClientTLS(identity); err == nil {
		t.Fatal("mismatched SPKI accepted")
	}
	identity = store.Config().Identity
	identity.Host = strings.Repeat("b", 32)
	if _, err = LocalForwardClientTLS(identity); err == nil {
		t.Fatal("mismatched source SAN accepted")
	}
	if _, err = LocalForwardClientTLS(expiredNodeIdentity(t, store)); err == nil {
		t.Fatal("expired source identity accepted")
	}
}

func TestLocalForwardTLSRefusesMismatchedStoreAndClosedSigner(t *testing.T) {
	store := workspaceTLSFixture(t, "a", "local-current")
	other := workspaceTLSFixture(t, "b", "other-current")
	original := store.config.Identity
	store.mu.Lock()
	store.config.Identity = other.Config().Identity
	store.mu.Unlock()
	if _, err := store.LocalForwardServerTLS(); err == nil {
		t.Fatal("signer different from current identity accepted")
	}
	store.mu.Lock()
	store.config.Identity = original
	store.mu.Unlock()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LocalForwardServerTLS(); err == nil {
		t.Fatal("closed signer accepted")
	}
}
