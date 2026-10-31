package mesh

import (
	"context"
	"golang.org/x/sys/unix"
)

// DATA selectors, not a resource/native/service grant. The actual service group
// derives them from its own retained original and separately retains this pin.
type ResidentImagePinHomeData struct {
	Device, Inode uint64
	UID           uint32
}

func NewResidentImagePinForHomeDataPending(home, debit, sha string, size int64, expected ResidentImagePinHomeData) (*ResidentImagePinPending, error) {
	if expected.Inode == 0 {
		return nil, errResidentPinOriginal
	}
	p, err := NewResidentImagePinPending(home, debit, sha, size)
	if err != nil {
		return p, err
	}
	data := expected
	p.expectedHome = &data
	return p, nil
}
func matchPinHomeData(expected, current ResidentImagePinHomeData) error {
	if expected.Inode == 0 || expected != current {
		return errResidentPinOriginal
	}
	return nil
}

// After canonical walk, compare the SAME held original home BEFORE first mkdir
// or writable-image creation. All later writes remain relative to that held FD.
// DATA equality alone never sets expectedHomeChecked or creates funding proof.
func (p *ResidentImagePinPending) checkExpectedHeldHomeOriginal(ctx context.Context) error {
	if ctx == nil {
		return errResidentPinOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.expectedHome == nil {
		return nil
	} // unchanged standalone legacy primitive
	f := p.homeFile
	if f == nil || f.file == nil || f.closeTried || f.closed || p.expectedHomeChecked {
		return errResidentPinOriginal
	}
	p.phase("original_funded_held_home_identity_before_ANY_pin_namespace_write")
	var st unix.Stat_t
	if err := unix.Fstat(f.fd, &st); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if uint32(st.Mode) != uint32(unix.S_IFDIR)|0700 || st.Nlink == 0 {
		return errResidentPinOriginal
	}
	if err := matchPinHomeData(*p.expectedHome, ResidentImagePinHomeData{uint64(st.Dev), uint64(st.Ino), st.Uid}); err != nil {
		return err
	}
	flags, err := unix.FcntlInt(uintptr(f.fd), unix.F_GETFD, 0)
	if err != nil {
		return err
	}
	if flags&unix.FD_CLOEXEC == 0 {
		return errResidentPinOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.expectedHomeChecked = true
	return nil
}
