package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
)

// dialForwardSourceTLS verifies the current local mesh signing identity on a
// loopback connection before the CLI sends the private bearer. Status supplies
// the exact address; the private mesh registry supplies the certificate pin.
// This authenticates the identity, not the operating-system process.
func dialForwardSourceTLS(ctx context.Context, home, bind string) (*tls.Conn, error) {
	host, _, err := net.SplitHostPort(bind)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("literal loopback TCP source address required")
	}
	registry, found, err := mesh.ReadRegistry(home)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("current local mesh identity required for TCP source authentication")
	}
	config, err := mesh.LocalForwardClientTLS(registry.Identity)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	dialer.Timeout = 5 * time.Second
	raw, err := dialer.DialContext(ctx, "tcp", bind)
	if err != nil {
		return nil, err
	}
	secure := tls.Client(raw, config)
	if err = secure.SetDeadline(time.Now().Add(5 * time.Second)); err == nil {
		err = secure.HandshakeContext(ctx)
	}
	if err != nil {
		_ = secure.Close()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("local TCP source identity not confirmed; no bearer or application bytes sent: %w", err)
	}
	if err = secure.SetDeadline(time.Time{}); err != nil {
		_ = secure.Close()
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		_ = secure.Close()
		return nil, err
	}
	return secure, nil
}
