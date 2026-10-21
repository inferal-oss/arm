# Validation record

## October 21, 2026: user installation target

`make install` now builds with CGo disabled and installs the executable under `~/.local/bin` by default. Verification used an isolated prefix with spaces, checked the installed binary's mode (0755), compared its content with the build output, invoked its version command, repeated installation, and checked package staging with `DESTDIR` and `/usr/local` as the prefix. These checks did not install ARM into live agent configuration or start a persistent user service. `PREFIX` and `BINDIR` are configurable; agent instructions remain a separate `arm install` operation.

## October 21, 2026: managed storage lifecycle

Dedicated storage leases now have backend/CLI coverage for independent execution and storage lifetimes, multiple supervised consumers, retained output after command exit, deadline validation, queued cancellation, cleanup preview, revocation of attached consumers, open-file blocking, interrupted quarantine/restart recovery, explicit handoff, and preserving handed-off data. Populated cleanup remains charged when physical reclamation is unverified; the tests assert that namespace removal alone does not return its disk budget.

Provider tests preserve outside sentinels reached by symlinks and hard links, reject replaced roots and artifacts, honor per-step deletion budgets, require recorded creation-time identity to resist inode reuse, and refuse directories lacking ownership identity. Native macOS checks use actual `lsof`; Linux checks use actual `/proc` descriptors and mappings. Mount-ID and filesystem-boundary checks are implemented; privileged bind-mount race fixtures and remount identity resolution remain additional acceptance work. The same-OS-user activity scan does not establish containment against privileged or deliberately hostile processes.

Actual offline Cargo builds passed on macOS and Linux arm64, retaining the executable in the attached storage lease's target directory without creating the project's ordinary target directory. Tests reject explicit target-directory overrides and cover failed process launch without leaving a phantom storage consumer. These checks establish the initial cooperative profile, not full Cargo alias/configuration/wrapper support.

The expanded compiled CLI smoke test passed on both platforms, covering storage creation, retained output, read-only cleanup preview, uninstall refusal for retained storage, explicit handoff with preserved files, conservative cleanup accounting, and the earlier admission/cancellation checks. The full macOS race suite passed after correcting an overly short combined deadline for consecutive healthy native activity queries. Each activity query remains bounded independently; ambiguous or timed-out checks refuse deletion. Linux tests run as the unprivileged, isolated container described below, with creation-time and mount identity checks enabled.

The development toolchain was also installed independently at `~/.local/share/go/1.27.3` and added to Fish's persistent user paths. A fresh Fish login shell resolved Go 1.27.3 and built the project using plain `make build`, with default user Go module/build caches and no project-specific environment overrides.

## October 21, 2026: host observations and Linux runtime

The official Go stable feed still reports Go 1.27.3. The new observation package and storage inspection command passed `go vet ./...` and the full macOS race suite, with `ARM_TEST_NATIVE_APFS=1` requiring actual container metadata rather than skipping unavailable native access. The compiled macOS CLI lifecycle check also passed.

Native APFS checks exercised a clone created with `clonefile`, sparse file allocation, hard-link deduplication, outside-symlink exclusion with an intact sentinel, and the distinct system/data volumes resolving to the same persistent container UUID. Logical and allocated counts stay separate, while exclusive and reclaimable bytes remain unavailable. Missing-path and malformed-counter checks confirm unknown is represented as `null`. Permission-restricted sandbox runs retain unavailable pool metadata; they are not substituted for the required unsandboxed APFS test.

All local test executables also passed on an actual Linux arm64 kernel (`7.2.9-orbstack-00391-g2b298d05fbd4`) in a disposable container. Tests covered agents' validation logic, private local state, installation/uninstallation, observation, manager admission/recovery/supervision, server authentication/streaming, and CLI behavior. The compiled Linux executable then passed the background-daemon and queued-cancellation smoke test, including the new storage inspection API. Build metadata reports Go 1.27.3 and the binary was built with `CGO_ENABLED=0`.

The Linux container ran as an unprivileged user, with no network, no capabilities, a read-only root, resource limits, a private temporary filesystem and only read-only test fixtures mounted from the host. `scripts/test-linux.py` makes this repeatable using a caller-selected cached Linux image with `/bin/sh` and Python 3; it does not pull an image or mount credentials, source, or the Docker socket. Native Linux tests were CGo-free executables, not race-instrumented builds; the race suite ran on macOS.

