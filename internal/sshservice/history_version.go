//go:build darwin || linux

package sshservice

import "golang.org/x/sys/unix"

func historyStatTimes(st *unix.Stat_t) ([2]int64, [2]int64) {
	return [2]int64{st.Mtim.Sec, st.Mtim.Nsec}, [2]int64{st.Ctim.Sec, st.Ctim.Nsec}
}
