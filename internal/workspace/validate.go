package workspace

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
)

func problem(code, text string) error { return model.Err(code, text) }
func validHex(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n && strings.ToLower(s) == s
}
func text(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func validKey(s string) bool { return text(s, 128) }
func reason(s string) bool   { return text(s, 4096) }
func validAuthority(a Authority) bool {
	n, e := strconv.ParseUint(a.UID, 10, 32)
	return validHex(a.Host, 16) && e == nil && strconv.FormatUint(n, 10) == a.UID
}
func hash(v any) string {
	raw, _ := json.Marshal(v)
	s := sha256.Sum256(raw)
	return hex.EncodeToString(s[:])
}
func next(n int64) (int64, error) {
	if n < 0 || n == math.MaxInt64 {
		return 0, problem("revision_exhausted", "workspace counter exhausted")
	}
	return n + 1, nil
}
func known(s string, list ...string) bool {
	for _, v := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ValidateIdentity is structural and cryptographic, intentionally time-neutral:
// expired historical members/tombstones remain valid inspectable evidence.
func ValidateIdentity(i Identity) (*x509.Certificate, error) {
	if !validHex(i.Host, 16) || !text(i.Name, 128) || !validHex(i.Fingerprint, 32) || !validHex(i.SPKI, 32) || len(i.Certificate) > 8192 {
		return nil, fmt.Errorf("invalid public workspace identity")
	}
	b, rest := pem.Decode([]byte(i.Certificate))
	if b == nil || b.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("one public workspace certificate required")
	}
	c, e := x509.ParseCertificate(b.Bytes)
	if e != nil {
		return nil, e
	}
	if c.IsCA || c.PublicKeyAlgorithm != x509.Ed25519 || c.KeyUsage != x509.KeyUsageDigitalSignature || len(c.DNSNames) != 1 || c.DNSNames[0] != "node-"+i.Host+".arm.invalid" || len(c.ExtKeyUsage) != 2 || !c.NotBefore.Before(c.NotAfter) || !i.Expires.Equal(c.NotAfter) || !bytes.Equal(c.RawIssuer, c.RawSubject) {
		return nil, fmt.Errorf("workspace certificate constraints mismatch")
	}
	if !(c.ExtKeyUsage[0] == x509.ExtKeyUsageClientAuth && c.ExtKeyUsage[1] == x509.ExtKeyUsageServerAuth || c.ExtKeyUsage[1] == x509.ExtKeyUsageClientAuth && c.ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth) {
		return nil, fmt.Errorf("workspace certificate requires both EKUs")
	}
	if e = c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature); e != nil {
		return nil, e
	}
	digest := sha256.Sum256(c.Raw)
	spki := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	if i.Fingerprint != hex.EncodeToString(digest[:]) || i.SPKI != hex.EncodeToString(spki[:]) {
		return nil, fmt.Errorf("workspace certificate pin mismatch")
	}
	return c, nil
}
func identityCurrent(i Identity, now time.Time) error {
	c, e := ValidateIdentity(i)
	if e != nil {
		return e
	}
	if now.Before(c.NotBefore) || !now.Before(c.NotAfter) {
		return problem("identity_expired", "workspace identity is expired or not yet valid")
	}
	return nil
}
func ValidatePolicy(p Policy) error {
	if p.MaxMembers < 1 || p.MaxMembers > MaxMembers || p.MaxPublishedAgents < 0 || p.MaxPublishedAgents > 32 || p.MembershipTTL <= 0 || p.MembershipTTL > time.Minute || p.StreamTTL <= 0 || p.StreamTTL > 30*time.Second || p.StreamTTL > p.MembershipTTL || p.RefreshInterval <= 0 || p.RefreshInterval > 15*time.Second || p.RefreshInterval > p.MembershipTTL || p.ClockSkew <= 0 || p.ClockSkew > MaxClockSkew || p.AdmissionMode != "delegated_introducers" {
		return fmt.Errorf("invalid bounded complete workspace policy")
	}
	return nil
}
func validateInstance(v Instance, a Authority) error {
	if v.Schema != Schema || v.Authority != a || !validAuthority(a) || !validHex(v.ID, 16) || !validHex(v.Generation, 16) || !text(v.Name, 128) || !known(v.Role, "directory", "member") || !known(v.State, "prepared", "joining", "active", "retired", "needs_attention") || v.Revision < 1 || v.PolicyRevision < 1 || v.SelfGeneration < 0 || v.Created.IsZero() || v.Updated.Before(v.Created) {
		return fmt.Errorf("invalid workspace instance")
	}
	if e := ValidatePolicy(v.Policy); e != nil {
		return e
	}
	if _, e := ValidateIdentity(v.Directory); e != nil {
		return e
	}
	if _, e := ValidateIdentity(v.Self); e != nil {
		return e
	}
	if v.Self.Host != a.Host || v.Role == "directory" && (v.Directory != v.Self || v.SelfGeneration != 1) && v.State != "prepared" {
		return fmt.Errorf("workspace authority/directory identity mismatch")
	}
	if v.State != "prepared" && !validHex(v.ConsentHash, 32) {
		return fmt.Errorf("workspace consent missing")
	}
	return nil
}
func validPrincipal(p Principal) bool {
	return validHex(p.Workspace, 16) && validHex(p.Generation, 16) && validHex(p.Host, 16) && p.MembershipGeneration > 0 && validHex(p.Fingerprint, 32)
}
func principalKey(p Principal) string {
	return "member:" + p.Workspace + ":" + p.Generation + ":" + p.Host + ":" + strconv.FormatInt(p.MembershipGeneration, 10) + ":" + p.Fingerprint
}
func nativeKey(a Authority) string { return "native:" + a.Host + ":" + a.UID }
func validateMember(v Member) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !validHex(v.Generation, 16) || v.MembershipGeneration < 1 || !known(v.State, "pending", "active", "revoked") || !validPrincipal(v.IntroducedBy) || v.IntroducedBy.Workspace != v.Workspace || v.IntroducedBy.Generation != v.Generation || !validKey(v.AdmissionKey) || !validHex(v.ConsentHash, 32) || v.Revision < 1 || v.Created.IsZero() || v.Updated.Before(v.Created) || !reason(v.Reason) {
		return fmt.Errorf("invalid workspace member")
	}
	if _, e := ValidateIdentity(v.Identity); e != nil {
		return e
	}
	if (v.State == "revoked") != (v.RevokedAt != nil) || v.RevokedAt != nil && (v.RevokedAt.IsZero() || v.RevokedAt.Before(v.Created)) {
		return fmt.Errorf("invalid member revocation evidence")
	}
	return nil
}
func validateTrusted(v TrustedMember) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !validHex(v.Generation, 16) || v.MembershipGeneration < 1 || !known(v.State, "active", "revoked", "identity_change_pending") || v.FirstSeen < 1 || v.LatestSeen < v.FirstSeen {
		return fmt.Errorf("invalid trusted-member continuity")
	}
	_, e := ValidateIdentity(v.Identity)
	return e
}
func validateClock(v ClockRecord) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !validHex(v.Generation, 16) || v.WallFloor.IsZero() || v.IssuedCount < 0 || v.LastRevision < 1 || v.LastIssued != nil && (v.LastIssued.IsZero() || v.LastIssued.After(v.WallFloor)) || v.IssuedCount == 0 && v.LastIssued != nil || v.IssuedCount > 0 && v.LastIssued == nil {
		return fmt.Errorf("invalid workspace issuance floor")
	}
	return nil
}
func validateDeny(v Deny, a Authority) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !validHex(v.Host, 16) || v.Revision < 1 || !validKey(v.Key) || v.Actor != a || !reason(v.Reason) || v.Created.IsZero() {
		return fmt.Errorf("invalid local workspace deny")
	}
	return nil
}
func validateReview(v Review) error {
	if v.Schema != Schema || !text(v.Name, 128) || !validHex(v.RequestHash, 32) || !known(v.Kind, "create", "join", "resume_admission", "resume_join") || !validHex(v.Workspace, 16) || !validHex(v.Generation, 16) || v.PolicyRevision < 1 || !reason(v.Reason) || v.Expires.IsZero() {
		return fmt.Errorf("invalid workspace review")
	}
	if e := ValidatePolicy(v.Policy); e != nil {
		return e
	}
	if _, e := ValidateIdentity(v.Source); e != nil {
		return e
	}
	if _, e := ValidateIdentity(v.Directory); e != nil {
		return e
	}
	return nil
}
func validateOperation(v Operation) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !text(v.Principal, 512) || !validKey(v.Key) || !known(v.Kind, "create", "join", "admit", "deny", "revoke", "retire", "resume_admission", "resume_join") || !validHex(v.RequestHash, 32) || v.Revision < 1 || !known(v.Phase, "awaiting_review", "approved", "proving", "completed", "declined", "cancelled", "interrupted", "unknown") || v.Created.IsZero() || v.Updated.Before(v.Created) || len(v.Error) > 512 {
		return fmt.Errorf("invalid workspace operation")
	}
	var request any
	switch v.Kind {
	case "create":
		request = &CreateRequest{}
	case "join":
		request = &JoinRequest{}
	case "admit":
		request = &AdmissionRequest{}
	case "resume_admission", "resume_join":
		request = &RecoveryRequest{}
	default:
		request = &MutationRequest{}
	}
	if e := Decode(v.Request, request); e != nil {
		return e
	}
	if e := validateRequest(v, request); e != nil {
		return e
	}
	if hash(request) != v.RequestHash {
		return fmt.Errorf("workspace request fingerprint mismatch")
	}
	if v.Review != nil {
		if e := validateReview(*v.Review); e != nil {
			return e
		}
		if v.Review.RequestHash != v.RequestHash || v.Review.Kind != v.Kind || hash(v.Review) != v.ReviewHash || !v.Review.Expires.Equal(v.ReviewExpires) || v.Review.Workspace != v.Workspace {
			return fmt.Errorf("workspace review fingerprint mismatch")
		}
	}
	if v.Private == nil || !validHex(v.Private.Boot, 16) || v.Private.ProofCount < 0 || v.Private.ProofCount > 4 || v.Private.Nonce != "" && !validHex(v.Private.Nonce, 32) || v.Private.Introducer != nil && !validPrincipal(*v.Private.Introducer) {
		return fmt.Errorf("workspace private operation binding missing")
	}
	if v.Private.ProofCount == 0 && v.Private.LastChallenge != "" || v.Private.ProofCount > 0 && !validHex(v.Private.LastChallenge, 32) {
		return fmt.Errorf("candidate proof count/hash evidence differs")
	}
	if v.Result != nil {
		return validateMember(*v.Result)
	}
	return nil
}
func terminal(phase string) bool { return known(phase, "completed", "declined", "cancelled") }

