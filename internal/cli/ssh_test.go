package cli

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestSSHExactByteCounts(t *testing.T) {
	for v, want := range map[string]int64{"0": 0, "9223372036854775807": math.MaxInt64, "1.5GiB": 1610612736, "0.001KB": 1, "9007199254740993B": 9007199254740993} {
		got, e := sshBytes(v)
		if e != nil || got != want {
			t.Fatal(v, got, e)
		}
	}
	for _, v := range []string{"9223372036854775808", "0.1B", "-1", "1e3", "NaN", " 1", "1GiB ", "1GB;echo bad", "1XB"} {
		if _, e := sshBytes(v); e == nil {
			t.Fatal("unsafe/rounded quantity accepted", v)
		}
	}
}
func TestSSHEnvironmentAndClosedProxy(t *testing.T) {
	input := []string{"TERM=xterm-256color", "SSH_AUTH_SOCK=/user/socket", "SSH_ASKPASS=secret", "DISPLAY=:1", "PATH=/usr/bin", "SSH_AGENT_PID=123", "SSH_ASKPASS_REQUIRE=force"}
	if got := sshEnvironment(input); !reflect.DeepEqual(got, []string{"TERM=xterm-256color", "PATH=/usr/bin"}) {
		t.Fatal(got)
	}
	for _, args := range [][]string{{}, {"host"}, {"--socket", "/tmp/x", "extra"}, {"--host", "example"}} {
		var out, err bytes.Buffer
		if code := sshProxyMain(args, strings.NewReader(""), &out, &err); code != 2 || out.Len() != 0 {
			t.Fatal(args, code, out.String(), err.String())
		}
	}
}
func TestSSHInvalidArgumentsRefuseBeforeBackendAccess(t *testing.T) {
	for _, args := range [][]string{{"node", "--json", "--reason", "q"}, {"node", "-t", "-T", "--reason", "q"}, {"node", "-t", "--reason", "q"}, {"node", "--reason", "q", "--", "a", "b"}, {"node", "--memory", "9223372036854775808", "--reason", "q"}, {"node", "--reason", "q", "--", ""}} {
		var out, err bytes.Buffer
		a := app{home: t.TempDir() + "/absent", ctx: context.Background(), in: strings.NewReader(""), out: &out, err: &err}
		if code, e := a.sshCommand(args); e == nil || code != 2 {
			t.Fatal(args, code, e)
		}
	}
}
func TestSSHHelpWorksOffline(t *testing.T) {
	for _, args := range [][]string{{"ssh", "--help"}, {"ssh", "setup", "--help"}, {"ssh", "disable", "--help"}, {"ssh", "pending", "--help"}, {"ssh", "lookup", "--help"}, {"ssh", "seal", "--help"}} {
		var out, err bytes.Buffer
		if code := Main(append([]string{"--home", t.TempDir() + "/missing"}, args...), strings.NewReader(""), &out, &err); code != 0 {
			t.Fatal(args, code, err.String())
		}
		if !strings.Contains(out.String(), "arm ssh") {
			t.Fatal(args, out.String())
		}
	}
}
