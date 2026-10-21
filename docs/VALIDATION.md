# Validation record

Custom host/global pools and transactional capacity increases/reductions were added to the specification and plan on October 21. They are not implemented or validated by the checks below.

## October 21, 2026: polling inbox and explicit acknowledgement

The native macOS race suite and `go vet ./...` passed with durable recipient inboxes, registration-scoped cursors, explicit receipt/acknowledgement and resumable instruction upgrades. Manager tests exercise private and broadcast fanout, no implicit receipt, idempotent acknowledgement/operator replies, pagination and pending filters, contact/session replacement, corrupt/wrong-host/future cursors, cancellation and the 64-request limit, legacy contact migration without guessed historical replay, restart recovery and a late native result preserving receipts. WebSocket tests hold a long poll while another response completes, reject a ninth in-flight request, and release all eight polls on a message.

The compiled `scripts/test-inbox.py` check passed on macOS and the user-designated OrbStack Linux machine: exact recipient selection, explicit receipt and acknowledgement, one operator reply, watcher reconnection/cursor recovery after backend shutdown/restart, interrupt exit status, and refusing silent contact replacement. The complete Linux test executables and all three compiled CLI smoke checks passed under the previously described unprivileged private process namespace. This does not change the documented host-wide storage observation limitation. The new inbox smoke is included in `make test-local` and both Linux harnesses.

Real authenticated tests again passed outside the sandbox with existing sign-ins and dedicated temporary projects/sessions: Codex `0.162.1` loaded instructions, accepted an exact-session queue message and returned its acknowledgement; Claude `2.1.306` loaded instructions, resumed the exact idle session, and returned its reply. Both received the operator broadcast. The same Claude session then changed to polling delivery, executed `arm agent watch --once` and `arm agent ack` via a narrowly allowed Bash command prefix, and produced a persisted acknowledgement and linked operator reply. ARM did not invoke a new Claude conversation to simulate polling delivery. This proves explicit tool-driven polling/acknowledgement in a resumed session, not active-session injection or automatic background/subagent forwarding.

Install/uninstall checks again passed preview, repeat install, active-lease refusal, preserving later user edits, removal preview and repeat uninstall. Upgrade tests cover old-block replacement, interrupted writes before/after replacement, uninstall at both boundaries, unchanged previews and file modes. Final review tests reject stale delivery policies and contact changes during pending native delivery, and preserve ambiguous files containing both upgrade versions; these also passed directly on the OrbStack host after the fixes. The `.cache/live-agent-results.json` report records the real CLI results; global user instruction files and unrelated agent sessions were unchanged.

## October 21, 2026: CLI parsing, defaults and help

The complete macOS race suite with required native APFS metadata and `go vet ./...` passed after the CLI review. Regression tests exercise `storage create SIZE -reason ...`, flags on either side of storage sizes and lease IDs, the retained `--disk` alternative, no-argument port reservations, explicit and positional port forms, and preservation of child arguments after either supported `run` boundary. Tests browse every leaf and group help entry without a backend or state-directory creation, verify unrelated options are absent, and reject extra operands, missing values, unsupported flags, conflicting positional/flag forms and ignored JSON settings before backend or installation changes.

The entire CLI test executable also passed directly under the configured unprivileged login on `LINUX_HOST`, without process-namespace isolation. The CGo-free compiled macOS binary passed both the storage/lifecycle/install-uninstall guard/cancellation smoke test and the port activation/recovery/native inventory smoke test. These client parsing changes do not require a running backend restart; they do not add OS resource quotas or complete the remaining specification.

## October 21, 2026: integrated port leases and OrbStack SSH acceptance

