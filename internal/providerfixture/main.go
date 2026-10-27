package providerfixture

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	p "github.com/inferal-oss/arm/internal/providerproto"
)

// Run is exported for the real executable and subprocess tests only. Home and
// faults are explicit operator flags, never selected by an untrusted request.
func Run(args []string, in io.Reader, out, diagnostic io.Writer) int {
	flags := flag.NewFlagSet("providerfixture", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	enabled := flags.Bool("enable-test-provider", false, "explicit disposable fixture consent")
	home := flags.String("home", "", "existing private disposable state directory")
	fault := flags.String("fault", "", "hold-before-commit or hold-after-commit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !*enabled || *home == "" || flags.NArg() != 0 {
		fmt.Fprintln(diagnostic, "fixture requires --enable-test-provider --home PRIVATE_DIRECTORY")
		return 2
	}
	if *fault != "" && *fault != "hold-before-commit" && *fault != "hold-after-commit" {
		fmt.Fprintln(diagnostic, "unknown fixture fault")
		return 2
	}
	decoded, err := p.ReadRequest(in)
	if err != nil {
		fmt.Fprintln(diagnostic, "invalid fixture request")
		return 2
	}
	store, err := Open(*home)
	if err != nil {
		fmt.Fprintln(diagnostic, "fixture state unavailable")
		return 1
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := store.Handle(ctx, decoded.Request(), func(stage string) {
		if *fault == "hold-"+stage {
			fmt.Fprintln(diagnostic, "fixture: "+stage)
			time.Sleep(30 * time.Second)
		}
	})
	if err != nil {
		fmt.Fprintln(diagnostic, "fixture outcome unknown")
		return 1
	}
	if err = p.WriteResponse(out, r); err != nil {
		fmt.Fprintln(diagnostic, "fixture reply unavailable")
		return 1
	}
	return 0
}
