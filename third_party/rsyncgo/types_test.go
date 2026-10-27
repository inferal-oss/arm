package rsync

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncwire"
)

func TestSumHeadPeerBounds(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		count, block, checksum, remainder int32
		want                              string
	}{
		{"count", MaxChecksumCount + 1, 700, 16, 0, "checksum count"},
		{"block", 1, MaxBlockLength + 1, 16, 0, "block length"},
		{"zero block", 1, 0, 16, 0, "block length"},
		{"negative count", -1, 700, 16, 0, "checksum count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			for _, v := range []int32{tc.count, tc.block, tc.checksum, tc.remainder} {
				if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
					t.Fatal(err)
				}
			}
			c := &rsyncwire.Conn{Reader: io.NopCloser(&b)}
			var head SumHead
			if err := head.ReadFrom(c); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