Port admission now has tests for atomic resource/port grants, queue waiting without partial budgets, normalized idempotency, exact expiry, dedicated parent/consumer lifetimes and denied renewal. Actual inherited TCP/UDP servers pass traffic through the supervised launch gate. Revocation tests keep a cooperative unrelated socket alive and charged, and an escaped inherited socket remains `needs_attention` until the fixture exits itself. Recovery tests prove saved descriptors are unknown rather than silently restored. Linux inventory tests distinguish held-inode claims from cooperative-ledger overlap; native fixtures retrieve their own process and start identity on both platforms. CLI tests cover public port commands, activation metadata, consumer attribution without double charging, and protected backend accounting.

`go vet ./...`, the complete native macOS race suite with required APFS metadata, and a Linux amd64 CGo-free build passed. The final port-provider tests also passed three consecutive macOS race runs after correcting an invalid test assumption: an IPv6-only ephemeral port can already be occupied in the independent IPv4 family. The fixture now distinguishes that occupancy from a broken IPv6-only setting before retrying. Native `lsof` unbound UDP records (`*:*`) are excluded from bound-port inventory; malformed process ownership remains incomplete data.

The compiled macOS and Linux executables passed `scripts/test-ports.py`: inherited TCP/UDP traffic, cooperative revocation with a surviving listener, exact test-owned daemon crash/restart, unknown-descriptor recovery, successful later reconciliation, native discovery, and refusal to release the backend resource as a workload lease. The previous compiled storage/lifecycle/cancellation smoke test also passed in the complete Linux runs. The isolated Linux container acceptance suite passed, including an actual offline Cargo build.

The user-designated machine `USER@LINUX_HOST` reports Linux `7.2.9-orbstack-00391-g2b298d05fbd4`, aarch64, an unprivileged UID. Direct login tests passed port provider/admission/activation, escaped listeners, recovery, CLI, native inventory and the new denied-observation storage fixture. The initial unrestricted full run correctly exposed denied `/proc` access to the same-user `sd-pam` service: successful-cleanup fixtures could not run, while ARM preserved files and its reservation. No host permissions or services were changed to make those tests pass. A separate full run under unprivileged `unshare --user --map-current-user --pid --fork --mount-proc` passed all applicable suites and both compiled CLI smoke tests. That private process namespace establishes successful-cleanup behavior with observable fixture processes; it does not establish host-wide observation privileges. The direct-host failure-path test separately proves preservation and accounting under the actual restriction.

`scripts/test-linux-ssh.py` makes these checks repeatable. It uploads only CGo-free test executables built with Go 1.27.3, ARM and smoke-test scripts into a unique remote temporary directory. The host needs no Go installation. Successful fixtures are removed, failed fixtures are retained for inspection, and targeted suites are supported. A recovery test was corrected to create its own process group inside the current PID namespace rather than assuming an inherited PGID is visible. Cargo, Codex and Claude were absent from this machine: Cargo was explicitly skipped there and tested in the separate Linux container; real agent-CLI delivery was not claimed or retested for this feature. Cross-namespace port reconciliation, IPv6 socket-option discovery and stronger process containment remain implementation work.

## October 21, 2026: port provider foundations

The internal port provider passed native macOS race tests and CGo-free executable tests on the actual Linux arm64 kernel described below. Checks exercise TCP/UDP separation, IPv4 wildcard conflicts, IPv6-only and dual-stack behavior, bounded range selection, rollback of an earlier bind when another claim fails, cooperative-ledger conflicts, stale namespace refusal and competing simultaneous binds. A subprocess receives actual bound TCP and UDP descriptors, serves traffic through both, and exits while the parent's copies continue holding the ports. The final probes succeed only after those copies close.

The full macOS race suite, `go vet ./...`, the expanded Linux acceptance harness and compiled Linux CLI smoke test passed. After adding the concurrent-bind case, the port package passed again under the macOS race detector and in the unprivileged Linux container. This establishes provider primitives only: port leases, restart reconciliation, inventory/owner attribution, CLI commands and operator views are not yet integrated. Existing Codex/Claude adapters were unchanged and authenticated sessions were not re-exercised for this work.

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
