# ARM rsync fork

This directory is a source-preserving fork of `github.com/gokrazy/rsync`
v0.3.8, commit `7a9e59b40898516def209eb944c49f2ba42d4007`. Its upstream
license is retained in `LICENSE`. The fork is compiled as part of ARM's main module at
`github.com/inferal-oss/arm/third_party/rsyncgo`. Its Go imports and local
Makefile command paths use that namespace. The original nested module metadata
is retained verbatim as `UPSTREAM-go.mod.txt` and `UPSTREAM-go.sum.txt`; these
are provenance records, not active modules. Dependencies (including fork tests)
are resolved by ARM's root `go.mod`/`go.sum`. No fork algorithms, protocol,
limits or reviewed fixes changed during the initial packaging migration.
The subsequent validation correction below changes only debug-log formatting. The upstream
README, license and author identifiers remain historical upstream material.

ARM changes:

* Packaging validation exposed an existing sender debug-log format error:
  supply its already-computed `strip` value for the third `%q` verb. This
  removes `%!q(MISSING)` from debug output and permits `go vet`/default tests.
  It changes no walk inputs, file lists, protocol, limits or filesystem effects.

* Bound peer checksum counts (65,536), block lengths (1 MiB), literal tokens
  (256 KiB), file lengths (64 GiB), file list entries (262,144), path lengths
  (upstream 4 KiB) and symlink targets (4 KiB) before allocation. Choose
  larger legal rsync blocks for large files to keep signatures bounded.
* Reject malformed/traversing, invalid UTF-8, duplicate or case-colliding
  wire paths; corrupt length/range tokens; and mismatched reconstructed
  lengths before staging. A receiving filesystem's Unicode normalization
  collisions still require ARM's native validation before publication.
* Stage nested temporary files beside their destination name.
* Reject unsupported special files and wildcard filters in the bounded
  subset, propagate file-list write errors, stop zero-progress reads and
  remove an unreachable branch found by `go vet`.
* Add `rsyncstream` for a fixed protocol-27 remote-shell option subset over
  caller-owned duplex streams, with cancellation joining its watcher.
* Reject negative long-name suffix lengths before allocation, and recheck the
  current source length on both whole-file and delta paths. Whole-file transfer
  hashes its original read stream, removing an independent hash worker that
  could outlive a wire failure. Final data returned together with EOF is sent
  and hashed; a zero-progress source read fails explicitly.

The supported option subset is recursive directory contents, optional
symlinks, permissions and modification times, plus unconditional rsync delta
evaluation (`--ignore-times`). Ownership, ACLs, xattrs, hardlink topology,
sparse extents, devices, sockets, FIFOs, compression, rsync delete, exclude
rules and arbitrary flags are outside the stream API. Receivers must target
an isolated staging root. ARM's planner must verify root identities, content,
metadata, exclusions and old entry state and perform native publication;
the upstream receiver's rename is not a reviewed conditional update.
Protocol 27's built-in content check is MD4; ARM should verify a modern
content hash before publishing staged data.
