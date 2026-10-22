package meshssh

import (
	"context"
	"strings"
	"testing"
)

func TestCaptureBound(t *testing.T) {
	c := &capture{limit: 4}
	n, e := c.Write([]byte("123456"))
	if n != 6 || e != nil || c.String() != "1234" || !c.overflow {
		t.Fatal("unbounded SSH capture")
	}
	n, e = c.Write([]byte("789"))
	if n != 3 || e != nil || c.String() != "1234" {
		t.Fatal("overflow grew capture")
	}
}

func TestUnsafeFixedCommandAndOversizedInputRejectedBeforeSSH(t *testing.T) {
	for _, value := range []struct {
		destination, binary string
		payload             any
	}{{"fixture", "arm; bad", nil}, {"fixture\nother", "arm", nil}, {"-Fbad", "arm", nil}, {"fixture", "arm", strings.Repeat("x", 1<<20)}} {
		if _, e := Call(context.Background(), value.destination, value.binary, value.payload, Identity{}, nil); e == nil {
			t.Fatal("unsafe/oversized bootstrap accepted")
		}
	}
}
