// providerfixture is an explicitly enabled disposable test provider. It is not
// registered/discovered by ARM and cannot grant capacity in the ARM Manager.
package main

import (
	"os"

	"getarm.org/internal/providerfixture"
)

func main() { os.Exit(providerfixture.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
