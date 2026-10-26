package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCarrierTargetFlagsRejectArchiveAndVerifyWithoutEffects(t *testing.T) {
	for _, args := range [][]string{
		{"--carrier-os=linux", "--carrier-arch=arm64"},
		{"--carrier-os="},
		{"--verify=missing.tar.gz", "--carrier-os=linux", "--carrier-arch=arm64"},
		{"--carrier", "--verify=missing.tar.gz", "--carrier-os=linux", "--carrier-arch=arm64"},
		{"--carrier", "--verify=missing.tar.gz"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "untouched")
			args = append(args, "--root="+root, "--output=output", "--go=must-not-run")
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "carrier") {
				t.Fatal("invalid mode accepted", code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("invalid mode created output", err)
			}
		})
	}
}

func TestCarrierTargetPairAndPlatformRejectedBeforeCompiler(t *testing.T) {
	for _, args := range [][]string{{"--carrier", "--carrier-os=linux"}, {"--carrier", "--carrier-arch=arm64"}, {"--carrier", "--carrier-os=windows", "--carrier-arch=amd64"}} {
		var stdout, stderr bytes.Buffer
		args = append(args, "--go=must-not-run")
		if code := run(context.Background(), args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "carrier") || strings.Contains(stderr.String(), "must-not-run") {
			t.Fatal(code, stderr.String())
		}
	}
}
