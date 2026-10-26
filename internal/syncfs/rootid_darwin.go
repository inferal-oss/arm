//go:build darwin

package syncfs

import "golang.org/x/sys/unix"

func readNativeRootEvidence(_ int, st unix.Stat_t) (rootCreationEvidence, error) {
	if st.Btim.Sec <= 0 || st.Btim.Nsec < 0 || st.Btim.Nsec >= 1_000_000_000 {
		return rootCreationEvidence{}, ErrNativeIdentityUnavailable
	}
	generation := uint64(st.Gen)
	return rootCreationEvidence{
		Method:     "darwin-fstat-birthtime-generation",
		BirthSec:   st.Btim.Sec,
		BirthNsec:  uint32(st.Btim.Nsec),
		Generation: &generation,
	}, nil
}
