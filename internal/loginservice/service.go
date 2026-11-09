// Package loginservice installs per-user native startup definitions for ARM.
package loginservice

import (
	"bytes"
	"context"
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
	native     bool
}

// ForUser returns nil on hosts without a supported login service manager.
func ForUser(ctx context.Context, executable, home string) (*Service, error) {
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
	if err := checkExecutable(executable); err != nil {
		return nil, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	s, err := newService(runtime.GOOS, dir, executable, home)
	if err != nil {
		return nil, err
	}
	if s.EnableLink != "" {
		if err := checkSystemdUnitDirectory(ctx, dir); err != nil {
			return nil, err
		}
	}
	s.native = true
	return s, nil
}

func checkExecutable(executable string) error {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("login setup requires an installed ARM executable")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(executable))
	if err != nil {
		return err
	}
	entry := filepath.Join(parent, filepath.Base(executable))
	// Recent Go versions can also run executables from the build cache.
	roots := []string{os.TempDir(), os.Getenv("GOTMPDIR"), os.Getenv("GOCACHE"), os.Getenv("XDG_RUNTIME_DIR"), "/tmp", "/var/tmp", "/run", "/dev/shm"}
	if cache, err := os.UserCacheDir(); err == nil {
		roots = append(roots, filepath.Join(cache, "go-build"))
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		dir, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		for _, path := range []string{entry, resolved} {
			if relative, err := filepath.Rel(dir, path); err == nil && filepath.IsLocal(relative) {
				return fmt.Errorf("login setup requires an installed ARM executable. Run arm start from a permanent installation")
			}
		}
	}
	return nil
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
	// Only signal the daemon. ARM owns cancellation and settlement of workloads.
	return fmt.Appendf(nil, `[Unit]
Description=ARM resource manager

[Service]
Type=exec
ExecStart=":%s" --home "%s" serve
KillMode=process

[Install]
WantedBy=default.target
`, escape.Replace(executable), escape.Replace(home))
}

// Setup contains only the writes described to the operator before consent.
type Setup struct {
	service        *Service
	definition     bool
	enableLink     bool
	PolicyOverride string
}

const workloadOwnership = "[Service]\nKillMode=process\n"

// Review returns nil when the current definition is already set up. An exact
// legacy Linux unit can be upgraded without overwriting it or its other drop-ins.
func (s *Service) Review() (*Setup, error) {
	plan := &Setup{service: s}
	data, err := readDefinition(s.Path, len(s.definition))
	if err != nil {
		return nil, err
	}
	switch {
	case data == nil:
		plan.definition = true
	case bytes.Equal(data, s.definition):
	case s.EnableLink != "" && bytes.Equal(data, bytes.Replace(s.definition, []byte("\nKillMode=process\n"), []byte("\nKillMode=mixed\n"), 1)):
		path := filepath.Join(s.Path+".d", "10-arm-workload-ownership.conf")
		override, err := readDefinition(path, len(workloadOwnership))
		if err != nil {
			return nil, err
		}
		if override == nil {
			plan.PolicyOverride = path
		} else if string(override) != workloadOwnership {
			return nil, fmt.Errorf("existing shutdown policy differs at %s", path)
		}
	default:
		return nil, fmt.Errorf("login service definition already exists with different content: %s", s.Path)
	}
	if s.EnableLink != "" {
		plan.enableLink, err = s.missingEnableLink()
		if err != nil {
			return nil, err
		}
	}
	if !plan.definition && !plan.enableLink && plan.PolicyOverride == "" {
		return nil, nil
	}
	return plan, nil
}

func (s *Service) missingEnableLink() (bool, error) {
	target, err := os.Readlink(s.EnableLink)
	if os.IsNotExist(err) {
		return true, nil
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
	return false, nil
}

func readDefinition(path string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, fmt.Errorf("unexpected login service file at %s", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("login service file changed during inspection: %s", path)
	}
	return io.ReadAll(io.LimitReader(f, int64(limit)+1))
}

// Apply registers or upgrades the reviewed definition for the next login.
func (p *Setup) Apply(ctx context.Context) error {
	s := p.service
	if s.native {
		if err := checkExecutable(s.Executable); err != nil {
			return err
		}
		if s.EnableLink != "" {
			if err := checkSystemdUnitDirectory(ctx, filepath.Dir(s.Path)); err != nil {
				return err
			}
		}
	}
	current, err := s.Review()
	if err != nil || current == nil {
		return err
	}
	if current.definition && !p.definition || current.enableLink && !p.enableLink || current.PolicyOverride != "" && current.PolicyOverride != p.PolicyOverride {
		return fmt.Errorf("login service changed during review. Run arm start to review it again")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if current.definition {
		if err := publishReviewedFile(s.Path, s.definition); err != nil {
			return err
		}
	}
	if current.PolicyOverride != "" {
		if err := publishReviewedFile(current.PolicyOverride, []byte(workloadOwnership)); err != nil {
			return err
		}
	}
	if current.enableLink {
		if err := os.MkdirAll(filepath.Dir(s.EnableLink), 0700); err != nil {
			return err
		}
		if err := os.Symlink(s.Path, s.EnableLink); err != nil && !os.IsExist(err) {
			return err
		}
	}
	if remaining, err := s.Review(); err != nil {
		return err
	} else if remaining != nil {
		return fmt.Errorf("login service setup could not be confirmed: %s", s.Path)
	}
	return nil
}

func publishReviewedFile(path string, data []byte) error {
	if err := writeDefinition(path, data); err != nil && !os.IsExist(err) {
		return err
	}
	actual, err := readDefinition(path, len(data))
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, data) {
		return fmt.Errorf("login service file changed during setup: %s", path)
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
