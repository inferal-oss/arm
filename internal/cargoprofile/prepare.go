package cargoprofile

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const MaxPolicyBytes = 64 << 10

// Prepared is immutable preparation evidence. The caller must durably record
// it and authorize a separate compilation gate; preparation never builds or
// starts project tasks. Environment/policy bytes remain private in Plan.
type Prepared struct {
	Schema  int            `json:"schema"`
	Command Command        `json:"command"`
	Audit   Audit          `json:"audit"`
	Tools   Toolchain      `json:"tools"`
	Owned   OwnedPaths     `json:"owned"`
	Source  SourceEvidence `json:"source"`
	Policy  FileEvidence   `json:"policy"`
	Plan    Plan           `json:"plan"`
	At      time.Time      `json:"prepared_at"`
}

// Prepare belongs inside the already admitted supervised helper group. Both
// policy callbacks must be durably acknowledged: nil before exclusive create,
// then native file evidence after fsync and before Cargo can use that policy.
func Prepare(parent context.Context, command Command, cwd string, owned OwnedPaths, environment []string, recordPolicy func(*FileEvidence) error) (out Prepared, err error) {
	if recordPolicy == nil {
		return out, fmt.Errorf("Cargo preparation requires a durable policy recorder")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if e := owned.Revalidate(ctx); e != nil {
		return out, e
	}
	home, e := ResolveCargoHome(cwd, environment)
	if e != nil {
		return out, e
	}
	audit, e := Discover(ctx, cwd, home, environment)
	if e != nil {
		return out, e
	}
	tools, e := ResolveTools(ctx, command, audit, environment)
	if e != nil {
		return out, e
	}
	// The resolver already verified original compiler selectors by native
	// identity. Pin canonical physical files in the generated command/env.
	normalized := replaceEnvironment(environment, map[string]string{"RUSTC": tools.Rustc.Path, "RUSTDOC": tools.Rustdoc.Path})
	plan, e := Compile(command, audit, owned.Roots(), Tools{Rustc: tools.Rustc.Path, Rustdoc: tools.Rustdoc.Path}, normalized)
	if e != nil {
		return out, e
	}
	plan.Environment = compilationEnvironment(plan.Environment, tools)
	if e = recordPolicy(nil); e != nil {
		return out, e
	}
	if e = ctx.Err(); e != nil {
		return out, e
	}
	policy, e := createOwnedPolicy(ctx, owned, plan.Policy)
	if e != nil {
		return out, e
	}
	if policy.SHA256 != plan.PolicySHA256 {
		return out, fmt.Errorf("Cargo policy digest differs from compiled plan")
	}
	if e = recordPolicy(&policy); e != nil {
		return out, e
	}
	raw, e := cargoMetadata(ctx, audit.CWD, plan.Environment, tools.Cargo.Path, plan.Metadata)
	if e != nil {
		return out, e
	}
	source, e := CaptureSource(ctx, raw, owned.Roots())
	if e != nil {
		return out, e
	}
	for _, manifest := range source.Members {
		relative, e := filepath.Rel(owned.Root.Path, manifest)
		if e == nil && !filepath.IsAbs(relative) && !outsideRelative(relative) {
			return out, fmt.Errorf("Cargo source manifest cannot be inside its newly owned output tree")
		}
	}
	out = Prepared{Schema: 1, Command: command, Audit: audit, Tools: tools, Owned: owned, Source: source, Policy: policy, Plan: plan, At: time.Now().UTC()}
	if e = out.Revalidate(ctx, environment); e != nil {
		return Prepared{}, e
	}
	return out, nil
}
func outsideRelative(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}

// compilationEnvironment does not inherit resolver-only offline mode. Keep
// caller network/cache choices, but no Rustup component installation/update or
// private trace output is permitted in the admitted compilation process.
func compilationEnvironment(environment []string, tools Toolchain) []string {
	values := map[string]string{"RUSTUP_AUTO_INSTALL": "0", "RUSTUP_SKIP_UPDATE_CHECK": "1", "RUSTUP_TERM_COLOR": "never", "RUSTUP_LOG": "", "RUSTUP_TRACE_DIR": ""}
	if tools.Toolchain != "" {
		values["RUSTUP_TOOLCHAIN"] = tools.Toolchain
	}
	return replaceEnvironment(environment, values)
}
func cargoMetadata(parent context.Context, cwd string, env []string, path string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	out, diagnostic := &queryCapture{limit: MaxMetadataBytes}, &queryCapture{limit: 64 << 10}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, diagnostic
	cmd.WaitDelay = 250 * time.Millisecond
	e := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Cargo metadata exceeded admitted preparation deadline")
	}
	if e != nil || out.overflow || diagnostic.overflow {
		return nil, fmt.Errorf("Cargo metadata failed/exceeded bounds; diagnostics omitted, inspect the project using its existing tools")
	}
	return out.bytes, nil
}
func createOwnedPolicy(ctx context.Context, owned OwnedPaths, raw []byte) (FileEvidence, error) {
	if len(raw) < 1 || len(raw) > MaxPolicyBytes {
		return FileEvidence{}, fmt.Errorf("Cargo policy byte bound exceeded")
	}
	if e := owned.Revalidate(ctx); e != nil {
		return FileEvidence{}, e
	}
	dir, e := openOwnedDirectory(owned.Control.Path, owned.Control.Identity)
	if e != nil {
		return FileEvidence{}, e
	}
	defer dir.Close()
	fd, e := unix.Openat(int(dir.Fd()), "policy.toml", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return FileEvidence{}, fmt.Errorf("Cargo policy exclusive birth refused: %w", e)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(owned.Control.Path, "policy.toml"))
	defer f.Close()
	if e = ctx.Err(); e != nil {
		return FileEvidence{}, e
	}
	if _, e = f.Write(raw); e != nil {
		return FileEvidence{}, e
	}
	if e = f.Sync(); e != nil {
		return FileEvidence{}, e
	}
	if e = dir.Sync(); e != nil {
		return FileEvidence{}, e
	}
	if e = owned.Revalidate(ctx); e != nil {
		return FileEvidence{}, e
	}
	return inspectOwnedPolicy(owned)
}
func inspectOwnedPolicy(owned OwnedPaths) (FileEvidence, error) {
	path := filepath.Join(owned.Control.Path, "policy.toml")
	f, e := openConfigNoFollow(path)
	if e != nil {
		return FileEvidence{}, fmt.Errorf("Cargo policy unavailable")
	}
	defer f.Close()
	var before unix.Stat_t
	if e = unix.Fstat(int(f.Fd()), &before); e != nil {
		return FileEvidence{}, e
	}
	if before.Mode != unix.S_IFREG|0600 || before.Uid != owned.Control.Identity.Owner || before.Nlink != 1 || before.Size < 1 || before.Size > MaxPolicyBytes {
		return FileEvidence{}, fmt.Errorf("Cargo policy is not an exclusive private bounded file")
	}
	slot, e := readConfigSlot(path)
	if e != nil {
		return FileEvidence{}, e
	}
	var after unix.Stat_t
	if e = unix.Fstat(int(f.Fd()), &after); e != nil {
		return FileEvidence{}, e
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || after.Nlink != 1 || slot.evidence.Device != uint64(after.Dev) || slot.evidence.Inode != after.Ino {
		return FileEvidence{}, fmt.Errorf("Cargo policy changed while inspected")
	}
	return slot.evidence, nil
}
func (p Prepared) Revalidate(ctx context.Context, environment []string) error {
	if p.Schema != 1 {
		return fmt.Errorf("unsupported Cargo preparation evidence")
	}
	if e := p.Owned.Revalidate(ctx); e != nil {
		return e
	}
	if e := p.Tools.RevalidateTools(ctx, p.Audit, environment); e != nil {
		return e
	}
	if e := p.Source.Revalidate(ctx); e != nil {
		return e
	}
	policy, e := inspectOwnedPolicy(p.Owned)
	if e != nil || !reflect.DeepEqual(policy, p.Policy) || policy.SHA256 != p.Plan.PolicySHA256 {
		return fmt.Errorf("Cargo policy identity/content changed; compilation not authorized")
	}
	return p.Owned.Revalidate(ctx)
}
