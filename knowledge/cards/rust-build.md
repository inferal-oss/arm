---
schema_version: 1
id: rust-build
title: "Rust builds and temporary artifacts"
tags: [rust, cargo, rustc, build, cleanup]
resources: [cpu, memory, storage]
adapter: rust-build
---
# Rust builds and temporary artifacts

Use `--profile rust-build` with supervised `arm run` and an active managed
storage lease for literal `cargo build`, `cargo check` or `cargo test --no-run`.
Request at least one CPU: Cargo jobs are the granted whole-core budget, rounded
down. The profile sets `CARGO_BUILD_JOBS` and a final private Cargo policy. These
are cooperative settings, not hard CPU, memory or disk quotas; build scripts and
linkers can exceed them or detach from the supervised group.

Preparation is charged to the lease before installed-tool queries, metadata or
output creation. ARM records owned directories and configuration/tool evidence,
then separately authorizes compilation after rechecking the lease, deadline and
evidence. Inspect `lease.cargo` for preparation, authorization and command start.
A lost acknowledgement is not permission to repeat the build with another key.

## Choose storage for write-heavy projects

For a large Rust project, inspect `arm storage pools --json` before creating
intermediate storage. Compare ARM's measured sequential write throughput, random
read latency/IOPS, benchmark timestamp/cache method, and live available space.
When fast writes matter and there is enough space for the expected `target` and
intermediate outputs, prefer the faster measured suitable pool. Operator tier
labels describe intended use; check the actual performance numbers. Missing or
stale measurements do not establish speed. Shared backing pool views overlap;
never add their available bytes. Performance is advisory under current load,
not guaranteed bandwidth. Request a benchmark only when authorized.

```sh
arm storage pools --json
arm storage create 20GiB --storage-pool POOL_NAME --ttl 30m --reason "Large write-heavy Cargo build on a measured suitable pool"
arm run --storage STORAGE_LEASE_ID --profile rust-build --cpu 2 --ttl 20m --cwd /absolute/project --reason "Build using selected fast intermediate storage" -- cargo build --release
```

Replace `POOL_NAME` with an actual configured pool that has enough available
capacity. Attach the returned storage lease: the Rust profile places its private
`CARGO_TARGET_DIR` and intermediate build directory beneath that allocation.
Do not override `CARGO_TARGET_DIR` or point cleanup at a shared cache. Selection
is explicit; ARM does not automatically migrate outputs or downgrade a request
to another pool.

## Give build directories a short, explicit lifetime

Often the requested result is an executable, not the complete `target` tree.
Prefer a separate managed storage lease for intermediate artifacts. Its lifetime
must cover the build and any export or validation steps; choose a short justified
TTL rather than keeping a large build tree for the rest of the agent session.

```sh
arm storage create 10GiB --ttl 20m --reason "Temporary Cargo artifacts until the requested executable is exported"
arm run --storage STORAGE_LEASE_ID --profile rust-build --cpu 2 --ttl 10m --cwd /absolute/project --reason "Build the requested release executable" -- cargo build --release --bin APP
```

Replace the storage ID and binary name with actual returned values. Add your
registered `--agent CONTACT` and project attribution when applicable. The storage
grant returns its managed path. Each build exclusively creates
`cargo-BUILD_LEASE_ID/{target,build,control}` beneath that allocation. The returned
build lease's `cargo.target`, `cargo.build` and `cargo.control` give the exact
paths; target and intermediate build directories are distinct. A preexisting
name is refused, never adopted or overwritten. Successful command exit retains
these files until ordinary storage cleanup.

Export the actual requested executable and explicitly chosen required runtime
files into a new bundle at the operator-approved output location:

```sh
arm storage export STORAGE_LEASE_ID /approved/output/app --file cargo-BUILD_LEASE_ID/target/release/APP=bin/APP --reason "Keep the requested executable" --dry-run
arm storage export STORAGE_LEASE_ID /approved/output/app --file cargo-BUILD_LEASE_ID/target/release/APP=bin/APP --reason "Keep the requested executable" --key EXPORT_KEY --wait
```

Replace `BUILD_LEASE_ID` with the returned build lease ID. Choose an unused
bundle name: export never overwrites an existing destination.
Its parent must exist, belong to ARM's user and not be writable by group/others.
Add repeated `--file SOURCE=RELATIVE_OUTPUT` selections for actual required
libraries, configuration or runtime data. Sources are relative to the managed
storage directory; output paths are relative to the new private bundle. Do not
assume a universal binary path: cross-compilation targets, Cargo configuration
and package layouts can change it. Verify the exported result before cleanup;
record the destination and what was retained. ARM copies only explicitly chosen
files. Automatic runtime dependency discovery is not implemented.

