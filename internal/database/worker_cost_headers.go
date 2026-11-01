package database

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
		{Name: "WorkerCurrentDatabaseData", Bytes: uint64(unsafe.Sizeof(WorkerCurrentDatabaseData{}))},
		{Name: "WorkerPreparedDatabaseData", Bytes: uint64(unsafe.Sizeof(WorkerPreparedDatabaseData{}))},
		{Name: "WorkerResidentConnectorInspection", Bytes: uint64(unsafe.Sizeof(WorkerResidentConnectorInspection{}))},
		{Name: "WorkerResidentDatabaseBinding", Bytes: uint64(unsafe.Sizeof(WorkerResidentDatabaseBinding{}))},
		{Name: "WorkerResidentDatabaseInspection", Bytes: uint64(unsafe.Sizeof(WorkerResidentDatabaseInspection{}))},
		{Name: "WorkerResidentOpen", Bytes: uint64(unsafe.Sizeof(WorkerResidentOpen{}))},
		{Name: "WorkerResidentSQLCloseObservation", Bytes: uint64(unsafe.Sizeof(WorkerResidentSQLCloseObservation{}))},
		{Name: "workerCurrentDatabaseCall", Bytes: uint64(unsafe.Sizeof(workerCurrentDatabaseCall{}))},
		{Name: "workerPreparedDatabaseCall", Bytes: uint64(unsafe.Sizeof(workerPreparedDatabaseCall{}))},
		{Name: "workerPreparedDatabaseRow", Bytes: uint64(unsafe.Sizeof(workerPreparedDatabaseRow{}))},
		{Name: "workerResidentConnectCall", Bytes: uint64(unsafe.Sizeof(workerResidentConnectCall{}))},
		{Name: "workerResidentConnector", Bytes: uint64(unsafe.Sizeof(workerResidentConnector{}))},
		{Name: "workerResidentSQLAbnormal", Bytes: uint64(unsafe.Sizeof(workerResidentSQLAbnormal{}))},
		{Name: "workerResidentSQLFD", Bytes: uint64(unsafe.Sizeof(workerResidentSQLFD{}))},
		{Name: "workerResidentSQLFileObservation", Bytes: uint64(unsafe.Sizeof(workerResidentSQLFileObservation{}))},
		{Name: "workerResidentSQLFiles", Bytes: uint64(unsafe.Sizeof(workerResidentSQLFiles{}))},
		{Name: "workerResidentSQLIdentity", Bytes: uint64(unsafe.Sizeof(workerResidentSQLIdentity{}))},
		{Name: "workerResidentSQLOperation", Bytes: uint64(unsafe.Sizeof(workerResidentSQLOperation{}))},
	}
}
