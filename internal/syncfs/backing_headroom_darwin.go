//go:build darwin

package syncfs

import "context"

// Darwin does not supply the first XFS provider. No ordinary statfs reading
// can stand in for independently enrolled backing-mount evidence.
func probePlatformBackingHeadroom(context.Context, *Root, *Root, EnrollmentRecord) (BackingHeadroom, error) {
	return BackingHeadroom{}, ErrBackingHeadroom
}
