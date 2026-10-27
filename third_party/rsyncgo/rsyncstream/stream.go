// Package rsyncstream runs the protocol 27 remote-shell rsync exchange on an
// already authenticated, bidirectional byte stream. It never opens SSH or a
// daemon listener. Receivers must use an isolated staging directory; the
// caller owns reviewed publication into any user-selected destination.
package rsyncstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/maincmd"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncopts"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncos"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncstats"
)

type Direction uint8

const (
	Push Direction = iota + 1 // client sends, server receives
	Pull                      // server sends, client receives
)

type Options struct {
	PreserveLinks bool
	PreservePerms bool
	PreserveTimes bool
	// ReceiverWriteLimit is local policy, never a protocol flag.
	ReceiverWriteLimit *int64
}

func (o Options) flags() []string {
	flags := []string{"--recursive", "--ignore-times"}
	if o.PreserveLinks {
		flags = append(flags, "--links")
	}
	if o.PreservePerms {
		flags = append(flags, "--perms")
	}
	if o.PreserveTimes {
		flags = append(flags, "--times")
	}
	return flags
}

func parsed(direction Direction, o Options) (*rsyncopts.Options, error) {
	if direction != Push && direction != Pull {
		return nil, fmt.Errorf("invalid rsync direction %d", direction)
	}
	env := &rsyncos.Env{Stderr: discardCloser{io.Discard}}
	opts := rsyncopts.NewOptionsWithGokrazyDefaults(env)
	pc := rsyncopts.NewContext(opts)
	if err := pc.ParseArguments(env, o.flags()); err != nil {
		return nil, err
	}
	if direction == Push {
		opts.SetSender()
	}
	return opts, nil
}

// ServerArgs returns the fixed protocol-27 argument subset suitable for an
// installed rsync --server interoperability test. root is a directory path.
func ServerArgs(direction Direction, o Options, root string) ([]string, error) {
	if err := checkRoot(root); err != nil {
		return nil, err
	}
	opts, err := parsed(direction, o)
	if err != nil {
		return nil, err
	}
	return append(opts.ServerOptions(), ".", filepath.Clean(root)+string(os.PathSeparator)), nil
}

func checkRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) {
		return errors.New("rsync root must be an absolute existing directory")
	}
	if filepath.Clean(root) == string(os.PathSeparator) {
		return errors.New("filesystem root is not an allowed rsync root")
	}
	st, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("rsync root is not a directory: %s", root)
	}
	return nil
}

type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

// RunClient exchanges standard rsync bytes over conn. For Pull, root must be
// an isolated staging directory. Closing conn on cancellation must unblock
// its Read and Write methods; RunClient joins its cancellation watcher.
func RunClient(ctx context.Context, conn io.ReadWriteCloser, direction Direction, o Options, root string) (*rsyncstats.TransferStats, error) {
	if o.ReceiverWriteLimit != nil {
		limit := *o.ReceiverWriteLimit
		if limit < 0 {
			return nil, errors.New("negative receiver write limit")
		}
		// Local policy belongs to this exchange; a caller's later mutation
		// must not expand it while the peer delays its handshake.
		o.ReceiverWriteLimit = &limit
	}
	if err := checkRoot(root); err != nil {
		return nil, err
	}
	opts, err := parsed(direction, o)
	if err != nil {
		return nil, err
	}
	env := &rsyncos.Env{Stderr: discardCloser{io.Discard}, Stdout: discardCloser{io.Discard}, DontRestrict: true, ReceiverWriteLimit: o.ReceiverWriteLimit}
	return run(ctx, conn, func() (*rsyncstats.TransferStats, error) {
		path := filepath.Clean(root)
		if direction == Push {
			path += string(os.PathSeparator)
		}
		return maincmd.ClientRun(env, opts, conn, []string{path}, true)
	})
}

// RunServer serves the matching remote-shell role. For Push, root must be an
// isolated staging directory. The accepted options are generated locally.
func RunServer(ctx context.Context, conn io.ReadWriteCloser, direction Direction, o Options, root string) (*rsyncstats.TransferStats, error) {
	if o.ReceiverWriteLimit != nil {
		limit := *o.ReceiverWriteLimit
		if limit < 0 {
			return nil, errors.New("negative receiver write limit")
		}
		// Local policy belongs to this exchange; a caller's later mutation
		// must not expand it while the peer delays its handshake.
		o.ReceiverWriteLimit = &limit
	}
	args, err := ServerArgs(direction, o, root)
	if err != nil {
		return nil, err
	}
	env := &rsyncos.Env{Stdin: conn, Stdout: discardCloser{conn}, Stderr: discardCloser{io.Discard}, DontRestrict: true, ReceiverWriteLimit: o.ReceiverWriteLimit}
	return run(ctx, conn, func() (*rsyncstats.TransferStats, error) {
		return maincmd.Main(ctx, env, append([]string{"rsync"}, args...), nil)
	})
}

func run(ctx context.Context, conn io.ReadWriteCloser, transfer func() (*rsyncstats.TransferStats, error)) (*rsyncstats.TransferStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	stats, err := transfer()
	if err != nil {
		conn.Close()
	}
	close(done)
	<-joined
	if ctx.Err() != nil {
		return stats, ctx.Err()
	}
	return stats, err
}
