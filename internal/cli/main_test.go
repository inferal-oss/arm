package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain resolves the temporary directory once. ARM homes refuse paths
// with symlinks, and some platforms place the default temporary directory
// behind one, such as /var on macOS.
func TestMain(m *testing.M) {
	resolved, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve temporary directory:", err)
		os.Exit(2)
	}
	os.Setenv("TMPDIR", resolved)
	os.Exit(m.Run())
}
