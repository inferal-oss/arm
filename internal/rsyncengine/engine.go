// Package rsyncengine binds ARM's authorized duplex transport to a bounded
// protocol-27 rsync transfer. A receiving root is an operation-owned staging
// directory. Publication into a selected endpoint root belongs to ARM's
// reviewed filesystem planner and is outside this package.
package rsyncengine

import (
	"context"
	"errors"
	"io"

	"github.com/inferal-oss/arm/third_party/rsyncgo/rsyncstream"
)

type Role uint8

const (
	ClientSender Role = iota + 1
	ClientReceiver
	ServerSender
	ServerReceiver
)

type Metadata struct {
	Symlinks    bool
	Permissions bool
	ModTimes    bool
	// ReceiverWriteLimit bounds cumulative regular-file bytes written during
	// this exchange. Policy is captured before the handshake; nil leaves
	// standalone engine callers unbounded.
	ReceiverWriteLimit *int64
}

// Stats are protocol progress reports and can contain peer-supplied values.
// They are not resource accounting or proof of a complete verified transfer.
type Stats struct {
	Read      int64
	Written   int64
	FileBytes int64
}

// Run performs one standard rsync exchange over an already authenticated
// stream. No daemon, SSH implementation, arbitrary rsync flags, deletion or
// publication is provided here. For a receiving role, root must be an
// operation-owned staging tree; the caller must verify the staged tree and
// perform reviewed native publication separately.
//
// On cancellation, Run closes stream to interrupt I/O and joins its own
// worker. The caller retains responsibility for joining its transport owner.
func Run(ctx context.Context, stream io.ReadWriteCloser, role Role, root string, metadata Metadata) (Stats, error) {
	if ctx == nil || stream == nil {
		return Stats{}, errors.New("nil rsync context or stream")
	}
	opts := rsyncstream.Options{PreserveLinks: metadata.Symlinks, PreservePerms: metadata.Permissions, PreserveTimes: metadata.ModTimes, ReceiverWriteLimit: metadata.ReceiverWriteLimit}
	var result struct{ Read, Written, Size int64 }
	var err error
	switch role {
	case ClientSender:
		stats, e := rsyncstream.RunClient(ctx, stream, rsyncstream.Push, opts, root)
		err = e
		if stats != nil {
			result.Read, result.Written, result.Size = stats.Read, stats.Written, stats.Size
		}
	case ClientReceiver:
		stats, e := rsyncstream.RunClient(ctx, stream, rsyncstream.Pull, opts, root)
		err = e
		if stats != nil {
			result.Read, result.Written, result.Size = stats.Read, stats.Written, stats.Size
		}
	case ServerSender:
		stats, e := rsyncstream.RunServer(ctx, stream, rsyncstream.Pull, opts, root)
		err = e
		if stats != nil {
			result.Read, result.Written, result.Size = stats.Read, stats.Written, stats.Size
		}
	case ServerReceiver:
		stats, e := rsyncstream.RunServer(ctx, stream, rsyncstream.Push, opts, root)
		err = e
		if stats != nil {
			result.Read, result.Written, result.Size = stats.Read, stats.Written, stats.Size
		}
	default:
		return Stats{}, errors.New("invalid rsync role")
	}
	return Stats{Read: result.Read, Written: result.Written, FileBytes: result.Size}, err
}
