package workspace

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func variantIdentity(t *testing.T, old Identity, key ed25519.PrivateKey, now time.Time, serial int64) Identity {
	t.Helper()
	tpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: old.Host}, DNSNames: []string{"node-" + old.Host + ".arm.invalid"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, tpl, tpl, key.Public(), key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	fp := sha256.Sum256(der)
	spki := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	old.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	old.Fingerprint = hex.EncodeToString(fp[:])
	old.SPKI = hex.EncodeToString(spki[:])
	old.Expires = parsed.NotAfter
	return old
}
func TestNewMemberAutoDiscoveryButCertificateRenewalQuarantinesOnlyTarget(t *testing.T) {
	d, b, root, r := activePair(t)
	c, _, admission := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, c, admission)
	q, e := r.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Accept(b.ctx, d.self, snapshot); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Check(b.ctx, root.ID, c.self.Host); e != nil {
		t.Fatal("fresh newly admitted node undiscoverable", e)
	}
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	newCertificate := variantIdentity(t, c.self, key, d.clock.Wall(), 2)
	// This client-side test represents an authority reporting an identity change
	// under a new directory revision. The current writer deliberately cannot
	// mutate accepted pins; a separately reviewed rotation producer is future.
	q, e = r.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	changed, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	changed.Revision++
	for i := range changed.Members {
		if changed.Members[i].Identity.Host == c.self.Host {
			changed.Members[i].Identity = newCertificate
		}
	}
	changed.Hash = snapshotHash(changed)
	if e = r.Accept(b.ctx, d.self, changed); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Check(b.ctx, root.ID, c.self.Host); code(e) != "identity_changed" {
		t.Fatal("changed node silently replaced pin", e)
	}
	if _, e = r.Check(b.ctx, root.ID, d.self.Host); e != nil {
		t.Fatal("one changed node corrupted entire workspace", e)
	}
	v, e := b.s.Read(b.ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, pin := range v.Trusted {
		if pin.Identity.Host == c.self.Host && (pin.Identity.Fingerprint != c.self.Fingerprint || pin.State != "identity_change_pending") {
			t.Fatal("old exact pin lost", pin)
		}
	}
}
func TestSameSPKICertificateIsStillExactPinChange(t *testing.T) {
	d, b, root, r := activePair(t)
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	old := variantIdentity(t, identityFixture(t, d.clock.Wall()), key, d.clock.Wall(), 1)
	renewed := variantIdentity(t, old, key, d.clock.Wall(), 2)
	if old.SPKI != renewed.SPKI || old.Fingerprint == renewed.Fingerprint {
		t.Fatal("fixture did not renew same key")
	}
	q, e := r.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	snap.Revision++
	snap.Members = append(snap.Members, MemberCard{old, 1, "active", false, true, 1})
	snap.Hash = snapshotHash(snap)
	if e = r.Accept(b.ctx, d.self, snap); e != nil {
		t.Fatal(e)
	}
	q, e = r.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap.Nonce = q.Nonce
	snap.Revision++
	for i := range snap.Members {
		if snap.Members[i].Identity.Host == old.Host {
			snap.Members[i].Identity = renewed
		}
	}
	snap.Hash = snapshotHash(snap)
	if e = r.Accept(b.ctx, d.self, snap); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Check(b.ctx, root.ID, old.Host); code(e) != "identity_changed" {
		t.Fatal("same SPKI bypassed exact DER pin", e)
	}
}
func TestSameRevisionCannotChangeMembersOrRefreshWithReplayedNonce(t *testing.T) {
	d, m, root, r := activePair(t)
	q, e := r.Begin(m.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	for i := range snap.Members {
		if snap.Members[i].Identity.Host == d.self.Host {
			snap.Members[i].HostInbox = !snap.Members[i].HostInbox
		}
	}
	snap.Hash = snapshotHash(snap)
	if e = r.Accept(m.ctx, d.self, snap); code(e) != "invalid_snapshot" {
		t.Fatal("same revision admitted mutable directory contents", e)
	}
	if _, e = r.Check(m.ctx, root.ID, d.self.Host); code(e) != "membership_unknown" {
		t.Fatal("invalid authenticated response silently kept prior authorization", e)
	}
}
