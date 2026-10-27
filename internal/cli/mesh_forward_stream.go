package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/meshsshrelay"
	"github.com/inferal-oss/arm/internal/model"
)

// A stream is a local client of one already-owned source listener. The raw
// bearer never enters a URL, backend RPC, process argument or stdout.
func (a *app) meshForwardStream(args []string) (int, error) {
	f := a.flags("mesh forward stream")
	key := f.String("key", "", "exact active source listener key")
	tokenFile := f.String("token-file", "", "private mode-0600 token file from source start")
	allowTTY := f.Bool("allow-tty", false, "permit raw application bytes on a terminal")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 || len(*key) != 32 || *tokenFile == "" || a.forceJSON {
		return 2, errors.New("exact --key and --token-file required; raw stream cannot use --json")
	}
	if a.outputTTY && !*allowTTY {
		return 2, errors.New("raw TCP bytes may control a terminal; pipe output or explicitly use --allow-tty")
	}
	conn, _, err := a.dialForwardSource(*key, *tokenFile)
	if err != nil {
		return 1, err
	}
	defer conn.Close()
	if err = bridgeForwardStream(a.ctx, a.in, a.out, conn); err != nil {
		return 1, model.Err("remote_outcome_unknown", fmt.Sprintf("raw TCP stream result unknown; inspect the application before retrying: %v", err))
	}
	return 0, nil
}

type forwardSourceClientStatus struct {
	Key           string `json:"key"`
	State         string `json:"state"`
	Bind          string `json:"bind"`
	Service       string `json:"service"`
	OriginalOwner bool   `json:"original_owner"`
}

// dialForwardSource confirms destination admission before application bytes
// enter a local source listener. The bearer stays in the private token file.
func (a *app) dialForwardSource(key, tokenFile string) (net.Conn, forwardSourceClientStatus, error) {
	token, err := readForwardToken(tokenFile)
	if err != nil {
		return nil, forwardSourceClientStatus{}, err
	}
	var status forwardSourceClientStatus
	if err = a.call("mesh_forward_status", struct {
		Key string `json:"key"`
	}{key}, &status); err != nil {
		return nil, status, err
	}
	host, _, err := net.SplitHostPort(status.Bind)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || status.Key != key || status.State != "active" || !status.OriginalOwner {
		return nil, status, errors.New("exact active daemon-owned loopback source listener required")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(a.ctx, "tcp", status.Bind)
	if err != nil {
		return nil, status, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = conn.Close()
		}
	}()
	if err = confirmForwardSource(a.ctx, conn, token); err != nil {
		return nil, status, err
	}
	accepted = true
	return conn, status, nil
}

// Admission owns its cancellation watcher until the connection is handed to
// the application phase. Reading one byte at a time preserves any service
// bytes already following READY on the same transport write.
func confirmForwardSource(ctx context.Context, conn net.Conn, token string) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	finished := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		<-watchDone
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	if err = conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	if _, err = io.WriteString(conn, "ARM-FORWARD/2 "+token+"\n"); err != nil {
		return errors.New("TCP source admission uncertain; no application bytes sent")
	}
	var ack [32]byte
	for i := range ack {
		if _, err = io.ReadFull(conn, ack[i:i+1]); err != nil {
			return errors.New("TCP destination admission not confirmed; no application bytes sent")
		}
		if ack[i] == '\n' {
			if string(ack[:i+1]) != "ARM-FORWARD/2 READY\n" {
				return errors.New("TCP destination admission refused; no application bytes sent")
			}
			break
		}
		if i == len(ack)-1 {
			return errors.New("invalid TCP source acknowledgment; no application bytes sent")
		}
	}
	if err = conn.SetDeadline(time.Now().Add(meshsshrelay.MaxLifetime)); err != nil {
		return err
	}
	return ctx.Err()
}

func readForwardToken(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		return "", errors.New("token file must be a singly linked regular file owned by this user with mode 0600")
	}
	data, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return "", err
	}
	if len(data) != 64 && (len(data) != 65 || data[64] != '\n') {
		return "", errors.New("token file must contain exactly one canonical 256-bit token")
	}
	token := strings.TrimSuffix(string(data), "\n")
	if _, err = meshforward.TokenDigest(token); err != nil {
		return "", err
	}
	return token, nil
}

func bridgeForwardStream(ctx context.Context, input io.Reader, output io.Writer, conn net.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopWatch:
		}
	}()
	defer func() { close(stopWatch); <-watchDone }()
	inputDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, input)
		if half, ok := conn.(interface{ CloseWrite() error }); ok {
			if e := half.CloseWrite(); err == nil {
				err = e
			}
		}
		inputDone <- err
	}()
	_, outputErr := io.Copy(output, conn)
	_ = conn.Close()
	select {
	case inputErr := <-inputDone:
		if outputErr != nil {
			return outputErr
		}
		return inputErr
	case <-time.After(250 * time.Millisecond):
		// A remote close cannot safely leave a CLI process blocked forever on
		// terminal stdin. Closing its reader is confined to this command process.
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
		select {
		case inputErr := <-inputDone:
			if outputErr != nil {
				return outputErr
			}
			return inputErr
		case <-time.After(250 * time.Millisecond):
			return errors.New("local input did not finish after remote close")
		}
	}
}
