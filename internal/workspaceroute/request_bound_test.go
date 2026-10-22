package workspaceroute

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoutedCompleteRequestBoundLeavesHeaderHeadroom(t *testing.T) {
	o, q := messageFixture()
	q.Envelope.Body = strings.Repeat("\x01", 9000)
	if !q.Envelope.Validate() {
		t.Fatal("fixture should retain a valid heavily escaped envelope")
	}
	var wire bytes.Buffer
	if e := WriteRequest(&wire, o, q); e != nil {
		t.Fatal("valid envelope cannot fit complete closed request", e)
	}
	if wire.Len() > 60<<10+4 {
		t.Fatal("request exceeded approved60KiB complete JSON bound", wire.Len())
	}
	if _, e := ReadRequest(&wire, o); e != nil {
		t.Fatal(e)
	}
	q.Envelope.Body = strings.Repeat("\x01", 16384)
	if q.Envelope.Validate() {
		t.Fatal("escaped envelope exceeded pre-dispatch headroom")
	}
	if e := WriteRequest(&wire, o, q); e == nil {
		t.Fatal("oversized escaped envelope reached transport")
	}
}
