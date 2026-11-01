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

func workerParentPostStartJournalDataOnly(t *testing.T) ([]byte, []byte, residentservice.WorkerParentCurrentServiceData, residentservice.WorkerServiceSelectorsData, mesh.ResidentImageInventory) {
	t.Helper()
	intent := []byte("synthetic-original-intent")
	h := strings.Repeat("a", 64)
	home := residentservice.NativeID{Device: 1, Inode: 1, UID: 1}
	own := residentservice.WorkerServiceSelectorsData{Home: "/synthetic", HomeID: home, DebitID: h, LaunchKey: "synthetic-data-key", ProfileSHA256: h, ContractSHA256: h, Contract: residentservice.WorkerStandingContract{InitialPackageSHA256: h, InitialManifestSHA256: h}, Build: residentservice.Build{SHA256: h, Size: 1}}
	bound := residentservice.WorkerRootBoundData{Record: residentservice.WorkerRootBoundRecord{DebitID: h, LaunchKey: own.LaunchKey, Generation: strings.Repeat("b", 32), CommitmentSHA256: h, ContractSHA256: h}, ProfileSHA256: h, ImageSHA256: h, InitialPackageSHA256: h, InitialManifestSHA256: h, HomeID: home, JournalBytes: 300, JournalSHA256: h}
	ih := sha256.Sum256(intent)
	footerData, err := WorkerFooterForServiceData(bound, hex.EncodeToString(ih[:]), h)
	if err != nil {
		t.Fatal(err)
	}
	footer, err := EncodeWorkerRootBoundFooterData(footerData)
	if err != nil {
		t.Fatal(err)
	}
	current := append(append([]byte(nil), intent...), footer...)
	fh, jh := sha256.Sum256(footer), sha256.Sum256(current)
	key, err := WorkerLaunchKeyCommitmentData(own.LaunchKey)
	if err != nil {
		t.Fatal(err)
	}
	c := residentprotocol.WorkerContextData{WorkspaceFormat: 5, ContractSHA256: h, InitialPackageSHA256: h, InitialManifestSHA256: h, Generation: bound.Record.Generation, DebitID: h, LaunchKeySHA256: key, ProfileSHA256: h, ImageSHA256: h, ImageBytes: 1, SettingsSHA256: h, EnvironmentSHA256: h, IntentSHA256: hex.EncodeToString(ih[:]), PreparedCommitmentSHA256: h, ServiceJournalBytes: 300, ServiceJournalSHA256: h, FooterBytes: int64(len(footer)), FooterSHA256: hex.EncodeToString(fh[:]), LaunchJournalBytes: int64(len(current)), LaunchJournalSHA256: hex.EncodeToString(jh[:])}
	image := mesh.ResidentImageInventory{Home: own.Home, DebitID: h, SHA256: h, Size: 1, HomeID: mesh.ResidentImageInventoryID{Device: 1, Inode: 1, UID: 1}}
	return current, intent, residentservice.WorkerParentCurrentServiceData{Context: c, HomeID: home, RootBound: bound}, own, image
}

