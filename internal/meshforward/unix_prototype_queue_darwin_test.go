//go:build arm_forward_review && arm_forward_unix_prototype && darwin

package meshforward

// Darwin sys/filio.h defines FIONREAD as _IOR('f', 127, int).
const unixPrototypeFIONREAD = 0x4004667f
