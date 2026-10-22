package manager

import (
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
	"os"
)

// Terminal mode translation is deliberately closed. Unknown modes were rejected
// at SSH admission; supported POSIX flags/control bytes preserve terminal input
// semantics without delegating an ioctl or arbitrary native flag word.
func applySSHTerminalModes(f *os.File, modes ssh.TerminalModes) error {
	term, err := unix.IoctlGetTermios(int(f.Fd()), sshTermiosGet)
	if err != nil {
		return err
	}
	for op, v := range modes {
		if index, ok := sshControlModes[op]; ok {
			if v > 255 {
				return fmt.Errorf("SSH terminal control byte outside range")
			}
			term.Cc[index] = uint8(v)
			continue
		}
		if op == 128 || op == 129 {
			if err = setSSHBaud(term, op, v); err != nil {
				return err
			}
			continue
		}
		flag, ok := sshTermiosFlags[op]
		if !ok {
			continue
		} // Known SSH modes without a native counterpart retain defaults.
		setSSHTermiosFlag(term, flag, v != 0)
	}
	return unix.IoctlSetTermios(int(f.Fd()), sshTermiosSet, term)
}

type sshTermiosFlag struct {
	field int
	bits  uint64
}

var sshControlModes = map[uint8]int{ssh.VINTR: unix.VINTR, ssh.VQUIT: unix.VQUIT, ssh.VERASE: unix.VERASE, ssh.VKILL: unix.VKILL, ssh.VEOF: unix.VEOF, ssh.VEOL: unix.VEOL, ssh.VEOL2: unix.VEOL2, ssh.VSTART: unix.VSTART, ssh.VSTOP: unix.VSTOP, ssh.VSUSP: unix.VSUSP, ssh.VREPRINT: unix.VREPRINT, ssh.VWERASE: unix.VWERASE, ssh.VLNEXT: unix.VLNEXT}
var sshTermiosFlags = map[uint8]sshTermiosFlag{
	ssh.IGNPAR: {0, unix.IGNPAR}, ssh.PARMRK: {0, unix.PARMRK}, ssh.INPCK: {0, unix.INPCK}, ssh.ISTRIP: {0, unix.ISTRIP}, ssh.INLCR: {0, unix.INLCR}, ssh.IGNCR: {0, unix.IGNCR}, ssh.ICRNL: {0, unix.ICRNL}, ssh.IXON: {0, unix.IXON}, ssh.IXANY: {0, unix.IXANY}, ssh.IXOFF: {0, unix.IXOFF}, ssh.IMAXBEL: {0, unix.IMAXBEL},
	ssh.OPOST: {1, unix.OPOST}, ssh.ONLCR: {1, unix.ONLCR}, ssh.OCRNL: {1, unix.OCRNL}, ssh.ONOCR: {1, unix.ONOCR}, ssh.ONLRET: {1, unix.ONLRET},
	ssh.PARENB: {2, unix.PARENB}, ssh.PARODD: {2, unix.PARODD},
	ssh.ISIG: {3, unix.ISIG}, ssh.ICANON: {3, unix.ICANON}, ssh.ECHO: {3, unix.ECHO}, ssh.ECHOE: {3, unix.ECHOE}, ssh.ECHOK: {3, unix.ECHOK}, ssh.ECHONL: {3, unix.ECHONL}, ssh.NOFLSH: {3, unix.NOFLSH}, ssh.TOSTOP: {3, unix.TOSTOP}, ssh.IEXTEN: {3, unix.IEXTEN}, ssh.ECHOCTL: {3, unix.ECHOCTL}, ssh.ECHOKE: {3, unix.ECHOKE}, ssh.PENDIN: {3, unix.PENDIN},
}
