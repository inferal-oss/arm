//go:build arm_forward_review && arm_forward_unix_prototype && (darwin || linux)

package cli

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/inferal-oss/arm/internal/meshforward"
)

// startForwardUnixPrototype is intentionally absent from public dispatch.
// Every accepted Unix client uses the existing exact-key source listener,
// pinned local TLS identity and private token admission before data copying.
// Production use requires a separate durable Manager adapter lease.
func (a *app) startForwardUnixPrototype(ctx context.Context, key, tokenFile string) (*meshforward.UnixPrototype, error) {
	if _, err := readForwardToken(tokenFile); err != nil {
		return nil, err
	}
	var status struct {
		forwardSourceClientStatus
		Expires time.Time `json:"expires"`
	}
	if err := a.call("mesh_forward_status", struct {
		Key string `json:"key"`
	}{key}, &status); err != nil {
		return nil, err
	}
	if status.Key != key || status.State != "active" || !status.OriginalOwner || !status.Expires.After(time.Now()) {
		return nil, errors.New("original active source listener required")
	}
	return meshforward.StartUnixPrototype(ctx, a.home, key, status.Expires, func(context.Context) (net.Conn, error) {
		conn, _, err := a.dialForwardSource(key, tokenFile)
		return conn, err
	})
}
