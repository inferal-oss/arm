package certverify

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"sync"
	"testing"
	"time"
)

func certificate(t *testing.T, extra int) []byte {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "signature-only test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	if extra > 0 {
		c.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 2, 3, 4}, Value: bytes.Repeat([]byte{1}, extra)}}
	}
	der, err := x509.CreateCertificate(rand.Reader, c, c, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestSignatureCacheOwnsExactBytesAndNeverCachesFailures(t *testing.T) {
	c := signatureCache{limit: 2}
	good := certificate(t, 0)
	input := bytes.Clone(good)
	if err := c.verify(input); err != nil {
		t.Fatal(err)
	}
	input[len(input)-1] ^= 1
	if err := c.verify(input); err == nil {
		t.Fatal("changed signature accepted from warm cache")
	}
	if len(c.entries) != 1 {
		t.Fatal("negative result retained")
	}
	if err := c.verify(good); err != nil {
		t.Fatal("caller mutation changed cache key", err)
	}
	if err := c.verify([]byte("malformed DER")); err == nil {
		t.Fatal("malformed certificate accepted")
	}
	if len(c.entries) != 1 {
		t.Fatal("parse failure retained")
	}
}

func TestSignatureCacheEvictionAndOversizedBypass(t *testing.T) {
	c := signatureCache{limit: 2}
	der := [][]byte{certificate(t, 0), certificate(t, 0), certificate(t, 0)}
	for _, raw := range der {
		if err := c.verify(raw); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.entries) != 1 {
		t.Fatal("cache exceeded bound or did not evict", len(c.entries))
	}
	if _, ok := c.entries[string(der[0])]; ok {
		t.Fatal("old entry survived full eviction")
	}
	large := certificate(t, maxDERBytes)
	if len(large) <= maxDERBytes {
		t.Fatal("fixture is not oversized")
	}
	if err := c.verify(large); err != nil {
		t.Fatal("uncached valid certificate rejected", err)
	}
	if len(c.entries) != 1 {
		t.Fatal("oversized key retained")
	}
	large[len(large)-1] ^= 1
	if err := c.verify(large); err == nil {
		t.Fatal("oversized bad signature accepted")
	}
	for _, limit := range []int{0, -1, maxEntries + 1} {
		uncached := signatureCache{limit: limit}
		if err := uncached.verify(der[0]); err != nil {
			t.Fatal(err)
		}
		if len(uncached.entries) != 0 {
			t.Fatal("invalid capacity enabled storage")
		}
	}
}

func TestSignatureCacheConcurrentColdAndWarmReads(t *testing.T) {
	c := signatureCache{limit: maxEntries}
	certs := [][]byte{certificate(t, 0), certificate(t, 0), certificate(t, 0)}
	var wg sync.WaitGroup
	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				if err := c.verify(certs[n%len(certs)]); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if len(c.entries) != len(certs) {
		t.Fatal("concurrent catalog mismatch", len(c.entries))
	}
}
