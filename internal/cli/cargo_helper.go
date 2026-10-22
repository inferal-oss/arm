package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/yrashk/arm/internal/cargoprofile"
	"github.com/yrashk/arm/internal/mesh"
)

// Hidden fixed native entrypoint before global flags/home preparation. Only
// anonymous inherited pipes are accepted; project logs remain stdout/stderr.
func cargoHelperMain(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return 125
	}
	in, out := os.NewFile(3, "cargo-parent-input"), os.NewFile(4, "cargo-parent-output")
	if in == nil || out == nil {
		return 125
	}
	defer in.Close()
	defer out.Close()
	for _, file := range []*os.File{in, out} {
		info, e := file.Stat()
		if e != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return 125
		}
	}
	image, e := mesh.CaptureRuntimeExecutable()
	if e != nil {
		return 125
	}
	defer image.Close()
	proof, e := image.Proof()
	if e != nil {
		return 125
	}
	runtime := cargoprofile.HelperRuntime{SHA256: proof.SHA256, Size: proof.Size, Version: proof.Version, Protocol: cargoprofile.HelperProtocol}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return cargoprofile.RunHelper(ctx, in, out, runtime, stdout, stderr)
}
