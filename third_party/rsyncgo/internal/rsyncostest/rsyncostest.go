package rsyncostest

import (
	"testing"

	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncos"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/testlogger"
)

func New(t *testing.T) *rsyncos.Env {
	return &rsyncos.Env{
		// Logs go to stderr, so wire that up to a testlogger.
		Stderr: testlogger.New(t),
	}
}
