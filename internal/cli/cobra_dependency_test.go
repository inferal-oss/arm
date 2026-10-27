package cli

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Keep the approved framework dependencies tied to a small API contract while
// production command dispatch remains unchanged. This installs no completion
// scripts and reads or changes no ARM state.
func TestCobraFlagSetContractPreservesArmNameAndLiteralValues(t *testing.T) {
	flags := pflag.NewFlagSet("arm-dependency-contract", pflag.ContinueOnError)
	value := flags.String("probe", "", "dependency API probe")
	var output bytes.Buffer
	called := 0
	command := &cobra.Command{
		Use:           "arm",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, args []string) error {
			called++
			if command.Name() != "arm" || len(args) != 0 {
				return fmt.Errorf("framework command identity/arguments changed")
			}
			return nil
		},
	}
	command.Flags().AddFlagSet(flags)
	command.SetOut(&output)
	command.SetErr(&output)
	want := "literal value; $(no shell execution)"
	command.SetArgs([]string{"--probe", want})
	if _, err := command.ExecuteC(); err != nil {
		t.Fatal(err, output.String())
	}
	if called != 1 || *value != want {
		t.Fatalf("framework dispatch/value changed: calls=%d value=%q", called, *value)
	}
}
