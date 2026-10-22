// build-bundle packages locally built platform companions; no release/download
// authentication is claimed. Run with the Go selected by make GO=... bundle.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/distribution"
	"github.com/yrashk/arm/internal/mesh"
)

func main() {
	goBinary := flag.String("go", "go", "exact Go executable used for all four builds")
	root := flag.String("root", ".", "ARM source module root")
	output := flag.String("output", "dist", "archive output directory; existing archives are refused")
	carrier := flag.Bool("carrier", false, "build one native ARM embedding four thin platform payloads")
	jobs := flag.Int("jobs", 0, "parallel payload builds, 0=automatic, 1..4; bounded by available Go CPUs")
	verify := flag.String("verify", "", "verify an existing bundle only; no compilation/execution")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "no positional operands accepted")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *verify != "" {
		if err := distribution.VerifyArchive(ctx, *verify); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Verified bundle paths, platforms, Go metadata, sizes and SHA256; expected ARM version/protocol recorded")
		return
	}
	if *carrier {
		binary, _, err := distribution.BuildCarrier(ctx, distribution.Options{Root: *root, GoBinary: *goBinary, Output: *output, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Jobs: *jobs, Log: os.Stderr})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(binary)
		return
	}
	archive, _, err := distribution.Build(ctx, distribution.Options{Root: *root, GoBinary: *goBinary, Output: *output, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Jobs: *jobs, Log: os.Stderr})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(archive)
}
