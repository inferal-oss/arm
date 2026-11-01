package residentbirth

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
		{Name: "WorkerCurrentLaunchData", Bytes: uint64(unsafe.Sizeof(WorkerCurrentLaunchData{}))},
		{Name: "WorkerFenceData", Bytes: uint64(unsafe.Sizeof(WorkerFenceData{}))},
		{Name: "WorkerFenceInspection", Bytes: uint64(unsafe.Sizeof(WorkerFenceInspection{}))},
		{Name: "WorkerFenceParentReferenceOwner", Bytes: uint64(unsafe.Sizeof(WorkerFenceParentReferenceOwner{}))},
		{Name: "WorkerFreshFencePending", Bytes: uint64(unsafe.Sizeof(WorkerFreshFencePending{}))},
		{Name: "WorkerInheritedFencePending", Bytes: uint64(unsafe.Sizeof(WorkerInheritedFencePending{}))},
		{Name: "WorkerLaunchJournal", Bytes: uint64(unsafe.Sizeof(WorkerLaunchJournal{}))},
		{Name: "WorkerParentPostStartData", Bytes: uint64(unsafe.Sizeof(WorkerParentPostStartData{}))},
		{Name: "WorkerPreparedFence", Bytes: uint64(unsafe.Sizeof(WorkerPreparedFence{}))},
		{Name: "WorkerRootBoundFooterData", Bytes: uint64(unsafe.Sizeof(WorkerRootBoundFooterData{}))},
		{Name: "WorkerRootBoundFooterResultData", Bytes: uint64(unsafe.Sizeof(WorkerRootBoundFooterResultData{}))},
		{Name: "workerCurrentFenceCall", Bytes: uint64(unsafe.Sizeof(workerCurrentFenceCall{}))},
		{Name: "workerFenceAbnormal", Bytes: uint64(unsafe.Sizeof(workerFenceAbnormal{}))},
		{Name: "workerFenceFD", Bytes: uint64(unsafe.Sizeof(workerFenceFD{}))},
		{Name: "workerFenceNative", Bytes: uint64(unsafe.Sizeof(workerFenceNative{}))},
		{Name: "workerFenceOperation", Bytes: uint64(unsafe.Sizeof(workerFenceOperation{}))},
		{Name: "workerFenceOwner", Bytes: uint64(unsafe.Sizeof(workerFenceOwner{}))},
		{Name: "workerFenceParentClose", Bytes: uint64(unsafe.Sizeof(workerFenceParentClose{}))},
		{Name: "workerFenceParentRetirement", Bytes: uint64(unsafe.Sizeof(workerFenceParentRetirement{}))},
		{Name: "workerFenceWrite", Bytes: uint64(unsafe.Sizeof(workerFenceWrite{}))},
		{Name: "workerFooterIO", Bytes: uint64(unsafe.Sizeof(workerFooterIO{}))},
		{Name: "workerInheritedFenceOperation", Bytes: uint64(unsafe.Sizeof(workerInheritedFenceOperation{}))},
		{Name: "workerInheritedFenceState", Bytes: uint64(unsafe.Sizeof(workerInheritedFenceState{}))},
		{Name: "workerParentPostStartCall", Bytes: uint64(unsafe.Sizeof(workerParentPostStartCall{}))},
		{Name: "workerParentPostStartHeld", Bytes: uint64(unsafe.Sizeof(workerParentPostStartHeld{}))},
		{Name: "workerParentPostStartName", Bytes: uint64(unsafe.Sizeof(workerParentPostStartName{}))},
		{Name: "workerRootBoundFooterCall", Bytes: uint64(unsafe.Sizeof(workerRootBoundFooterCall{}))},
	}
}
