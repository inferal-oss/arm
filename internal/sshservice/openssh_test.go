package sshservice

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func invocationFixture() Invocation {
	return Invocation{Executable: "/private/ARM's app %x/arm", ProxySocket: "/private/ARM's app %x/proxy.sock", AgentSocket: "/private/ARM's app %x/agent.sock", PublicIdentity: "/private/ARM's app %x/identity.pub", KnownHosts: "/private/ARM's app %x/known hosts", Host: strings.Repeat("a", 32), Account: "armuser"}
}
func TestOpenSSHOptionsAreClosedAndNativeParserAcceptsPaths(t *testing.T) {
	v := invocationFixture()
	args, err := OpenSSHArguments(v)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(args, "\n")
	for _, want := range []string{"-F\n/dev/null", "ForwardAgent=no", "ForwardX11=no", "ClearAllForwardings=yes", "StrictHostKeyChecking=yes", "IdentitiesOnly=yes", "ProxyJump=none", "PermitLocalCommand=no", "SendEnv=-*"} {
		if !strings.Contains(all, want) {
			t.Fatal("missing fixed safety option", want)
		}
	}
	// -G only resolves generated configuration: no socket/network/key operation.
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("native OpenSSH unavailable; option parser acceptance unverified")
	}
	cmd := exec.Command(ssh, append([]string{"-G"}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("native option parse failed", string(out), err)
	}
	output := string(out)
	for _, want := range []string{"user armuser", "hostname arm-" + v.Host, "identitiesonly yes", "forwardagent no", "forwardx11 no", "stricthostkeychecking true", "permitlocalcommand no"} {
		if !strings.Contains(output, want) {
			t.Fatal("resolved SSH option changed", want, output)
		}
	}
}
func TestProxyCommandNeverEvaluatesPathText(t *testing.T) {
	dir := privateFixture(t)
	sentinel := filepath.Join(dir, "must-not-exist")
	executable := filepath.Join(dir, "arm ' $(touch must-not-exist) %n")
	// A private harmless executable records argv without invoking a user shell.
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	v := invocationFixture()
	v.Executable = executable
	v.ProxySocket = filepath.Join(dir, "sock ' $(touch must-not-exist) %n")
	args, err := OpenSSHArguments(v)
	if err != nil {
		t.Fatal(err)
	}
	var proxy string
	for i := range args {
		if strings.HasPrefix(args[i], "ProxyCommand=") {
			proxy = strings.TrimPrefix(args[i], "ProxyCommand=")
		}
	}
	// OpenSSH expands %% to a literal percent before its fixed shell invocation.
	proxy = strings.ReplaceAll(proxy, "%%", "%")
	cmd := exec.Command("/bin/sh", "-c", proxy)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	want := "_ssh-proxy\n--socket\n" + v.ProxySocket + "\n"
	if string(out) != want {
		t.Fatal("private proxy argv changed", string(out))
	}
	if _, err = os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("path text executed")
	}
}
func TestOpenSSHRejectsControlAndCallerSelectedOptionFragments(t *testing.T) {
	for _, change := range []func(*Invocation){func(v *Invocation) { v.Host = "-oProxyCommand=evil" }, func(v *Invocation) { v.Account = "armuser;evil" }, func(v *Invocation) { v.Executable = "relative" }, func(v *Invocation) { v.ProxySocket = "/private/new\ncommand" }} {
		v := invocationFixture()
		change(&v)
		if _, err := OpenSSHArguments(v); err == nil {
			t.Fatal("untrusted argument fragment accepted")
		}
	}
}
