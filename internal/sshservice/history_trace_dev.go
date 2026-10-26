//go:build arm_devtrace

package sshservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Development observation only: the callback has no cleanup authority. Tests
// install it serially; normal builds erase this observer at compile time.
var historyCleanupObserver func(historyCleanupObservation)

type historyCleanupObservation struct {
	Stage         string                 `json:"stage"`
	Expected      SourceHistoryOperation `json:"-"`
	Current       SourceHistoryOperation `json:"-"`
	Row           string                 `json:"row"`
	ExpectedRow   string                 `json:"expectedRow"`
	Absent        []bool                 `json:"absent"`
	PendingAbsent []bool                 `json:"pendingAbsent"`
	Worker        string                 `json:"worker"`
	WorkerOwner   string                 `json:"workerOwner"`
	WorkerFD      int                    `json:"workerFD"`
	WorkerCurrent bool                   `json:"workerCurrent"`
	ColdCurrent   bool                   `json:"coldCurrent"`
	Context       bool                   `json:"context"`
	ContextOwner  string                 `json:"contextOwner"`
	Deadline      string                 `json:"deadline"`
	RemainingNS   int64                  `json:"remainingNS"`
	Bounded       bool                   `json:"bounded"`
	Work          int                    `json:"work"`
}

func historyCleanupObserve(stage string, ctx context.Context, home string, expected SourceHistoryOperation, worker *historyWorker, work int) {
	if historyCleanupObserver == nil {
		return
	}
	v := historyCleanupObservation{Stage: stage, Expected: expected, ExpectedRow: stateHash([]byte(mustHistoryJSON(expected))), Context: ctx.Err() == nil, Work: work}
	deadline, bounded := ctx.Deadline()
	v.Bounded = bounded
	if bounded {
		v.ContextOwner = fmt.Sprintf("%p", ctx)
		v.Deadline = deadline.Format(time.RFC3339Nano)
		v.RemainingNS = time.Until(deadline).Nanoseconds()
	}
	db, e := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
	if e != nil {
		panic(e)
	}
	defer db.Close()
	var raw string
	if e = db.QueryRow(`SELECT data FROM source_history_operations WHERE key=?`, expected.Request.Key).Scan(&raw); e != nil {
		panic(e)
	}
	if e = json.Unmarshal([]byte(raw), &v.Current); e != nil {
		panic(e)
	}
	v.Row = stateHash([]byte(mustHistoryJSON(v.Current)))
	v.ColdCurrent = true
	for _, m := range expected.Request.Plan.Members {
		var source, kind, pack, record, sha, intent string
		err := db.QueryRow(`SELECT source,kind,pack,record,sha256,intent_sha256 FROM source_history_refs WHERE target=? AND key=?`, m.Target, m.Key).Scan(&source, &kind, &pack, &record, &sha, &intent)
		if err != nil || source != m.Source || kind != m.Kind || pack != expected.PackID || record != mustHistoryJSON(sourceHistoryKey(m)) || sha != m.SHA256 || intent != m.IntentSHA256 {
			v.ColdCurrent = false
		}
		_, e = os.Lstat(filepath.Join(home, "mesh", m.File))
		v.Absent = append(v.Absent, os.IsNotExist(e))
		if m.PendingFile == "" {
			v.PendingAbsent = append(v.PendingAbsent, true)
		} else {
			_, e = os.Lstat(filepath.Join(home, "mesh", m.PendingFile))
			v.PendingAbsent = append(v.PendingAbsent, os.IsNotExist(e))
		}
	}
	if worker != nil {
		v.WorkerOwner = fmt.Sprintf("%p", worker)
		v.WorkerFD = int(worker.file.Fd())
		id, e := statState(worker.file)
		if e == nil {
			v.Worker = mustHistoryJSON(historyID(id))
			named, e := os.Lstat(filepath.Join(home, SourceHistoryName, sourceHistoryWorkerLockName))
			v.WorkerCurrent = e == nil && os.SameFile(idInfo(worker.file), named)
		}
	}
	historyCleanupObserver(v)
}
func idInfo(f *os.File) os.FileInfo {
	v, e := f.Stat()
	if e != nil {
		panic(e)
	}
	return v
}
