package workspace

import (
	"strings"
	"testing"
	"time"
)

func TestWarmSignatureStillChecksIdentityPinsTimeAndDetachedCertificates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	i := identityFixture(t, now)
	first, err := ValidateIdentity(i)
	if err != nil {
		t.Fatal(err)
	}
	// A caller may mutate its parsed result; no parsed object or backing DER
	// can become shared authority for a later call with the same public PEM.
	first.DNSNames[0] = "changed.invalid"
	first.Raw[0] ^= 1
	if _, err = ValidateIdentity(i); err != nil {
		t.Fatal("shared parsed object or DER", err)
	}
	for name, mutate := range map[string]func(*Identity){
		"host":        func(v *Identity) { v.Host = strings.Repeat("0", 32) },
		"fingerprint": func(v *Identity) { v.Fingerprint = strings.Repeat("0", 64) },
		"spki":        func(v *Identity) { v.SPKI = strings.Repeat("0", 64) },
		"expiry":      func(v *Identity) { v.Expires = v.Expires.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := i
			mutate(&changed)
			if _, err := ValidateIdentity(changed); err == nil {
				t.Fatal("warm signature bypassed identity binding")
			}
		})
	}
	if err = identityCurrent(i, now); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now.Add(-2 * time.Hour), i.Expires, i.Expires.Add(time.Hour)} {
		if err = identityCurrent(i, at); err == nil {
			t.Fatal("warm signature bypassed current validity", at)
		}
	}
	// Historical identity inspection is intentionally time-neutral.
	historical := identityFixture(t, now.Add(-48*time.Hour))
	if _, err = ValidateIdentity(historical); err != nil {
		t.Fatal(err)
	}
	if err = identityCurrent(historical, now); err == nil {
		t.Fatal("historical signature became current authority")
	}
}
