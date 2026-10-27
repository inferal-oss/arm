# Dashboard screenshots for the public website

The Pages job runs this helper before the website build. It builds the current
checkout's CLI privately, starts a foreground backend with a new temporary
state directory, creates a genuine example counted-pool lease, and captures:

- `web-monitor.png`: live physical observations in the embedded web Monitor.
- `web-resources.png`: the Resources page, including the example pool/lease.
- `terminal.png`: the real `arm dashboard terminal` Resources screen.

The terminal image is produced by replaying the command's actual 140×80 PTY
byte stream in pinned xterm.js and capturing that terminal renderer in Chromium.
It is not a separate implementation of the terminal dashboard. Native host
measurements, timing and runner capacity are real and can vary between builds;
these are illustrative CI captures, not a benchmark or a supported host limit.

The backend collects native observations for six seconds before the terminal
starts. Capture then drains at least six seconds of the Resources view's real
PTY frames, so the first startup labels are not mistaken for an observed status
update. This is a bounded collection window, not a requirement to show healthy
values: genuine unavailable measurements stay unavailable. The browser ticket
is issued after the PTY capture to preserve its short bootstrap lifetime.

The xterm.js dark terminal profile uses background `#101519`, foreground
`#e1e9ed`, and 14 px DejaVu Sans Mono, matching the dashboard's canvas/text colors
in `internal/tui/theme.go`. This sets the terminal emulator's normal/reset colors;
it does not alter application data, ANSI output, or the dashboard's panel colors.
The PTY and xterm.js share their bounded grid from `geometry.json` through the
private capture configuration. The 80-row height includes complete Resources
cards after live status and scope explanations arrive. Chromium's terminal
viewport grows to fit that grid with padding, and the PNG captures the whole
terminal element. Grid equality is checked before capture. Sampling durations,
terminal geometry and this profile are recorded in the public metadata.

Use a fresh GitHub Actions runner with Go selected by the repository's `go.mod`,
Node.js 26, Python 3 and Chromium system libraries:

```sh
cd scripts/dashboard-screenshots
npm ci --ignore-scripts
npx playwright install --with-deps chromium
cd ../..
python3 scripts/dashboard-screenshots/capture.py
```

The helper deliberately refuses execution outside GitHub Actions to prevent
publishing a workstation's host/peer context by accident. It never installs ARM
or reuses a live state directory. Its temporary backend, PTY, credentials and
raw transcript are stopped/removed on success and failure. Only the three PNGs
and `metadata.json` (source/binary/image hashes and image sizes) are placed in
`website/public/screenshots/`. Those generated files are ignored and Vite copies
them into the Pages artifact. Partial output is removed and fails the build.
No browser traces, videos, storage states, bootstrap URLs or transcripts are
uploaded. Dependency licenses remain in the build-only package; these libraries
are not bundled into the public website.

For a prebuilt executable from the same source, `--arm /absolute/path/to/arm`
avoids a second build. No local installed executable is selected automatically.
The helper's single Go build uses `GOMAXPROCS=1` and `-p 1`; a local heavy build
must still use the project's shared CPU queue. Source/lock metadata preparation
and Python/JavaScript syntax checks require no build or backend.

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
