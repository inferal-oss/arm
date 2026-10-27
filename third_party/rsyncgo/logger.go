package rsync

import "getarm.org/third_party/rsyncgo/internal/log"

// Logger is an interface that allows specifying your own logger.
// By default, the Go log package is used, which prints to stderr.
type Logger = log.Logger
