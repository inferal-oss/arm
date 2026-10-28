package nodeavailability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProfileStrictRequestAndCanonicalAuthority(t *testing.T) {
	valid := `{"key":"profile-change","expected_revision":0,"profile":"intermittent","reason":"owner declaration"}`
	u, err := DecodeUpdate([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"key"`, `"Key"`, 1),
		strings.Replace(valid, `"key":`, `"key":"duplicate","key":`, 1),
		strings.Replace(valid, `"profile":"intermittent"`, `"profile":null`, 1),
		strings.Replace(valid, `"expected_revision":0`, `"expected_revision":"0"`, 1),
		strings.Replace(valid, `"intermittent"`, `"guaranteed"`, 1),
		strings.Replace(valid, `"owner declaration"`, `" owner declaration"`, 1),
		valid + `{}`,
	} {
		if _, err := DecodeUpdate([]byte(raw)); err == nil {
			t.Fatal("invalid request accepted", raw)
		}
	}
	host := strings.Repeat("a", 32)
	d := Document{Schema: 1, Host: host, Owner: "42", Revision: 1, Profile: u.Profile, Reason: u.Reason, UpdatedAt: time.Now().UTC(), LastUpdate: u}
	raw, _ := json.Marshal(d)
	if _, err := DecodeDocument(raw, host, "42"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{append([]byte(" "), raw...), []byte(strings.Replace(string(raw), `"schema":1`, `"schema":1,"schema":1`, 1)), []byte(strings.Replace(string(raw), `"expected_revision":0`, `"Expected_revision":0`, 1))} {
		if _, err := DecodeDocument(raw, host, "42"); err == nil {
			t.Fatal("noncanonical metadata accepted")
		}
	}
	if _, err := DecodeDocument(raw, strings.Repeat("b", 32), "42"); err == nil {
		t.Fatal("other host metadata accepted")
	}
	if _, err := DecodeDocument(raw, host, "43"); err == nil {
		t.Fatal("other owner metadata accepted")
	}
	report, _ := json.Marshal(d.Report(host))
	if _, err := DecodeReport(report, host); err != nil {
		t.Fatal("actual optional report rejected", err)
	}
	for _, bad := range []string{strings.Replace(string(report), `"profile":`, `"Profile":`, 1), strings.Replace(string(report), `"revision":1`, `"revision":1,"revision":1`, 1), strings.Replace(string(report), `"schema":1`, `"schema":2`, 1)} {
		if _, err := DecodeReport([]byte(bad), host); err == nil {
			t.Fatal("invalid optional report accepted")
		}
	}
	if strings.Contains(string(report), u.Reason) || strings.Contains(string(report), u.Key) || strings.Contains(string(report), `"owner"`) {
		t.Fatal("private update evidence leaked to peer report", string(report))
	}
}
