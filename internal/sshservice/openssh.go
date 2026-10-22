package sshservice

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

type Invocation struct {
	Executable, ProxySocket, AgentSocket, PublicIdentity, KnownHosts, Host, Account string
	Terminal                                                                        bool
}

// OpenSSHArguments accepts no user SSH option/command fragments. The fixed
// ProxyCommand has only safely quoted private paths; the target and remote
// command never enter its shell. File/socket identities are verified by the
// invocation owner separately before launching ssh.
func OpenSSHArguments(v Invocation) ([]string, error) {
	if !validID(v.Host) || !validAccount(v.Account) {
		return nil, fmt.Errorf("invalid exact SSH host/account")
	}
	for _, p := range []string{v.Executable, v.ProxySocket, v.AgentSocket, v.PublicIdentity, v.KnownHosts} {
		if !safePath(p) {
			return nil, fmt.Errorf("absolute bounded SSH invocation paths required")
		}
	}
	proxy := shellQuote(v.Executable) + " _ssh-proxy --socket " + shellQuote(v.ProxySocket)
	proxy = strings.ReplaceAll(proxy, "%", "%%")
	options := []string{"BatchMode=yes", "IdentitiesOnly=yes", "PreferredAuthentications=publickey", "PubkeyAuthentication=yes", "PasswordAuthentication=no", "KbdInteractiveAuthentication=no", "GSSAPIAuthentication=no", "HostbasedAuthentication=no", "StrictHostKeyChecking=yes", "VerifyHostKeyDNS=no", "CheckHostIP=no", "NoHostAuthenticationForLocalhost=no", "UpdateHostKeys=no", "KnownHostsCommand=none", "GlobalKnownHostsFile=/dev/null", "CertificateFile=none", "PKCS11Provider=none", "AddKeysToAgent=no", "ControlMaster=no", "ControlPath=none", "ControlPersist=no", "PermitLocalCommand=no", "LocalCommand=none", "RemoteCommand=none", "ProxyJump=none", "ClearAllForwardings=yes", "ForwardAgent=no", "ForwardX11=no", "Tunnel=no", "EscapeChar=none", "SendEnv=-*", "ConnectTimeout=10", "ConnectionAttempts=1", "LogLevel=ERROR", "HostKeyAlias=arm-" + v.Host, "IdentityAgent=" + optionPath(v.AgentSocket), "IdentityFile=" + optionPath(v.PublicIdentity), "UserKnownHostsFile=" + optionPath(v.KnownHosts), "ProxyCommand=" + proxy}
	args := []string{"-F", "/dev/null", "-a", "-x"}
	for _, o := range options {
		args = append(args, "-o", o)
	}
	if v.Terminal {
		args = append(args, "-tt")
	} else {
		args = append(args, "-T")
	}
	return append(args, "-l", v.Account, "--", "arm-"+v.Host), nil
}
func KnownHostsLine(host string, key ssh.PublicKey) (string, error) {
	if !validID(host) || key == nil || key.Type() != ssh.KeyAlgoED25519 {
		return "", fmt.Errorf("exact Ed25519 host identity required")
	}
	return "arm-" + host + " " + string(ssh.MarshalAuthorizedKey(key)), nil
}
func optionPath(p string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(p) + `"`
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func validID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func validAccount(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}
func safePath(s string) bool {
	if !filepath.IsAbs(s) || len(s) > 4096 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