Linux amd64 and arm64 release cross-builds passed. Linux filesystem fixtures used tmpfs; these results do not validate Btrfs/XFS/ZFS-specific accounting or a native service installation. Snapshot creation, exact exclusive/reclaimable APFS extents, sleep/wake, pressure-aware admission, per-lease usage, GPU and port providers, and automatic warning/escalation remain unvalidated implementation work. Real authenticated Codex/Claude messaging was last exercised in the earlier milestone below; these observation changes did not alter those adapters.

## October 20, 2026: initial local milestone

Validated October 20, 2026, on Darwin 25.6.0 arm64 with Go 1.27.3. The toolchain was selected from the [official stable release feed](https://go.dev/dl/?mode=json) and its archive checksum verified. The ARM release build uses `CGO_ENABLED=0` and `modernc.org/sqlite` v1.60.1.

## Automated and compiled binary checks

| Check | Result |
| --- | --- |
| `go vet ./...` | Passed. |
| `go test -race ./...` | Passed. |
| CLI and backend tests repeated five times under the race detector | Passed after fixing a shutdown/checkpoint race. |
| `python3 scripts/test-local.py` | Passed against the compiled executable. |
| macOS arm64 build with CGo disabled | Passed; binary build metadata confirms Go 1.27.3 and `CGO_ENABLED=0`. |
| Linux amd64 and arm64 builds with CGo disabled | Passed cross-compilation. |
| Native Linux runtime tests | Pending; cross-compilation does not establish runtime compatibility. |

The compiled CLI check starts a background daemon in a private temporary state directory, browses budgets and consuming leases including their reasons, queues a command behind an occupied budget, interrupts the waiting CLI, and verifies that the cancelled command does not execute when capacity becomes available. It then releases the remaining lease and stops the daemon.

The Go suite also exercises expiry, process descendants, request cancellation arriving before admission, recovered process groups, multiplexed WebSocket events and responses, authentication and origin checks, duplicate authorities, persistent messages, and filesystem safeguards around integration removal.

## Real agent CLI checks

The native integration suite ran outside the credential-restricting sandbox with the existing sign-ins and dedicated test sessions.

| Agent | Installed version | Verified behavior |
| --- | --- | --- |
| Codex | `codex-cli 0.162.1` | Loaded ARM instructions in an isolated Codex configuration; accepted a message through its native queue command; the registered session returned the expected acknowledgement; received an operator broadcast. |
| Claude | `2.1.306 (Claude Code)` | Loaded project-scoped ARM instructions in a temporary project; resumed an exact idle session and returned the expected acknowledgement under the same session ID; its reply reached the operator inbox; received a broadcast. |

The suite also passed:

- Installation preview without writes and repeat installation without duplicate blocks.
- Operator inbox persistence and delivery results for the selected registered recipients.
- Uninstall refusal while a lease was outstanding.
- Uninstall preview, removal of unchanged managed blocks, preservation of user edits added after installation, and repeat uninstall.

Separate installer tests verified edited managed-block preservation, original file permissions, interrupted installation, symlink rejection, replaced-directory protection, and exclusive integration operations. Private state-directory checks leave existing directory permissions unchanged.

Codex acceptance by its queue command and the subsequent model acknowledgement are separate observations. Claude active-session injection is not implemented or claimed; the tested path requires an explicitly idle session and permits a response-only turn.

The tests did not alter the user's global agent instruction files or message unrelated sessions. Codex used an isolated configuration root with an authentication symlink; no credential contents were copied or printed. Claude used its existing keychain sign-in and a temporary project; native session history may retain the dedicated test transcripts. JSON results are written to `.cache/live-agent-results.json` for local inspection.

## Scope of this result

The October 20 checks establish the first local backend and CLI milestone. They do not establish APFS allocation accuracy, physical usage enforcement, managed artifact reclamation, GPU or port-provider behavior, native daemon services, web/TUI functionality, or remote scheduling. The October 21 additions above establish limited read-only observations, not exact reclaimable extents or enforcement. Remaining work is tracked in [PLAN.md](PLAN.md), [IMPLEMENTATION.md](IMPLEMENTATION.md), and [SPEC.md](../SPEC.md).
