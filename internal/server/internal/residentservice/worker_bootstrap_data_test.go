package residentservice

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func workerBootstrapFixtureData(t *testing.T) (WorkerBootstrapData, workerJournalData) {
	t.Helper()
	records := workerRecordsData(t)
	secret := [32]byte{1, 2, 3}
	records[1].Debit.SecretCommitment = digest(secret[:])
	raw, err := encodeWorkerJournalData(records[:2])
	if err != nil {
		t.Fatal(err)
	}
	j, err := decodeWorkerJournalData(raw)
	if err != nil {
		t.Fatal(err)
	}
	g, d := j.records[0].Grant, j.records[1].Debit
	b := WorkerBootstrapData{Schema: 2, Domain: workerBootstrapDomain, Role: WorkerCoreRole, ChannelID: NativeID{1, 10, 1}, Home: g.Intent.Home, HomeID: g.Intent.HomeID, Build: g.Intent.Profile.Core.Build, DebitID: d.ID, LaunchKey: d.LaunchKey, ProfileSHA256: d.ProfileSHA256, ContractSHA256: d.ContractSHA256, InitialPackageSHA256: d.InitialPackageSHA256, InitialManifestSHA256: d.InitialManifestSHA256, InitialManifestBytes: g.Intent.Profile.Contract.InitialManifestBytes, JournalBytes: j.bytes, JournalSHA256: hex.EncodeToString(j.last[:]), Secret: secret}
	return b, j
}

func TestWorkerBootstrapCanonicalFrameAndLegacySeparationData(t *testing.T) {
	b, j := workerBootstrapFixtureData(t)
	if err := validateWorkerBootstrapJournalData(b, j); err != nil {
		t.Fatal(err)
	}
	frame, err := encodeWorkerBootstrapFrameData(b)
	if err != nil {
		t.Fatal(err)
	}
	after, err := decodeWorkerBootstrapFrameData(frame)
	if err != nil || after != b {
		t.Fatal("canonical DATA differs", err)
	}
	raw, _ := json.Marshal(b)
	var old P1BootstrapData
	if decodeOperatorDocument(raw, MaxProfileBytes, &old) == nil {
		t.Fatal("legacy bootstrap grammar widened")
	}
	if len(frame) != workerTransportHeaderBytes+len(raw) {
		t.Fatal("full frame overhead missing")
	}
}

func TestWorkerBootstrapRejectsSchemaRoleAndBindingsData(t *testing.T) {
	for _, name := range []string{"schema", "domain", "role", "home", "home-id", "channel", "uid", "build", "debit", "key", "profile", "contract", "package", "manifest", "manifest-size", "journal-size", "journal-digest", "secret", "zero-secret"} {
		t.Run(name, func(t *testing.T) {
			b, j := workerBootstrapFixtureData(t)
			switch name {
			case "schema":
				b.Schema = 1
			case "domain":
				b.Domain = "arm-resident-bootstrap-v1"
			case "role":
				b.Role = "--arm-resident-core-v1"
			case "home":
				b.Home = "/other"
			case "home-id":
				b.HomeID.Inode++
			case "channel":
				b.ChannelID.Inode = 0
			case "uid":
				b.ChannelID.UID++
			case "build":
				b.Build.SHA256 = strings.Repeat("f", 64)
			case "debit":
				b.DebitID = strings.Repeat("f", 64)
			case "key":
				b.LaunchKey = "other"
			case "profile":
				b.ProfileSHA256 = strings.Repeat("f", 64)
			case "contract":
				b.ContractSHA256 = strings.Repeat("f", 64)
			case "package":
				b.InitialPackageSHA256 = strings.Repeat("f", 64)
			case "manifest":
				b.InitialManifestSHA256 = strings.Repeat("f", 64)
			case "manifest-size":
				b.InitialManifestBytes++
			case "journal-size":
				b.JournalBytes++
			case "journal-digest":
				b.JournalSHA256 = strings.Repeat("f", 64)
			case "secret":
				b.Secret[0]++
			case "zero-secret":
				b.Secret = [32]byte{}
			}
			if validateWorkerBootstrapJournalData(b, j) == nil {
				t.Fatal("changed DATA binding accepted")
			}
		})
	}
}

func TestWorkerBootstrapFrameBoundsAndCanonicalPayloadData(t *testing.T) {
	b, _ := workerBootstrapFixtureData(t)
	valid, err := encodeWorkerBootstrapFrameData(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"header", "checksum", "length", "truncated", "oversize", "unknown", "duplicate", "trailing", "whitespace"} {
		t.Run(name, func(t *testing.T) {
			frame := append([]byte(nil), valid...)
			switch name {
			case "header":
				frame = frame[:workerTransportHeaderBytes]
			case "checksum":
				frame[4] ^= 1
			case "length":
				binary.BigEndian.PutUint32(frame[:4], ^uint32(0))
			case "truncated":
				frame = frame[:len(frame)-1]
			case "oversize":
				frame = make([]byte, workerBootstrapMaxWireBytes+1)
			default:
				payload := append([]byte(nil), frame[workerTransportHeaderBytes:]...)
				switch name {
				case "unknown":
					payload = append([]byte(`{"unknown":1,`), payload[1:]...)
				case "duplicate":
					payload = append([]byte(`{"schema":2,`), payload[1:]...)
				case "trailing":
					payload = append(payload, []byte("{}")...)
				case "whitespace":
					payload = append(payload, '\n')
				}
				frame = make([]byte, workerTransportHeaderBytes+len(payload))
				binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
				sum := sha256.Sum256(payload)
				copy(frame[4:workerTransportHeaderBytes], sum[:])
				copy(frame[workerTransportHeaderBytes:], payload)
			}
			if _, err := decodeWorkerBootstrapFrameData(frame); err == nil {
				t.Fatal("invalid full frame/canonical DATA accepted")
			}
		})
	}
}
