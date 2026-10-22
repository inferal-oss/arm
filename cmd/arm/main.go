package main

import (
	"fmt"
	"os"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/cli"
	"github.com/yrashk/arm/internal/companions"
	"github.com/yrashk/arm/internal/mesh"
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
