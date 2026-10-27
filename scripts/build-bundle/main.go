// build-bundle packages locally built platform companions; no release/download
// authentication is claimed. Run with the Go selected by make GO=... bundle.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/distribution"
	"getarm.org/internal/mesh"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("build-bundle", flag.ContinueOnError)
	flags.SetOutput(stderr)
	goBinary := flags.String("go", "go", "exact Go executable used for all payload and carrier builds")
	root := flags.String("root", ".", "ARM source module root")
	version := flags.String("version", buildinfo.Version, "ARM version injected into every payload, carrier and catalog")
	output := flags.String("output", "dist", "output directory; existing archives/carriers are refused")
	carrier := flags.Bool("carrier", false, "build one ARM embedding four thin platform payloads; native by default")
	carrierOS := flags.String("carrier-os", "", "final carrier OS (darwin/linux); requires --carrier and --carrier-arch")
	carrierArch := flags.String("carrier-arch", "", "final carrier architecture (amd64/arm64); requires --carrier and --carrier-os")
	jobs := flags.Int("jobs", 0, "parallel payload builds, 0=automatic, 1..4; bounded by available Go CPUs")
	verify := flags.String("verify", "", "verify an existing bundle only; no compilation/execution")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "no positional operands accepted")
		return 2
	}
	targetFlags := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "carrier-os" || f.Name == "carrier-arch" {
			targetFlags = true
		}
	})
	if targetFlags && (!*carrier || *verify != "") {
		fmt.Fprintln(stderr, "--carrier-os/--carrier-arch require --carrier and cannot be used with --verify or archive builds")
		return 2
	}
	if *carrier && *verify != "" {
		fmt.Fprintln(stderr, "--carrier and --verify are mutually exclusive")
		return 2
	}
	if *verify != "" {
		if err := distribution.VerifyArchive(ctx, *verify); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "Verified bundle paths, platforms, Go metadata, sizes and SHA256; expected ARM version/protocol recorded")
		return 0
	}
	if *carrier {
		binary, _, err := distribution.BuildCarrier(ctx, distribution.Options{Root: *root, GoBinary: *goBinary, Output: *output, Version: *version, Protocol: mesh.LifecycleProtocol, Jobs: *jobs, CarrierOS: *carrierOS, CarrierArch: *carrierArch, Log: stderr})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, binary)
		return 0
	}
	archive, _, err := distribution.Build(ctx, distribution.Options{Root: *root, GoBinary: *goBinary, Output: *output, Version: *version, Protocol: mesh.LifecycleProtocol, Jobs: *jobs, Log: stderr})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, archive)
	return 0
}
