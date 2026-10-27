# Routed TCP forwarding: relay-loss acceptance receipt

This receipt records two successful native CI runs of the review-gated B→A→C
TCP forwarding fault fixture. It is evidence for the narrow relay-loss behavior
below, not approval to enable production forwarding. The relay carries traffic;
the destination's grant names the original source and local service. The
fixture also configures A's existing generic workspace relay transport grant.
That test setup does not settle the final relay admission policy.

## Tested revisions and jobs

| Run | Tested commit | Native jobs and result |
| --- | --- | --- |
| [40451815199](https://github.com/inferal-oss/arm/actions/runs/40451815199) | `cb6aff2a143aecbd5996367fe2db364eeb980b52` | Ubuntu 24.04 `122016056768` and macOS 15 `122016057083`: review-only Unix control, review build, and relay-loss fixture succeeded |
| [40452672550](https://github.com/inferal-oss/arm/actions/runs/40452672550) | `56553bc04907d4c39c3298dade74c3d67c2475b3` | Ubuntu 24.04 `122018784176` and macOS 15 `122018783964`: bounded diagnostic control, review build, and relay-loss fixture succeeded |

GitHub's job metadata reports each named step and both jobs as `success`. The
decoded job logs contain checkout fetches for the commits above, the commands
`go build -tags arm_forward_review -o "$RUNNER_TEMP/arm" ./cmd/arm` and
`python3 -B scripts/test-routed-forward-loss.py --arm "$RUNNER_TEMP/arm"`, and
the fixture's `PASS public CLI B→A→C ...` line. The later run is the final
bounded fixture; the earlier run checks the same core forwarding inputs before
the fixture's diagnostic bound change.

The following SHA-256 values identify the **UTF-8 decoded job-log text**
returned by the GitHub connector, including its leading BOM, not GitHub's
compressed download bytes. The step excerpt is the exact byte sequence from
the `##[group]Run python3 -B scripts/test-routed-forward-loss.py` line through
the fixture's `PASS public CLI` line, inclusive. Raw logs and private runner
paths stay outside Git.

| Run / OS | Job log SHA-256 | Relay-loss step excerpt SHA-256 |
| --- | --- | --- |
| 40451815199 / Ubuntu 24.04 | `57bfe32010d7ad9a566c859d6660c07055798bc58f3dfd1e70b1f29b2766dbe7` | `8f5b4cc1fc5ae5742dd7c844fb60c03e1e61aaab5d5b6fb7db9fb2999346ee54` |
| 40451815199 / macOS 15 | `73ac13eea9ecbb8f1579600438248f0e1393cf09e5e02accf2ee2a034a7f474c` | `f722af816402e003091ce3af187b6dd16255d3528a3d1494f61e53090b5045c3` |
| 40452672550 / Ubuntu 24.04 | `d589466af8aa360d5207bbc99c2021c086ce9250cda50b15f8544600698519a5` | `5427e388deb6350e16674927718aeaad532bc3ca1f4948311a856e4836dd9034` |
| 40452672550 / macOS 15 | `5ea63080af8a60380abd75713977b37862fbdf8960f2f79181432ab501b0233d` | `7e235fd10459684fcae1983e7e6fe9babde7ce510e4251056cf0f86387db1893` |

## Source identity

These are hashes of Git objects at each tested commit, calculated after the
runs. They were **not** emitted as a manifest by the CI jobs. The selected core
manifest covers `go.mod`, `go.sum`, production `.go` files in
`internal/meshforward/` and `internal/meshsshrelay/` (excluding tests and the
Unix prototype), production `internal/server/mesh_forward*.go` plus
`internal/server/mesh_ssh_route.go`, production
`internal/cli/mesh_forward*.go` (excluding tests and the Unix prototype),
`internal/manager/forward_listener.go`, and
`internal/manager/relay_transit.go`. For every selected file in sorted path
order, concatenate a line of the form `<file SHA-256>  <repository path>\n`;
then SHA-256 that UTF-8 manifest. This is a selected source-input comparison,
not a complete Go build dependency closure.

| Tested commit | Selected core files | Core manifest SHA-256 | Core plus fixture/workflow files | Combined manifest SHA-256 |
| --- | ---: | --- | ---: | --- |
| `cb6aff2` | 33 | `8d075934b7431f9fbd0f06768acd6433eb2e64b9b4fe8f19b64b79feb6b4c3c1` | 35 | `bc6525e88ec05c11008c49898a6160f56454a57033534e78b9f318101bdd81ed` |
| `56553bc` | 33 | `8d075934b7431f9fbd0f06768acd6433eb2e64b9b4fe8f19b64b79feb6b4c3c1` | 36 | `f24c29d870ed3a11e8f7d8e939671adcf7701b7def57de70f63b192a6873d06f` |

The combined manifest adds the files present at that revision among
`.github/workflows/forward-route-loss-candidate.yml`,
`scripts/test-routed-forward-loss.py`, and
`scripts/test-routed-forward-loss-bounds.py`. Individual SHA-256 values:

| File | `cb6aff2` | `56553bc` |
| --- | --- | --- |
| Relay-loss fixture | `efd9505d4d1cb0699c8b90508f3b78c20a097a9690f4e3608e699c2264bf2c01` | `01e715940a35a55ce75dcc235b73569b7f244dd8d89b6aae9def18e9110ed8a9` |
| Bounded diagnostic control | absent | `ed91b6a4911a591b9cc4a9534679a92f1d6dfb6c7bbafa5e57c893a28ba8bea2` |
| Branch-only workflow | `29b3813472fa16521133d0c72253ac95d0172ef56359e50e60543bdb9056cd79` | `9751bacfc1e9412b282165974d9154d49fe8bb510d380c5e32218e1d8e794725` |

## What the fixture actually asserts

The fixture launches original foreground `serve` children with owned `Popen`
handles and requires default SIGCHLD handling and exclusive reaping before
launch. It enrolls B and C only with A, admits both to one workspace, and
asserts B and C each report exactly A as their only mesh peer; B's roster
contains no C. A gets generic workspace relay transport capacity, C grants
one literal-loopback `effect` service to B's original host identity, and B
opens a 90-second source listener with a mode-0600 token file.

An HTTP request from B through A reaches C's service and records exactly one
effect while its response is held. The fixture signals `SIGKILL` only through
A's original unreaped child handle, waits for that exact child once, checks
exit `-SIGKILL` and release of A's daemon lock, and then releases the service
response. B's client must fail with `remote_outcome_unknown`, empty stdout,
and no second effect observed at client exit. Its output files are mode 0600 and checked against the
64-KiB bound while the request is live and after it exits. B's active
connection count must reach zero; C's `tcp-final:` hold must be terminal or
`needs_attention`. Restarting A must mint a second foreground child handle,
and its relay-transit hold must remain `needs_attention` with outcome
`relay_supervision_lost`. B's listener stop must become terminal or explicitly
retain uncertainty; if it retains uncertainty, the fixture exercises public
recovery to `reconciled`.

The CI logs report the fixture's single `PASS` line, rather than a separate
machine-readable receipt for each internal assertion. The assertions above
are evidenced by the tested fixture source plus successful step completion;
their individual runtime values were not exported from the private test homes.

## Boundaries of this receipt

Neither run captured before/after source manifests on the runner, an executable
hash or Go buildinfo for the built binary, or a separate native Manager owner
receipt. The after-the-fact Git-object hashes above do not fill those gaps.
This fixture injects one local A process death after one effect; it does not
prove a physical network partition, alternate-path recovery, cross-host
behavior, unmodified TCP or browser clients, or a production enablement
decision. Keep the forwarding production fence until those separately scoped
checks and authority decisions are completed.

## Preserved earlier failures

[Run 40450538894](https://github.com/inferal-oss/arm/actions/runs/40450538894)
failed its fault-fixture step on both platforms. The first fixture used the
wrong source-status field and had an endpoint-PID-based cleanup path. Those
results do not establish fault or cleanup acceptance. The final controller
uses retained foreground child handles instead.

[Run 40451105620](https://github.com/inferal-oss/arm/actions/runs/40451105620)
also failed: Ubuntu reached the destination-hold check with the wrong request
key field; macOS did not observe the destination effect within its original
window, with insufficient early-exit diagnostics to establish why. Neither is
relabeled as a pass or a diagnosed capacity failure. The successful successors
use the actual status/request JSON fields, bounded diagnostics and their own
exact source revisions. The live wait checks file sizes; these are not hard
filesystem-space quotas on foreground daemon logs.
