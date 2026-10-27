// node-registry-probe is a test-only native client for the two closed read APIs.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/inferal-oss/arm/internal/client"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: node-registry-probe HOME [STABLE_HOST]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	op, params := "node_list", any(nil)
	if len(os.Args) == 3 {
		op, params = "node_inspect", map[string]string{"host": os.Args[2]}
	}
	var value json.RawMessage
	if err := client.Call(ctx, os.Args[1], op, params, &value); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(append(value, '\n')); err != nil {
		os.Exit(1)
	}
}
