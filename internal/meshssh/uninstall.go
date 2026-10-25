package meshssh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

// UninstallHelper executes only a hash-verified retained companion. The normal
// known_hosts/account gate still withholds stdin until authentication matches.
func UninstallHelper(ctx context.Context, destination, binary string, q Transfer, payload any, expected Identity, result any) (Identity, error) {
	if !validDestination(destination) || q.Validate() != nil || !safePath(binary) || filepath.Dir(filepath.Dir(binary)) != q.Root || !regexp.MustCompile(`^recovery-[a-f0-9]{64}$`).MatchString(filepath.Base(filepath.Dir(binary))) || filepath.Base(binary) != "arm" {
		return expected, fmt.Errorf("exact retained uninstall companion required")
	}
	raw, e := json.Marshal(payload)
	if e != nil || len(raw) > 65536 {
		return expected, fmt.Errorf("bounded uninstall helper request required")
	}
	observed, out, e := exchange(ctx, destination, []string{"/bin/sh", "-c", quote(uninstallScript), "arm-uninstall", quote(binary), quote(q.SHA256), quote(q.OS)}, bytes.NewReader(raw), expected, 20*time.Second)
	if e != nil {
		return observed, e
	}
	return observed, json.Unmarshal(out, result)
}

const uninstallScript = `set -eu
binary=$1; digest=$2; os=$3
test -f "$binary"; test ! -L "$binary"
if test "$os" = linux; then if test -x /usr/bin/sha256sum; then actual=$(/usr/bin/sha256sum "$binary"); else actual=$(/bin/sha256sum "$binary"); fi; else actual=$(/usr/bin/shasum -a 256 "$binary"); fi
test "${actual%% *}" = "$digest"
exec "$binary" mesh bootstrap
`
