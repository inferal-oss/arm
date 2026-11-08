package guardian

import (
	"io"
	"os"
	"strconv"
	"strings"
)

func native(pid int) (string, string) {
	base := "/proc/" + strconv.Itoa(pid)
	exe, _ := os.Readlink(base + "/exe")
	exe = strings.TrimSuffix(exe, " (deleted)")
	return exe, unifiedCgroup(readSmall(base+"/cgroup", 4<<10))
}

func readSmall(path string, limit int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, limit))
	return b
}

// unifiedCgroup returns the cgroup v2 path ("0::/path").
func unifiedCgroup(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			return rest
		}
	}
	return ""
}

// classify reads the command line only of Chromium-family processes, and
// only for the --type= switch that names the helper role.
func classify(pid int, p Process) string {
	if !browser(p) {
		return ""
	}
	return chromiumType(readSmall("/proc/"+strconv.Itoa(pid)+"/cmdline", 64<<10))
}

func workingDirectory(pid int) string {
	cwd, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	return cwd
}
