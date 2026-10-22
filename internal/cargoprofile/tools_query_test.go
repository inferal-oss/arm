package cargoprofile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This worker exercises native process/pipes/deadlines, not compiler identity
// or successful Cargo resolution. Those have separate real-tool fixtures.
func TestToolQueryWorker(t *testing.T) {
	switch os.Getenv("ARM_TEST_QUERY_WORKER") {
	case "stdout":
		fmt.Fprint(os.Stdout, strings.Repeat("private-query-canary", 100000))
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("private-query-canary", 10000))
	case "timeout":
		if err := os.WriteFile("query-started", []byte("ready"), 0600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
	}
}

func TestToolQueriesBoundRealChildOutputAndDeadline(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"stdout", "stderr", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := fixtureRoot(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			out, err := toolQuery(ctx, root, []string{"ARM_TEST_QUERY_WORKER=" + mode}, executable, "-test.run=^TestToolQueryWorker$")
			if err == nil || out != nil || strings.Contains(err.Error(), "private-query-canary") {
				t.Fatal("unchecked query output or diagnostic escaped", len(out), err)
			}
			if mode == "timeout" {
				if _, err := os.Stat(filepath.Join(root, "query-started")); err != nil {
					t.Fatal("deadline fixture did not execute the native child", err)
				}
				if ctx.Err() != context.DeadlineExceeded || time.Since(started) > 5*time.Second {
					t.Fatal("query did not return after caller deadline", ctx.Err(), time.Since(started))
				}
			}
		})
	}
}
