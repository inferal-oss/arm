package meshsshrelay

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestClosedRoutedSSHIdentityAndLookupRecords(t *testing.T) {
	for _, q := range []Request{{Version: Version, Operation: OperationIdentity}, {Version: Version, Operation: OperationLookup, Key: "original-key"}} {
		var b bytes.Buffer
		if err := WriteRequest(&b, q); err != nil {
			t.Fatal(err)
		}
		got, err := ReadRequest(&b)
		if err != nil || got.Digest() != q.Digest() {
			t.Fatal(got, err)
		}
		out := q.Reply()
		out.Failure = &Failure{Code: "unavailable", Message: "closed test failure"}
		if err = WriteResponse(&b, q, out); err != nil {
			t.Fatal(err)
		}
		response, err := ReadResponse(&b, q)
		if err != nil || response.Failure == nil {
			t.Fatal(response, err)
		}
	}
	bad := []byte(`{"schema_version":1,"operation":"identity","operation":"execute"}`)
	var b bytes.Buffer
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(bad)))
	b.Write(h[:])
	b.Write(bad)
	if _, err := ReadRequest(&b); err == nil {
		t.Fatal("duplicate or alias operation accepted")
	}
	b.Reset()
	binary.BigEndian.PutUint32(h[:], MaxRequestBytes+1)
	b.Write(h[:])
	if _, err := ReadRequest(&b); err == nil {
		t.Fatal("unbounded routed SSH request allocated")
	}
}
