package rsyncostest

import (
	"testing"

	"getarm.org/third_party/rsyncgo/internal/rsyncos"
	"getarm.org/third_party/rsyncgo/internal/testlogger"
)

func New(t *testing.T) *rsyncos.Env {
	return &rsyncos.Env{
		// Logs go to stderr, so wire that up to a testlogger.
		Stderr: testlogger.New(t),
	}
}
