package residentservice

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func workerRecordsData(t *testing.T) []WorkerRecordData {
	t.Helper()
	p, e := workerEvidenceData(t)
	profileRaw, _ := json.Marshal(p)
	evidenceRaw, _ := json.Marshal(e)
	profileSHA, err := p.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	intent := WorkerOperatorIntent{Key: "synthetic-worker-data", Home: "/synthetic", HomeID: NativeID{1, 1, 1}, Profile: p, Reason: "synthetic DATA only, never a grant", Inputs: OperatorInputs{
		Profile:  OperatorFileInput{Path: "/synthetic/profile3", ID: NativeID{1, 4, 1}, Size: int64(len(profileRaw)), SHA256: profileSHA},
		Evidence: OperatorFileInput{Path: "/synthetic/evidence3", ID: NativeID{1, 5, 1}, Size: int64(len(evidenceRaw)), SHA256: p.EvidenceSHA256},
	}}
	plan, err := intent.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	contractSHA, _ := p.Contract.SHA256()
	grant := &WorkerGrantRecord{ID: strings.Repeat("c", 64), Revision: 1, PlanSHA: plan, Intent: intent}
	debit := &WorkerDebitRecord{ID: strings.Repeat("d", 64), LaunchKey: "synthetic-worker-launch", GrantID: grant.ID, GrantRevision: 1, ProfileSHA256: profileSHA, ContractSHA256: contractSHA, InitialPackageSHA256: p.Contract.InitialPackageSHA256, InitialManifestSHA256: p.Contract.InitialManifestSHA256, SecretCommitment: strings.Repeat("e", 64), LockID: NativeID{1, 2, 1}, JournalID: NativeID{1, 3, 1}}
	root := &WorkerRootBoundRecord{DebitID: debit.ID, LaunchKey: debit.LaunchKey, Generation: strings.Repeat("f", 32), CommitmentSHA256: strings.Repeat("a", 64), ContractSHA256: contractSHA}
	return []WorkerRecordData{{Schema: 3, Sequence: 1, Kind: "worker-operator-authorized", Grant: grant}, {Schema: 3, Sequence: 2, Kind: "worker-debit-prepared", Debit: debit}, {Schema: 3, Sequence: 3, Kind: "worker-root-bound", Root: root}}
}

func TestWorkerLedgerCanonicalDataAndLegacySeparation(t *testing.T) {
	for _, name := range []string{"authorized", "debited", "root-bound"} {
		t.Run(name, func(t *testing.T) {
			records := workerRecordsData(t)
			size := map[string]int{"authorized": 1, "debited": 2, "root-bound": 3}[name]
			raw, err := encodeWorkerJournalData(records[:size])
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := decodeWorkerJournalData(raw)
			if err != nil || len(parsed.records) != size || parsed.bytes != int64(len(raw)) {
				t.Fatal(parsed, err)
			}
			again, err := encodeWorkerJournalData(parsed.records)
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatal("canonical DATA roundtrip", err)
			}
			if _, err = decodeJournal(raw); err == nil {
				t.Fatal("old native journal grammar accepted worker3")
			}
		})
	}
	legacy := encodeData(t, dataRecords(t))
	if _, err := decodeWorkerJournalData(legacy); err == nil {
		t.Fatal("old journal promoted into worker grammar")
	}
	old := dataRecords(t)
	old[0].Schema = 3
	if ValidateLedger(old) == nil {
		t.Fatal("legacy ledger schema widened")
	}
}

