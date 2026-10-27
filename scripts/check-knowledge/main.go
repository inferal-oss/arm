// Command check-knowledge validates embedded metadata and code bindings before
// the supported build target creates a distributable executable.
package main

import (
	"flag"
	"fmt"
	"os"

	"getarm.org/knowledge"
)

func main() {
	indexPath := flag.String("write-index", "", "generate the metadata index at PATH (development only)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional argument")
		os.Exit(2)
	}
	if *indexPath != "" {
		data, err := knowledge.SourceIndex()
		if err == nil {
			err = os.WriteFile(*indexPath, data, 0644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot generate knowledge index:", err)
			os.Exit(1)
		}
		fmt.Println("Generated knowledge index:", *indexPath)
		return
	}
	cards, err := knowledge.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid knowledge catalog:", err)
		os.Exit(1)
	}
	fmt.Printf("Validated %d embedded knowledge card(s) and implementation bindings\n", len(cards))
}
