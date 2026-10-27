package cli

import (
	"fmt"
	"getarm.org/internal/mesh"
)

func (a *app) meshArtifacts(args []string) (int, error) {
	if len(args) == 0 || args[0] == "list" {
		f := a.flags("mesh artifacts")
		if len(args) > 0 {
			args = args[1:]
		}
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() != 0 {
			return 2, fmt.Errorf("artifacts list accepts no operands")
		}
		items, e := mesh.Artifacts(a.home)
		if e != nil {
			return 1, e
		}
		return 0, a.print(items)
	}
	if args[0] != "import" {
		return 2, fmt.Errorf("use mesh artifacts list or import FILE --reason PURPOSE")
	}
	f := a.flags("mesh artifacts import")
	reason := f.String("reason", "", "audited local companion artifact purpose")
	dry := f.Bool("dry-run", false, "inspect/hash only, no local catalog write")
	if e := a.parse(f, args[1:], false); e != nil {
		return 2, e
	}
	if f.NArg() != 1 {
		return 2, fmt.Errorf("artifact import requires one existing ARM companion executable")
	}
	var artifact mesh.Artifact
	var e error
	if *dry {
		artifact, e = mesh.InspectArtifact(f.Arg(0), *reason)
	} else {
		artifact, e = mesh.ImportArtifact(a.home, f.Arg(0), *reason)
	}
	if e != nil {
		return 1, e
	}
	return 0, a.print(artifact)
}
