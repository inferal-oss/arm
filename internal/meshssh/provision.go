package meshssh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Probe struct {
	OS        string          `json:"os"`
	Arch      string          `json:"arch"`
	UID       string          `json:"uid"`
	Home      string          `json:"home"`
	HashTool  string          `json:"hash_tool"`
	Installed string          `json:"installed_binary,omitempty"`
	StateHome string          `json:"state_home"`
	CPUs      int             `json:"logical_cpus"`
	Startup   json.RawMessage `json:"saved_startup,omitempty"`
}

func validDestination(destination string) bool {
	if destination == "" || len(destination) > 512 || strings.HasPrefix(destination, "-") {
		return false
	}
	for _, r := range destination {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func quote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }

// ValidStateHome permits ordinary platform paths (including spaces), but no
// control characters or ambiguous noncanonical/relative namespace.
func ValidStateHome(v string) bool {
	if len(v) > 4096 || !filepath.IsAbs(v) || filepath.Clean(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func safePath(v string) bool {
	return len(v) <= 4096 && filepath.IsAbs(v) && filepath.Clean(v) == v && regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`).MatchString(v)
}

const probeScript = `set -eu
IFS= read -r gate; test "$gate" = ARM_READONLY_PROBE
os=$(uname -s); arch=$(uname -m); uid=$(id -u)
hash=none
if test "$os" = Linux; then if test -x /usr/bin/sha256sum; then hash=/usr/bin/sha256sum; elif test -x /bin/sha256sum; then hash=/bin/sha256sum; fi
elif test "$os" = Darwin && test -x /usr/bin/shasum; then hash=/usr/bin/shasum; fi
found=$(command -v "$1" 2>/dev/null || :)
state=$2
if test -z "$state"; then
 if test -n "${ARM_HOME:-}"; then state=$ARM_HOME
 elif test "$os" = Darwin; then state="$HOME/Library/Application Support/arm"
 else state="${XDG_CONFIG_HOME:-$HOME/.config}/arm"; fi
fi
cpus=$(getconf _NPROCESSORS_ONLN)
printf 'ARM_PROBE_V1\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' "$os" "$arch" "$uid" "$HOME" "$hash" "$found" "$state" "$cpus"
if test -e "$state/daemon-launch.json"; then test ! -L "$state/daemon-launch.json"; test "$(wc -c < "$state/daemon-launch.json")" -le 65536; cat "$state/daemon-launch.json"; else printf null; fi
printf '\n'
`

// ProbeHost executes only a fixed allowlisted read-only shell probe; it never
// starts ARM or executes an arbitrary discovered installed binary.
func ProbeHost(ctx context.Context, destination, binary, home string, expected Identity) (Probe, Identity, error) {
	var p Probe
	if home != "" && !ValidStateHome(home) {
		return p, expected, fmt.Errorf("canonical bounded remote state home required")
	}
	if !validDestination(destination) || !regexp.MustCompile(`^(arm|/[A-Za-z0-9_./-]+)$`).MatchString(binary) {
		return p, expected, fmt.Errorf("safe direct destination/installed binary required")
	}
	observed, raw, e := exchange(ctx, destination, []string{"/bin/sh", "-c", quote(probeScript), "arm-probe", quote(binary), quote(home)}, strings.NewReader("ARM_READONLY_PROBE\n"), expected, 20*time.Second)
	if e != nil {
		return p, observed, e
	}
	lines := strings.SplitN(string(raw), "\n", 10)
	if len(lines) != 10 || lines[0] != "ARM_PROBE_V1" {
		return p, observed, fmt.Errorf("unsupported bounded SSH platform probe")
	}
	p = Probe{OS: lines[1], Arch: lines[2], UID: lines[3], Home: lines[4], HashTool: lines[5], Installed: lines[6], StateHome: lines[7], Startup: json.RawMessage(strings.TrimSpace(lines[9]))}
	p.CPUs, _ = strconv.Atoi(lines[8])
	switch p.OS {
	case "Linux":
		p.OS = "linux"
	case "Darwin":
		p.OS = "darwin"
	default:
		return p, observed, fmt.Errorf("unsupported target OS")
	}
	switch p.Arch {
	case "aarch64", "arm64":
		p.Arch = "arm64"
	case "x86_64":
		p.Arch = "amd64"
	default:
		return p, observed, fmt.Errorf("unsupported target architecture")
	}
	if _, e = strconv.ParseUint(p.UID, 10, 32); e != nil || !safePath(p.Home) || p.HashTool == "none" || p.CPUs < 1 || p.CPUs > 65536 || !ValidStateHome(p.StateHome) || (p.Installed != "" && !safePath(p.Installed)) {
		return p, observed, fmt.Errorf("target requires safe absolute HOME/binary and fixed SHA256 tool; no install attempted")
	}
	return p, observed, nil
}

type Transfer struct {
	Root        string `json:"install_root"`
	Key         string `json:"key"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size_bytes"`
	ExpectedUID string `json:"expected_uid"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Version     string `json:"expected_arm_version"`
	Protocol    int    `json:"expected_lifecycle_protocol"`
	Reason      string `json:"reason"`
}

func (q Transfer) Validate() error {
	if !safePath(q.Root) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(q.Key) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(q.SHA256) || q.Size < 1 || q.Size > 256<<20 || len(q.Reason) > 4096 || strings.TrimSpace(q.Reason) == "" {
		return fmt.Errorf("invalid bounded artifact transfer")
	}
	if _, e := strconv.ParseUint(q.ExpectedUID, 10, 32); e != nil {
		return e
	}
	if (q.OS != "linux" && q.OS != "darwin") || (q.Arch != "arm64" && q.Arch != "amd64") || q.Version == "" || q.Protocol < 1 {
		return fmt.Errorf("invalid artifact platform/protocol")
	}
	return nil
}

// All writes wait for this stdin gate, sent only after main-client SSH account
// and host-key verification. mkdir is exclusive; a partial stage is preserved.
const uploadScript = `set -eu
IFS= read -r gate; test "$gate" = ARM_REVIEWED_PROVISION_V1
root=$1; key=$2; digest=$3; size=$4; uid=$5; payload=$6; os=$7
umask 077
test "$(id -u)" = "$uid"
# Refuse symlink traversal in every existing component, then create only owned
# private ARM directories; source and destination are never passed to eval.
checkchain() { p=$1; while test "$p" != /; do test ! -L "$p" || exit 71; p=${p%/*}; test -n "$p" || p=/; done; }
checkchain "$root"
if ! test -e "$root"; then mkdir -p "$root"; chmod 700 "$root"; fi
test -d "$root"; test ! -L "$root"
if test "$os" = linux; then test "$(/usr/bin/stat -c '%u:%a' "$root")" = "$uid:700"; else test "$(/usr/bin/stat -f '%u:%Lp' "$root")" = "$uid:700"; fi
stage="$root/stage-$key"
mkdir "$stage"; chmod 700 "$stage"
cat > "$stage/arm"
test "$(wc -c < "$stage/arm" | tr -d ' ')" = "$size"
if test "$os" = linux; then if test -x /usr/bin/sha256sum; then actual=$(/usr/bin/sha256sum "$stage/arm"); else actual=$(/bin/sha256sum "$stage/arm"); fi
else actual=$(/usr/bin/shasum -a 256 "$stage/arm"); fi
test "${actual%% *}" = "$digest"
chmod 700 "$stage/arm"
exec "$stage/arm" mesh provision-finalize "$payload"
`

func Upload(ctx context.Context, destination string, q Transfer, input io.Reader, expected Identity, result any) (Identity, error) {
	if !validDestination(destination) {
		return expected, fmt.Errorf("invalid SSH destination")
	}
	if e := q.Validate(); e != nil {
		return expected, e
	}
	payload, e := json.Marshal(q)
	if e != nil || len(payload) > 16384 {
		return expected, fmt.Errorf("transfer descriptor exceeded bound")
	}
	remote := []string{"/bin/sh", "-c", quote(uploadScript), "arm-upload", quote(q.Root), quote(q.Key), quote(q.SHA256), strconv.FormatInt(q.Size, 10), quote(q.ExpectedUID), quote(base64.RawStdEncoding.EncodeToString(payload)), quote(q.OS)}
	reader := &io.LimitedReader{R: input, N: q.Size + 1}
	observed, out, e := exchange(ctx, destination, remote, io.MultiReader(strings.NewReader("ARM_REVIEWED_PROVISION_V1\n"), reader), expected, 2*time.Minute)
	if e != nil {
		return observed, e
	}
	if reader.N != 1 {
		return observed, fmt.Errorf("artifact source size changed during upload")
	}
	if e = json.Unmarshal(out, result); e != nil {
		return observed, e
	}
	return observed, nil
}

// LookupInstalled checks artifact bytes BEFORE executing only its read-only
// native receipt query. It never starts a backend or repairs partial files.
func LookupInstalled(ctx context.Context, destination string, q Transfer, expected Identity, result any) (Identity, error) {
	if !validDestination(destination) {
		return expected, fmt.Errorf("invalid SSH destination")
	}
	if e := q.Validate(); e != nil {
		return expected, e
	}
	raw, _ := json.Marshal(q)
	script := `set -eu
IFS= read -r gate; test "$gate" = ARM_READONLY_LOOKUP
binary=$1; digest=$2; os=$3; payload=$4
test -f "$binary"; test ! -L "$binary"
if test "$os" = linux; then if test -x /usr/bin/sha256sum; then actual=$(/usr/bin/sha256sum "$binary"); else actual=$(/bin/sha256sum "$binary"); fi; else actual=$(/usr/bin/shasum -a 256 "$binary"); fi
test "${actual%% *}" = "$digest"
exec "$binary" mesh provision-lookup "$payload"
`
	binary := q.Root + "/artifact-" + q.SHA256 + "/arm"
	observed, out, e := exchange(ctx, destination, []string{"/bin/sh", "-c", quote(script), "arm-lookup", quote(binary), quote(q.SHA256), quote(q.OS), quote(base64.RawStdEncoding.EncodeToString(raw))}, bytes.NewReader([]byte("ARM_READONLY_LOOKUP\n")), expected, 20*time.Second)
	if e != nil {
		return observed, e
	}
	return observed, json.Unmarshal(out, result)
}
