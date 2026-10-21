package ports

import (
	"strings"
	"testing"
)

func TestProcSocketAddressesAndMalformedRows(t *testing.T) {
	for encoded, want := range map[string]string{"0100007F:1F90": "127.0.0.1", "00000000000000000000000001000000:1F90": "::1"} {
		a, p, err := procAddress(encoded)
		if err != nil || a != want || p != 8080 {
			t.Fatalf("decode %s: %s %d %v", encoded, a, p, err)
		}
	}
	r, err := parseProc(strings.NewReader("header\n0: 0100007F:1F90 00000000:0000 0A 0:0 0:0 0 501 0 12345\n1: 0100007F:1F91 00000000:0000 01 0:0 0:0 0 501 0 12346\n"), "tcp4")
	if err != nil || len(r) != 1 || r[0].ID != "12345" || r[0].UID != "501" {
		t.Fatalf("socket table: %+v %v", r, err)
	}
	if _, err := parseProc(strings.NewReader("header\nincomplete\n"), "tcp4"); err == nil {
		t.Fatal("partial socket table marked complete")
	}
	if _, _, err := procAddress("nothex:1F90"); err == nil {
		t.Fatal("invalid address accepted")
	}
	if _, err := procStart([]byte("123 (tricky ) name) S 1")); err == nil {
		t.Fatal("incomplete process start accepted")
	}
}
