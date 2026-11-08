package guardian

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Processes the guardian never targets: the operating system, the display
// and login session, remote access, terminals that hold the operator's
// shells, and ARM itself. Names compare case-insensitively.
var protectedNames = map[string]bool{
	"launchd": true, "kernel_task": true, "windowserver": true, "loginwindow": true, "dock": true, "finder": true,
	"systemuiserver": true, "controlcenter": true, "coreaudiod": true, "sshd": true, "sshd-session": true, "mds": true,
	"systemd": true, "init": true, "xorg": true, "xwayland": true, "gnome-shell": true, "kwin_wayland": true, "kwin_x11": true,
	"plasmashell": true, "gdm": true, "sddm": true, "lightdm": true, "dbus-daemon": true, "dbus-broker": true, "pipewire": true,
	"wireplumber": true, "pulseaudio": true, "polkitd": true, "networkmanager": true, "journald": true, "udevd": true,
	"terminal": true, "iterm2": true, "ghostty": true, "wezterm": true, "wezterm-gui": true, "alacritty": true, "kitty": true,
	"xterm": true, "urxvt": true, "rxvt": true, "foot": true, "konsole": true, "gnome-terminal-server": true, "xfce4-terminal": true, "tilix": true, "terminator": true,
	"tmux": true, "screen": true, "mosh-server": true, "arm": true, "tailscaled": true,
}

var protectedPrefixes = []string{"/System/", "/usr/libexec/", "/usr/sbin/", "/sbin/", "/usr/lib/systemd/", "/lib/systemd/"}

// Protection decides which processes and groups are never targets.
type Protection struct {
	UID   string       // only this user's processes are eligible
	Self  map[int]bool // ARM's own PID
	Extra []string     // operator entries: names or path prefixes
}

// Process returns a reason when p must not be targeted.
func (pr Protection) Process(p Process) string {
	switch {
	case p.Identity.PID <= 1:
		return "init or kernel process"
	case pr.Self[p.Identity.PID]:
		return "ARM itself"
	case p.Identity.UID != pr.UID:
		return "another user's process"
	case p.Identity.UID == "0":
		// Under root every system service shares the owner; the guardian
		// then has no safe way to tell services from programs.
		return "root process"
	case p.PPID == 2 || p.Identity.PID == 2 || p.Identity.PGID == 0:
		return "kernel thread"
	case p.Executable == "":
		return "executable unknown (kernel thread or restricted)"
	case strings.HasPrefix(p.Cgroup, "/system.slice/") || p.Cgroup == "/init.scope" || strings.HasPrefix(p.Cgroup, "/kubepods"):
		return "system service"
	}
	name := strings.ToLower(strings.TrimSuffix(p.Name, ".app"))
	if protectedNames[name] {
		return "protected system or session program"
	}
	for _, prefix := range protectedPrefixes {
		if strings.HasPrefix(p.Executable, prefix) {
			return "operating system component"
		}
	}
	if bundle := appBundle(p.Executable); bundle != "" && protectedNames[strings.ToLower(strings.TrimSuffix(filepath.Base(bundle), ".app"))] {
		return "protected session application"
	}
	for _, e := range pr.Extra {
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "/") && strings.HasPrefix(p.Executable, e) || strings.EqualFold(e, p.Name) || strings.EqualFold(e, strconv.Itoa(p.Identity.PID)) {
			return "operator protected"
		}
	}
	return ""
}

// Group returns a reason when any member is protected. A group is acted on
// as a whole, so one protected member protects it.
func (pr Protection) Group(g *Group) string {
	for _, m := range g.Members {
		if r := pr.Process(m.Process); r != "" {
			return r + " (" + m.Name + ")"
		}
	}
	for _, e := range pr.Extra {
		if strings.EqualFold(e, g.Name) {
			return "operator protected"
		}
	}
	return ""
}