Export requires no attached active/queued consumers and verified idle managed
files. It rejects symlinks, unmanaged sources and hardlinks outside the owned
tree; Cargo's hardlinks entirely within that tree are supported. Output uses
independent inodes, preserves ordinary executable bits and strips unsafe modes.
The operation journals selected identities, versions, content digests and
publication phases. Reuse `EXPORT_KEY` only for the exact request: a disconnected
client must inspect its operation rather than create a second export blindly.

At most two copies run concurrently. Exports select up to 64 files and 16 GiB
total, also bounded by the source storage promise. Preparation is bounded to
30 seconds; copying is bounded to two minutes and the source lease's expiry,
with cancellation checks between I/O steps. Filesystem operations still depend
on the operating system; this is not a hard kernel I/O deadline or disk quota.
Destination capacity is an advisory filesystem estimate, not reserved physical
space. Export is an explicit handoff of the selected output copies to the
operator; it does not silently release their intermediate source lease.

If publication is interrupted, inspect the saved operation:

```sh
arm storage exports --lease STORAGE_LEASE_ID
arm storage exports inspect EXPORT_ID
arm storage exports reconcile EXPORT_ID
```

Reconciliation can verify a completely published bundle against its saved
identity and digests. It does not resume partial copying or publish a staging
directory. Partial, changed or uncertain output is preserved; after reviewing
its recorded paths, `arm storage exports handoff EXPORT_ID --reason "..."`
explicitly transfers remaining output responsibility without deleting it.
Unresolved exports block source cleanup/new consumers. Expiry still ends the
source entitlement; blocked reclamation remains visible and charged.

Once consumers and export work have stopped:

```sh
arm storage cleanup STORAGE_LEASE_ID --dry-run
arm release STORAGE_LEASE_ID
arm inspect STORAGE_LEASE_ID --json
```

Inspect the outcome. A refused or incomplete cleanup stays visible and charged;
it is not proof that space was reclaimed. If incremental artifacts are useful
for a concrete next step, explain that need and shorten their remaining lifetime:

```sh
arm storage retain STORAGE_LEASE_ID --ttl 10m --reason "One incremental follow-up remains"
```

`retain` records the rationale and a shorter deadline (at most 24 hours); it
cannot extend expiry or cut across a consumer's deadline. Request extensions
through ordinary renewal before expiry when ARM permits. Release ordinary
execution leases as soon as work finishes. ARM may reclaim eligible managed
artifacts after storage entitlement ends; never use expiry as an output archive.

## Ownership and detection limits

Managed storage records filesystem identity and attached supervised consumers.
Cleanup verifies those records and checks for activity. ARM must preserve files
when ownership or observation is uncertain. Do not run `rm -rf target`, delete a
shared Cargo registry/cache, or remove another process's artifacts to satisfy a
warning. Unmanaged target directories are not adopted merely by naming a path.

ARM resolves an existing native Cargo/rustc/rustdoc installation, including an
already-installed Rustup selection; it never installs a toolchain automatically.
It observes supported Cargo configuration from the physical invocation directory
and ancestors, member manifests, lock presence and compiler-supported targets.
It does not snapshot all source, libraries or sysroot contents, and cannot prevent
adversarial changes after its checks.

The supported grammar refuses Cargo aliases/custom commands, shell wrappers,
`-j`/`--jobs`, caller `--config`, output overrides, included/symlink configuration,
custom target JSON/search paths, nightly/bootstrap and unmodeled compiler,
linker or runner settings. Runtime tests and remote rust-build are not supported.
Normal fetching is allowed according to caller Cargo settings; `--offline` and
`--frozen` retain their meaning. Shared Cargo caches, registry locks and downloads
remain external activity and never become cleanup authority.

The ledger currently retains up to 4,096 Cargo histories. Further new admissions
are refused atomically; exact existing-key retries remain available. Routine
history archival is not implemented; do not purge evidence to bypass refusal.

Preparation is bounded and cannot be renewed. Queue/start deadlines can prevent
launch without creating any build roots. A lost post-authorization receipt or
failed final persistence retains responsibility and storage protection; inspect
the exact lease and use explicit reconciliation where verified evidence permits.
A missing PID is not proof that an unknown spawn never happened. Full Cargo
compatibility, persistent/shared caches, automatic idle-artifact detection and
runtime dependency discovery are not implemented. Current idle checks cover
explicit owned allocations during export or cleanup, not abandoned-directory
discovery throughout a project.

On APFS and other sharing filesystems, logical or allocated file sizes do not
prove exclusive reclaimable bytes: clones, snapshots and open files can retain
blocks. ARM reports uncertainty instead of promising that deleting a tree returns
all of its apparent size. Keep final outputs separate from temporary artifacts
and preserve unrelated or still-active data.
