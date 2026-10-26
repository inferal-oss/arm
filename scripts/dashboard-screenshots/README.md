# Dashboard screenshots for the public website

The Pages job runs this helper before the website build. It builds the current
checkout's CLI privately, starts a foreground backend with a new temporary
state directory, creates a genuine example counted-pool lease, and captures:

- `web-monitor.png`: live physical observations in the embedded web Monitor.
- `web-resources.png`: the Resources page, including the example pool/lease.
- `terminal.png`: the real `arm dashboard terminal` Resources screen.

The terminal image is produced by replaying the command's actual 140×60 PTY
byte stream in pinned xterm.js and capturing that terminal renderer in Chromium.
It is not a separate implementation of the terminal dashboard. Native host
measurements, timing and runner capacity are real and can vary between builds;
these are illustrative CI captures, not a benchmark or a supported host limit.

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
