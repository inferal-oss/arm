package cli

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/daemonconfig"
)

func TestConfigCLIReadOnlyMissingInvalidPartialAndHelp(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"check", "show"} {
		home := filepath.Join(base, action)
		var out, errout bytes.Buffer
		if code := Main([]string{"--home", home, "config", action}, strings.NewReader(""), &out, &errout); code != 0 || !strings.Contains(out.String(), `"file_present": false`) {
			t.Fatal(code, out.String(), errout.String())
		}
		if _, e = os.Stat(home); !os.IsNotExist(e) {
			t.Fatal("created state", e)
		}
	}
	home := filepath.Join(base, "private")
	if e = os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"schema=2", "schema=1\n[daemon]\ncpu=2"} {
		if e = os.WriteFile(filepath.Join(home, daemonconfig.Filename), []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		var out, errout bytes.Buffer
		code := Main([]string{"--home", home, "config", "show"}, strings.NewReader(""), &out, &errout)
		if raw == "schema=2" {
			if code != 2 || !strings.Contains(errout.String(), "configuration refused") {
				t.Fatal(code, errout.String())
			}
		} else if code != 0 || !strings.Contains(out.String(), `"cpu": "file"`) {
			t.Fatal(code, out.String(), errout.String())
		}
		entries, _ := os.ReadDir(home)
		if len(entries) != 1 {
			t.Fatal("config inspection created backend state", entries)
		}
	}
	for _, args := range [][]string{{"config", "--help"}, {"config", "show", "--help"}, {"__complete", "config", ""}} {
		var out, errout bytes.Buffer
		if code := Main(append([]string{"--home", filepath.Join(base, "not-created")}, args...), strings.NewReader(""), &out, &errout); code != 0 {
			t.Fatal(args, code, out.String(), errout.String())
		}
		if _, e = os.Lstat(filepath.Join(base, "not-created")); !os.IsNotExist(e) {
			t.Fatal("help/completion created state", e)
		}
	}
}

func TestDaemonExplicitStartupArgumentsDoNotPromoteDefaults(t *testing.T) {
	f := flag.NewFlagSet("fixture", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	cpu := f.Float64("cpu", 9, "")
	memory := f.String("memory", "auto", "")
	disk := f.String("disk", "auto", "")
	listen := f.String("listen", "127.0.0.1:0", "")
	f.Bool("yes", false, "")
	if e := f.Parse([]string{"--memory", "512MiB", "--yes"}); e != nil {
		t.Fatal(e)
	}
	o := startupOverrides(f, cpu, memory, disk, listen)
	if o.CPU != nil || o.Disk != nil || o.Listen != nil || o.Memory == nil || *o.Memory != "512MiB" {
		t.Fatal("defaults promoted", o)
	}
	if got := startupArguments("selected-home", f); !reflect.DeepEqual(got, []string{"--home", "selected-home", "serve", "--memory", "512MiB"}) {
		t.Fatal(got)
	}
}

func TestDaemonInvalidStartupRefusesBeforeCreatingState(t *testing.T) {
	for _, args := range [][]string{{"serve", "--cpu", "0"}, {"daemon", "start", "--memory", "0"}, {"serve", "--listen", "0.0.0.0:0"}} {
		home := filepath.Join(t.TempDir(), "absent")
		var out, errout bytes.Buffer
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout); code != 2 || !strings.Contains(errout.String(), "configuration refused") {
			t.Fatal(args, code, errout.String())
		}
		if _, e := os.Lstat(home); !os.IsNotExist(e) {
			t.Fatal("invalid startup created state", e)
		}
	}
}
