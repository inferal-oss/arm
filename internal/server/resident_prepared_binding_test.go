package server

import (
	"strings"
	"testing"
)

func TestPreparedRootRequiresActualEntryAndOriginalBindings(t *testing.T) {
	root := &residentChildPending{}
	root.self = root
	for _, candidate := range []*residentChildPending{nil, {}, {self: root}, root} {
		if err := candidate.validateOriginalPreparedBindings(); err != errResidentChildOriginal {
			t.Fatal(err)
		}
	}
	if root.preparedValidation != nil || root.preparedValidationTried || root.native != nil || root.manager != nil || root.server != nil || processResidentCoreEntry.call != nil {
		t.Fatal("DATA entered native validation")
	}
}
func TestPreparedCommitmentDataDomainAndOverflowRefusals(t *testing.T) {
	h := strings.Repeat("a", 64)
	data := residentPreparedCommitmentData{Schema: "1", Domain: "arm-resident-prepared-binding-v1", Generation: strings.Repeat("a", 32), Home: "/synthetic", WorkspaceFormat: "4", SettingsSHA256: h, DebitID: h, LaunchKey: "synthetic-key", ProfileSHA256: h, EnvironmentSHA256: h, IntentSHA256: h, ImageSHA256: h, ImageBytes: 1, BirthJournalSHA256: h, BirthJournalBytes: 1}
	if _, _, err := encodePreparedCommitmentData(data); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"domain", "schema", "format-five", "relative-home", "oversize-key", "bad-generation", "uppercase-digest"} {
		t.Run(name, func(t *testing.T) {
			bad := data
			switch name {
			case "domain":
				bad.Domain = "foreign"
			case "schema":
				bad.Schema = "2"
			case "format-five":
				bad.WorkspaceFormat = "5"
			case "relative-home":
				bad.Home = "relative"
			case "oversize-key":
				bad.LaunchKey = strings.Repeat("x", 4097)
			case "bad-generation":
				bad.Generation = strings.Repeat("g", 32)
			case "uppercase-digest":
				bad.EnvironmentSHA256 = strings.Repeat("A", 64)
			}
			if _, _, err := encodePreparedCommitmentData(bad); err != errResidentChildOriginal {
				t.Fatal("foreign/overflow DATA accepted", err)
			}
		})
	}
}
