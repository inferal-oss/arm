package meshstream

import (
	"crypto/ed25519"
	"encoding/json"
	"golang.org/x/crypto/ssh"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func controlFixture() Control {
	key, _ := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, 32)))
	return Control{1, strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 64), 1, "armuser", "op", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), "test stream", time.Minute, model.Resources{CPU: 1, Memory: math.MaxInt64}, "/private/work"}
}
func TestControlExactIntegersAndClosedSchema(t *testing.T) {
	q := controlFixture()
	raw, _ := json.Marshal(q)
	got, err := decodeControl(raw)
	if err != nil || got.Resources.Memory != math.MaxInt64 {
		t.Fatal("int64 lost", got, err)
	}
	for _, raw := range []string{strings.Replace(string(raw), `"version":1`, `"version":1,"Version":1`, 1), strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1), strings.Replace(string(raw), `"cpu":1`, `"cpu":1,"CPU":1`, 1), strings.Replace(string(raw), `"cpu":1`, `"cpu":null`, 1), strings.Replace(string(raw), `"cpu":1`, `"cpu":1,"cpu":2`, 1), strings.Replace(string(raw), `"memory_bytes":9223372036854775807`, `"memory_bytes":9223372036854775808`, 1), string(raw) + ` {}`} {
		if _, err := decodeControl([]byte(raw)); err == nil {
			t.Fatal("ambiguous control accepted", raw)
		}
	}
}
func TestControlAuthorityAndGrantBound(t *testing.T) {
	q := controlFixture()
	s := Scope{q.Source, q.Target, q.Pair, q.Account, "cert", q.Revision, q.TTL, q.Resources, time.Now().Add(time.Hour)}
	if err := q.Authorize(s); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Scope){func(s *Scope) { s.Source = q.Target }, func(s *Scope) { s.Pair = strings.Repeat("d", 64) }, func(s *Scope) { s.Revision++ }, func(s *Scope) { s.Account = "other" }, func(s *Scope) { s.MaxTTL /= 2 }, func(s *Scope) { s.Limits.Memory-- }, func(s *Scope) { s.Certificate = "" }, func(s *Scope) { s.ValidUntil = time.Now().Add(-time.Second) }} {
		copy := s
		change(&copy)
		if q.Authorize(copy) == nil {
			t.Fatal("authority mismatch accepted", copy)
		}
	}
}
