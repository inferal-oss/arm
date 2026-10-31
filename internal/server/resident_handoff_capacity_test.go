package server

import (
	"strings"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

func TestHandoffCapacityDataIncludesExactlyOneOuterHeaderAndCrossBounds(t *testing.T) {
	h := strings.Repeat("a", 64)
	c := residentprotocol.ContextData{Generation: strings.Repeat("b", 32), DebitID: h, LaunchKeySHA256: h, ProfileSHA256: h, ImageSHA256: h, ImageBytes: 1, SettingsSHA256: h, EnvironmentSHA256: h, IntentSHA256: h, PreparedCommitmentSHA256: h, ServiceJournalBytes: 1024, ServiceJournalSHA256: h, FooterBytes: 1024, FooterSHA256: h, LaunchJournalBytes: 1056, LaunchJournalSHA256: h}
	sizes, err := p1HandoffCapacityData(c)
	if err != nil {
		t.Fatal(err)
	}
	var key, nonce [32]byte
	ack, _ := residentprotocol.EncodeBoundAckPayloadData(key, c)
	challenge, _ := residentprotocol.EncodeCurrentChallengePayloadData(key, c, nonce)
	proof, _ := residentprotocol.EncodeCurrentProofPayloadData(key, c, nonce)
	for i, payload := range [][]byte{ack, challenge, proof} {
		frame, err := residentrefs.EncodeFrameData(payload)
		if err != nil || len(frame) != sizes[i] || len(frame) != len(payload)+36 || len(frame) > residentrefs.MaxWireBytes {
			t.Fatal(i, err)
		}
	}
	if residentprotocol.MaxFooterBytes != residentbirth.MaxP1RootBoundFooterBytes || residentprotocol.MaxLaunchJournalBytes != residentbirth.MaxP1LaunchJournalBytes || residentprotocol.MaxServiceJournalBytes != residentservice.MaxJournalBytes {
		t.Fatal("closed byte bound drift")
	}
	c.LaunchJournalBytes = c.FooterBytes
	if _, err := p1HandoffCapacityData(c); err != residentprotocol.ErrInvalid {
		t.Fatal("missing original intent accepted", err)
	}
}
func TestHandoffPreDebitCapacityDataRequiresKnownEnvAndImage(t *testing.T) {
	h := strings.Repeat("a", 64)
	inputs := residentservice.P1LaunchInputsData{Home: "/synthetic", Build: residentservice.Build{SHA256: h, Size: 1, GOOS: "linux", GOARCH: "amd64", GoBuild: "synthetic-data", Protocol: 1}, ProfileSHA256: h, EnvironmentSHA256: h, GrantJournalBytes: 1}
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
	key := strings.Repeat("\x01", 128)
	if _, err := p1BootstrapCapacityData(inputs, settings, key); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"environment", "image"} {
		t.Run(name, func(t *testing.T) {
			bad := inputs
			if name == "environment" {
				bad.EnvironmentSHA256 = ""
			} else {
				bad.Build.Size = residentprotocol.MaxImageBytes + 1
			}
			if _, err := p1BootstrapCapacityData(bad, settings, key); err == nil {
				t.Fatal("unknown/oversize known selector accepted")
			}
		})
	}
}
