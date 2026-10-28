package kubesource

import (
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func TestNewProfilesHaveExactBudgetsAndPortRole(t *testing.T) {
	for _, tc := range []struct {
		stage         byte
		content, disk int64
		files         int
	}{{Start, 196608, 262144, 3}, {Bootstrap, 65536, 131072, 1}, {Forward, 65536, 131072, 1}} {
		s := Scope{Operation: strings.Repeat("a", 64), Attempt: strings.Repeat("b", 32), Stage: tc.stage, B: MaxPayload}
		if err := s.Validate(); err != nil || s.ContentBytes() != tc.content || s.DiskBytes() != tc.disk || len(s.Purposes()) != tc.files {
			t.Fatal(s, err)
		}
		if _, err := s.Limit("payload"); err == nil {
			t.Fatal("new non-payload profile minted payload")
		}
		for _, parent := range []bool{false, true} {
			if got, p, err := Parse(s.Key(parent)); err != nil || got != s || p != parent {
				t.Fatal(got, p, err)
			}
		}
		_, role, err := ParseRole(s.PortKey())
		if tc.stage == Forward {
			if err != nil || role != PortRole {
				t.Fatal(role, err)
			}
		} else if err == nil {
			t.Fatal("non-forward profile accepted a port parent")
		}
	}
	f := Scope{Operation: strings.Repeat("a", 64), Attempt: strings.Repeat("b", 32), Stage: Forward, B: 1}
	host := strings.Repeat("c", 32)
	source := model.Lease{ID: strings.Repeat("1", 32), Host: host, Owner: "501", Request: f.Request(host, "", false)}
	source.Request.Kind = "relay-transit"
	source.Request.StorageLease = strings.Repeat("4", 32)
	parent := model.Lease{ID: strings.Repeat("2", 32), Host: host, Owner: "501", Request: f.PortRequest(host, "", "host")}
	source.Request.PortLease = parent.ID
	binding := parent.Request.Ports[0]
	binding.Port = 32123
	parent.Ports = []model.PortBinding{{PortClaim: binding, State: "held"}}
	if err := ValidatePortPair(source, parent); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*model.Lease){func(p *model.Lease) { p.Request.Ports[0].Address = "0.0.0.0" }, func(p *model.Lease) { p.Request.Ports[0].Port = 32123 }, func(p *model.Lease) { p.Ports[0].Namespace = "other" }, func(p *model.Lease) { p.Request.Ports[0].Namespace = "net:[1]" }} {
		p := parent
		p.Request.Ports = append([]model.PortClaim{}, parent.Request.Ports...)
		p.Ports = append([]model.PortBinding{}, parent.Ports...)
		mutate(&p)
		if ValidatePortPair(source, p) == nil {
			t.Fatal("noncanonical port scope accepted")
		}
	}
	if !IsReserved(model.Request{Project: "other", Key: f.PortKey()}) {
		t.Fatal("kp1 not reserved independently of project")
	}
}

// A role's exact request cannot make named-root custody equivalent to default.
// Cover even a zero-value pointer: presence itself changes the storage binding.
func TestSourceRolesRequireDefaultStorageBinding(t *testing.T) {
	s := Scope{Operation: strings.Repeat("a", 64), Attempt: strings.Repeat("b", 32), Stage: Forward, B: 1}
	host := strings.Repeat("c", 32)
	for _, role := range []Role{SourceRole, StorageRole, PortRole} {
		name := "source"
		request := s.Request(host, "", false)
		request.Kind = "relay-transit"
		if role == StorageRole {
			name = "storage"
			request = s.Request(host, "", true)
		}
		if role == PortRole {
			name = "port"
			request = s.PortRequest(host, "", "host")
		}
		t.Run(name, func(t *testing.T) {
			l := model.Lease{ID: strings.Repeat("1", 32), Host: host, Owner: "501", Request: request}
			if got, gotRole, err := ValidateRoleKind(l); err != nil || got != s || gotRole != role {
				t.Fatal("nil default-root binding refused", got, gotRole, err)
			}
			for _, binding := range []*model.StoragePoolBinding{{}, {Name: "named-root", Volume: "fixture-volume", Backing: "fixture-backing", SharesDefault: true}} {
				l.StorageBinding = binding
				if _, _, err := ValidateRoleKind(l); err == nil {
					t.Fatal("non-default role binding accepted", role, binding)
				}
			}
		})
	}
}
