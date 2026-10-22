package cargoprofile

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"
)

const HelperProtocol = 1
const MaxHelperFrame = 1 << 20
const MaxHelperMessages = 64

var helperNonce = regexp.MustCompile(`^[a-f0-9]{64}$`)
var helperKinds = map[string]bool{"init": true, "hello": true, "preflight": true, "birth": true, "recorded": true, "prepared": true, "policy": true, "compile": true, "started": true, "finished": true, "error": true}

// HelperPacket is private inherited-pipe IPC, never a native/browser/peer RPC.
// Each direction has an independent strictly increasing sequence and exact
// lease/nonce binding. The process supervisor owns cancellation and deadlines.
type HelperPacket struct {
	Version  int             `json:"version"`
	Lease    string          `json:"lease"`
	Nonce    string          `json:"nonce"`
	Sequence int             `json:"sequence"`
	Kind     string          `json:"kind"`
	Data     json.RawMessage `json:"data"`
}

// DecodeCanonical requires the exact producer representation. In particular,
// case aliases, missing/null scalars, unknown fields, duplicate keys, trailing
// JSON and noncanonical optional defaults cannot be normalized into evidence.
func DecodeCanonical(raw []byte, destination any) error {
	if len(raw) < 1 || len(raw) > MaxHelperFrame || !utf8.Valid(raw) {
		return fmt.Errorf("Cargo helper evidence exceeds bounds")
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	tokens.UseNumber()
	if e := uniqueMetadata(tokens, 0); e != nil {
		return fmt.Errorf("Cargo helper evidence is ambiguous")
	}
	if _, e := tokens.Token(); e != io.EOF {
		return fmt.Errorf("Cargo helper evidence has trailing data")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(destination); e != nil {
		return fmt.Errorf("Cargo helper evidence has unsupported shape")
	}
	canonical, e := json.Marshal(destination)
	if e != nil {
		return fmt.Errorf("Cargo helper evidence cannot be encoded")
	}
	var compact bytes.Buffer
	if e = json.Compact(&compact, raw); e != nil || !bytes.Equal(compact.Bytes(), canonical) {
		return fmt.Errorf("Cargo helper evidence must use exact canonical fields and values")
	}
	return nil
}
func WriteHelperPacket(w io.Writer, packet HelperPacket) error {
	if e := validateHelperPacket(packet); e != nil {
		return e
	}
	raw, e := json.Marshal(packet)
	if e != nil || len(raw) > MaxHelperFrame {
		return fmt.Errorf("Cargo helper frame exceeds bound")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
	if e = writeHelperBytes(w, size[:]); e != nil {
		return e
	}
	return writeHelperBytes(w, raw)
}
func writeHelperBytes(w io.Writer, raw []byte) error {
	for len(raw) > 0 {
		n, e := w.Write(raw)
		if n < 0 || n > len(raw) {
			return fmt.Errorf("invalid Cargo helper pipe write")
		}
		raw = raw[n:]
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
func ReadHelperPacket(r io.Reader) (packet HelperPacket, err error) {
	var size [4]byte
	if _, e := io.ReadFull(r, size[:]); e != nil {
		return packet, e
	}
	n := binary.BigEndian.Uint32(size[:])
	if n < 1 || n > MaxHelperFrame {
		return packet, fmt.Errorf("Cargo helper frame exceeds bound")
	}
	raw := make([]byte, int(n))
	if _, e := io.ReadFull(r, raw); e != nil {
		return packet, e
	}
	if e := DecodeCanonical(raw, &packet); e != nil {
		return HelperPacket{}, e
	}
	if e := validateHelperPacket(packet); e != nil {
		return HelperPacket{}, e
	}
	return packet, nil
}
func validateHelperPacket(packet HelperPacket) error {
	if packet.Version != HelperProtocol || !cargoLeaseID.MatchString(packet.Lease) || !helperNonce.MatchString(packet.Nonce) || packet.Sequence < 1 || packet.Sequence > MaxHelperMessages || !helperKinds[packet.Kind] || len(packet.Data) < 2 || bytes.Equal(packet.Data, []byte("null")) {
		return fmt.Errorf("Cargo helper frame binding/type is invalid")
	}
	return nil
}

type HelperChannel struct {
	In             io.Reader
	Out            io.Writer
	Lease          string
	Nonce          string
	sent, received int
}

func (c *HelperChannel) Send(kind string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	packet := HelperPacket{Version: HelperProtocol, Lease: c.Lease, Nonce: c.Nonce, Sequence: c.sent + 1, Kind: kind, Data: raw}
	if e = WriteHelperPacket(c.Out, packet); e != nil {
		return e
	}
	c.sent++
	return nil
}
func (c *HelperChannel) Next() (HelperPacket, error) {
	packet, e := ReadHelperPacket(c.In)
	if e != nil {
		return HelperPacket{}, e
	}
	if packet.Lease != c.Lease || packet.Nonce != c.Nonce || packet.Sequence != c.received+1 {
		return HelperPacket{}, fmt.Errorf("Cargo helper frame does not match current one-use phase")
	}
	c.received++
	return packet, nil
}
func (c *HelperChannel) Receive(kind string, destination any) error {
	packet, e := c.Next()
	if e != nil {
		return e
	}
	if packet.Kind != kind {
		return fmt.Errorf("Cargo helper frame has unexpected phase")
	}
	return DecodeCanonical(packet.Data, destination)
}
