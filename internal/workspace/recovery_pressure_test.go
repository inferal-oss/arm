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
	"strings"
	"testing"
	"time"

	"getarm.org/internal/local"
)

func TestRecoveryActualWriterUnrelatedPressurePreservesFirstRecovery(t *testing.T) {
	t.Run("audit", func(t *testing.T) { actualWriterRecoveryPressure(t, false) })
	t.Run("bytes", func(t *testing.T) { actualWriterRecoveryPressure(t, true) })
}
func actualWriterRecoveryPressure(t *testing.T, wide bool) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, join, admit := joinedCandidate(t, d, root)
	_, started, e := d.s.BeginProof(d.ctx, rootPrincipal(root), admit.Key)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.s.RecordProofFailure(d.ctx, rootPrincipal(root), admit.Key, started.Revision); e != nil {
		t.Fatal(e)
	}
	// Populate with real signed candidate identities and actual durable pending
	// admission producers. No made-up SQL operation placeholders are involved.
	for i := 0; i < 128; i++ {
		before, e := d.s.Read(d.ctx)
		if e != nil {
			t.Fatal(e)
		}
		candidate := identityFixture(t, d.clock.Wall())
		if wide {
			candidate = wideRecoveryIdentity(t, d.clock.Wall())
		}
		_, e = d.s.PrepareAdmission(d.ctx, rootPrincipal(root), AdmissionRequest{Key: "pressure-admit-" + candidate.Host, Candidate: candidate, ConsentHash: local.ID() + local.ID(), HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: strings.Repeat("p", 4096)})
		if code(e) == "history_full" {
			after, read := d.s.Read(d.ctx)
			if read != nil {
				t.Fatal(read)
			}
			if completeBytes(after) != completeBytes(before) || len(after.Operations) != len(before.Operations) || len(after.Members) != len(before.Members) || after.Instances[0].Revision != before.Instances[0].Revision {
				t.Fatal("refused admission leaked rows/revision")
			}
			break
		}
		if e != nil {
			t.Fatal("pressure was not a reserved audit/byte boundary", e)
		}
		if i == 127 {
			t.Fatal("fixture did not reach bounded audit pressure")
		}
	}
	// Spend only unreserved capacity with actual operator deny records. Use a
	// large then a minimal reason so the byte edge is below a recovery envelope.
	for _, reason := range []string{strings.Repeat("d", 4096), "d"} {
		for i := 0; i < 128; i++ {
			before, e := d.s.Read(d.ctx)
			if e != nil {
				t.Fatal(e)
			}
			q := MutationRequest{Key: "pressure-deny-" + local.ID(), Host: local.ID(), Revision: before.Instances[0].Revision, Reason: reason}
			_, e = d.s.Deny(d.ctx, root.ID, q)
			if code(e) == "history_full" {
				after, read := d.s.Read(d.ctx)
				if read != nil {
					t.Fatal(read)
				}
				if completeBytes(after) != completeBytes(before) || len(after.Operations) != len(before.Operations) || len(after.Denies) != len(before.Denies) || after.Instances[0].Revision != before.Instances[0].Revision {
					t.Fatal("refused unrelated deny leaked rows/revision")
				}
				break
			}
			if e != nil {
				t.Fatal("unexpected pressure result", e)
			}
			if i == 127 {
				t.Fatal("fixture did not exhaust unreserved capacity")
			}
		}
	}
	d.s, e = Open(d.ctx, d.db, d.authority, d.self, d.clock)
	if e != nil {
		t.Fatal(e)
	}
	m.s, e = Open(m.ctx, m.db, m.authority, m.self, m.clock)
	if e != nil {
		t.Fatal(e)
	}
	jq := joinRecovery(t, m, root, join, "pressure-join-recovery")
	jchild, e := m.s.PrepareRecovery(m.ctx, "resume_join", jq)
	if e != nil {
		t.Fatal(e)
	}
	jchild, e = m.s.DecideRecovery(m.ctx, root.ID, jchild.Key, jchild.Revision, jchild.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	parent, _ := operationOf(&v, root.ID, rootPrincipalKey(root), admit.Key)
	aq := RecoveryRequest{Key: "pressure-admission-recovery", Workspace: root.ID, Generation: root.Generation, Original: OperationRef{parent.Principal, parent.Key, parent.RequestHash, parent.Revision}, Attempt: 1, Consent: join.ReviewHash, DirectoryPin: d.self.Fingerprint, CandidatePin: m.self.Fingerprint, PolicyRevision: root.PolicyRevision, HostInbox: true, CandidateAuthorization: jchild.ReviewHash, PermitProof: true, Reason: "first reserved recovery after unrelated producers filled capacity"}
	child, e := d.s.PrepareRecovery(d.ctx, "resume_admission", aq)
	if e != nil {
		t.Fatalf("promised first recovery could not prepare after unrelated pressure: %v (actual ledger bytes %d)", e, completeBytes(v))
	}
	child, e = d.s.DecideRecovery(d.ctx, root.ID, child.Key, child.Revision, child.ReviewHash, true)
	if e != nil {
		t.Fatalf("reserved recovery decision: %v", e)
	}
	challenge, child, e := d.s.BeginRecoveryProof(d.ctx, root.ID, child.Key)
	if e != nil {
		t.Fatalf("reserved proof dispatch: %v", e)
	}
	proof, e := m.s.ProveJoin(m.ctx, d.self, challenge)
	if e != nil {
		t.Fatal(e)
	}
	done, e := d.s.CompleteRecoveryProof(d.ctx, root.ID, child.Key, child.Revision, proof)
	if e != nil || done.Phase != "completed" {
		t.Fatal("reserved family completion", done, e)
	}
	// Remaining unrelated pending work is explicitly cancelled before local
	// retirement; completing our family does not falsely settle those families.
	v, e = d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, op := range v.Operations {
		if op.Kind == "admit" && !terminal(op.Phase) {
			if _, e = d.s.CancelAdmission(d.ctx, root.ID, root.Self.Host, op.Key, op.Revision); e != nil {
				t.Fatal("reserved unrelated cancellation", e)
			}
		}
	}
	v, e = d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.s.Retire(d.ctx, root.ID, MutationRequest{Key: "reserved-local-retirement", Revision: v.Instances[0].Revision, Reason: "explicit local retirement after all pending family settlements"}); e != nil {
		t.Fatal("reserved retirement receipt", e)
	}
}

func wideRecoveryIdentity(t *testing.T, now time.Time) Identity {
	t.Helper()
	host := local.ID()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host, Organization: []string{strings.Repeat("Z", 2048)}}, DNSNames: []string{"node-" + host + ".arm.invalid"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	leaf, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	fp, spki := sha256.Sum256(der), sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	identity := Identity{Host: host, Name: "bounded-wide-certificate", Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(fp[:]), SPKI: hex.EncodeToString(spki[:]), Expires: leaf.NotAfter}
	if _, e = ValidateIdentity(identity); e != nil {
		t.Fatal("wide certificate must be a valid public identity", len(identity.Certificate), e)
	}
	return identity
}
