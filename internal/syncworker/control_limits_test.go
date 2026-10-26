package syncworker

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

type smallControlHold struct{ testHold }

func (*smallControlHold) MemoryBytes() int64 { return 1 << 20 }

func TestControlRejectsStructuralMemoryOverrun(t *testing.T) {
	// The raw body fits the existing 1/8 wire allowance, but the decoded
	// directory structs alone occupy at least 1.28 MiB before slice growth
	// and decoder/raw buffers, beyond the entire 1 MiB hold.
	const entries = 40000
	body := []byte(`{"version":1,"kind":"ready","key":"k","plan":"p","batch":"b","dirs":[` + strings.Repeat(`{},`, entries-1) + `{}]}`)
	var wire bytes.Buffer
	var header [8]byte
	binary.BigEndian.PutUint64(header[:], uint64(len(body)))
	wire.Write(header[:])
	wire.Write(body)
	hold := &smallControlHold{}
	intent := hold.intent
	intent.Key, intent.PlanDigest = "k", "p"
	f, err := readFrame(t.Context(), &wire, hold, intent, "ready", "b")
	if err == nil {
		t.Fatalf("accepted %d directory structs from %d-byte frame under 1 MiB hold", len(f.Dirs), len(body))
	}
}
