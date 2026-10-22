package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeshLifecycleInspectAndHelpNeverCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"mesh", "connect", "--help"}, {"mesh", "disconnect", "--help"}, {"help", "mesh", "connect"}, {"mesh", "bootstrap"}} {
		var out, errout bytes.Buffer
		input := ""
		if args[len(args)-1] == "bootstrap" {
			input = `{"Action":"lifecycle_inspect"}`
		}
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(input), &out, &errout); code != 0 {
			t.Fatalf("%v %d %s", args, code, &errout)
		}
		if input != "" {
			var value map[string]any
			if json.Unmarshal(out.Bytes(), &value) != nil || value["running"] != false || value["protocol"] != float64(1) {
				t.Fatal(out.String())
			}
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("read-only operation created state", err)
		}
	}
	for _, input := range []string{`{"Action":"lifecycle_inspect","Injected":true}`, `{"Action":"lifecycle_inspect"} {}`, `{"Action":"lifecycle_ensure"}`} {
		var out, errout bytes.Buffer
		if code := Main([]string{"--home", home, "mesh", "bootstrap"}, strings.NewReader(input), &out, &errout); code == 0 {
			t.Fatal("invalid/unreviewed helper accepted", input)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("invalid helper wrote state", err)
		}
	}
}
