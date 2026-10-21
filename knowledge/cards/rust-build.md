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

Export the actual requested executable and any required runtime files to the
operator-approved output location outside the temporary build directory. Do not
assume a universal binary path: cross-compilation targets, Cargo configuration
and package layouts can change it. Verify the exported result before cleanup;
record the destination and what was retained. An export helper and automatic
runtime dependency discovery are not implemented, so perform these steps
explicitly with the permitted ordinary tools.

Once consumers and export work have stopped:

```sh
arm storage cleanup STORAGE_LEASE_ID --dry-run
arm storage cleanup STORAGE_LEASE_ID
arm inspect STORAGE_LEASE_ID --json
```

Inspect the outcome. A refused or incomplete cleanup stays visible and charged;
it is not proof that space was reclaimed. If incremental artifacts are useful
for a concrete next step, explain that need and retain them only briefly under
the storage lease, renewing before expiry when ARM permits. Release ordinary
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
automatic idle-artifact detection and safe export helpers remain planned.

On APFS and other sharing filesystems, logical or allocated file sizes do not
prove exclusive reclaimable bytes: clones, snapshots and open files can retain
blocks. ARM reports uncertainty instead of promising that deleting a tree returns
all of its apparent size. Keep final outputs separate from temporary artifacts
and preserve unrelated or still-active data.
