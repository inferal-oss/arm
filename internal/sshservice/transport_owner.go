package sshservice

import (
	"context"
	"errors"

	"golang.org/x/crypto/ssh"
)

type executionTransportKey struct{}
type executionTransport struct {
	channel ssh.Channel
	close   func() error
}

// executionContext belongs to Server's sole execution channel. Server rejects
// every additional channel, so this closer never cancels another invocation.
func executionContext(ctx context.Context, channel ssh.Channel, close func() error) context.Context {
	return context.WithValue(ctx, executionTransportKey{}, executionTransport{channel, close})
}

// OriginalTransportCloser lends only the exact one-session connection closer
// installed by Server. It cannot be reconstructed from requests or persisted
// descriptors, and cannot be borrowed for a different channel.
func OriginalTransportCloser(ctx context.Context, channel ssh.Channel) (func() error, error) {
	if ctx == nil || channel == nil {
		return nil, errors.New("original execution transport required")
	}
	owned, ok := ctx.Value(executionTransportKey{}).(executionTransport)
	if !ok || owned.channel != channel || owned.close == nil {
		return nil, errors.New("channel has no original execution transport owner")
	}
	return owned.close, nil
}
