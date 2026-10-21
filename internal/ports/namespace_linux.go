package ports

import (
	"fmt"
	"os"
	"strings"
)

// Namespace identifies this boot's current network namespace. This provider
// never enters another namespace; callers must not treat a saved label as one.
func Namespace() (string, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read boot identity: %w", err)
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return "", fmt.Errorf("read network namespace: %w", err)
	}
	if len(boot) > 128 || strings.TrimSpace(string(boot)) == "" || !strings.HasPrefix(ns, "net:[") {
		return "", fmt.Errorf("invalid boot or network namespace identity")
	}
	return "linux:" + strings.TrimSpace(string(boot)) + ":" + ns, nil
}
