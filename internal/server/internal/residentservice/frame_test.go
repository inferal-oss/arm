package residentservice

import (
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// These are SYNTHETIC SERIALIZED DATA, never a measured profile/operator action
// or positive service capability. No data below is passed to a native issuer,
// debit, child or ManagerCharge constructor in this test.
func dataRecords(t *testing.T) []Record {
	t.Helper()
	profile := MeasuredProfile{Schema: 1, Build: Build{SHA256: strings.Repeat("a", 64), Size: 1, GOOS: "linux", GOARCH: "amd64", GoBuild: "synthetic-parser-data", Protocol: 1}, Resources: Resources{CPU: 1, Memory: 1, Disk: 1}, Bounds: Bounds{Tasks: 1, Descriptors: 1, Children: 1, Sockets: 1, NativePending: 1, IOBytes: 1, RetainedReceiptBytes: 1}, EvidenceSHA256: strings.Repeat("b", 64)}
	profileRaw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	inputs := OperatorInputs{Profile: OperatorFileInput{Path: "/synthetic/profile", ID: NativeID{1, 4, 1}, Size: int64(len(profileRaw)), SHA256: digest(profileRaw)}, Evidence: OperatorFileInput{Path: "/synthetic/evidence", ID: NativeID{1, 5, 1}, Size: 1, SHA256: profile.EvidenceSHA256}}
	intent := OperatorIntent{Inputs: inputs, Key: "synthetic-data", Home: "/synthetic", HomeID: NativeID{Device: 1, Inode: 1, UID: 1}, Profile: profile, Reason: "serialized parser data only"}
	plan, err := intent.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	grant := &GrantRecord{ID: strings.Repeat("c", 64), Revision: 1, PlanSHA: plan, Intent: intent}
	profileSHA, err := profile.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	debit := &DebitRecord{ID: strings.Repeat("d", 64), LaunchKey: "serialized-data-key", GrantID: grant.ID, GrantRevision: 1, ProfileSHA256: profileSHA, SecretCommitment: strings.Repeat("e", 64), LockID: NativeID{1, 2, 1}, JournalID: NativeID{1, 3, 1}}
	root := &RootBoundRecord{DebitID: debit.ID, LaunchKey: debit.LaunchKey, Generation: strings.Repeat("f", 32), CommitmentSHA256: strings.Repeat("a", 64)}
	return []Record{{Schema: 1, Sequence: 1, Kind: "operator-authorized", Grant: grant}, {Schema: 1, Sequence: 2, Kind: "debit-prepared", Debit: debit}, {Schema: 1, Sequence: 3, Kind: "root-bound", Root: root}}
}

func encodeData(t *testing.T, records []Record) []byte {
	t.Helper()
	var data []byte
	var previous [sha256.Size]byte
	for _, record := range records {
		frame, err := encodeFrame(record, previous)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, frame...)
		previous = sha256.Sum256(frame)
	}
	return data
}

func TestResidentServiceFrameDataRoundTripAndRefusesCorruption(t *testing.T) {
	records := dataRecords(t)
	data := encodeData(t, records)
	decoded, err := decodeJournal(data)
	if err != nil || len(decoded.records) != 3 || decoded.bytes != int64(len(data)) {
		t.Fatal(decoded, err)
	}
	for _, kind := range []string{"truncated-header", "truncated-payload", "checksum", "chain", "trailing", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			mutated := append([]byte(nil), data...)
			switch kind {
			case "truncated-header":
				mutated = mutated[:10]
			case "truncated-payload":
				mutated = mutated[:len(mutated)-1]
			case "checksum":
				mutated[frameHeaderBytes] ^= 1
			case "chain":
				mutated[16] ^= 1
			case "trailing":
				mutated = append(mutated, 0)
			case "oversize":
				mutated = make([]byte, MaxJournalBytes+1)
			}
			if _, err := decodeJournal(mutated); err == nil {
				t.Fatal("malformed serialized ledger accepted")
			}
		})
	}
}

func TestResidentServiceDataRefusesUnknownPartialAndNonfinite(t *testing.T) {
	records := dataRecords(t)
	for _, kind := range []string{"missing-grant", "extra-state", "wrong-debit", "wrong-root", "zero", "nonfinite", "unknown-platform"} {
		t.Run(kind, func(t *testing.T) {
			current := dataRecords(t)
			switch kind {
			case "missing-grant":
				current = current[1:]
			case "extra-state":
				current = append(current, Record{Schema: 1, Sequence: 4, Kind: "released"})
			case "wrong-debit":
				current[1].Debit.GrantID = strings.Repeat("f", 64)
			case "wrong-root":
				current[2].Root.DebitID = strings.Repeat("a", 64)
			case "zero":
				current[0].Grant.Intent.Profile.Resources.Memory = 0
			case "nonfinite":
				current[0].Grant.Intent.Profile.Resources.CPU = math.Inf(1)
			case "unknown-platform":
				current[0].Grant.Intent.Profile.Build.GOOS = "unknown"
			}
			if err := ValidateLedger(current); err == nil {
				t.Fatal("invalid serialized state accepted")
			}
		})
	}
	if _, err := decodeJournal(encodeData(t, records[:1])); err != nil {
		t.Fatal(err)
	}
	// An intact serialized grant has created NO owner/debit/charge capability.
	if _, err := (&ManagerCharge{}).ObserveOriginal(); err == nil {
		t.Fatal("record data became service authority")
	}
}
