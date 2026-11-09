// Package loginservice installs per-user native startup definitions for ARM.
package loginservice

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

type Service struct {
	Name       string
	Manager    string
	Path       string
	EnableLink string
	Executable string
	Home       string
	definition []byte
}

// ForUser returns nil on hosts without a supported login service manager.
func ForUser(executable, home string) (*Service, error) {
	var dir string
	var err error
	switch runtime.GOOS {
	case "darwin":
		dir, err = os.UserHomeDir()
		dir = filepath.Join(dir, "Library", "LaunchAgents")
	case "linux":
		// This runtime directory is created by the systemd system manager.
		if info, err := os.Stat("/run/systemd/system"); err != nil || !info.IsDir() {
			return nil, nil
		}
		dir, err = os.UserConfigDir()
		dir = filepath.Join(dir, "systemd", "user")
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	return newService(runtime.GOOS, dir, executable, home)
}

func newService(platform, dir, executable, home string) (*Service, error) {
	for _, path := range []string{dir, executable, home} {
		if !filepath.IsAbs(path) || !utf8.ValidString(path) || strings.ContainsAny(path, "\uFFFE\uFFFF") || strings.ContainsFunc(path, unicode.IsControl) {
			return nil, fmt.Errorf("login service requires absolute paths without control characters")
		}
	}
	digest := sha256.Sum256([]byte(home))
	s := &Service{Executable: executable, Home: home}
	switch platform {
	case "darwin":
		s.Manager = "launchd"
		s.Name = fmt.Sprintf("org.getarm.arm.%x", digest[:8])
		s.Path = filepath.Join(dir, s.Name+".plist")
		s.definition = launchAgent(s.Name, executable, home)
	case "linux":
		// systemd rejects these characters in ExecStart's executable even
		// after unquoting. The state directory is an ordinary argv argument.
		if strings.ContainsAny(executable, "\\\"'*?[") {
			return nil, fmt.Errorf("systemd cannot use this ARM executable path")
		}
		s.Manager = "systemd --user"
		s.Name = fmt.Sprintf("arm-%x.service", digest[:8])
		s.Path = filepath.Join(dir, s.Name)
		s.EnableLink = filepath.Join(dir, "default.target.wants", s.Name)
		s.definition = systemdUnit(executable, home)
	default:
		return nil, fmt.Errorf("unsupported login service platform %q", platform)
	}
	return s, nil
}

func launchAgent(name, executable, home string) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n<key>Label</key>\n<string>" + name + "</string>\n")
	b.WriteString("<key>ProgramArguments</key>\n<array>\n")
	for _, arg := range []string{executable, "--home", home, "serve"} {
		b.WriteString("<string>")
		_ = xml.EscapeText(&b, []byte(arg))
		b.WriteString("</string>\n")
	}
	b.WriteString("</array>\n<key>LimitLoadToSessionType</key>\n<string>Aqua</string>\n<key>RunAtLoad</key>\n<true/>\n")
	b.WriteString("<key>StandardErrorPath</key>\n<string>")
	_ = xml.EscapeText(&b, []byte(filepath.Join(home, "daemon.log")))
	b.WriteString("</string>\n</dict>\n</plist>\n")
	return b.Bytes()
}

func systemdUnit(executable, home string) []byte {
	escape := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%")
	// The colon prefix disables systemd environment substitution for the argv.
	return fmt.Appendf(nil, `[Unit]
Description=ARM resource manager

[Service]
Type=exec
ExecStart=":%s" --home "%s" serve
KillMode=mixed

[Install]
WantedBy=default.target
`, escape.Replace(executable), escape.Replace(home))
}

// Enabled recognizes only the exact definition and, for systemd, its enable
// link. Existing conflicting files and symlinks are left to the operator.
func (s *Service) Enabled() (bool, error) {
	present, err := s.definitionPresent()
	if err != nil || s.EnableLink == "" {
		return present, err
	}
	target, err := os.Readlink(s.EnableLink)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(s.EnableLink), target)
	}
	if filepath.Clean(target) != s.Path {
		return false, fmt.Errorf("login service link already points elsewhere: %s", s.EnableLink)
	}
	return present, nil
}

func (s *Service) definitionPresent() (bool, error) {
	info, err := os.Lstat(s.Path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode().IsRegular() && info.Size() == int64(len(s.definition)) {
		f, err := os.OpenFile(s.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return false, err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return false, err
		}
		if !os.SameFile(info, opened) {
			return false, fmt.Errorf("login service definition changed during inspection: %s", s.Path)
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(len(s.definition))+1))
		if err != nil {
			return false, err
		}
		if bytes.Equal(data, s.definition) {
			return true, nil
		}
	}
	return false, fmt.Errorf("login service definition already exists with different content: %s", s.Path)
}

// Enable registers the definition for the next login without starting a new
// backend. A matching unit left by an interrupted enable can be reused.
func (s *Service) Enable() error {
	if enabled, err := s.Enabled(); enabled || err != nil {
		return err
	}
	present, err := s.definitionPresent()
	if err != nil {
		return err
	}
	if !present {
		if err := writeDefinition(s.Path, s.definition); err != nil && !os.IsExist(err) {
			return err
		}
		if present, err := s.definitionPresent(); err != nil {
			return err
		} else if !present {
			return fmt.Errorf("login service definition disappeared: %s", s.Path)
		}
	}
	if s.EnableLink != "" {
		if err := os.MkdirAll(filepath.Dir(s.EnableLink), 0700); err != nil {
			return err
		}
		if err := os.Symlink(filepath.Join("..", s.Name), s.EnableLink); err != nil && !os.IsExist(err) {
			return err
		}
	}
	if enabled, err := s.Enabled(); err != nil {
		return err
	} else if !enabled {
		return fmt.Errorf("login service could not be confirmed: %s", s.Path)
	}
	return nil
}

func writeDefinition(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".arm-login-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Publish all bytes atomically without replacing a concurrently created file.
	return os.Link(f.Name(), path)
}
