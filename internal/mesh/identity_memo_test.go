package mesh

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestWarmSignaturePreservesCurrentMeshIdentityChecks(t *testing.T) {
	i, keyPEM, err := generate(strings.Repeat("a", 32), "memo-fixture", "127.0.0.1:12345")
	if err != nil {
		t.Fatal(err)
	}
	first, err := ValidateIdentity(i)
	if err != nil {
		t.Fatal(err)
	}
	first.DNSNames[0] = "changed.invalid"
	first.Raw[0] ^= 1
	if _, err = ValidateIdentity(i); err != nil {
		t.Fatal("parsed certificate was shared", err)
	}
	for name, mutate := range map[string]func(*Identity){
		"host":        func(v *Identity) { v.Host = strings.Repeat("b", 32) },
		"fingerprint": func(v *Identity) { v.Fingerprint = strings.Repeat("0", 64) },
		"spki":        func(v *Identity) { v.SPKI = strings.Repeat("0", 64) },
		"expiry":      func(v *Identity) { v.Expires = v.Expires.Add(time.Second) },
		"address":     func(v *Identity) { v.Address = "127.0.0.1:0" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := i
			mutate(&changed)
			if _, err := ValidateIdentity(changed); err == nil {
				t.Fatal("warm signature bypassed current identity constraints")
			}
		})
	}
	block, _ := pem.Decode(keyPEM)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	old, err := parseIdentity(i, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	old.NotBefore = now.Add(-time.Hour)
	old.NotAfter = now.Add(-time.Minute)
	der, err := x509.CreateCertificate(rand.Reader, old, old, old.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	i.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	i.Fingerprint = hex.EncodeToString(sum[:])
	i.Expires = old.NotAfter
	// Historical registry evidence remains readable and warms only its signature.
	if _, err = parseIdentity(i, false); err != nil {
		t.Fatal(err)
	}
	if _, err = ValidateIdentity(i); err == nil {
		t.Fatal("cached historical signature bypassed expiry")
	}
}
