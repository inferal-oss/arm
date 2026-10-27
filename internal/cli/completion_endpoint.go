package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"getarm.org/internal/local"
)

var completionEndpointReads = make(chan struct{}, 1)

// Endpoint files are local metadata, not a streaming input. Reject FIFOs,
// symlinks, nonprivate files and oversized records before reading. A process-
// wide slot also bounds outstanding reads on an unexpectedly stalled filesystem.
func readCompletionEndpoint(ctx context.Context, home string) (local.Endpoint, error) {
	type result struct {
		endpoint local.Endpoint
		err      error
	}
	select {
	case completionEndpointReads <- struct{}{}:
	case <-ctx.Done():
		return local.Endpoint{}, ctx.Err()
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-completionEndpointReads }()
		endpoint, err := completionEndpointFile(home)
		done <- result{endpoint, err}
	}()
	select {
	case value := <-done:
		return value.endpoint, value.err
	case <-ctx.Done():
		return local.Endpoint{}, ctx.Err()
	}
}

func completionEndpointFile(home string) (local.Endpoint, error) {
	fd, err := syscall.Open(filepath.Join(home, "endpoint.json"), syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return local.Endpoint{}, err
	}
	f := os.NewFile(uintptr(fd), "completion endpoint")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return local.Endpoint{}, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Geteuid()) || info.Size() > 16<<10 {
		return local.Endpoint{}, fmt.Errorf("completion requires a bounded private regular endpoint file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil {
		return local.Endpoint{}, err
	}
	if len(raw) > 16<<10 {
		return local.Endpoint{}, fmt.Errorf("completion endpoint is oversized")
	}
	var endpoint local.Endpoint
	if err := json.Unmarshal(raw, &endpoint); err != nil {
		return endpoint, err
	}
	if endpoint.URL == "" || endpoint.Token == "" {
		return endpoint, fmt.Errorf("completion endpoint is incomplete")
	}
	return endpoint, nil
}
