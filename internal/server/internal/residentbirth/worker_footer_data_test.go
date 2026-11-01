package residentbirth

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"

	"getarm.org/internal/server/internal/residentservice"
)

func workerFooterDataOnly(t *testing.T) WorkerRootBoundFooterData {
	t.Helper()
	h := strings.Repeat("a", 64)
	d, err := WorkerFooterForServiceData(residentservice.WorkerRootBoundData{Record: residentservice.WorkerRootBoundRecord{DebitID: h, LaunchKey: "synthetic-data-key", Generation: strings.Repeat("b", 32), CommitmentSHA256: h, ContractSHA256: h}, ProfileSHA256: h, ImageSHA256: h, InitialPackageSHA256: h, InitialManifestSHA256: h, JournalBytes: 300, JournalSHA256: h}, h, h)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestWorkerFooterClosedCanonicalFrameDataRejectsAliasesAndDamage(t *testing.T) {
	d := workerFooterDataOnly(t)
	frame, err := EncodeWorkerRootBoundFooterData(d)
	if err != nil || len(frame) > MaxWorkerRootBoundFooterBytes {
		t.Fatal(len(frame), err)
	}
	decoded, err := DecodeWorkerRootBoundFooterData(frame)
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
				bad = make([]byte, MaxWorkerRootBoundFooterBytes+1)
			default:
				payload := append([]byte(nil), frame[workerFooterHeaderBytes:len(frame)-len(workerFooterEnd)]...)
				switch name {
				case "duplicate":
					payload = append([]byte(`{"schema":2,`), payload[1:]...)
				case "unknown":
					payload = append([]byte(`{"foreign":1,`), payload[1:]...)
				case "alias":
					payload = bytes.Replace(payload, []byte(`"schema"`), []byte(`"Schema"`), 1)
				}
				bad = make([]byte, workerFooterHeaderBytes+len(payload)+len(workerFooterEnd))
				copy(bad, workerFooterMagic[:])
				binary.BigEndian.PutUint32(bad[8:12], uint32(len(payload)))
				h := sha256.Sum256(payload)
				copy(bad[12:44], h[:])
				copy(bad[44:], payload)
				copy(bad[44+len(payload):], workerFooterEnd[:])
			}
			if _, err := DecodeWorkerRootBoundFooterData(bad); err != errOriginalWorkerFence {
				t.Fatal("foreign frame DATA accepted", err)
			}
		})
	}
}
func TestWorkerFooterKeyCommitmentDataAndFullFramingBounds(t *testing.T) {
	key := strings.Repeat("x", 128)
	h, err := WorkerLaunchKeyCommitmentData(key)
	if err != nil || len(h) != 64 {
		t.Fatal(err)
	}
	other, _ := WorkerLaunchKeyCommitmentData(key[:len(key)-1] + "y")
	if h == other {
		t.Fatal("key not committed")
	}
	if _, err := WorkerLaunchKeyCommitmentData(key + "x"); err == nil {
		t.Fatal(err)
	}
	d := workerFooterDataOnly(t)
	d.LaunchKeySHA256 = h
	d.ServiceJournalBytes = 3 * (16 << 10)
	frame, err := EncodeWorkerRootBoundFooterData(d)
	if err != nil {
		t.Fatal(err)
	}
	if total, err := WorkerLaunchJournalCapacityData(maxWorkerIntentBytes, len(frame)); err != nil || total > MaxWorkerLaunchJournalBytes {
		t.Fatal(total, err)
	}
	for _, pair := range [][2]int{{0, 1}, {maxWorkerIntentBytes + 1, 1}, {1, 0}, {1, MaxWorkerRootBoundFooterBytes + 1}} {
		if _, err := WorkerLaunchJournalCapacityData(pair[0], pair[1]); err != errOriginalWorkerFence {
			t.Fatal(pair, err)
		}
	}
	d.PreparedCommitmentSHA256 = strings.Repeat("A", 64)
	if _, err := EncodeWorkerRootBoundFooterData(d); err != errOriginalWorkerFence {
		t.Fatal(err)
	}
}

func TestWorkerFooterRejectsLegacyFramesAndUnboundContractData(t *testing.T) {
	legacy, err := EncodeP1RootBoundFooterData(footerDataOnly(t))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := EncodeWorkerRootBoundFooterData(workerFooterDataOnly(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeWorkerRootBoundFooterData(legacy); err == nil {
		t.Fatal("legacy footer entered worker grammar")
	}
	if _, err = DecodeP1RootBoundFooterData(worker); err == nil {
		t.Fatal("worker footer entered legacy grammar")
	}
	for _, field := range []string{"contract", "package", "manifest", "schema", "service-count"} {
		t.Run(field, func(t *testing.T) {
			d := workerFooterDataOnly(t)
			switch field {
			case "contract":
				d.ContractSHA256 = ""
			case "package":
				d.InitialPackageSHA256 = ""
			case "manifest":
				d.InitialManifestSHA256 = ""
			case "schema":
				d.Schema = 1
			case "service-count":
				d.ServiceJournalBytes = 1
			}
			if _, err := EncodeWorkerRootBoundFooterData(d); err == nil {
				t.Fatal("unbound footer accepted")
			}
		})
	}
}
