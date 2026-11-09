# Host guardian

The host guardian protects a host from programs that ARM does not manage. It
watches CPU, memory and I/O pressure. When pressure stays high, it acts on the
heaviest unmanaged program through an escalation ladder, before the host stops
responding. It is opt-in and separate from lease policy. The governing rules
are in [SPEC.md](../SPEC.md#host-guardian).

## Pressure

The shared host monitor samples pressure every two seconds and adds it to each
monitor sample (`pressure` in `arm monitor --json`) and to its bounded history.
Each resource has a level: `normal`, `elevated`, `high`, `critical`, or
`unknown`. A reading reports the highest level among its known signals. A
resource with no known signal is unknown, and unknown never causes action.

| Resource | Linux | macOS |
|---|---|---|
| CPU | PSI `cpu some avg10` (25/50/80 %); without PSI, 1-minute load per CPU (1.25/2/4) | 1-minute load per CPU (1.25/2/4) |
| Memory | PSI `memory some` (5/15/30 %) and `full` (1/5/15 %); without PSI, RAM available (15/8/4 %) | Kernel memory status (warn → elevated, critical → critical) |
| Memory, both | Swap traffic (4/32/128 MiB/s); swap remaining below 20 % (elevated at most) | Same |
| I/O | PSI `io some` (20/40/60 %) and `full` (5/15/30 %); without PSI, busiest disk queue | Busiest disk queue (2/8/32 requests in service) |

The numbers are the elevated, high and critical boundaries. They are
documented heuristics, not kernel semantics. Where PSI exists, it is
authoritative and the fallback signals are shown with a normal level. The disk
queue is the device service-time counter delta divided by elapsed time
(diskstats weighted milliseconds on Linux, `IOBlockStorageDriver` Total Time on
macOS). macOS has no stall-time counters, so its CPU and I/O levels are
fallback estimates.

## Programs

Every guardian tick enumerates host processes (at most 8192) through bounded
native interfaces: libproc on macOS and `/proc` on Linux. It reads names,
executable paths, cgroups, CPU time, memory footprint or resident size, and
storage bytes. It does not read environments. Command lines are read only for
Chromium-family browser helpers, and only to find `--type=` and
`--extension-process`. Rates come from two inventories matched by full process
identity, so a reused PID never inherits counters.

Processes are grouped into programs:

- **app-bundle**: the outermost macOS `.app` bundle, such as every Chrome
  helper under `Google Chrome.app`.
- **service-unit**: a dedicated systemd scope or service, such as
  `app-gnome-firefox-1234.scope`. Login sessions, the user manager and
  terminal tab or pane scopes (`vte-spawn-*`, `tmux-spawn-*`) are not groups,
  so a shell is never grouped with its jobs.
- **process-group**: otherwise, a process group, keyed by its leader's full
  identity.

## Eligibility

A group can be a target only if every member is eligible. The guardian never
targets:

- ARM itself or any process ARM started;
- ARM-supervised work: live lease roots, their descendants and their process
  groups;
- processes of other users, and any process when the guardian runs as root;
- PID 1, kernel threads, processes whose executable is unknown, system
  cgroups (`/system.slice/`, `/init.scope`, `/kubepods`), and paths under
  `/System/`, `/usr/libexec/`, `/usr/sbin/`, `/sbin/` and systemd directories;
- session, display, audio, terminal, multiplexer and remote-access programs,
  such as WindowServer, loginwindow, Dock, Finder, gnome-shell, Xorg, sshd,
  tmux and Terminal;
- operator entries in `guardian.protect`.

A culprit must also use a significant share of the resource: at least the
larger of one core and 15 % of CPUs, 10 % of RAM, or 8 MiB/s of storage I/O.

## Ladder

An incident opens when a resource reaches `high`. It closes after `recover`
(60 s) at `elevated` or below, and then every reversible action it took is
undone. Each step runs once per ladder pass, after its delay and only at its
minimum level, with `cooldown` between applied steps.

| Step | Effect | Manned default | Unmanned default |
|---|---|---|---|
| notify | Operator inbox message and desktop notification | high, 15 s | high, 15 s |
| deprioritize | macOS background policy (low CPU priority, efficiency cores, throttled I/O); Linux idle I/O class and nice 10 on every thread. Not used for memory. New members of the group follow. | high, 30 s | high, 30 s |
| ask | Request to the registered agent whose CLI is an ancestor of the program, matched by adapter and working directory | high, 45 s | high, 45 s |
| shed | Graceful termination of up to three of the busiest page renderers of a Chromium-based browser. The browser shows those tabs as crashed and keeps running. Extensions and Electron applications are excluded. | critical, 60 s | high, 90 s |
| freeze | SIGSTOP on every member not already stopped by its owner, then resume after `freeze_max` (5 min) or recovery. On Linux, a frozen dedicated service unit is asked to reclaim memory. macOS has no such interface. | — | critical, 120 s |
| terminate | SIGTERM, leaves first, then SIGKILL after `terminate_grace` to survivors with the same identity | — | critical, 240 s |

Mode gating: `observe` records every step as `planned`; `advise` executes
notify and ask only; `act` executes all. Process steps count against
`max_actions_per_hour`. When a pass took a process step and pressure persists
for another recovery period, the ladder restarts after notification, so a new
culprit can be handled. A pass of only skipped steps does not repeat.

Freezing removes CPU use but does not by itself release memory. Under CPU
pressure, a frozen group is therefore no longer the culprit.

## Safety

- Each signal and priority change revalidates the target's identity (PID,
  start time, user, process group and boot namespace) immediately before
  acting. On Linux, a process descriptor is opened first, so PID reuse cannot
  redirect the signal. macOS has no process descriptors and keeps a short
  window between revalidation and delivery.
