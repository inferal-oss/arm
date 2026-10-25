# Private SSH test endpoint

This developer fixture serves the exact `ARM_BINARY mesh bootstrap` command
and ARM’s fixed read-only platform probe, both bound to one selected private
image and disposable ARM home. It is a test prerequisite, not a node service
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
port (`--listen 127.0.0.1:0` by default). It rejects arbitrary shell commands,
forwarding and PTY requests. Readiness records its selected address, public host key, PID and ARM
image hash; it never edits `authorized_keys` or SSH configuration.

Input/output, concurrent connections/sessions, request count and helper lifetimes
are bounded. A closed one-shot fault selects exactly one revocation key (`{"key":"...","stage":"before|after"}`) or bootstrap Pair (`{"pair":"<64 lowercase hex>","stage":"before|after"}`), never both. It can drop that selected request before execution or discard its reply after the real helper returns. Pair controls do not match revocations, and revoke controls do not match Pair; wrong selectors leave the control unconsumed. Its event log
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

Readiness is published only after its complete private stage is synced and
closed, using an atomic no-overwrite link. Existing final entries and unrelated
staging names are never adopted or replaced. Changed staging identities are
preserved and reported as errors. Readers wait for absence only; malformed or
empty published readiness is a failure, never a retry instruction.

The read-only probe exact-matches the product’s canonical command for the
selected image/home and requires its exact stdin marker. The fixture executes
the original fixed script with trusted native argv; received shell text is never
evaluated. Its real OS/architecture/UID/CPU/image/startup observations are logged
under `read_only_probe`. It does not execute the selected ARM image, create its
state home, consume revocation/Pair fault controls, install or upload anything.
Changed scripts, another image/home, extra shell operations and malformed input
are refused. The same request/output/timeout/native Wait rules apply. The fixed
shell and OS utilities are trusted native dependencies; process groups are not
descendant containment or proof of resource-obligation cleanup.
