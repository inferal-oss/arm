package residentservice

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

type workerNativeRead struct {
	owner         *workerCustody
	kind          string
	before, after os.FileInfo
	data          []byte
	read, calls   int
	finished      bool
	firstError    error
}
type workerNativeAppend struct {
	owner                *workerCustody
	frame                []byte
	offset               int64
	written, calls       int
	syncTried, committed bool
	firstError           error
}

func (w *workerCustody) readJournalOriginal(ctx context.Context) (workerJournalData, error) {
	var empty workerJournalData
	if err := workerContextData(ctx); err != nil {
		return empty, err
	}
	if w == nil || w.self != w || w.raw == nil {
		return empty, ErrInvalid
	}
	r := w.raw
	r.mu.Lock()
	if !w.typedOriginalLocked() || !w.legacyEmptyLocked() || r.active == nil || r.active.owner != r || r.files.journal == nil || len(w.reads) >= 4 {
		r.mu.Unlock()
		return empty, ErrInvalid
	}
	for _, prior := range w.reads {
		if prior == nil || prior.owner != w || !prior.finished || prior.firstError != nil {
			r.mu.Unlock()
			return empty, ErrInvalid
		}
	}
	call := &workerNativeRead{owner: w, kind: r.active.kind}
	w.reads = append(w.reads, call)
	r.mu.Unlock()
	fail := func(err error) (workerJournalData, error) {
		r.mu.Lock()
		if call.firstError == nil {
			call.firstError = err
		}
		r.mu.Unlock()
		return empty, err
	}
	r.files.progress("worker_journal_stat_retained")
	before, err := r.files.journal.file.Stat()
	r.mu.Lock()
	call.before = before
	r.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	if before.Size() <= 0 || before.Size() > workerJournalMaxBytes {
		return fail(ErrInvalid)
	}
	data := make([]byte, int(before.Size()))
	r.mu.Lock()
	call.data = data
	r.mu.Unlock()
	for at, calls := 0, 0; at < len(data); calls++ {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		if calls >= 32 {
			return fail(ErrInvalid)
		}
		r.files.progress("worker_journal_original_read_pending")
		n, readErr := r.files.journal.file.ReadAt(data[at:], int64(at))
		at += n
		r.mu.Lock()
		call.read, call.calls = at, calls+1
		r.mu.Unlock()
		if readErr != nil && !(errors.Is(readErr, io.EOF) && at == len(data)) {
			return fail(readErr)
		}
		if n == 0 {
			return fail(io.ErrNoProgress)
		}
	}
	after, err := r.files.journal.file.Stat()
	r.mu.Lock()
	call.after = after
	r.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fail(ErrInvalid)
	}
	if err = privateNative(r.files.journal, false); err != nil {
		return fail(err)
	}
	decoded, err := decodeWorkerJournalData(data)
	if err != nil {
		return fail(err)
	}
	r.mu.Lock()
	call.finished = true
	r.mu.Unlock()
	return decoded, nil
}

func (w *workerCustody) appendJournalOriginal(ctx context.Context, next WorkerRecordData) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	if w == nil || w.self != w || w.raw == nil {
		return ErrInvalid
	}
	r := w.raw
	r.mu.Lock()
	valid := w.typedOriginalLocked() && w.legacyEmptyLocked() && r.active != nil && r.active.owner == r && r.files.journal != nil && w.append == nil
	r.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	candidate := append(append([]WorkerRecordData(nil), w.journal.records...), next)
	if ValidateWorkerLedgerData(candidate) != nil {
		return ErrInvalid
	}
	frame, err := encodeWorkerRecordFrameData(next, w.journal.last)
	if err != nil || w.journal.bytes < 0 || w.journal.bytes > workerJournalMaxBytes-int64(len(frame)) {
		return ErrInvalid
	}
	// Registration precedes even the native size check. No partial append can
	// be replaced or retried after a stat/write/sync error.
	call := &workerNativeAppend{owner: w, frame: frame, offset: w.journal.bytes}
	r.mu.Lock()
	w.append = call
	r.mu.Unlock()
	fail := func(err error) error {
		r.mu.Lock()
		if call.firstError == nil {
			call.firstError = err
		}
		r.mu.Unlock()
		return err
	}
	r.files.progress("worker_journal_preappend_stat_pending")
	s, err := r.files.journal.file.Stat()
	if err != nil {
		return fail(err)
	}
	if s.Size() != call.offset {
		return fail(ErrUnresolved)
	}
	for at := 0; at < len(frame); {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		r.mu.Lock()
		if call.calls >= 32 {
			r.mu.Unlock()
			return fail(io.ErrShortWrite)
		}
		call.calls++
		r.mu.Unlock()
		r.files.progress("worker_journal_original_write_pending")
		n, writeErr := r.files.journal.file.WriteAt(frame[at:], call.offset+int64(at))
		at += n
		r.mu.Lock()
		call.written = at
		r.mu.Unlock()
		if writeErr != nil {
			return fail(writeErr)
		}
		if n == 0 {
			return fail(io.ErrNoProgress)
		}
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	r.mu.Lock()
	call.syncTried = true
	r.mu.Unlock()
	r.files.progress("worker_journal_original_sync_pending")
	if err = r.files.journal.file.Sync(); err != nil {
		return fail(err)
	}
	r.mu.Lock()
	call.committed = true
	w.journal = workerJournalData{records: candidate, bytes: call.offset + int64(len(frame)), last: sha256.Sum256(frame)}
	r.mu.Unlock()
	return nil
}