func validateCard(c MemberCard) error {
	if c.MembershipGeneration < 1 || !known(c.State, "pending", "active", "revoked") || c.Revision < 1 {
		return fmt.Errorf("invalid public membership card")
	}
	_, e := ValidateIdentity(c.Identity)
	return e
}

func validateRequest(o Operation, value any) error {
	valid := false
	switch q := value.(type) {
	case *CreateRequest:
		valid = q.Key == o.Key && text(q.Name, 128) && reason(q.Reason) && ValidatePolicy(q.Policy) == nil && o.Review != nil && o.Review.Policy == q.Policy && o.Review.Name == q.Name && o.Review.Reason == q.Reason
	case *JoinRequest:
		valid = q.Key == o.Key && q.Workspace == o.Workspace && validHex(q.Generation, 16) && text(q.Name, 128) && reason(q.Reason) && validHex(q.Introducer, 16) && q.PolicyRevision > 0 && ValidatePolicy(q.Policy) == nil && o.Review != nil && o.Review.Policy == q.Policy && o.Review.Name == q.Name && o.Review.Reason == q.Reason && o.Review.Directory == q.Directory && o.Review.Generation == q.Generation && o.Review.Introducer == q.RequestIntroducer
	case *AdmissionRequest:
		_, e := ValidateIdentity(q.Candidate)
		valid = q.Key == o.Key && e == nil && validHex(q.ConsentHash, 32) && q.PolicyRevision > 0 && reason(q.Reason) && o.Private != nil && o.Private.Introducer != nil && o.Principal == principalKey(*o.Private.Introducer) && o.Private.IntroducerRevision > 0 && o.Private.CandidateRevision > 0 && validHex(o.Private.Nonce, 32) && o.Review == nil && !o.ReviewExpires.IsZero()
	case *RecoveryRequest:
		valid = validRecoveryRequest(*q) && q.Key == o.Key && q.Workspace == o.Workspace && o.Review != nil && o.Review.Reason == q.Reason
	case *MutationRequest:
		valid = q.Key == o.Key && q.Revision > 0 && reason(q.Reason) && (o.Kind == "retire" && q.Host == "" || o.Kind != "retire" && validHex(q.Host, 16)) && o.Review == nil
	}
	if !valid {
		return fmt.Errorf("workspace operation/request binding mismatch")
	}
	return nil
}
