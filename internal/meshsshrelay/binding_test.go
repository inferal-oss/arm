package meshsshrelay

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestBindingRecordClosedAndRouteSpecific(t *testing.T) {
	now := time.Now().UTC()
	o, _, _ := fixture(now)
	want, err := bindingFor(now, o)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := writeBinding(&wire, want); err != nil {
		t.Fatal(err)
	}
	got, err := readBinding(&wire)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	bad := []byte(`{"schema_version":1,"route_id":"` + o.Route + `","binding_sha256":"` + want.Digest + `","route_id":"` + o.Route + `"}`)
	var record bytes.Buffer
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(bad)))
	record.Write(size[:])
	record.Write(bad)
	if _, err := readBinding(&record); err == nil {
		t.Fatal("duplicate binding field accepted")
	}
	record.Reset()
	binary.BigEndian.PutUint32(size[:], maxBindingBytes+1)
	record.Write(size[:])
	if _, err := readBinding(&record); err == nil {
		t.Fatal("oversize binding allocated")
	}
}
