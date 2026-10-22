package sharelease

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

func readManifestParts(ctx context.Context, tx *sql.Tx, publications map[string]Publication) error {
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN length(CAST(share AS BLOB))<=32 THEN share END,CASE WHEN length(CAST(generation AS BLOB))<=32 THEN generation END,part,length(CAST(data AS BLOB)) FROM share_manifest_parts LIMIT ?`, MaxPublications*MaxManifestParts+1)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	sizes := map[string]int{}
	total := 0
	for rows.Next() {
		var share, generation string
		var part, size int
		if err = rows.Scan(&share, &generation, &part, &size); err != nil {
			rows.Close()
			return err
		}
		total++
		p, ok := publications[share]
		if total > MaxPublications*MaxManifestParts || !ok || p.Summary == nil || generation != p.Generation || part < 0 || part >= p.Summary.Parts || size < 1 || size > MaxManifestEncodedPartBytes {
			rows.Close()
			return fmt.Errorf("orphan/oversized manifest part")
		}
		counts[share]++
		sizes[share] += size
		if sizes[share] > p.Summary.StoredBytes {
			rows.Close()
			return fmt.Errorf("manifest stored-byte roster exceeded")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range publications {
		if p.Summary == nil {
			if counts[p.ID] != 0 {
				return fmt.Errorf("unprepared publication has manifest parts")
			}
			continue
		}
		if counts[p.ID] != p.Summary.Parts || sizes[p.ID] != p.Summary.StoredBytes {
			return fmt.Errorf("incomplete complete-manifest part roster")
		}
		entries, err := tx.QueryContext(ctx, `SELECT share,generation,part,data FROM share_manifest_parts WHERE share=? ORDER BY part LIMIT ?`, p.ID, MaxManifestParts+1)
		if err != nil {
			return err
		}
		parts := []ManifestPart{}
		for entries.Next() {
			var part ManifestPart
			if err = entries.Scan(&part.Share, &part.Generation, &part.Index, &part.Data); err != nil {
				entries.Close()
				return err
			}
			parts = append(parts, part)
		}
		err = entries.Err()
		entries.Close()
		if err != nil {
			return err
		}
		m, err := DecodeManifest(parts, *p.Summary)
		if err != nil {
			return err
		}
		owner, _ := strconv.ParseUint(p.Owner, 10, 32)
		if m.Binding.Authority != p.Authority || m.Binding.Owner != uint32(owner) || m.Binding.Storage != p.Request.Storage || m.Binding.StorageRevision != int64(p.Request.StorageRevision) || m.Entries[0].Version.Identity != p.StorageIdentity {
			return fmt.Errorf("manifest differs from exact publication backing/authority")
		}
	}
	return nil
}
func (s *Snapshot) readJournal(ctx context.Context, tx *sql.Tx, authority, owner string, hashes map[string]string, revisions map[string]int64, updates map[string]time.Time) error {
	bounds, err := tx.QueryContext(ctx, `SELECT length(CAST(data AS BLOB)) FROM share_journal ORDER BY sequence LIMIT ?`, MaxJournalRows+1)
	if err != nil {
		return err
	}
	count := 0
	var size int64
	for bounds.Next() {
		var n int64
		if err = bounds.Scan(&n); err != nil {
			bounds.Close()
			return err
		}
		count++
		size += n
		if count > MaxJournalRows || n < 1 || n > MaxJournalRecordBytes || size > MaxJournalBytes {
			bounds.Close()
			return fmt.Errorf("share journal count/byte bound exceeded")
		}
	}
	err = bounds.Err()
	bounds.Close()
	if err != nil {
		return err
	}
	s.Capacity.Rows = count
	s.Capacity.Bytes = size
	if err = s.Capacity.Validate(); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,data FROM share_journal ORDER BY sequence LIMIT ?`, MaxJournalRows+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	last := map[string]Journal{}
	previousSequence := int64(0)
	for rows.Next() {
		var sequence int64
		var raw []byte
		if err = rows.Scan(&sequence, &raw); err != nil {
			return err
		}
		j, err := DecodeJournal(raw)
		if err != nil {
			return err
		}
		if sequence < 1 || sequence <= previousSequence || j.Authority != authority || j.Owner != owner {
			return fmt.Errorf("journal sequence/authority differs")
		}
		previousSequence = sequence
		key := j.Entity + ":" + j.ID
		if _, ok := hashes[key]; !ok {
			return fmt.Errorf("journal refers to missing entity")
		}
		old, exists := last[key]
		if exists && (j.Revision != old.Revision+1 || j.BeforeSHA256 != old.AfterSHA256 || j.At.Before(old.At)) || !exists && j.Revision != 1 {
			return fmt.Errorf("journal entity revision/hash chain differs")
		}
		if !exists && (j.Entity == "publication" && j.Transition != "reserved" || j.Entity == "consumer" && j.Transition != "admitted") {
			return fmt.Errorf("journal lacks first admission")
		}
		last[key] = j
		s.Journal = append(s.Journal, JournalRow{sequence, j, len(raw)})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for key, expected := range hashes {
		j, ok := last[key]
		if !ok || j.AfterSHA256 != expected || j.Revision != revisions[key] || !j.At.Equal(updates[key]) {
			return fmt.Errorf("current share entity not committed by final journal row")
		}
	}
	for _, p := range s.Publications {
		j := last["publication:"+p.ID]
		transition := p.State
		if p.State == "preparing" {
			if p.WorkerState == "reserved" {
				transition = "reserved"
			} else if p.WorkerState == "running" {
				transition = "worker_started"
			} else {
				return fmt.Errorf("preparing worker lacks exact state")
			}
		}
		if p.State == "ending" && p.WorkerState == "joined" {
			transition = "worker_joined"
		}
		if j.Transition != transition {
			return fmt.Errorf("publication journal transition differs")
		}
	}
	for _, c := range s.Consumers {
		transition := c.State
		if c.State == "admitted" && c.WorkerState == "running" {
			transition = "worker_started"
		}
		if last["consumer:"+c.Admission.Lease].Transition != transition {
			return fmt.Errorf("consumer journal transition differs")
		}
	}
	for _, f := range s.Fences {
		if last["fence:"+f.ControlSHA256].Transition != f.State {
			return fmt.Errorf("fence journal transition differs")
		}
	}
	return nil
}
