package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/buildinfo"
)

func TestNodeMetadataCLIHelpAndVersionSourceBeforeState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"node", "--help"}, {"help", "node"}} {
		var out, stderr bytes.Buffer
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr); code != 0 || !strings.Contains(out.String(), "--host") || !strings.Contains(out.String(), "reported") {
			t.Fatal(code, &out, &stderr)
		}
	}
	for _, args := range [][]string{{"node", "unexpected"}, {"node", "--annotations", "{}"}, {"node", "--host"}} {
		var out, stderr bytes.Buffer
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr); code != 2 {
			t.Fatal(args, code, &out, &stderr)
		}
	}
	if Version != buildinfo.Version {
		t.Fatal("CLI and node metadata version sources differ")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline help/validation wrote state", err)
	}
}
