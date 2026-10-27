package main

import (
	"fmt"
	"os"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/cli"
	"getarm.org/internal/companions"
	"getarm.org/internal/mesh"
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
