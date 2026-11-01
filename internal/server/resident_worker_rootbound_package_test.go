package server

import (
	"bytes"
	"strings"
	"testing"

	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

// Only bounded synthetic DATA enters the production pre-append calculation and
// actual frame/footer codecs. No grant, original prepared owner, FD or native
// append is fabricated or executed by these cross-component regression checks.
func workerRootBoundPackageFixtureData() (residentservice.WorkerPreparedServiceData, residentWorkerPreparedCommitmentData, residentservice.WorkerRootBoundRecord) {
	service := residentservice.WorkerPreparedServiceData{HomeID: residentservice.NativeID{Device: 1, Inode: 2, UID: 1}, ProfileSHA256: strings.Repeat("1", 64), Build: residentservice.Build{SHA256: strings.Repeat("2", 64)}, InitialPackageSHA256: strings.Repeat("6", 64), InitialManifestSHA256: strings.Repeat("7", 64), BirthJournalBytes: 1024, BirthJournalSHA256: strings.Repeat("3", 64)}
	commitment := residentWorkerPreparedCommitmentData{InitialPackageSHA256: service.InitialPackageSHA256, InitialManifestSHA256: service.InitialManifestSHA256}
	record := residentservice.WorkerRootBoundRecord{DebitID: strings.Repeat("4", 64), LaunchKey: "synthetic-package-regression", Generation: strings.Repeat("5", 32), CommitmentSHA256: strings.Repeat("8", 64), ContractSHA256: strings.Repeat("9", 64)}
	return service, commitment, record
}

func TestWorkerExpectedRootBoundCarriesPreparedPackageThroughActualFooterCodecsData(t *testing.T) {
	service, commitment, record := workerRootBoundPackageFixtureData()
	frame, err := residentservice.EncodeWorkerRootBoundFrameData(record, service.BirthJournalSHA256)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := workerExpectedRootBoundServiceData(service, commitment, record, frame)
	if err != nil {
		t.Fatal(err)
	}
	if expected.Record != record || expected.InitialPackageSHA256 != service.InitialPackageSHA256 || expected.InitialManifestSHA256 != service.InitialManifestSHA256 || expected.HomeID != service.HomeID || expected.JournalBytes != service.BirthJournalBytes+int64(len(frame)) {
		t.Fatal("expected service lost original prepared binding")
	}
	footer, err := residentbirth.WorkerFooterForServiceData(expected, strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := residentbirth.EncodeWorkerRootBoundFooterData(footer)
	if err != nil {
		t.Fatal("complete valid prepared package failed before append", err)
	}
	decoded, err := residentbirth.DecodeWorkerRootBoundFooterData(encoded)
	if err != nil || decoded != footer || decoded.InitialPackageSHA256 != service.InitialPackageSHA256 || decoded.InitialManifestSHA256 != service.InitialManifestSHA256 {
		t.Fatal("footer did not roundtrip exact package commitments", err)
	}
	again, err := residentbirth.EncodeWorkerRootBoundFooterData(decoded)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatal("noncanonical footer")
	}
}

func TestWorkerExpectedRootBoundRefusesMissingOrMutatedPackageBeforeAppendData(t *testing.T) {
	for _, kind := range []string{"missing-package", "missing-manifest", "missing-both", "mutated-package", "mutated-manifest", "swapped-commitments"} {
		t.Run(kind, func(t *testing.T) {
			service, commitment, record := workerRootBoundPackageFixtureData()
			switch kind {
			case "missing-package":
				service.InitialPackageSHA256 = ""
			case "missing-manifest":
				service.InitialManifestSHA256 = ""
			case "missing-both":
				service.InitialPackageSHA256 = ""
				service.InitialManifestSHA256 = ""
				commitment.InitialPackageSHA256 = ""
				commitment.InitialManifestSHA256 = ""
			case "mutated-package":
				service.InitialPackageSHA256 = strings.Repeat("c", 64)
			case "mutated-manifest":
				service.InitialManifestSHA256 = strings.Repeat("d", 64)
			case "swapped-commitments":
				service.InitialPackageSHA256, service.InitialManifestSHA256 = service.InitialManifestSHA256, service.InitialPackageSHA256
			}
			frame, err := residentservice.EncodeWorkerRootBoundFrameData(record, service.BirthJournalSHA256)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := workerExpectedRootBoundServiceData(service, commitment, record, frame)
			if err == nil {
				footer, footerErr := residentbirth.WorkerFooterForServiceData(expected, strings.Repeat("a", 64), strings.Repeat("b", 64))
				if footerErr == nil {
					_, footerErr = residentbirth.EncodeWorkerRootBoundFooterData(footer)
				}
				if footerErr == nil {
					t.Fatal("missing or changed prepared package passed actual pre-append codec chain")
				}
			}
		})
	}
}
