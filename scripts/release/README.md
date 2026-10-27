# Tagged carrier releases

The `Tagged carrier releases` workflow publishes on a full semantic version tag
such as `v1.2.3` or `v1.2.3-rc.1`. The tag must point to a commit in `origin/main`.
It does not create or move tags. Manual dispatch and scoped launch-branch pushes
build validation artifacts and never publish a release.

Each release has four archives:

- `arm-vVERSION-darwin-amd64.tar.gz`
- `arm-vVERSION-darwin-arm64.tar.gz`
- `arm-vVERSION-linux-amd64.tar.gz`
- `arm-vVERSION-linux-arm64.tar.gz`

Every archive contains a native `arm` **carrier** with all four compressed
macOS/Linux amd64/arm64 payloads embedded, its `catalog.json`, and full license
notices. The release also includes `release.json` (source commit, toolchain,
carrier hashes and four-payload catalogs) and `SHA256SUMS`. The source archives
GitHub generates are separate from these executable carriers.

For a particular tag/platform, download its archive and `SHA256SUMS`, verify the
archive's SHA-256, extract it, and place the `arm` executable on your PATH. Keep
the license notices with redistributed copies. No Go, make, or sibling payload
files are needed to run a released carrier. Remote provisioning still follows
ARM's normal explicit installation/authorization review.

## Build identity and validation

The Go toolchain comes from `go.mod`. Builds are CGo-free and run with
`GOMAXPROCS=1` and one payload build job. The ordinary distribution builder
injects the same version into every executable and catalog and checks all four
compressed payload streams before compiling each carrier. The native carrier's
real startup and `version` output are checked; cross-platform targets receive
Go platform/build metadata checks. This does not substitute for running each
platform on its native OS.

The workflow first runs the affected Go tests and Python failure-path checks,
then builds all four targets in one standard Linux runner. Only the six
explicit release assets are uploaded. Build directories, source checkouts,
Go caches, credentials and raw logs are excluded. The license collector
includes Go's license, every module used by any target, and tracked bundled-code
notices; missing notices fail the build.

A manual validation run can use `v0.0.0-ci`; it produces the same carriers and
checks but creates no tag or release. From a reviewed source checkout the
underlying command is:

```sh
python3 -B scripts/release/build.py --tag v0.0.0-ci --output /absolute/fresh/output
```

On the shared workstation, heavy build/test commands must use the existing
shared FIFO queue, with its normal limits; do not run this command directly
outside that queue. GitHub-hosted runs use their own bounded runner resources.

## Publication and recovery

The build job has read-only repository access. Only the tag-push publication job
for `inferal-oss/arm` has `contents: write`. It checks source/tag identity again,
verifies every local artifact and reconciles an existing release by exact names
and actual downloaded byte hashes. It creates a draft first, attaches all
assets, checks the complete server set, rechecks that the remote tag still
identifies the built commit, and only then publishes. Prerelease tags are
marked accordingly; GitHub chooses the stable latest release by semantic
version/date.

Retries reuse matching assets. Conflicting, duplicate, extra, incomplete
published, or unknown-outcome assets stop the job without overwriting or
deleting anything. An interrupted draft remains available for inspection and a
same-source rerun. Lost create/upload/publication responses are resolved through
read-only lookup before any further mutation. Existing immutable published
releases are accepted only when all expected bytes match; they are never edited.

Checksums detect changed bytes but are not independent proof of publisher
identity. Trust the repository/tag and GitHub release provenance before running
a downloaded executable.

This workflow does not change `arm install`, the existing agent-integration
command, or make ordinary `go install` produce a multi-platform carrier.

## Command ownership

Command execution is shared with the other build helper through
`scripts/owned_process.py`. It supports single-threaded Python on 64-bit
macOS/Linux x86-64 or arm64, with default SIGCHLD handling and open standard
file descriptors. Unsupported conditions refuse command launch. Captured
stdout and stderr have a combined 32 MiB limit.

The controller observes the original child without reaping it, then closes
process-group signalling authority before its first final wait. Cancellation
uses bounded TERM/KILL and pipe-drain stages. An external reaper, denied group
signal or detached pipe owner produces an explicit failure; those states do
not establish completed cleanup. Descendants outside the retained group are
never followed or signalled.

The native regressions run with:

```sh
python3 -B scripts/test_owned_process.py
```
