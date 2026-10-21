package ports

import (
	"os"
	"testing"
)

func TestNativeInventoryFindsOwnBoundSockets(t *testing.T) {
	sockets := open(t, claim("tcp4", "127.0.0.1", 0), claim("udp4", "127.0.0.1", 0))
	r := Discover(t.Context())
	if r.Namespace == "" || r.Method == "" || r.Detail == "" || r.At.IsZero() {
		t.Fatalf("inventory metadata missing: %+v", r)
	}
	for _, bound := range sockets {
		found := false
		for _, s := range r.Sockets {
			if s.Network == bound.Binding.Network && s.Port == bound.Binding.Port && s.Address == bound.Binding.Address {
				for _, o := range s.Owners {
					if o.PID == os.Getpid() && o.UID != "" && o.StartIdentity != "" {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("own %s port %d not attributed; complete=%v detail=%s", bound.Binding.Network, bound.Binding.Port, r.Complete, r.Detail)
		}
	}
}