- One action records and acts on at most 512 processes, so every target it
  signals can be reverted. Sticky deprioritization drops exited targets and
  stays within the same bound.
- Undo matches a target by PID, start time and boot namespace, so a process
  that changed its process group or session is still restored.
- Reversible actions are journaled as `pending` before their first signal. The
  journal is `guardian/actions.jsonl` in the ARM home, bounded and rewritten
  atomically. Unreverted reversible records are always kept.
- At startup, even with the guardian off, the guardian resumes and restores
  the priority of targets left by an earlier run whose identity still matches.
  On shutdown it reverts its active reversible actions.
- A revert that fails is retried up to five times before being reported,
  including after startup and when the guardian is off.
- `arm guardian undo ACTION_ID` (or the dashboard Undo button) resumes a
  frozen program or restores its priority. The program is then spared for the
  rest of the incident.
- Messaging hooks run outside the guardian lock, after process actions.

## Interfaces

- `arm guardian status [--json]` shows a one-line verdict, mode, pressure,
  incidents, the heaviest programs and recent actions with undo commands.
  `--watch` keeps refreshing every `--interval` (2 s by default): a live view
  in a terminal, or one JSON object per line with `--json` or when output is
  redirected. A failed read becomes an `error` line and is retried.
- `arm guardian ladder` shows the ladder the next backend start uses.
  `edit` opens an interactive editor; `set STEP [--level] [--after]`,
  `remove STEP`, `preset manned|unmanned` and `reset` change it from scripts.
  They edit `config.toml` offline and apply at the next backend start.
- `arm guardian undo ACTION_ID` reverts one action.
- WebSocket operations `guardian_status` and `guardian_undo`, both allowed for
  the local dashboard. `guardian_status` stays available while a node is
  stopping.
- Both dashboards have a Guardian page beside Monitor, opened with `g`. The
  number keys of the other views do not change. The page shows the same
  verdict as `arm guardian status` (unknown is never reported as clear),
  pressure, mode, role, hourly budget, the ladder, open incidents, the
  heaviest programs and recent actions with undo. It reads the guardian every
  two seconds while visible in the terminal and every four seconds in the
  browser. A remote node's dashboard has no Guardian page.
- In the terminal page, `↑↓` select an action, `Enter` inspects it, `x` asks to
  undo it and `y` confirms; any other key keeps it.
- The terminal Monitor view starts with a pressure window and the guardian's
  latest state. The web Monitor view shows a Host pressure card with level
  history and a Host guardian summary that opens the Guardian page.

## Limitations

- Thresholds are heuristics. macOS CPU and I/O levels are estimates without
  stall counters.
- macOS restore clears background policy even if the program was already in
  the background before the guardian lowered it.
- On Linux, an unprivileged guardian cannot lower a nice value again. Restore
  resets the I/O class and records the kept nice value as an expected partial
  result, not a failure.
- Inventory rates are interval estimates from non-atomic samples. Shared
  memory can be counted in several members.
- Agent attribution requires an ancestor `claude` or `codex` process and
  exactly one registered local contact with the same adapter and working
  directory.
- Desktop notifications use `osascript` on macOS and `notify-send` on Linux,
  when available.