// SYNTHETIC serialized DATA only: no native original/service observation is
// constructed, and no successful postStart/Start/Root authority is inferred.
func TestWorkerParentPostStartJournalDataRequiresOwnedIntentServiceAndContext(t *testing.T) {
	data, intent, service, own, image := workerParentPostStartJournalDataOnly(t)
	got, err := validateWorkerParentPostStartJournalData(data, intent, service, own, image)
	if err != nil || got.JournalBytes != len(data) || got.FooterBytes != len(data)-len(intent) || got.JournalSHA256 != service.Context.LaunchJournalSHA256 {
		t.Fatal("valid serialized DATA refused", err)
	}
	for _, name := range []string{"empty", "overflow", "truncated", "extended", "intent", "footer", "service-generation", "service-commitment", "service-sha", "service-count", "own-debit", "own-key", "own-profile", "own-image-bytes", "inventory-image", "home", "inventory-home", "context-generation", "context-footer-hash", "context-journal-hash", "context-intent-hash", "context-settings", "footer-count", "journal-count"} {
		t.Run(name, func(t *testing.T) {
			data, intent, service, own, image := workerParentPostStartJournalDataOnly(t)
			other := strings.Repeat("c", 64)
			switch name {
			case "empty":
				data = nil
			case "overflow":
				data = make([]byte, MaxWorkerLaunchJournalBytes+1)
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
			if _, err := validateWorkerParentPostStartJournalData(data, intent, service, own, image); err != errOriginalWorkerFence {
				t.Fatal("mutated serialized DATA accepted", err)
			}
		})
	}
}

func TestWorkerParentPostStartRefusesFlagsWithoutRetainedOriginalOperations(t *testing.T) {
	h := &WorkerFreshFencePending{}
	o := &workerFenceOwner{self: h, group: &residentservice.WorkerServiceGroup{}, command: &exec.Cmd{}}
	h.owner = o
	o.state = &workerFenceNative{owner: o}
	prepared := &WorkerPreparedFence{owner: o}
	o.prepared = prepared
	// Adversarial inert flags, not successful originals. Missing actual operation
	// identities must refuse before any service/native/IO/process observation.
	o.bound, o.intentCommitted, o.writerWithdrawn, o.shortSealTried, o.beforeStartTried = true, true, true, true, true
	o.intent = []byte("synthetic-only")
	o.verification = append([]byte(nil), o.intent...)
	o.written = len(o.intent)
	copyFence, copyPrepared := *h, *prepared // neither wrapper contains a mutex
	for _, f := range []*WorkerFreshFencePending{nil, {}, &copyFence, h} {
		if data, err := f.ValidateWorkerPostStartOriginal(context.Background(), prepared, o.command, &residentservice.WorkerParentAckAuthentication{}); err != errOriginalWorkerFence || data != (WorkerParentPostStartData{}) {
			t.Fatal("flags became postStart authority", err)
		}
		if data, err := f.WorkerPostStartOriginalData(prepared, o.command, &residentservice.WorkerParentAckAuthentication{}); err != errOriginalWorkerFence || data != (WorkerParentPostStartData{}) {
			t.Fatal("unissued completed call accepted", err)
		}
	}
	if _, err := h.ValidateWorkerPostStartOriginal(context.Background(), &copyPrepared, o.command, &residentservice.WorkerParentAckAuthentication{}); err != errOriginalWorkerFence {
		t.Fatal("copied prepared wrapper accepted", err)
	}
	if o.postStartTried || o.postStart != nil || o.active != nil || o.firstError != nil || o.withdrawOriginal != nil || o.shortSealOriginal != nil || len(o.writes) != 0 || o.opened != 0 || o.closed != 0 {
		t.Fatal("refusal changed original custody/history")
	}
	o.active = &workerFenceOperation{owner: o, kind: "short-seal"}
	if _, err := h.ValidateWorkerPostStartOriginal(context.Background(), prepared, o.command, &residentservice.WorkerParentAckAuthentication{}); err != ErrWorkerFencePending || o.postStartTried {
		t.Fatal("pending original accepted", err)
	}
}

func TestWorkerParentPostStartSelectorRefusesPendingAndAbnormalFinishMetadata(t *testing.T) {
	h := &WorkerFreshFencePending{}
	o := &workerFenceOwner{self: h, command: &exec.Cmd{}, group: &residentservice.WorkerServiceGroup{}}
	h.owner = o
	prepared := &WorkerPreparedFence{owner: o}
	o.prepared = prepared
	service := &residentservice.WorkerParentAckAuthentication{}
	c := &workerParentPostStartCall{owner: o, prepared: prepared, command: o.command, group: o.group, service: service, normalReturned: true}
	o.postStart = c
	for _, op := range []*workerFenceOperation{nil, {}, {owner: o, ctx: context.Background(), kind: "worker-post-start"}, {owner: o, ctx: context.Background(), kind: "worker-post-start", normalFinished: true, abnormal: true}, {owner: o, ctx: context.Background(), kind: "worker-post-start", normalFinished: true, finalError: errOriginalWorkerFence}} {
		c.op = op
		if data, err := h.WorkerPostStartOriginalData(prepared, o.command, service); err != errOriginalWorkerFence || data != (WorkerParentPostStartData{}) {
			t.Fatal("pending/abnormal/error metadata accepted", err)
		}
	}
	if o.active != nil || o.firstError != nil || o.postStartTried || o.state != nil {
		t.Fatal("selector caused native or original mutation")
	}
}
