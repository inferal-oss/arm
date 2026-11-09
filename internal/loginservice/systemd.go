package loginservice

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

func checkSystemdUnitDirectory(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	const manager = "org.freedesktop.systemd1"
	out, err := exec.CommandContext(ctx, "busctl", "--user", "--no-pager", "--json=short", "get-property",
		manager, "/org/freedesktop/systemd1", manager+".Manager", "UnitPath").Output()
	if err != nil {
		return fmt.Errorf("could not read the systemd user manager's unit search path: %w", err)
	}
	return verifyUnitDirectory(out, dir)
}

func verifyUnitDirectory(data []byte, dir string) error {
	var property struct {
		Type string   `json:"type"`
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(data, &property); err != nil || property.Type != "as" || len(property.Data) == 0 {
		return fmt.Errorf("could not verify the systemd user manager's unit search path")
	}
	for _, path := range property.Data {
		if filepath.IsAbs(path) && filepath.Clean(path) == dir {
			return nil
		}
	}
	return fmt.Errorf("the systemd user manager does not search %q. Use the same XDG_CONFIG_HOME as the user manager", dir)
}
