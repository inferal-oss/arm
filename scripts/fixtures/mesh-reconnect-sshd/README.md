# Private SSH test endpoint

This developer fixture serves one fixed command, `ARM_BINARY mesh bootstrap`,
against one disposable ARM home. It is a test prerequisite, not a node service
or a public reconnect implementation.

Build it with `go build ./scripts/fixtures/mesh-reconnect-sshd`. Run its tests with:

```sh
go test -race ./scripts/fixtures/mesh-reconnect-sshd
```

The shutdown regression uses generated in-memory keys, loopback connections and
an owned sleep helper. It does not run ARM, external SSH or change host settings.

An integration harness supplies absolute paths for `--arm`, `--home`,
`--host-key`, `--client-public-key`, `--ready`, `--events` and `--fault-control`.
Use newly generated fixture keys and a private directory. The endpoint accepts
only the exact current account/client key and literal `127.0.0.1` with a numeric
port (`--listen 127.0.0.1:0` by default). It rejects shell, forwarding and PTY
requests. Readiness records its selected address, public host key, PID and ARM
image hash; it never edits `authorized_keys` or SSH configuration.

Input/output, concurrent connections/sessions, request count and helper lifetimes
are bounded. An exact-key, one-shot fault can drop a revocation request before
execution or discard its reply after the real helper returns. Its event log
retains the actual helper exit, whether `Wait` failed, and input/output hashes.
Only a zero native exit with a successful `Wait` forwards captured output and
returns SSH success. Signalled helpers, cancellation and incomplete output
(including `WaitDelay`) return failure; their actual exit remains in the log.

Parent cancellation or whole-connection closure joins every session through
its helper's actual `Wait` and output-copy completion. Closing an individual
session while retaining the connection is not a separate cancellation trigger;
its helper is still bounded by the fixture timeout. This joins the fixed helper; ARM backends
that it starts must be stopped and verified separately by the integration
harness. It does not establish descendant or resource-obligation cleanup.
