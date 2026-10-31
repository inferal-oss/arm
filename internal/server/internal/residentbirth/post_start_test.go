package residentbirth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/server/internal/residentservice"
)

func parentPostStartJournalDataOnly(t *testing.T) ([]byte, []byte, residentservice.P1ParentCurrentServiceData, residentservice.P1ServiceSelectors, mesh.ResidentImageInventory) {
	t.Helper()
	intent := []byte("synthetic-original-intent")
	h := strings.Repeat("a", 64)
	home := residentservice.NativeID{Device: 1, Inode: 1, UID: 1}
	own := residentservice.P1ServiceSelectors{Home: "/synthetic", HomeID: home, DebitID: h, LaunchKey: "synthetic-data-key", ProfileSHA256: h, Build: residentservice.Build{SHA256: h, Size: 1}}
	bound := residentservice.P1RootBoundData{Record: residentservice.RootBoundRecord{DebitID: h, LaunchKey: own.LaunchKey, Generation: strings.Repeat("b", 32), CommitmentSHA256: h}, ProfileSHA256: h, ImageSHA256: h, HomeID: home, JournalBytes: 300, JournalSHA256: h}
	ih := sha256.Sum256(intent)
	footerData, err := P1FooterForServiceData(bound, hex.EncodeToString(ih[:]), h)
	if err != nil {
		t.Fatal(err)
	}
	footer, err := EncodeP1RootBoundFooterData(footerData)
	if err != nil {
		t.Fatal(err)
	}
	current := append(append([]byte(nil), intent...), footer...)
	fh, jh := sha256.Sum256(footer), sha256.Sum256(current)
	key, err := P1LaunchKeyCommitmentData(own.LaunchKey)
	if err != nil {
		t.Fatal(err)
	}
	c := residentprotocol.ContextData{Generation: bound.Record.Generation, DebitID: h, LaunchKeySHA256: key, ProfileSHA256: h, ImageSHA256: h, ImageBytes: 1, SettingsSHA256: h, EnvironmentSHA256: h, IntentSHA256: hex.EncodeToString(ih[:]), PreparedCommitmentSHA256: h, ServiceJournalBytes: 300, ServiceJournalSHA256: h, FooterBytes: int64(len(footer)), FooterSHA256: hex.EncodeToString(fh[:]), LaunchJournalBytes: int64(len(current)), LaunchJournalSHA256: hex.EncodeToString(jh[:])}
	image := mesh.ResidentImageInventory{Home: own.Home, DebitID: h, SHA256: h, Size: 1, HomeID: mesh.ResidentImageInventoryID{Device: 1, Inode: 1, UID: 1}}
	return current, intent, residentservice.P1ParentCurrentServiceData{Context: c, HomeID: home, RootBound: bound}, own, image
}

// SYNTHETIC serialized DATA only: no native original/service observation is
// constructed, and no successful postStart/Start/Root authority is inferred.
func TestP1ParentPostStartJournalDataRequiresOwnedIntentServiceAndContext(t *testing.T) {
	data, intent, service, own, image := parentPostStartJournalDataOnly(t)
	got, err := validateParentPostStartJournalData(data, intent, service, own, image)
	if err != nil || got.JournalBytes != len(data) || got.FooterBytes != len(data)-len(intent) || got.JournalSHA256 != service.Context.LaunchJournalSHA256 {
		t.Fatal("valid serialized DATA refused", err)
	}
	for _, name := range []string{"empty", "overflow", "truncated", "extended", "intent", "footer", "service-generation", "service-commitment", "service-sha", "service-count", "own-debit", "own-key", "own-profile", "own-image-bytes", "inventory-image", "home", "inventory-home", "context-generation", "context-footer-hash", "context-journal-hash", "context-intent-hash", "context-settings", "footer-count", "journal-count"} {
		t.Run(name, func(t *testing.T) {
			data, intent, service, own, image := parentPostStartJournalDataOnly(t)
			other := strings.Repeat("c", 64)
			switch name {
			case "empty":
				data = nil
			case "overflow":
				data = make([]byte, MaxP1LaunchJournalBytes+1)
			case "truncated":
				data = data[:len(data)-1]
			case "extended":
				data = append(data, 'x')
			case "intent":
				intent[0] ^= 1
			case "footer":
				data[len(data)-1] ^= 1
			case "service-generation":
				service.RootBound.Record.Generation = strings.Repeat("c", 32)
			case "service-commitment":
				service.RootBound.Record.CommitmentSHA256 = other
			case "service-sha":
				service.RootBound.JournalSHA256 = other
			case "service-count":
				service.RootBound.JournalBytes++
			case "own-debit":
				own.DebitID = other
			case "own-key":
				own.LaunchKey = "different-data-key"
			case "own-profile":
				own.ProfileSHA256 = other
			case "own-image-bytes":
				own.Build.Size++
			case "inventory-image":
				image.SHA256 = other
			case "home":
				service.HomeID.Inode++
			case "inventory-home":
				image.HomeID.Inode++
			case "context-generation":
				service.Context.Generation = strings.Repeat("c", 32)
			case "context-footer-hash":
				service.Context.FooterSHA256 = other
			case "context-journal-hash":
				service.Context.LaunchJournalSHA256 = other
			case "context-intent-hash":
				service.Context.IntentSHA256 = other
			case "context-settings":
				service.Context.SettingsSHA256 = other
			case "footer-count":
				service.Context.FooterBytes++
			case "journal-count":
				service.Context.LaunchJournalBytes++
			}
			if _, err := validateParentPostStartJournalData(data, intent, service, own, image); err != errOriginalFence {
				t.Fatal("mutated serialized DATA accepted", err)
			}
		})
	}
}

