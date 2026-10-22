package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMeshMachineConfirmationIsSingleValueAndBoundedBootstrap(t *testing.T) {
	var out bytes.Buffer
	a := app{home: filepath.Join(t.TempDir(), "state"), out: &out, err: &out, ctx: context.Background(), forceJSON: true}
	if e := a.meshConfirm(map[string]bool{"preview": true}, true, false); e != nil || out.Len() != 0 {
		t.Fatal("machine --yes emitted a second JSON value", out.String(), e)
	}
	if e := a.meshConfirm(map[string]bool{"preview": true}, false, true); e != nil || !strings.Contains(out.String(), "preview") {
		t.Fatal("dry run did not show plan", e)
	}
	for _, input := range []string{`{"Action":"identity"} {}`, strings.Repeat("x", (1<<20)+1)} {
		out.Reset()
		code := Main([]string{"--home", a.home, "mesh", "bootstrap"}, strings.NewReader(input), &out, &out)
		if code == 0 {
			t.Fatal("malformed bootstrap accepted")
		}
		if _, e := os.Stat(a.home); !os.IsNotExist(e) {
			t.Fatal("invalid bootstrap touched state", e)
		}
	}
}
func TestMeshSSHPolicyRejectsProxyAndEnvironmentForwarding(t *testing.T) {
	for _, config := range []string{"proxyjump gateway\n", "proxycommand external-command\n", "sendenv *\n", "sendenv SSH_*\n", "sendenv HOME\n"} {
		if validateSSHConfiguration(config) == nil {
			t.Fatal("unsafe effective SSH config accepted", config)
		}
	}
	if e := validateSSHConfiguration("sendenv LANG LC_*\nproxyjump none\nproxycommand none\n"); e != nil {
		t.Fatal(e)
	}
	t.Setenv("ARM_REVIEW_SECRET", "do-not-forward")
	for _, entry := range meshSSHEnvironment() {
		if strings.HasPrefix(entry, "ARM_REVIEW_SECRET=") {
			t.Fatal("agent environment forwarded")
		}
	}

}
func TestMeshSSHFlagsOverrideHostileConfiguration(t *testing.T) {
	ssh, e := exec.LookPath("ssh")
	if e != nil {
		t.Skip("OpenSSH unavailable; actual SSH gate remains required")
	}
	config := filepath.Join(t.TempDir(), "ssh-config")
	text := "Host fixture.invalid\n StrictHostKeyChecking no\n UpdateHostKeys yes\n VerifyHostKeyDNS yes\n CheckHostIP yes\n NoHostAuthenticationForLocalhost yes\n GSSAPIDelegateCredentials yes\n ForwardAgent yes\n ForwardX11 yes\n Tunnel yes\n AddKeysToAgent yes\n ControlMaster auto\n ControlPath /tmp/unapproved-control\n PermitLocalCommand yes\n RemoteCommand unsafe-command\n SetEnv ARM_REVIEW_SET=secret\n SendEnv ARM_REVIEW_SECRET\n"
	if e = os.WriteFile(config, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	args := append([]string{"-G", "-F", config}, meshSSHFlags()...)
	args = append(args, "fixture.invalid")
	cmd := exec.CommandContext(t.Context(), ssh, args...)
	cmd.Env = meshSSHEnvironment()
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	effective := string(out)
	for _, wanted := range []string{"stricthostkeychecking true", "updatehostkeys false", "verifyhostkeydns false", "checkhostip no", "nohostauthenticationforlocalhost no", "gssapidelegatecredentials no", "forwardagent no", "forwardx11 no", "tunnel false", "addkeystoagent false", "controlmaster false", "permitlocalcommand no", "setenv ARM_MESH_BOOTSTRAP=1"} {
		if !strings.Contains(effective, wanted) {
			t.Fatalf("SSH override missing %q in %s", wanted, effective)
		}
	}
	if strings.Contains(effective, "unsafe-command") || strings.Contains(effective, "ARM_REVIEW_SET") {
		t.Fatal("configured SetEnv survived override")
	}
	if e = validateSSHConfiguration(effective); e != nil {
		t.Fatal(e)
	}
}

func TestMeshConfirmationCancellationDoesNotWaitForInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	a := app{ctx: ctx, in: reader, out: &out, inputTTY: true, outputTTY: true}
	done := make(chan error, 1)
	go func() { done <- a.meshConfirm(map[string]bool{"change": true}, false, false) }()
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancel approved mesh change")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel left confirmation blocked")
	}
}
