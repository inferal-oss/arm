package manager

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
		{Name: "Manager", Bytes: uint64(unsafe.Sizeof(Manager{}))},
		{Name: "ResidentWorkerAuthority", Bytes: uint64(unsafe.Sizeof(ResidentWorkerAuthority{}))},
		{Name: "ResidentWorkerCurrentManagerData", Bytes: uint64(unsafe.Sizeof(ResidentWorkerCurrentManagerData{}))},
		{Name: "ResidentWorkerManagerInspection", Bytes: uint64(unsafe.Sizeof(ResidentWorkerManagerInspection{}))},
		{Name: "ResidentWorkerManagerPending", Bytes: uint64(unsafe.Sizeof(ResidentWorkerManagerPending{}))},
		{Name: "ResidentWorkerManagerServerBinding", Bytes: uint64(unsafe.Sizeof(ResidentWorkerManagerServerBinding{}))},
		{Name: "ResidentWorkerPreparedManagerData", Bytes: uint64(unsafe.Sizeof(ResidentWorkerPreparedManagerData{}))},
		{Name: "RunRequest", Bytes: uint64(unsafe.Sizeof(RunRequest{}))},
		{Name: "job", Bytes: uint64(unsafe.Sizeof(job{}))},
		{Name: "originalJobSettlement", Bytes: uint64(unsafe.Sizeof(originalJobSettlement{}))},
		{Name: "residentWorkerCurrentStandingData", Bytes: uint64(unsafe.Sizeof(residentWorkerCurrentStandingData{}))},
		{Name: "residentWorkerFreshFormatCall", Bytes: uint64(unsafe.Sizeof(residentWorkerFreshFormatCall{}))},
		{Name: "residentWorkerFreshQuery", Bytes: uint64(unsafe.Sizeof(residentWorkerFreshQuery{}))},
		{Name: "residentWorkerFreshScan", Bytes: uint64(unsafe.Sizeof(residentWorkerFreshScan{}))},
		{Name: "residentWorkerManagerAbnormal", Bytes: uint64(unsafe.Sizeof(residentWorkerManagerAbnormal{}))},
		{Name: "residentWorkerManagerCurrentCall", Bytes: uint64(unsafe.Sizeof(residentWorkerManagerCurrentCall{}))},
		{Name: "residentWorkerManagerOperation", Bytes: uint64(unsafe.Sizeof(residentWorkerManagerOperation{}))},
		{Name: "residentWorkerManagerPreparedCall", Bytes: uint64(unsafe.Sizeof(residentWorkerManagerPreparedCall{}))},
		{Name: "residentWorkerSchemaExec", Bytes: uint64(unsafe.Sizeof(residentWorkerSchemaExec{}))},
		{Name: "residentWorkerSchemaInstallCall", Bytes: uint64(unsafe.Sizeof(residentWorkerSchemaInstallCall{}))},
		{Name: "residentWorkerServiceBinding", Bytes: uint64(unsafe.Sizeof(residentWorkerServiceBinding{}))},
		{Name: "workerReservedObjectData", Bytes: uint64(unsafe.Sizeof(workerReservedObjectData{}))},
	}
}
