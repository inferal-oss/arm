//go:build linux && !amd64 && !arm64

package syncfs

import "context"

func probePlatformQuota(context.Context, *Root, *Root, string) (NativeQuotaEvidence, error) {
	return NativeQuotaEvidence{}, ErrQuotaUnsupported
}
