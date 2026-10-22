package mesh

import (
	"strings"
	"testing"
)

func TestSyntheticRouteAdapterRequiresUniqueVerifiedPrivateCandidates(t *testing.T) {
	route := SSHRoute{ClientIP: "::1", ServerIP: "::1", Synthetic: true}
	tests := []struct {
		name          string
		route         SSHRoute
		assigned, dns []string
		want          string
	}{
		{"normal-not-gateway", SSHRoute{ClientIP: "10.1.1.1", ServerIP: "10.1.1.2"}, []string{"10.1.1.2"}, nil, ""},
		{"unique-dns-match", route, []string{"10.1.1.2", "10.2.1.2"}, []string{"10.2.1.2"}, "10.2.1.2"},
		{"mapped-dns-private-ipv4", route, []string{"10.1.1.2", "fd00::2"}, []string{"10.9.9.9"}, "10.1.1.2"},
		{"unique-ipv6", route, []string{"fd00::2"}, nil, "fd00::2"},
		{"ambiguous-ipv4", route, []string{"10.1.1.2", "10.2.1.2"}, nil, ""},
		{"ambiguous-dns", route, []string{"10.1.1.2", "10.2.1.2"}, []string{"10.1.1.2", "10.2.1.2"}, ""},
		{"public-rejected", route, []string{"8.8.8.8"}, nil, ""},
		{"loopback-rejected", route, []string{"127.0.0.1"}, nil, ""},
		{"linklocal-rejected", route, []string{"fe80::1"}, nil, ""},
		{"scoped-rejected", route, []string{"fd00::1%en0"}, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SyntheticTarget(tt.route, tt.assigned, tt.dns)
			if tt.want == "" {
				if err == nil {
					t.Fatal("unsafe/ambiguous route accepted", got)
				}
			} else if err != nil || got != tt.want {
				t.Fatal(got, err)
			}
		})
	}
	if _, err := SyntheticTarget(route, strings.Fields(strings.Repeat("10.1.1.2 ", 65)), nil); err == nil {
		t.Fatal("unbounded report accepted")
	}
	if err := AutomaticIP("203.0.113.123", true); err == nil {
		t.Fatal("unassigned explicit interface accepted")
	}
	if _, err := RouteSource("127.0.0.1"); err == nil {
		t.Fatal("loopback source route accepted")
	}
	if parsed, err := ParseSSHRoute("::1 0 ::1 22"); err != nil || !parsed.Synthetic {
		t.Fatal(parsed, err)
	}
}
