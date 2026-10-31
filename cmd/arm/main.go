package main

import (
	"fmt"
	"os"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/cli"
	"getarm.org/internal/companions"
	"getarm.org/internal/mesh"
	"getarm.org/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == server.ResidentCoreRole {
		os.Exit(server.RunOriginalResidentCore(os.Args[1:]))
	}
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