func TestP1ParentPostStartRefusesFlagsWithoutRetainedOriginalOperations(t *testing.T) {
	h := &FreshFence{}
	o := &fenceOwner{self: h, group: &residentservice.P1ServiceGroup{}, command: &exec.Cmd{}}
	h.owner = o
	o.state = &fenceNative{owner: o}
	prepared := &PreparedFence{owner: o}
	o.prepared = prepared
	// Adversarial inert flags, not successful originals. Missing actual operation
	// identities must refuse before any service/native/IO/process observation.
	o.bound, o.intentCommitted, o.writerWithdrawn, o.shortSealTried, o.beforeStartTried = true, true, true, true, true
	o.intent = []byte("synthetic-only")
	o.verification = append([]byte(nil), o.intent...)
	o.written = len(o.intent)
	copyFence, copyPrepared := *h, *prepared // neither wrapper contains a mutex
	for _, f := range []*FreshFence{nil, {}, &copyFence, h} {
		if data, err := f.ValidateP1PostStartOriginal(context.Background(), prepared, o.command, &residentservice.P1ParentCurrentObservation{}); err != errOriginalFence || data != (P1ParentPostStartData{}) {
			t.Fatal("flags became postStart authority", err)
		}
		if data, err := f.P1PostStartOriginalData(prepared, o.command, &residentservice.P1ParentCurrentObservation{}); err != errOriginalFence || data != (P1ParentPostStartData{}) {
			t.Fatal("unissued completed call accepted", err)
		}
	}
	if _, err := h.ValidateP1PostStartOriginal(context.Background(), &copyPrepared, o.command, &residentservice.P1ParentCurrentObservation{}); err != errOriginalFence {
		t.Fatal("copied prepared wrapper accepted", err)
	}
	if o.postStartTried || o.postStart != nil || o.active != nil || o.firstError != nil || o.withdrawOriginal != nil || o.shortSealOriginal != nil || len(o.writes) != 0 || o.opened != 0 || o.closed != 0 {
		t.Fatal("refusal changed original custody/history")
	}
	o.active = &fenceOperation{owner: o, kind: "short-seal"}
	if _, err := h.ValidateP1PostStartOriginal(context.Background(), prepared, o.command, &residentservice.P1ParentCurrentObservation{}); err != ErrFencePending || o.postStartTried {
		t.Fatal("pending original accepted", err)
	}
}

func TestP1ParentPostStartSelectorRefusesPendingAndAbnormalFinishMetadata(t *testing.T) {
	h := &FreshFence{}
	o := &fenceOwner{self: h, command: &exec.Cmd{}, group: &residentservice.P1ServiceGroup{}}
	h.owner = o
	prepared := &PreparedFence{owner: o}
	o.prepared = prepared
	service := &residentservice.P1ParentCurrentObservation{}
	c := &p1ParentPostStartCall{owner: o, prepared: prepared, command: o.command, group: o.group, service: service, normalReturned: true}
	o.postStart = c
	for _, op := range []*fenceOperation{nil, {}, {owner: o, ctx: context.Background(), kind: "p1-post-start"}, {owner: o, ctx: context.Background(), kind: "p1-post-start", normalFinished: true, abnormal: true}, {owner: o, ctx: context.Background(), kind: "p1-post-start", normalFinished: true, finalError: errOriginalFence}} {
		c.op = op
		if data, err := h.P1PostStartOriginalData(prepared, o.command, service); err != errOriginalFence || data != (P1ParentPostStartData{}) {
			t.Fatal("pending/abnormal/error metadata accepted", err)
		}
	}
	if o.active != nil || o.firstError != nil || o.postStartTried || o.state != nil {
		t.Fatal("selector caused native or original mutation")
	}
}
