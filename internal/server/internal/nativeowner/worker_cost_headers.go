package nativeowner

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
		{Name: "NativeClosure", Bytes: uint64(unsafe.Sizeof(NativeClosure{}))},
		{Name: "ServeIssuer", Bytes: uint64(unsafe.Sizeof(ServeIssuer{}))},
		{Name: "WorkerInspection", Bytes: uint64(unsafe.Sizeof(WorkerInspection{}))},
		{Name: "WorkerManagerClaim", Bytes: uint64(unsafe.Sizeof(WorkerManagerClaim{}))},
		{Name: "WorkerManagerView", Bytes: uint64(unsafe.Sizeof(WorkerManagerView{}))},
		{Name: "WorkerServeIssuer", Bytes: uint64(unsafe.Sizeof(WorkerServeIssuer{}))},
		{Name: "WorkerServerClaim", Bytes: uint64(unsafe.Sizeof(WorkerServerClaim{}))},
		{Name: "WorkerServerView", Bytes: uint64(unsafe.Sizeof(WorkerServerView{}))},
		{Name: "WorkerSnapshot", Bytes: uint64(unsafe.Sizeof(WorkerSnapshot{}))},
		{Name: "nativeAbnormal", Bytes: uint64(unsafe.Sizeof(nativeAbnormal{}))},
		{Name: "nativeEffect", Bytes: uint64(unsafe.Sizeof(nativeEffect{}))},
		{Name: "nativeOwner", Bytes: uint64(unsafe.Sizeof(nativeOwner{}))},
	}
}
