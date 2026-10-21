---
schema_version: 1
id: rust-build
title: "Rust builds and temporary artifacts"
tags: [rust, cargo, rustc, build, cleanup]
resources: [cpu, memory, storage]
adapter: rust-build
---
# Rust builds and temporary artifacts

Use `--profile rust-build` with supervised `arm run` for Cargo work. The profile
sets `CARGO_BUILD_JOBS` to the granted whole-core budget, rounded down with a
minimum of one. Request at least one CPU. ARM rejects explicit `-j` / `--jobs`
and `--config` arguments instead of silently accepting a conflicting jobs limit.
This is a cooperative concurrency setting, not a hard CPU or memory quota.

## Give build directories a short, explicit lifetime

Often the requested result is an executable, not the complete `target` tree.
Prefer a separate managed storage lease for intermediate artifacts. Its lifetime
must cover the build and any export or validation steps; choose a short justified
TTL rather than keeping a large build tree for the rest of the agent session.

```sh
arm storage create 10GiB --ttl 20m --reason "Temporary Cargo artifacts until the requested executable is exported"
arm run --storage STORAGE_LEASE_ID --profile rust-build --cpu 2 --ttl 10m --reason "Build the requested release executable" -- cargo build --release --bin APP
```

Replace the storage ID and binary name with actual returned values. Add your
registered `--agent CONTACT` and project attribution when applicable. The storage
grant returns its managed path. With this attachment, ARM sets `CARGO_TARGET_DIR`,
`CARGO_BUILD_TARGET_DIR` and `CARGO_BUILD_BUILD_DIR` to the managed `target`
subdirectory. Successful command exit does not by itself discard retained files.

Export the actual requested executable and explicitly chosen required runtime
files into a new bundle at the operator-approved output location:

```sh
arm storage export STORAGE_LEASE_ID /approved/output/app --file target/release/APP=bin/APP --reason "Keep the requested executable" --dry-run
arm storage export STORAGE_LEASE_ID /approved/output/app --file target/release/APP=bin/APP --reason "Keep the requested executable" --key EXPORT_KEY --wait
```

Choose an unused bundle name: export never overwrites an existing destination.
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

The current Cargo profile does not fully resolve Cargo aliases, arbitrary shell
wrappers, workspace configuration, separate Cargo build directories or every
override mechanism. The profile's argument validation and environment settings
are implemented in code; this card does not extend their enforcement. Check the
effective paths before relying on cleanup. Shared caches, persistent services,
automatic idle-artifact detection and broader export/dependency discovery remain
planned. Current idle checks cover explicit owned allocations during an export
or cleanup, not discovery of arbitrary abandoned project directories.

On APFS and other sharing filesystems, logical or allocated file sizes do not
prove exclusive reclaimable bytes: clones, snapshots and open files can retain
blocks. ARM reports uncertainty instead of promising that deleting a tree returns
all of its apparent size. Keep final outputs separate from temporary artifacts
and preserve unrelated or still-active data.
