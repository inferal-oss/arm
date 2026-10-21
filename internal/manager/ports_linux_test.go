package manager

import (
	"net"
	"strconv"
	"testing"
)

func TestPortInventorySeparatesOwnershipFromLedgerOverlap(t *testing.T) {
	m := newManager(t)
	held, err := m.Acquire(portRequest("held-inode"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := portRequest("cooperative-inode")
	r.Ports[0].Mode = "cooperative"
	r.Ports[0].Network = "udp4"
	cooperative, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	external, err := net.ListenPacket("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(cooperative.Ports[0].Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	inventory, err := m.DiscoverPorts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	heldFound, cooperativeFound := false, false
	for _, s := range inventory.Sockets {
		if s.Network == "tcp4" && s.Port == held.Ports[0].Port {
			for _, id := range s.Claims {
				heldFound = heldFound || id == held.ID
			}
		}
		if s.Network == "udp4" && s.Port == cooperative.Ports[0].Port {
			if len(s.Claims) != 0 {
				t.Fatal("cooperative ledger asserted ownership of an external socket")
			}
			for _, id := range s.OverlappingLeases {
				cooperativeFound = cooperativeFound || id == cooperative.ID
			}
		}
	}
	if !heldFound || !cooperativeFound {
		t.Fatalf("missing distinct inode/ledger associations: %+v", inventory)
	}
}
