package observe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, fmt.Errorf("observation output exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(p)
}
func command(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = 250 * time.Millisecond
	var out = limitedBuffer{limit: 1 << 20}
	cmd.Stdout = &out
	// Errors deliberately exclude command output, which can contain unrelated
	// host metadata and should not be copied into ordinary diagnostics.
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out.Bytes(), nil
}
