package usage

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestLinuxStatIdentityAndMemoryCounters(t *testing.T) {
	fields := []string{"S", "1", "42", "42", "0", "0", "0", "0", "0", "0", "0", "12", "3", "0", "0", "20", "0", "1", "0", "99", "4096", "2"}
	values, err := parseStat([]byte("42 (comm ) contains spaces) " + strings.Join(fields, " ")))
	if err != nil || values.pgid != 42 || values.start != "99" || values.user != 12 || values.system != 3 || values.rss != 2 {
		t.Fatalf("proc stat offsets/identity: %+v %v", values, err)
	}
	fields[2] = "0"
	if values, err := parseStat([]byte("1 (namespace parent) " + strings.Join(fields, " "))); err != nil || values.pgid != 0 {
		t.Fatalf("namespace-invisible process group was treated as corrupt: %+v %v", values, err)
	}
	for _, bad := range []string{"", "1 (missing fields) S 1", "1 no comm"} {
		if _, err := parseStat([]byte(bad)); err == nil {
			t.Fatal("malformed stat accepted")
		}
	}
	if v, err := privateResident([]byte("Rss: 100 kB\nPrivate_Clean: 2 kB\nPrivate_Dirty: 3 kB\nShared_Clean: 95 kB\n")); err != nil || v != 5120 {
		t.Fatalf("private/shared memory semantics: %d %v", v, err)
	}
	for _, bad := range []string{"Private_Clean: 0 kB", "Private_Clean: 1 kB\nPrivate_Dirty: -1 kB", "Private_Clean: 1 kB\nPrivate_Dirty: 1 bytes", "Private_Clean: 1 kB\nPrivate_Clean: 2 kB\nPrivate_Dirty: 1 kB", fmt.Sprintf("Private_Clean: %d kB\nPrivate_Dirty: 1 kB", uint64(math.MaxUint64))} {
		if _, err := privateResident([]byte(bad)); err == nil {
			t.Fatalf("unknown/malformed memory treated as zero: %q", bad)
		}
	}
}
func TestLinuxClockTickAuxiliaryVector(t *testing.T) {
	word := strconv.IntSize / 8
	data := make([]byte, 4*word)
	if word == 8 {
		binary.NativeEndian.PutUint64(data, 17)
		binary.NativeEndian.PutUint64(data[word:], 100)
	} else {
		binary.NativeEndian.PutUint32(data, 17)
		binary.NativeEndian.PutUint32(data[word:], 100)
	}
	if value, err := clockTicks(data); err != nil || value != 100 {
		t.Fatal("clock tick metadata was not decoded", value, err)
	}
	for _, bad := range [][]byte{nil, {1}, make([]byte, 4*word)} {
		if _, err := clockTicks(bad); err == nil {
			t.Fatal("missing clock tick rate was guessed")
		}
	}
}
