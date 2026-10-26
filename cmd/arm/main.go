package main

import (
	"fmt"
	"os"

	"github.com/inferal-oss/arm/internal/buildinfo"
	"github.com/inferal-oss/arm/internal/cli"
	"github.com/inferal-oss/arm/internal/companions"
	"github.com/inferal-oss/arm/internal/mesh"
)

func main() {
	if err := buildinfo.ValidateBundleTag(mesh.LifecycleProtocol); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := companions.Embedded(buildinfo.Version, mesh.LifecycleProtocol); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
