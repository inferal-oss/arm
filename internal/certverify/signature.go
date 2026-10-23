// Package certverify memoizes only successful cryptographic checks of exact
// certificate DER. It never caches identity, trust, time or authorization.
package certverify

import (
	"crypto/x509"
	"sync"
)

const (
	maxEntries  = 256
	maxDERBytes = 8192
)

type signatureCache struct {
	mu      sync.Mutex
	entries map[string]struct{}
	limit   int
}

var signatures = signatureCache{limit: maxEntries}

// SelfSignature verifies a DER certificate's signature with its own public key.
// Callers must independently parse/check constraints, identity, pins and validity
// on EVERY use. No parsed pointer or caller-owned byte slice is retained.
func SelfSignature(der []byte) error { return signatures.verify(der) }

func (c *signatureCache) verify(der []byte) error {
	cacheable := len(der) <= maxDERBytes && c.limit > 0 && c.limit <= maxEntries
	var key string
	if cacheable {
		key = string(der)
		c.mu.Lock()
		_, ok := c.entries[key]
		c.mu.Unlock()
		if ok {
			return nil
		}
		// Verify exactly the owned immutable key, including when the caller
		// later reuses its input buffer. No x509 object is shared with callers.
		der = []byte(key)
	}
	// Parsing these exact bytes avoids treating mutable x509.Certificate fields
	// supplied by a caller as if they were the previously verified DER.
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	if err = cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		return err
	}
	if cacheable {
		// The conversion owns its bytes. Subsequent caller mutations cannot alter
		// a key or convert a negative result into an accepted cache hit.
		c.mu.Lock()
		_, present := c.entries[key]
		if c.entries == nil || !present && len(c.entries) >= c.limit {
			c.entries = make(map[string]struct{}, c.limit)
		}
		c.entries[key] = struct{}{}
		c.mu.Unlock()
	}
	return nil
}
