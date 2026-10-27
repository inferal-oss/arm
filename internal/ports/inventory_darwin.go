package ports

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func parseLsof(data string) ([]model.SocketObservation, error) {
	result := []model.SocketObservation{}
	pid := 0
	uid, kind, protocol, inode := "", "", "", ""
	for _, field := range strings.Split(data, "\x00") {
		field = strings.TrimLeft(field, "\n")
		if field == "" {
			continue
		}
		value := field[1:]
		switch field[0] {
		case 'p':
			var err error
			pid, err = strconv.Atoi(value)
			if err != nil || pid <= 0 {
				return result, fmt.Errorf("invalid process ID")
			}
			uid = ""
		case 'u':
			uid = value
		case 'f':
			kind, protocol, inode = "", "", ""
		case 't':
			kind = value
		case 'P':
			protocol = strings.ToLower(value)
		case 'i':
			inode = value
		case 'n':
			if pid <= 0 || uid == "" {
				return result, fmt.Errorf("process ownership metadata unavailable")
			}
			if (kind != "IPv4" && kind != "IPv6") || (protocol != "tcp" && protocol != "udp") {
				return result, fmt.Errorf("unrecognized socket metadata")
			}
			endpoint, _, _ := strings.Cut(value, "->")
			address, port, err := net.SplitHostPort(endpoint)
			if err != nil {
				return result, err
			}
			if port == "*" {
				continue
			} // native lsof also lists unbound UDP sockets
			if address == "*" {
				address = "0.0.0.0"
				if kind == "IPv6" {
					address = "::"
				}
			}
			n, err := strconv.Atoi(port)
			if err != nil || n < 0 || n > 65535 {
				return result, fmt.Errorf("invalid local port")
			}
			if n == 0 {
				continue
			}
			family := "4"
			if kind == "IPv6" {
				family = "6"
			}
			start := ""
			if k, e := unix.SysctlKinfoProc("kern.proc.pid", pid); e == nil && int(k.Proc.P_pid) == pid {
				start = fmt.Sprintf("darwin:start:%d:%d", k.Proc.P_starttime.Sec, k.Proc.P_starttime.Usec)
			}
			result = append(result, model.SocketObservation{ID: inode, Network: protocol + family, Address: address, Port: n, UID: uid, Owners: []model.SocketOwner{{PID: pid, UID: uid, StartIdentity: start}}})
		default:
			return result, fmt.Errorf("unrecognized socket inventory field")
		}
	}
	return result, nil
}
func discover(ctx context.Context, r *model.PortInventory) {
	r.Method = "native lsof fields with process start-time observation"
	r.Scope = "visible host TCP listeners and bound UDP sockets"
	r.Detail = "Non-atomic permission-limited snapshot; IPv6-only/socket-sharing options are unknown. Process IDs and observed starts grant no termination authority."
	r.Complete = os.Geteuid() == 0
	r.AttributionComplete = r.Complete
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-iUDP", "-F0pufnPti")
	cmd.WaitDelay = 250 * time.Millisecond
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		r.Complete = false
		r.AttributionComplete = false
		r.Detail += "; native inspection reported warnings"
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			r.Complete = false
			r.AttributionComplete = false
			r.Detail += "; native inspection failed"
		}
	}
	sockets, parseErr := parseLsof(out.String())
	r.Sockets = sockets
	if parseErr != nil {
		r.Complete = false
		r.AttributionComplete = false
		r.Detail += "; native fields were incomplete: " + parseErr.Error()
	}
}
