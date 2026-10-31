package residentservice

import (
	"context"
	"io"
	"testing"
)

// Transfer projection DATA only; no active group/debit/grant/native holder or
// issuer is fabricated or invoked. Fullwrite remains explicitly not an ACK.
func TestP1TransferDataKeepsAttemptBytesFullWriteAndACKDistinct(t *testing.T) {
	if data := projectP1TransferData(nil); data.Attempted || data.FullWrite || data.ACKObserved {
		t.Fatal(data)
	}
	for _, c := range []struct {
		name string
		call channelOperation
		full bool
	}{
		{"attempted-zero", channelOperation{bytes: []byte("data")}, false},
		{"cancelled-zero", channelOperation{bytes: []byte("data"), firstError: context.Canceled}, false},
		{"partial-error", channelOperation{bytes: []byte("data"), transferred: 2, writes: 1, firstError: io.ErrShortWrite}, false},
		{"all-bytes-error-unconfirmed", channelOperation{bytes: []byte("data"), transferred: 4, writes: 1, firstError: io.ErrUnexpectedEOF}, false},
		{"fullwrite-noACK", channelOperation{bytes: []byte("data"), transferred: 4, writes: 1, writeComplete: true}, true},
		{"fullwrite-then-cancel-noACK", channelOperation{bytes: []byte("data"), transferred: 4, writes: 1, writeComplete: true, firstError: context.Canceled}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := projectP1TransferData(&c.call)
			if !data.Attempted || data.FullWrite != c.full || data.ACKObserved || data.Written != c.call.transferred || data.Writes != c.call.writes || data.FirstError != c.call.firstError {
				t.Fatal(data)
			}
		})
	}
}