func TestWorkerLedgerRejectsBindingAndOrderingDriftData(t *testing.T) {
	for _, name := range []string{"schema", "sequence", "kind", "extra-record", "multiple-bodies", "grant-revision", "expected-revision", "plan", "intent", "grant-id", "profile", "contract", "package", "manifest", "secret", "lock", "journal-uid", "root-debit", "root-launch", "root-generation", "root-commitment", "root-contract"} {
		t.Run(name, func(t *testing.T) {
			r := workerRecordsData(t)
			g, d, b := r[0].Grant, r[1].Debit, r[2].Root
			switch name {
			case "schema":
				r[0].Schema = 1
			case "sequence":
				r[1].Sequence = 1
			case "kind":
				r[1].Kind = "debit-prepared"
			case "extra-record":
				r = append(r, r[2])
			case "multiple-bodies":
				r[0].Debit = d
			case "grant-revision":
				g.Revision = 2
			case "expected-revision":
				g.Intent.ExpectedRevision = 1
			case "plan":
				g.PlanSHA = strings.Repeat("f", 64)
			case "intent":
				g.Intent.Key = "other-key"
			case "grant-id":
				d.GrantID = strings.Repeat("f", 64)
			case "profile":
				d.ProfileSHA256 = strings.Repeat("f", 64)
			case "contract":
				d.ContractSHA256 = strings.Repeat("f", 64)
			case "package":
				d.InitialPackageSHA256 = strings.Repeat("f", 64)
			case "manifest":
				d.InitialManifestSHA256 = strings.Repeat("f", 64)
			case "secret":
				d.SecretCommitment = "missing"
			case "lock":
				d.LockID.Inode = 0
			case "journal-uid":
				d.JournalID.UID++
			case "root-debit":
				b.DebitID = strings.Repeat("f", 64)
			case "root-launch":
				b.LaunchKey = "other"
			case "root-generation":
				b.Generation = strings.Repeat("F", 32)
			case "root-commitment":
				b.CommitmentSHA256 = "missing"
			case "root-contract":
				b.ContractSHA256 = strings.Repeat("f", 64)
			}
			if ValidateWorkerLedgerData(r) == nil {
				t.Fatal("invalid worker ledger DATA accepted")
			}
			if _, err := encodeWorkerJournalData(r); err == nil {
				t.Fatal("invalid ledger encoded")
			}
		})
	}
}

// Mutate a valid DATA frame, recomputing its checksum, so canonical/schema
// refusals cannot accidentally pass only because the outer checksum was bad.
func replaceWorkerPayloadData(t *testing.T, valid []byte, payload []byte) []byte {
	t.Helper()
	frame := append([]byte{}, valid[:workerJournalHeaderBytes]...)
	binary.BigEndian.PutUint32(frame[12:16], uint32(len(payload)))
	h := sha256.Sum256(payload)
	copy(frame[48:80], h[:])
	frame = append(frame, payload...)
	frame = append(frame, valid[len(valid)-len(workerJournalEnd):]...)
	return frame
}

func TestWorkerJournalCanonicalPayloadAndEnvelopeRefusalsData(t *testing.T) {
	raw, err := encodeWorkerJournalData(workerRecordsData(t)[:1])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"magic", "end", "checksum", "previous", "sequence", "length-overflow", "truncated", "journal-oversize", "unknown", "duplicate", "trailing", "whitespace", "legacy-schema"} {
		t.Run(name, func(t *testing.T) {
			v := append([]byte{}, raw...)
			payload := append([]byte{}, raw[workerJournalHeaderBytes:len(raw)-len(workerJournalEnd)]...)
			switch name {
			case "magic":
				v[6] = '1'
			case "end":
				v[len(v)-2] = '1'
			case "checksum":
				v[48] ^= 1
			case "previous":
				v[16] = 1
			case "sequence":
				binary.BigEndian.PutUint32(v[8:12], 2)
			case "length-overflow":
				binary.BigEndian.PutUint32(v[12:16], ^uint32(0))
			case "truncated":
				v = v[:len(v)-1]
			case "journal-oversize":
				v = make([]byte, workerJournalMaxBytes+1)
			case "unknown":
				payload = append([]byte(`{"extra":1,`), payload[1:]...)
				v = replaceWorkerPayloadData(t, raw, payload)
			case "duplicate":
				payload = append([]byte(`{"schema":3,`), payload[1:]...)
				v = replaceWorkerPayloadData(t, raw, payload)
			case "trailing":
				payload = append(payload, []byte("{}")...)
				v = replaceWorkerPayloadData(t, raw, payload)
			case "whitespace":
				payload = append(payload, '\n')
				v = replaceWorkerPayloadData(t, raw, payload)
			case "legacy-schema":
				payload = bytes.Replace(payload, []byte(`"schema":3`), []byte(`"schema":1`), 1)
				v = replaceWorkerPayloadData(t, raw, payload)
			}
			if _, err := decodeWorkerJournalData(v); err == nil {
				t.Fatal("invalid frame/journal accepted")
			}
		})
	}
}

