//go:build arm_share_benchmark

package main

import (
	"context"
	"errors"
	"time"

	"github.com/pkg/sftp"
)

func mounted(context.Context, *sftp.Client, int) (int64, []byte, time.Duration, map[string]any, error) {
	return 0, nil, 0, nil, errors.New("authenticated confined macOS SMB adapter not implemented; no fallback")
}
