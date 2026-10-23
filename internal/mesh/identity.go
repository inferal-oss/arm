// Package mesh implements explicit, pairwise node trust. It does not discover
// peers or turn host reachability into authorization.
package mesh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"github.com/yrashk/arm/internal/certverify"
	"github.com/yrashk/arm/internal/local"
	"math/big"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Identity struct {
	Owner         string    `json:"owner"`
	OwnerUsername string    `json:"owner_username"`
	Host          string    `json:"host"`
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	Certificate   string    `json:"certificate"`
	SPKI          string    `json:"spki_sha256"`
	Fingerprint   string    `json:"fingerprint"`
	Expires       time.Time `json:"expires_at"`
}

func SAN(host string) string { return "node-" + host + ".arm.invalid" }
func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && strings.ToLower(id) == id
}
func validName(s string) bool {
	if s == "" || len(s) > 128 || s == "localhost" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
func ValidateIdentity(i Identity) (*x509.Certificate, error) { return parseIdentity(i, true) }
func parseIdentity(i Identity, checkTime bool) (*x509.Certificate, error) {
	if !validID(i.Host) || !validName(i.Name) {
		return nil, fmt.Errorf("identity requires stable host ID and a nonreserved name")
	}
	host, port, err := net.SplitHostPort(i.Address)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || host == "" || strings.ContainsAny(i.Address, "/?#@\\") {
		return nil, fmt.Errorf("identity requires explicit host:port address")
	}
	block, rest := pem.Decode([]byte(i.Certificate))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("one PEM certificate required")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if cert.IsCA || cert.PublicKeyAlgorithm != x509.Ed25519 || len(cert.DNSNames) != 1 || cert.DNSNames[0] != SAN(i.Host) || cert.KeyUsage != x509.KeyUsageDigitalSignature || len(cert.ExtKeyUsage) != 2 {
		return nil, fmt.Errorf("unexpected node certificate constraints")
	}
	if err = certverify.SelfSignature(cert.Raw); err != nil {
		return nil, fmt.Errorf("node certificate is not self-signed: %w", err)
	}
	hasClient, hasServer := false, false
	for _, u := range cert.ExtKeyUsage {
		hasClient = hasClient || u == x509.ExtKeyUsageClientAuth
		hasServer = hasServer || u == x509.ExtKeyUsageServerAuth
	}
	if !hasClient || !hasServer {
		return nil, fmt.Errorf("node certificate requires client/server purposes")
	}
	if checkTime && (time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter)) {
		return nil, fmt.Errorf("node certificate is expired or not yet valid")
	}
	spki := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	if i.SPKI != hex.EncodeToString(spki[:]) {
		return nil, fmt.Errorf("SPKI fingerprint mismatch")
	}
	sum := sha256.Sum256(cert.Raw)
	if i.Fingerprint != hex.EncodeToString(sum[:]) || !i.Expires.Equal(cert.NotAfter) {
		return nil, fmt.Errorf("certificate fingerprint/expiry mismatch")
	}
	return cert, nil
}
func generate(host, name, address string) (Identity, []byte, error) {
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return Identity{}, nil, e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return Identity{}, nil, e
	}
	now := time.Now().UTC().Truncate(time.Second)
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "ARM node " + host}, DNSNames: []string{SAN(host)}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if e != nil {
		return Identity{}, nil, e
	}
	sum := sha256.Sum256(der)
	spkiDER, _ := x509.MarshalPKIXPublicKey(pub)
	spki := sha256.Sum256(spkiDER)
	owner := local.CurrentIdentity()
	i := Identity{Owner: owner.UID, OwnerUsername: owner.Username, Host: host, Name: name, Address: address, Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(sum[:]), SPKI: hex.EncodeToString(spki[:]), Expires: template.NotAfter}
	if _, e = ValidateIdentity(i); e != nil {
		return i, nil, e
	}
	raw, e := x509.MarshalPKCS8PrivateKey(key)
	return i, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), e
}
func loadCertificate(home string, identity Identity) (tls.Certificate, error) {
	key, e := privateFile(filepath.Join(home, "mesh", "node-key.pem"))
	if e != nil {
		return tls.Certificate{}, e
	}
	return tls.X509KeyPair([]byte(identity.Certificate), key)
}

func renewIdentity(identity Identity, certificate tls.Certificate) (Identity, error) {
	old, e := parseIdentity(identity, false)
	if e != nil {
		return identity, e
	}
	key, ok := certificate.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		return identity, fmt.Errorf("node key is not Ed25519")
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return identity, e
	}
	now := time.Now().UTC().Truncate(time.Second)
	template := &x509.Certificate{SerialNumber: serial, Subject: old.Subject, DNSNames: []string{SAN(identity.Host)}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if e != nil {
		return identity, e
	}
	sum := sha256.Sum256(der)
	identity.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	identity.Fingerprint = hex.EncodeToString(sum[:])
	identity.Expires = template.NotAfter
	_, e = ValidateIdentity(identity)
	return identity, e
}
