package manager

import (
	"fmt"
	"golang.org/x/sys/unix"
)

const sshTermiosGet = unix.TIOCGETA
const sshTermiosSet = unix.TIOCSETA

func setSSHTermiosFlag(t *unix.Termios, f sshTermiosFlag, on bool) {
	var target *uint64
	switch f.field {
	case 0:
		target = &t.Iflag
	case 1:
		target = &t.Oflag
	case 2:
		target = &t.Cflag
	case 3:
		target = &t.Lflag
	}
	if on {
		*target |= uint64(f.bits)
	} else {
		*target &^= uint64(f.bits)
	}
}
func setSSHBaud(t *unix.Termios, op byte, value uint32) error {
	speeds := map[uint32]uint64{0: unix.B0, 50: unix.B50, 75: unix.B75, 110: unix.B110, 134: unix.B134, 150: unix.B150, 200: unix.B200, 300: unix.B300, 600: unix.B600, 1200: unix.B1200, 1800: unix.B1800, 2400: unix.B2400, 4800: unix.B4800, 9600: unix.B9600, 19200: unix.B19200, 38400: unix.B38400, 57600: unix.B57600, 115200: unix.B115200, 230400: unix.B230400}
	speed, ok := speeds[value]
	if !ok {
		return fmt.Errorf("unsupported SSH terminal baud rate")
	}
	if op == 128 {
		t.Ispeed = speed
	} else {
		t.Ospeed = speed
	}
	return nil
}
