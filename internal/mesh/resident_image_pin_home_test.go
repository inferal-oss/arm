package mesh

import (
	"context"
	"golang.org/x/sys/unix"
	"strings"
	"testing"
)

// Pre-effect DATA control, no actual home, descriptor, funding/issuer, source
// capture or native comparison is fabricated. Both mkdir paths refuse BEFORE
// dereferencing a parent or issuing any native operation until actual held-home
// validation has published its private marker. Equal DATA cannot publish it.
func TestP1PinHomeDataCannotAuthorizeNamespaceEffects(t *testing.T) {
	expected := ResidentImagePinHomeData{Device: 1, Inode: 2, UID: 3}
	p, err := NewResidentImagePinForHomeDataPending("/synthetic", strings.Repeat("a", 64), strings.Repeat("b", 64), 1, expected)
	if err != nil {
		t.Fatal(err)
	}
	expected.Inode++
	if p.expectedHome.Inode != 2 {
		t.Fatal("expected selectors alias caller")
	}
	if err = matchPinHomeData(*p.expectedHome, *p.expectedHome); err != nil {
		t.Fatal("valid DATA", err)
	}
	for _, current := range []ResidentImagePinHomeData{{Device: 2, Inode: 2, UID: 3}, {Device: 1, Inode: 3, UID: 3}, {Device: 1, Inode: 2, UID: 4}, {}} {
		if err = matchPinHomeData(*p.expectedHome, current); err != errResidentPinOriginal {
			t.Fatal("different home DATA accepted", err)
		}
	}
	for _, name := range []string{"resident-images", "pin-" + strings.Repeat("a", 64)} {
		if f, err := p.mkdir(context.Background(), nil, name); f != nil || err != errResidentPinOriginal {
			t.Fatal("DATA enabled namespace write", err)
		}
	}
	if f, err := p.open(context.Background(), -1, "arm", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL); f != nil || err != errResidentPinOriginal {
		t.Fatal("DATA enabled writable open", err)
	}
	if p.expectedHomeChecked || len(p.files) != 0 || p.source.InspectOriginal().RawDescriptorRetained {
		t.Fatal("DATA published native validation/effect")
	}
}
