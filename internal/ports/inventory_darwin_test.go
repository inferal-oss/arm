package ports

import "testing"

func TestLsofFieldsAndMalformedInput(t *testing.T) {
	r, err := parseLsof("p999999\x00u501\x00\nf3\x00tIPv6\x00PTCP\x00i123\x00n*:8080\x00\nf4\x00tIPv4\x00PUDP\x00n127.0.0.1:9000->127.0.0.1:9001\x00\n")
	if err != nil || len(r) != 2 || r[0].Address != "::" || r[0].Port != 8080 || r[1].Port != 9000 || r[0].Owners[0].PID != 999999 {
		t.Fatalf("lsof fixture: %+v %v", r, err)
	}
	if _, err := parseLsof("pbad\x00"); err == nil {
		t.Fatal("invalid process ID accepted")
	}
	if _, err := parseLsof("p123\x00n*:8080\x00"); err == nil {
		t.Fatal("incomplete socket identity accepted")
	}
	if r, err := parseLsof("p123\x00u501\x00f3\x00tIPv4\x00PUDP\x00n*:*\x00"); err != nil || len(r) != 0 {
		t.Fatal("unbound UDP socket treated as listener", err)
	}
}
