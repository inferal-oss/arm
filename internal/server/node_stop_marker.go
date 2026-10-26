package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
	"golang.org/x/sys/unix"
)

func readStopMarker(dir *database.Directory, record nodestop.Record, lockID model.FileIdentity) error {
	name := "node-stop-closed-" + record.Plan.Target.Generation + ".json"
	file, identity, err := dir.Open(name, unix.O_RDONLY)
	if err != nil {
		return err
	}
	if file == nil {
		return fmt.Errorf("joined-generation marker missing")
	}
	if identity.Size > 64<<10 {
		file.Close()
		return fmt.Errorf("closed-generation marker exceeds bound")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 64<<10+1))
	err = errors.Join(readErr, file.Close())
	if err != nil {
		return err
	}
	var closed nodestop.Closed
	if err = nodestop.Decode(raw, &closed); err != nil {
		return err
	}
	if err = closed.Validate(record, lockID); err != nil {
		return err
	}
	after, err := dir.Observe(name, true)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	if !after.Present || after.Device != identity.Device || after.Inode != identity.Inode || after.Mode != identity.Mode || after.Owner != identity.Owner || after.Size != identity.Size || after.SHA256 != hex.EncodeToString(hash[:]) {
		return fmt.Errorf("closed-generation marker changed during reading")
	}
	if err = dir.Validate(); err != nil {
		return err
	}
	traceStopMarker(dir.Path, record, closed, lockID, hex.EncodeToString(hash[:]))
	return nil
}
