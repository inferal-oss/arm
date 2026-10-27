//go:build !linux || (linux && !amd64 && !arm64)

package syncfs

import "context"

func verifyPlatformCompleteScope(context.Context, ScopeClaim, *Root, *Root, *Root) (ScopeBinding, error) {
	return ScopeBinding{}, ErrQuotaUnsupported
}
