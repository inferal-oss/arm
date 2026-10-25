package database

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

var errStartupWALChanged = errors.New("startup WAL continuity changed")

// A WAL header is32 bytes; a frame additionally needs24 bytes plus a database
// page (at least512). Empty bookkeeping may be absent, zero-length or a valid
// header without ANY frame. Partial/bad headers are unknown, not safe emptiness.
// This is not a general WAL replay/corruption parser; SQLite owns that protocol.
func emptyStartupWAL(file *os.File, id File) (bool, error) {
	if !id.Present || id.Size == 0 {
		return true, nil
	}
	if id.Size > 32 {
		return false, nil
	}
	if id.Size != 32 {
		return false, fmt.Errorf("%w: partial WAL header", errStartupWALChanged)
	}
	var header [32]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return false, err
	}
	after, err := nativeFilePolicy(file, "state.db-wal", false, true)
	if err != nil {
		return false, err
	}
	if !sameIdentity(after, id) || after.Size != id.Size || after.Modified != id.Modified || after.Changed != id.Changed {
		return false, fmt.Errorf("%w: header changed during read", errStartupWALChanged)
	}
	magic := binary.BigEndian.Uint32(header[:4])
	version := binary.BigEndian.Uint32(header[4:8])
	page := binary.BigEndian.Uint32(header[8:12])
	if (magic != 0x377f0682 && magic != 0x377f0683) || version != 3007000 || page < 512 || page > 65536 || page&(page-1) != 0 {
		return false, fmt.Errorf("%w: malformed empty WAL header", errStartupWALChanged)
	}
	var order binary.ByteOrder = binary.LittleEndian
	if magic&1 != 0 {
		order = binary.BigEndian
	}
	var first, second uint32
	for i := 0; i < 24; i += 8 {
		first += order.Uint32(header[i:i+4]) + second
		second += order.Uint32(header[i+4:i+8]) + first
	}
	if first != binary.BigEndian.Uint32(header[24:28]) || second != binary.BigEndian.Uint32(header[28:32]) {
		return false, fmt.Errorf("%w: bad empty WAL checksum", errStartupWALChanged)
	}
	return true, nil
}

func (r *StartupRead) validateWAL() (err error) {
	file, now, err := r.Directory.openSource("state.db-wal")
	if err != nil {
		return err
	}
	if file != nil {
		defer func() { err = errors.Join(err, file.Close()) }()
	}
	if !r.main.Present && now.Present {
		return fmt.Errorf("%w: new orphan WAL", errStartupWALChanged)
	}
	if r.wal.Present && !r.walEmpty {
		if !sameIdentity(now, r.wal) || now.Size != r.wal.Size || now.Modified != r.wal.Modified || now.Changed != r.wal.Changed {
			return fmt.Errorf("%w: original nonempty WAL disappeared or changed", errStartupWALChanged)
		}
		return nil
	}
	empty, err := emptyStartupWAL(file, now)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("%w: new WAL content cannot be treated as absent intent", errStartupWALChanged)
	}
	return nil
}
