package residentservice

import (
	"bytes"
	"getarm.org/internal/residentrefs"
	"testing"
)

// Full-frame DATA parser only. No actual service group/debit/endpoint/native
// holder is fabricated, and no transfer or parent/child Start is invoked.
func TestP1ParentFrameDataBindsFullHeaderChecksumAndCapacity(t *testing.T) {
	payload := bytes.Repeat([]byte{'d'}, residentrefs.MaxWireBytes-36)
	frame, err := residentrefs.EncodeFrameData(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateP1ParentFrameData(frame); err != nil {
		t.Fatal("valid full-capacity DATA", err)
	}
	for _, name := range []string{"empty", "oversize", "length", "checksum", "payload", "truncated"} {
		t.Run(name, func(t *testing.T) {
			data := append([]byte(nil), frame...)
			switch name {
			case "empty":
				data = nil
			case "oversize":
				data = append(data, 'x')
			case "length":
				data[0] ^= 1
			case "checksum":
				data[4] ^= 1
			case "payload":
				data[len(data)-1] ^= 1
			case "truncated":
				data = data[:len(data)-1]
			}
			if err := validateP1ParentFrameData(data); err != ErrInvalid {
				t.Fatal("malformed frame accepted", err)
			}
		})
	}
}
