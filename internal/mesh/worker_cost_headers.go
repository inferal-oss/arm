package mesh

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
		{Name: "WorkerImageBindingInspection", Bytes: uint64(unsafe.Sizeof(WorkerImageBindingInspection{}))},
		{Name: "WorkerImageBindingPending", Bytes: uint64(unsafe.Sizeof(WorkerImageBindingPending{}))},
		{Name: "WorkerInheritedImagePending", Bytes: uint64(unsafe.Sizeof(WorkerInheritedImagePending{}))},
		{Name: "WorkerPinParentReferenceOwner", Bytes: uint64(unsafe.Sizeof(WorkerPinParentReferenceOwner{}))},
		{Name: "WorkerResidentImagePinPending", Bytes: uint64(unsafe.Sizeof(WorkerResidentImagePinPending{}))},
		{Name: "WorkerRuntimeProofData", Bytes: uint64(unsafe.Sizeof(WorkerRuntimeProofData{}))},
		{Name: "WorkerRuntimeView", Bytes: uint64(unsafe.Sizeof(WorkerRuntimeView{}))},
		{Name: "workerImageBindingOperation", Bytes: uint64(unsafe.Sizeof(workerImageBindingOperation{}))},
		{Name: "workerImageBindingState", Bytes: uint64(unsafe.Sizeof(workerImageBindingState{}))},
		{Name: "workerPinParentClose", Bytes: uint64(unsafe.Sizeof(workerPinParentClose{}))},
		{Name: "workerPinParentRetirement", Bytes: uint64(unsafe.Sizeof(workerPinParentRetirement{}))},
	}
}