func TestWorkerGrantWholeFramePreflightData(t *testing.T) {
	r := workerRecordsData(t)
	g := r[0].Grant
	p, e := workerEvidenceData(t)
	p.Core.RuntimeEnvironment.Entries = append(p.Core.RuntimeEnvironment.Entries, "PATH=/"+strings.Repeat("e", 3000))
	e.Core.RuntimeEnvironment = p.Core.RuntimeEnvironment.Clone()
	coreRaw, _ := json.Marshal(e.Core)
	p.Core.EvidenceSHA256 = digest(coreRaw)
	workerOuterDigestData(t, &p, e)
	profileRaw, _ := json.Marshal(p)
	evidenceRaw, _ := json.Marshal(e)
	profileSHA, err := p.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	g.Intent.Profile = p
	g.Intent.Home = "/"
	g.Intent.Reason = strings.Repeat("r", 1024)
	g.Intent.Inputs.Profile.Path = "/" + strings.Repeat("p", 4093)
	g.Intent.Inputs.Evidence.Path = "/" + strings.Repeat("e", 4093)
	g.Intent.Inputs.Profile.Size = int64(len(profileRaw))
	g.Intent.Inputs.Profile.SHA256 = profileSHA
	g.Intent.Inputs.Evidence.SHA256 = p.EvidenceSHA256
	g.Intent.Inputs.Evidence.Size = int64(len(evidenceRaw))
	payload, _ := json.Marshal(r[0])
	extra := workerJournalFrameWireBytes - workerJournalHeaderBytes - len(workerJournalEnd) - len(payload)
	if extra <= 0 || extra > 4094 {
		t.Fatal("fixture cannot target exact full-frame bound", extra)
	}
	g.Intent.Home = "/" + strings.Repeat("h", extra)
	g.PlanSHA, err = g.Intent.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	frame, err := encodeWorkerJournalData(r[:1])
	if err != nil || len(frame) != workerJournalFrameWireBytes {
		t.Fatal("exact full-frame DATA refused", len(frame), err)
	}
	g.Intent.Home += "h"
	if g.Intent.Validate() == nil {
		t.Fatal("intent reserved payload without envelope overhead")
	}
}

func TestWorkerRecordShapeBoundsBeforeMarshalData(t *testing.T) {
	for _, name := range []string{"kind", "grant-id", "grant-home", "debit-key", "root-key"} {
		t.Run(name, func(t *testing.T) {
			r := workerRecordsData(t)
			index := 0
			switch name {
			case "kind":
				r[0].Kind = strings.Repeat("k", 33)
			case "grant-id":
				r[0].Grant.ID = strings.Repeat("g", 65)
			case "grant-home":
				r[0].Grant.Intent.Home = "/" + strings.Repeat("h", 4096)
			case "debit-key":
				index = 1
				r[1].Debit.LaunchKey = strings.Repeat("k", 129)
			case "root-key":
				index = 2
				r[2].Root.LaunchKey = strings.Repeat("k", 129)
			}
			if workerRecordShapeBoundsData(r[index]) == nil {
				t.Fatal("unbounded variable-size DATA accepted")
			}
			if _, err := workerRecordPayloadData(r[index]); err == nil {
				t.Fatal("shape escaped to encoder")
			}
		})
	}
}
