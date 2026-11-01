package server

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
		{Name: "Server", Bytes: uint64(unsafe.Sizeof(Server{}))},
		{Name: "residentWorkerChildOperation", Bytes: uint64(unsafe.Sizeof(residentWorkerChildOperation{}))},
		{Name: "residentWorkerChildPending", Bytes: uint64(unsafe.Sizeof(residentWorkerChildPending{}))},
		{Name: "residentWorkerCoreEntry", Bytes: uint64(unsafe.Sizeof(residentWorkerCoreEntry{}))},
		{Name: "residentWorkerCoreEntryCall", Bytes: uint64(unsafe.Sizeof(residentWorkerCoreEntryCall{}))},
		{Name: "residentWorkerCurrentServerData", Bytes: uint64(unsafe.Sizeof(residentWorkerCurrentServerData{}))},
		{Name: "residentWorkerPreparedCommitmentData", Bytes: uint64(unsafe.Sizeof(residentWorkerPreparedCommitmentData{}))},
		{Name: "residentWorkerPreparedServerData", Bytes: uint64(unsafe.Sizeof(residentWorkerPreparedServerData{}))},
		{Name: "residentWorkerProtocolPhase", Bytes: uint64(unsafe.Sizeof(residentWorkerProtocolPhase{}))},
		{Name: "residentWorkerProtocolResult", Bytes: uint64(unsafe.Sizeof(residentWorkerProtocolResult{}))},
		{Name: "residentWorkerRootBoundCall", Bytes: uint64(unsafe.Sizeof(residentWorkerRootBoundCall{}))},
		{Name: "residentWorkerRootCompletionCheck", Bytes: uint64(unsafe.Sizeof(residentWorkerRootCompletionCheck{}))},
		{Name: "residentWorkerRootPreparedCall", Bytes: uint64(unsafe.Sizeof(residentWorkerRootPreparedCall{}))},
		{Name: "residentWorkerRootProtocolCall", Bytes: uint64(unsafe.Sizeof(residentWorkerRootProtocolCall{}))},
		{Name: "residentWorkerServerAbnormal", Bytes: uint64(unsafe.Sizeof(residentWorkerServerAbnormal{}))},
		{Name: "residentWorkerServerBinding", Bytes: uint64(unsafe.Sizeof(residentWorkerServerBinding{}))},
		{Name: "residentWorkerServerCurrentCall", Bytes: uint64(unsafe.Sizeof(residentWorkerServerCurrentCall{}))},
		{Name: "residentWorkerServerInspection", Bytes: uint64(unsafe.Sizeof(residentWorkerServerInspection{}))},
		{Name: "residentWorkerServerOperation", Bytes: uint64(unsafe.Sizeof(residentWorkerServerOperation{}))},
		{Name: "residentWorkerServerPending", Bytes: uint64(unsafe.Sizeof(residentWorkerServerPending{}))},
		{Name: "residentWorkerServerPreparedCall", Bytes: uint64(unsafe.Sizeof(residentWorkerServerPreparedCall{}))},
		{Name: "workerRootBootstrapData", Bytes: uint64(unsafe.Sizeof(workerRootBootstrapData{}))},
	}
}
