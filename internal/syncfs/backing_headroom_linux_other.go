//go:build linux && !amd64 && !arm64

package syncfs

import "context"

func probePlatformBackingHeadroom(context.Context, *Root, *Root, EnrollmentRecord) (BackingHeadroom, error) {
	return BackingHeadroom{}, ErrBackingHeadroom
}
