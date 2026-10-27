// release-build uses the ordinary carrier builder for all four delivery targets.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/inferal-oss/arm/internal/companions"
	"github.com/inferal-oss/arm/internal/distribution"
	"github.com/inferal-oss/arm/internal/mesh"
)

type carrier struct {
	OS      string              `json:"os"`
	Arch    string              `json:"arch"`
	Catalog companions.Manifest `json:"catalog"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context) error {
	version := flag.String("version", "", "validated release version")
	output := flag.String("output", "", "fresh carrier staging directory")
	flag.Parse()
	if *version == "" || *output == "" || flag.NArg() != 0 {
		return fmt.Errorf("version and fresh output are required")
	}
	if err := os.Mkdir(*output, 0700); err != nil {
		return err
	}
	var records []carrier
	for _, p := range [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		dir := filepath.Join(*output, p[0]+"-"+p[1])
		binary, catalog, err := distribution.BuildCarrier(ctx, distribution.Options{Root: ".", GoBinary: "go", Output: dir, Version: *version, Protocol: mesh.LifecycleProtocol, Jobs: 1, CarrierOS: p[0], CarrierArch: p[1], Log: os.Stderr})
		if err != nil {
			return err
		}
		if err = catalog.Validate(*version, mesh.LifecycleProtocol); err != nil {
			return err
		}
		// This executes the native carrier's real startup/catalog checks without a backend.
		if p == [2]string{runtime.GOOS, runtime.GOARCH} {
			cmd := exec.CommandContext(ctx, binary, "--json", "version")
			raw, err := cmd.Output()
			if err != nil {
				return fmt.Errorf("native release carrier startup failed: %w", err)
			}
			var identity struct {
				Version string `json:"version"`
				OS      string `json:"os"`
				Arch    string `json:"arch"`
			}
			if err = json.Unmarshal(raw, &identity); err != nil || identity.Version != *version || identity.OS != p[0] || identity.Arch != p[1] {
				return fmt.Errorf("native release carrier identity mismatch")
			}
		}
		records = append(records, carrier{p[0], p[1], catalog})
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*output, "catalogs.json"), append(raw, '\n'), 0600)
}
