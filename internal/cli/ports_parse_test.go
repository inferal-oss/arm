package cli

import "testing"

func TestPortFlagParsesIPv6AndSelectionRange(t *testing.T) {
	var p portFlags
	if err := p.Set("web=tcp6://[::1]:8000-8003"); err != nil || len(p) != 1 || p[0].Name != "web" || p[0].Address != "::1" || p[0].Port != 8000 || p[0].RangeEnd != 8003 {
		t.Fatalf("port flag: %+v %v", p, err)
	}
	for _, bad := range []string{"8080", "web=tcp4://127.0.0.1:bad", "web=tcp4://127.0.0.1:8000-0", "tcp6://::1:8000"} {
		if err := p.Set(bad); err == nil {
			t.Fatalf("accepted malformed port %q", bad)
		}
	}
}
