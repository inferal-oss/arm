package cargoprofile

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestCargoHelperCanonicalFramesAndOneUseBinding(t *testing.T) {
	lease := strings.Repeat("a", 32)
	nonce := strings.Repeat("b", 64)
	var wire bytes.Buffer
	sender := HelperChannel{Out: &wire, Lease: lease, Nonce: nonce}
	type grant struct {
		Allowed  bool `json:"allowed"`
		Revision int  `json:"revision"`
	}
	if e := sender.Send("preflight", grant{Allowed: true, Revision: 2}); e != nil {
		t.Fatal(e)
	}
	copied := append([]byte{}, wire.Bytes()...)
	receiver := HelperChannel{In: &wire, Lease: lease, Nonce: nonce}
	var got grant
	if e := receiver.Receive("preflight", &got); e != nil || !got.Allowed || got.Revision != 2 {
		t.Fatal(got, e)
	}
	receiver.In = bytes.NewReader(copied)
	if e := receiver.Receive("preflight", &got); e == nil {
		t.Fatal("same phase replayed")
	}
	for _, change := range []func(*HelperChannel){func(c *HelperChannel) { c.Nonce = strings.Repeat("c", 64) }, func(c *HelperChannel) { c.Lease = strings.Repeat("c", 32) }} {
		c := HelperChannel{In: bytes.NewReader(copied), Lease: lease, Nonce: nonce}
		change(&c)
		if e := c.Receive("preflight", &got); e == nil {
			t.Fatal("cross-binding packet accepted")
		}
	}
	if e := DecodeCanonical([]byte(`{"allowed":true,"revision":2}`), &got); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"Allowed":true,"revision":2}`, `{"allowed":true}`, `{"allowed":null,"revision":2}`, `{"allowed":true,"revision":2,"revision":2}`, `{"allowed":true,"revision":2,"extra":0}`, `{"allowed":true,"revision":2} {}`} {
		if e := DecodeCanonical([]byte(raw), &got); e == nil {
			t.Fatal("nonexact evidence accepted", raw)
		}
	}
}
func TestCargoHelperFrameLengthTruncationAndUnknownTypes(t *testing.T) {
	for _, n := range []uint32{0, MaxHelperFrame + 1, ^uint32(0)} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], n)
		if _, e := ReadHelperPacket(bytes.NewReader(size[:])); e == nil {
			t.Fatal("frame bound accepted", n)
		}
	}
	var buffer bytes.Buffer
	packet := HelperPacket{Version: 1, Lease: strings.Repeat("a", 32), Nonce: strings.Repeat("b", 64), Sequence: 1, Kind: "init", Data: json.RawMessage(`{}`)}
	if e := WriteHelperPacket(&buffer, packet); e != nil {
		t.Fatal(e)
	}
	raw := buffer.Bytes()
	if _, e := ReadHelperPacket(bytes.NewReader(raw[:len(raw)-1])); e == nil {
		t.Fatal("truncated frame accepted")
	}
	packet.Kind = "shell"
	if e := WriteHelperPacket(&bytes.Buffer{}, packet); e == nil {
		t.Fatal("unknown message kind accepted")
	}
}
