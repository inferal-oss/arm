package kubesource

import (
	"getarm.org/internal/model"
	"strings"
	"testing"
)

func TestClosedScopeAndCumulativeBudgets(t *testing.T) {
	for _, stage := range []byte{Ship, Inspect} {
		for _, b := range []int64{1, MaxPayload} {
			s := Scope{strings.Repeat("a", 64), strings.Repeat("b", 32), stage, b}
			for _, parent := range []bool{false, true} {
				got, p, err := Parse(s.Key(parent))
				if err != nil || got != s || p != parent || len(s.Key(parent)) > 128 {
					t.Fatal(got, p, err)
				}
			}
			if stage == Inspect && (s.ContentBytes() != 262144 || s.DiskBytes() != 327680 || len(s.Purposes()) != 4) {
				t.Fatal(s)
			}
		}
	}
	for _, key := range []string{"ks1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 32) + ":i:01", "ks1:" + strings.Repeat("A", 64) + ":" + strings.Repeat("b", 32) + ":i:1", "kd1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 32) + ":i:268435457"} {
		if _, _, err := Parse(key); err == nil {
			t.Fatal(key)
		}
	}
}
func TestPairRejectsSwapsAndScopeDrift(t *testing.T) {
	s := Scope{strings.Repeat("a", 64), strings.Repeat("b", 32), Inspect, 1}
	source := model.Lease{ID: strings.Repeat("1", 32), Host: strings.Repeat("c", 32), Owner: "501", Request: s.Request(strings.Repeat("c", 32), "", false)}
	source.Request.Kind = "relay-transit"
	parent := model.Lease{ID: strings.Repeat("2", 32), Host: source.Host, Owner: source.Owner, Request: s.Request(source.Host, "", true), ManagedDir: "owned", Artifact: &model.Artifact{Name: strings.Repeat("2", 32), Identity: &model.FileIdentity{Owner: 501, BirthKnown: true, Inode: 1}}}
	source.Request.StorageLease = parent.ID
	if err := ValidatePair(source, parent); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*model.Lease){func(l *model.Lease) { l.Owner = "other" }, func(l *model.Lease) { l.Request.Resources.Disk = 262144 }, func(l *model.Lease) { l.Request.TTL++ }, func(l *model.Lease) { l.Request.Key = strings.Replace(l.Request.Key, ":i:", ":s:", 1) }} {
		p := parent
		mutate(&p)
		if ValidatePair(source, p) == nil {
			t.Fatal("mutated pair accepted")
		}
	}
}
