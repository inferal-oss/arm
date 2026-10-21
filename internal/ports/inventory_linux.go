package ports

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yrashk/arm/internal/model"
)

func procAddress(s string) (string, int, error) {
	ip, p, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, fmt.Errorf("invalid endpoint")
	}
	data, err := hex.DecodeString(ip)
	if err != nil || (len(data) != 4 && len(data) != 16) {
		return "", 0, fmt.Errorf("invalid address")
	}
	for offset := 0; offset < len(data); offset += 4 {
		v := binary.NativeEndian.Uint32(data[offset : offset+4])
		binary.BigEndian.PutUint32(data[offset:offset+4], v)
	}
	a, _ := netip.AddrFromSlice(data)
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return "", 0, err
	}
	return a.String(), int(port), nil
}
func parseProc(r io.Reader, network string) ([]model.SocketObservation, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, (4<<20)+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	result := []model.SocketObservation{}
	if !scanner.Scan() {
		return nil, fmt.Errorf("missing socket table header")
	}
	for scanner.Scan() {
		if len(result) >= 16384 {
			return result, fmt.Errorf("socket inventory entry limit reached")
		}
		f := strings.Fields(scanner.Text())
		if len(f) < 4 {
			return result, fmt.Errorf("incomplete socket row")
		}
		if strings.HasPrefix(network, "tcp") && f[3] != "0A" {
			continue
		}
		if len(f) < 10 {
			return result, fmt.Errorf("incomplete bound socket row")
		}
		address, port, err := procAddress(f[1])
		if err != nil {
			return result, err
		}
		if port == 0 {
			continue
		}
		if _, err := strconv.ParseUint(f[9], 10, 64); err != nil {
			return result, fmt.Errorf("invalid socket inode")
		}
		result = append(result, model.SocketObservation{ID: f[9], Network: network, Address: address, Port: port, UID: f[7]})
	}
	return result, scanner.Err()
}
func procStart(data []byte) (string, error) {
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", fmt.Errorf("invalid process identity")
	}
	f := strings.Fields(string(data[end+1:]))
	if len(f) < 20 {
		return "", fmt.Errorf("incomplete process identity")
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil {
		return "", err
	}
	return f[19], nil
}
func boundedRead(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("metadata size limit exceeded")
	}
	return b, err
}
func discover(ctx context.Context, r *model.PortInventory) {
	r.Method = "/proc/net tables and process descriptor identities"
	r.Scope = "current network namespace; TCP listeners and bound UDP sockets"
	r.Detail = "Non-atomic snapshot; IPv6-only/socket-sharing options are unknown. Invisible processes and empty owner lists do not establish availability."
	index := map[string][]int{}
	for _, network := range []string{"tcp4", "tcp6", "udp4", "udp6"} {
		if ctx.Err() != nil {
			return
		}
		file := strings.TrimSuffix(network, "4")
		b, err := boundedRead("/proc/net/"+file, 4<<20)
		if err != nil {
			r.Complete = false
			r.Detail += "; a socket table was unavailable"
			continue
		}
		sockets, err := parseProc(strings.NewReader(string(b)), network)
		if err != nil {
			r.Complete = false
			r.Detail += "; a socket table was partial"
		}
		for _, s := range sockets {
			index[s.ID] = append(index[s.ID], len(r.Sockets))
			r.Sockets = append(r.Sockets, s)
		}
	}
	dir, err := os.Open("/proc")
	if err != nil {
		r.AttributionComplete = false
		r.Detail += "; process listing unavailable"
		return
	}
	entries, err := dir.ReadDir(32769)
	dir.Close()
	if err != nil && err != io.EOF {
		r.AttributionComplete = false
		r.Detail += "; process listing was partial"
	}
	if len(entries) == 32769 {
		r.AttributionComplete = false
		r.Detail += "; process listing limit reached"
	}
	// hidepid and other visibility restrictions can omit processes without an
	// error. An unprivileged scan cannot certify complete host attribution.
	if os.Geteuid() != 0 {
		r.AttributionComplete = false
	}
	count := 0
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		count++
		if count > 32768 {
			r.AttributionComplete = false
			r.Detail += "; process count limit reached"
			return
		}
		path := filepath.Join("/proc", entry.Name())
		stat, err := boundedRead(filepath.Join(path, "stat"), 64<<10)
		if err != nil {
			r.AttributionComplete = false
			continue
		}
		start, err := procStart(stat)
		if err != nil {
			r.AttributionComplete = false
			continue
		}
		fds, err := os.Open(filepath.Join(path, "fd"))
		if err != nil {
			r.AttributionComplete = false
			continue
		}
		seen := map[string]bool{}
		fdCount := 0
		for {
			batch, readErr := fds.ReadDir(256)
			for _, fd := range batch {
				fdCount++
				if fdCount > 65536 || ctx.Err() != nil {
					r.AttributionComplete = false
					break
				}
				link, err := os.Readlink(filepath.Join(path, "fd", fd.Name()))
				if err != nil {
					r.AttributionComplete = false
					continue
				}
				if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
					continue
				}
				inode := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
				if seen[inode] {
					continue
				}
				seen[inode] = true
				for _, i := range index[inode] {
					r.Sockets[i].Owners = append(r.Sockets[i].Owners, model.SocketOwner{PID: pid, UID: r.Sockets[i].UID, StartIdentity: r.Namespace + ":start-ticks:" + start})
				}
			}
			if readErr != nil || fdCount > 65536 || ctx.Err() != nil {
				if readErr != nil && readErr != io.EOF {
					r.AttributionComplete = false
				}
				break
			}
		}
		fds.Close()
		// Detect PID recycling during the descriptor scan before attributing it.
		after, err := boundedRead(filepath.Join(path, "stat"), 64<<10)
		end, _ := procStart(after)
		if err != nil || end != start {
			r.AttributionComplete = false
			for i := range r.Sockets {
				owners := r.Sockets[i].Owners[:0]
				for _, o := range r.Sockets[i].Owners {
					if o.PID != pid {
						owners = append(owners, o)
					}
				}
				r.Sockets[i].Owners = owners
			}
		}
	}
}
