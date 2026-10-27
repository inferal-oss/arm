//go:build arm_forward_review && arm_forward_unix_prototype && linux

package meshforward

import "golang.org/x/sys/unix"

const unixPrototypeFIONREAD = unix.TIOCINQ
