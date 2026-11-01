package residentrefs

import (
	"getarm.org/internal/residentcost"
	"unsafe"
)

// WorkerOwnHeadersData reports only compiled ARM struct sizes. Each declared
// worker owner/record header is listed explicitly; no reflection into stdlib,
// native, driver, runtime or heap internals and no native effects occur here.
// Dynamic buffers/maps/strings and helper internals are separate source and
// unknown reservations. These DATA create neither funding nor original custody.
func WorkerOwnHeadersData() []residentcost.OwnHeaderData {
	return []residentcost.OwnHeaderData{
		{Name: "ChildPending", Bytes: uint64(unsafe.Sizeof(ChildPending{}))},
		{Name: "ComposedIdentities", Bytes: uint64(unsafe.Sizeof(ComposedIdentities{}))},
		{Name: "ControlOriginal", Bytes: uint64(unsafe.Sizeof(ControlOriginal{}))},
		{Name: "FenceOriginal", Bytes: uint64(unsafe.Sizeof(FenceOriginal{}))},
		{Name: "Identity", Bytes: uint64(unsafe.Sizeof(Identity{}))},
		{Name: "ImageOriginal", Bytes: uint64(unsafe.Sizeof(ImageOriginal{}))},
		{Name: "Inspection", Bytes: uint64(unsafe.Sizeof(Inspection{}))},
		{Name: "P1ChallengeRead", Bytes: uint64(unsafe.Sizeof(P1ChallengeRead{}))},
		{Name: "ServiceOriginal", Bytes: uint64(unsafe.Sizeof(ServiceOriginal{}))},
		{Name: "WorkerChallengeRead", Bytes: uint64(unsafe.Sizeof(WorkerChallengeRead{}))},
		{Name: "WorkerLauncherOriginal", Bytes: uint64(unsafe.Sizeof(WorkerLauncherOriginal{}))},
		{Name: "abnormalOriginal", Bytes: uint64(unsafe.Sizeof(abnormalOriginal{}))},
		{Name: "childState", Bytes: uint64(unsafe.Sizeof(childState{}))},
		{Name: "controlIOCall", Bytes: uint64(unsafe.Sizeof(controlIOCall{}))},
		{Name: "controlPhaseCall", Bytes: uint64(unsafe.Sizeof(controlPhaseCall{}))},
		{Name: "originalEntry", Bytes: uint64(unsafe.Sizeof(originalEntry{}))},
		{Name: "originalOperation", Bytes: uint64(unsafe.Sizeof(originalOperation{}))},
		{Name: "originalWaiter", Bytes: uint64(unsafe.Sizeof(originalWaiter{}))},
		{Name: "recipientState", Bytes: uint64(unsafe.Sizeof(recipientState{}))},
		{Name: "workerControlIOCall", Bytes: uint64(unsafe.Sizeof(workerControlIOCall{}))},
		{Name: "workerControlPhaseCall", Bytes: uint64(unsafe.Sizeof(workerControlPhaseCall{}))},
	}
}
