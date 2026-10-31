package residentbirth

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"

	"getarm.org/internal/server/internal/residentservice"
)

func footerDataOnly(t *testing.T) P1RootBoundFooterData {
	t.Helper()
	h := strings.Repeat("a", 64)
	d, err := P1FooterForServiceData(residentservice.P1RootBoundData{Record: residentservice.RootBoundRecord{DebitID: h, LaunchKey: "synthetic-data-key", Generation: strings.Repeat("b", 32), CommitmentSHA256: h}, ProfileSHA256: h, ImageSHA256: h, JournalBytes: 1, JournalSHA256: h}, h, h)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestP1FooterClosedCanonicalFrameDataRejectsAliasesAndDamage(t *testing.T) {
	d := footerDataOnly(t)
	frame, err := EncodeP1RootBoundFooterData(d)
	if err != nil || len(frame) > MaxP1RootBoundFooterBytes {
		t.Fatal(len(frame), err)
	}
	decoded, err := DecodeP1RootBoundFooterData(frame)
	if err != nil || decoded != d {
		t.Fatal(err)
	}
	for _, name := range []string{"magic", "checksum", "end", "length", "truncated", "trailing", "oversize", "duplicate", "unknown", "alias"} {
		t.Run(name, func(t *testing.T) {
			bad := append([]byte(nil), frame...)
			switch name {
			case "magic":
				bad[0] ^= 1
			case "checksum":
				bad[12] ^= 1
			case "end":
				bad[len(bad)-1] ^= 1
			case "length":
				binary.BigEndian.PutUint32(bad[8:12], 1)
			case "truncated":
				bad = bad[:len(bad)-1]
			case "trailing":
				bad = append(bad, 'x')
			case "oversize":
				bad = make([]byte, MaxP1RootBoundFooterBytes+1)
			default:
				payload := append([]byte(nil), frame[p1FooterHeaderBytes:len(frame)-len(p1FooterEnd)]...)
				switch name {
				case "duplicate":
					payload = append([]byte(`{"schema":1,`), payload[1:]...)
				case "unknown":
					payload = append([]byte(`{"foreign":1,`), payload[1:]...)
				case "alias":
					payload = bytes.Replace(payload, []byte(`"schema"`), []byte(`"Schema"`), 1)
				}
				bad = make([]byte, p1FooterHeaderBytes+len(payload)+len(p1FooterEnd))
				copy(bad, p1FooterMagic[:])
				binary.BigEndian.PutUint32(bad[8:12], uint32(len(payload)))
				h := sha256.Sum256(payload)
				copy(bad[12:44], h[:])
				copy(bad[44:], payload)
				copy(bad[44+len(payload):], p1FooterEnd[:])
			}
			if _, err := DecodeP1RootBoundFooterData(bad); err != errOriginalFence {
				t.Fatal("foreign frame DATA accepted", err)
			}
		})
	}
}
func TestP1FooterKeyCommitmentDataAndFullFramingBounds(t *testing.T) {
	key := strings.Repeat("\x01", 4096)
	h, err := P1LaunchKeyCommitmentData(key)
	if err != nil || len(h) != 64 {
		t.Fatal(err)
	}
	other, _ := P1LaunchKeyCommitmentData(key[:len(key)-1] + "x")
	if h == other {
		t.Fatal("key not committed")
	}
	if _, err := P1LaunchKeyCommitmentData(key + "x"); err != errOriginalFence {
		t.Fatal(err)
	}
	d := footerDataOnly(t)
	d.LaunchKeySHA256 = h
	d.ServiceJournalBytes = residentservice.MaxJournalBytes
	frame, err := EncodeP1RootBoundFooterData(d)
	if err != nil {
		t.Fatal(err)
	}
	if total, err := P1LaunchJournalCapacityData(maxIntentBytes, len(frame)); err != nil || total > MaxP1LaunchJournalBytes {
		t.Fatal(total, err)
	}
	for _, pair := range [][2]int{{0, 1}, {maxIntentBytes + 1, 1}, {1, 0}, {1, MaxP1RootBoundFooterBytes + 1}} {
		if _, err := P1LaunchJournalCapacityData(pair[0], pair[1]); err != errOriginalFence {
			t.Fatal(pair, err)
		}
	}
	d.PreparedCommitmentSHA256 = strings.Repeat("A", 64)
	if _, err := EncodeP1RootBoundFooterData(d); err != errOriginalFence {
		t.Fatal(err)
	}
}
func TestP1PreFooterPrefixEOFDataRequiresExactOriginalBytes(t *testing.T) {
	prefix := []byte("synthetic-original-intent-data")
	if !validP1PrefixEOFData(prefix, append([]byte(nil), prefix...), true) {
		t.Fatal("equal prefix DATA")
	}
	if validP1PrefixEOFData(prefix, prefix, false) || validP1PrefixEOFData(prefix, append(append([]byte(nil), prefix...), 'x'), true) || validP1PrefixEOFData(prefix, prefix[:len(prefix)-1], true) || validP1PrefixEOFData(nil, nil, true) {
		t.Fatal("missing EOF or foreign prefix accepted")
	}
}
