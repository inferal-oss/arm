package providerproto

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Byte bounds are not wall-time deadlines. A future owned process runner must
// supply deadline/cancellation-aware I/O and establish actual process ownership.
func readFull(r io.Reader, b []byte) error {
	empty := 0
	for len(b) > 0 {
		n, e := r.Read(b)
		if n < 0 || n > len(b) {
			return fmt.Errorf("invalid provider reader count")
		}
		b = b[n:]
		if e != nil {
			if len(b) == 0 {
				return nil
			}
			if e == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return e
		}
		if n == 0 {
			empty++
			if empty >= 8 {
				return io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
	return nil
}
func readFrame(r io.Reader, max int) ([]byte, error) {
	var prefix [4]byte
	if e := readFull(r, prefix[:]); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || uint64(n) > uint64(max) {
		return nil, fmt.Errorf("provider frame byte limit")
	}
	b := make([]byte, int(n))
	if e := readFull(r, b); e != nil {
		return nil, e
	}
	var extra [1]byte
	for range 8 {
		n, e := r.Read(extra[:])
		if n != 0 {
			return nil, fmt.Errorf("trailing provider frame bytes")
		}
		if e == io.EOF {
			return b, nil
		}
		if e != nil {
			return nil, e
		}
	}
	return nil, io.ErrNoProgress
}
func writeFrame(w io.Writer, b []byte) error {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(b)))
	for _, part := range [][]byte{prefix[:], b} {
		for len(part) > 0 {
			n, e := w.Write(part)
			if n < 0 || n > len(part) {
				return fmt.Errorf("invalid provider writer count")
			}
			part = part[n:]
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
		}
	}
	return nil
}
func ReadRequest(r io.Reader) (DecodedRequest, error) {
	b, e := readFrame(r, MaxRequestBytes)
	if e != nil {
		return DecodedRequest{}, e
	}
	return DecodeRequest(b)
}
func ReadResponse(r io.Reader) (DecodedResponse, error) {
	b, e := readFrame(r, MaxResponseBytes)
	if e != nil {
		return DecodedResponse{}, e
	}
	return DecodeResponse(b)
}
func WriteRequest(w io.Writer, q Request) error {
	b, e := EncodeRequest(q)
	if e != nil {
		return e
	}
	return writeFrame(w, b)
}
func WriteResponse(w io.Writer, r Response) error {
	b, e := EncodeResponse(r)
	if e != nil {
		return e
	}
	return writeFrame(w, b)
}
