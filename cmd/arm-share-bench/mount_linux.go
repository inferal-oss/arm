//go:build arm_share_benchmark

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/pkg/sftp"
	"github.com/yrashk/arm/internal/sharemount"
	"github.com/yrashk/arm/internal/shareview"
)

func mounted(ctx context.Context, client *sftp.Client, readBytes int) (n int64, digest []byte, elapsed time.Duration, extra map[string]any, err error) {
	if os.Geteuid() == 0 {
		return 0, nil, 0, nil, errors.New("native mount must be nonroot")
	}
	r := &remote{client: client, files: make(map[string]*sftp.File)}
	view, err := shareview.New(ctx, r, "selected")
	if err != nil {
		return
	}
	tree, err := sharemount.New(ctx, view)
	if err != nil {
		return
	}
	base, err := os.MkdirTemp("", "arm-share-bench-mount-")
	if err != nil {
		return
	}
	point := filepath.Join(base, "mount")
	if err = os.Mkdir(point, 0700); err != nil {
		return
	}
	server, err := fs.Mount(point, tree.Root(), sharemount.Options())
	if err != nil {
		os.RemoveAll(base)
		return
	}
	// No force/lazy detach, arbitrary PID signals or automatic retry. On any
	// unknown unmount/join outcome retain the generated root and report failure.
	defer func() {
		if e := server.Unmount(); e != nil {
			err = errors.Join(err, e)
			extra = map[string]any{"retained_mount_root": base}
			return
		}
		server.Wait()
		tree.BeginClose()
		if e := tree.WaitDrained(ctx); e != nil {
			err = errors.Join(err, e)
			return
		}
		count, e := tree.HandleStatus()
		if e != nil || count != 0 || view.Outstanding() != 0 {
			err = errors.Join(err, e, errors.New("unsettled adapter handles"))
			return
		}
		extra = map[string]any{"ordinary_unmount": true, "server_joined": true, "adapter_drained": true, "handles": count, "memory_plan": sharemount.AdmissionMemory(), "read_requests": tree.ReadStats(), "explicit_user_read_bytes": readBytes, "public_consumer_settlement": false}
		err = errors.Join(err, os.RemoveAll(base))
	}()
	f, err := os.Open(filepath.Join(point, "payload"))
	if err != nil {
		return
	}
	h := sha256.New()
	start := time.Now()
	if readBytes == 0 {
		n, err = io.Copy(h, f)
	} else {
		// Hide File.WriteTo: this explicit read-size workload is separate from
		// ordinary-copy/small-read practical acceptance, never substituted for it.
		n, err = io.CopyBuffer(h, struct{ io.Reader }{f}, make([]byte, readBytes))
	}
	elapsed = time.Since(start)
	digest = h.Sum(nil)
	err = errors.Join(err, f.Close())
	return
}
