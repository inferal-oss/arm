package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type meshSSHIdentity struct {
	Account string `json:"account"`
	HostKey string `json:"host_key"`
}
type meshCapture struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (w *meshCapture) Write(data []byte) (int, error) {
	n := len(data)
	remaining := w.limit - w.Len()
	if remaining < len(data) {
		w.overflow = true
		data = data[:max(0, remaining)]
	}
	_, _ = w.Buffer.Write(data)
	return n, nil
}
func meshSSHFlags() []string {
	return []string{"-T", "-a", "-x", "-o", "BatchMode=yes", "-o", "FingerprintHash=sha256", "-o", "StrictHostKeyChecking=yes", "-o", "VerifyHostKeyDNS=no", "-o", "NoHostAuthenticationForLocalhost=no", "-o", "GSSAPIDelegateCredentials=no", "-o", "Tunnel=no", "-o", "CheckHostIP=no", "-o", "KnownHostsCommand=none", "-o", "AddKeysToAgent=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none", "-o", "UpdateHostKeys=no", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "SetEnv=ARM_MESH_BOOTSTRAP=1", "-o", "SendEnv=-*"}
}

// Only a deliberately minimal subprocess environment is eligible for SendEnv.
// A configured wildcard that could forward even these variables is refused.
func meshSSHEnvironment() []string {
	var result []string
	for _, key := range []string{"HOME", "PATH", "SSH_AUTH_SOCK"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	return result
}
func checkSSHConfiguration(ctx context.Context, destination string) error {
	args := append([]string{"-G"}, meshSSHFlags()...)
	args = append(args, "--", destination)
	command := exec.CommandContext(ctx, "ssh", args...)
	command.Env = meshSSHEnvironment()
	out := &meshCapture{limit: 65536}
	errout := &meshCapture{limit: 4096}
	command.Stdout, command.Stderr = out, errout
	if e := command.Run(); e != nil {
		return fmt.Errorf("SSH configuration unavailable: %w", e)
	}
	if out.overflow || errout.overflow {
		return fmt.Errorf("SSH configuration output exceeded bound")
	}
	return validateSSHConfiguration(out.String())
}
func validateSSHConfiguration(configuration string) error {
	for _, line := range strings.Split(configuration, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if (fields[0] == "proxyjump" || fields[0] == "proxycommand") && fields[1] != "none" {
			return fmt.Errorf("initial mesh SSH bootstrap supports direct SSH only; configure the direct target explicitly")
		}
		if fields[0] == "sendenv" {
			for _, pattern := range fields[1:] {
				if strings.HasPrefix(pattern, "-") {
					continue
				}
				for _, key := range []string{"HOME", "PATH", "SSH_AUTH_SOCK"} {
					matched, e := filepath.Match(pattern, key)
					if e != nil || matched {
						return fmt.Errorf("SSH SendEnv could forward bootstrap environment; use a direct alias without wildcard environment forwarding")
					}
				}
			}
		}
	}
	return nil
}

// Parse only the main OpenSSH client's private -E log. Remote stderr and nested
// ssh transcript cannot satisfy this gate. Mutation bytes wait for reviewed pin.
func (a *app) meshSSH(destination, binary string, payload meshBootstrap, expected meshSSHIdentity, result any) (meshSSHIdentity, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	if e := checkSSHConfiguration(ctx, destination); e != nil {
		return expected, e
	}
	dir, e := os.MkdirTemp("", "arm-ssh-bootstrap-")
	if e != nil {
		return expected, e
	}
	defer os.RemoveAll(dir)
	logPath := filepath.Join(dir, "client.log")
	file, e := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return expected, e
	}
	_ = file.Close()
	args := append([]string{"-v", "-E", logPath}, meshSSHFlags()...)
	args = append(args, "--", destination, binary, "mesh", "bootstrap")
	command := exec.CommandContext(ctx, "ssh", args...)
	command.Env = meshSSHEnvironment()
	input, e := command.StdinPipe()
	if e != nil {
		return expected, e
	}
	out := &meshCapture{limit: 1 << 20}
	diagnostic := &meshCapture{limit: 65536}
	command.Stdout, command.Stderr = out, diagnostic
	if e = command.Start(); e != nil {
		return expected, e
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	userPattern := regexp.MustCompile(`(?m)^debug1: Authenticating to .* as '([^']+)'`)
	keyPattern := regexp.MustCompile(`(?m)^debug1: Server host key: [^ ]+ (SHA256:[A-Za-z0-9+/]+)`)
	authenticated := regexp.MustCompile(`(?m)^Authenticated to `)
	observed := meshSSHIdentity{}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, statErr := os.Stat(logPath)
		if statErr != nil || info.Size() > 65536 {
			_ = input.Close()
			cancel()
			<-wait
			return expected, fmt.Errorf("SSH client diagnostic exceeds bound")
		}
		log, readErr := os.ReadFile(logPath)
		if readErr != nil {
			_ = input.Close()
			cancel()
			<-wait
			return expected, readErr
		}
		user, key := userPattern.FindSubmatch(log), keyPattern.FindSubmatch(log)
		if len(user) == 2 && len(key) == 2 && authenticated.Match(log) {
			observed = meshSSHIdentity{Account: string(user[1]), HostKey: string(key[1])}
			break
		}
		select {
		case e := <-wait:
			return expected, fmt.Errorf("verified SSH authentication failed: %w: %.1024s", e, diagnostic.String())
		case <-ctx.Done():
			_ = input.Close()
			<-wait
			return expected, ctx.Err()
		case <-ticker.C:
		}
	}
	if expected.HostKey != "" && observed != expected {
		_ = input.Close()
		cancel()
		<-wait
		return expected, fmt.Errorf("SSH account/host key changed since preview; no mutation payload sent")
	}
	raw, e := json.Marshal(payload)
	if e == nil {
		_, e = input.Write(raw)
	}
	_ = input.Close()
	waitErr := <-wait
	if e != nil {
		return observed, e
	}
	if waitErr != nil {
		return observed, fmt.Errorf("verified SSH helper failed: %w: %.2048s", waitErr, diagnostic.String())
	}
	if out.overflow || diagnostic.overflow {
		return observed, fmt.Errorf("SSH bootstrap output exceeded bound")
	}
	if e = json.Unmarshal(out.Bytes(), result); e != nil {
		return observed, e
	}
	return observed, nil
}
