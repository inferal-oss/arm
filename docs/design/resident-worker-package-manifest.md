# Untrusted local package declaration v1

This is the private pure metadata cut on accepted
`2eec7dd15a6c3ef8c75aed92ba2ec0d967f6dfcf`. The
[parser/encoder](../../internal/workerpackage/manifest.go) and
[grammar/limits](../../internal/workerpackage/README.md) do not open a package or
mint an approval, native identity, compatibility result or resource capability.
They leave the [owner/staging contract](resident-worker-owner.md) and
[transaction/admission contract](resident-worker-transaction.md) unchanged.

Root fields are schema, os, arch, build, protocol, entrypoint and files; file
fields are path, size_bytes and sha256. The array declares an `arm` executable
and optional `assets/` files, with no directory entries, URL, archive, arbitrary
command, signature, feature authority or approved flag. Unknown fields refuse.
This supports declaration of the documented first trusted-local executable and
asset set. Native verification/approval still needs an independently reviewed
source, real held bytes/manifest membership and admitted immutable staging.

Parsing enforces a 64 KiB raw ceiling before UTF-8/JSON work and incrementally
validates exact decoded keys/counts. All seven root and three file fields are
required. Escaped spelling can decode to an exact key; duplicate decoded spelling,
case and Unicode-fold aliases cannot hide another value. Integer tokens reject
signs, exponent/fraction/leading-zero/overflow forms. Paths are bounded ASCII
data with canonical components, case-fold uniqueness and no file/directory
prefix ambiguity. Build/protocol labels use the
[private version alphabet](../../internal/resident/protocol.go#L32), with v1's
128/64-byte caps; the string label is not the actual negotiated frame version.

The encoder revalidates candidate data, copies/sorts files by bytewise path and
emits fixed root order `schema,os,arch,build,protocol,entrypoint,files` and file
order `path,size_bytes,sha256`, exact decimal bytes and deterministic JSON string
quoting. The whole canonical output must also fit 64 KiB. Its SHA256 commitment
changes with declared data and is independent of input field/file order or
ordinary equivalent JSON escapes. Input byte/slice order remains unchanged.

The 512 MiB per-file/total declared ceiling and 128-file limit constrain parsing
only. They are not native artifact/profile limits or memory/disk admission and
must not relax mesh Artifact.Validate, its resident-version check or actual
verification/staging bounds. Even an empty asset's declared digest is merely a
well-formed claim; this helper never checks it against content.

[Focused evidence](../validation/resident-worker-package-manifest-2026-10-29.json)
qualifies unit parsing/canonical behavior only. All current caller/Manager/Server/
native/mesh/CLI/schema/module/policy code remains unchanged. Later approval,
binding/retained closure, launch/controller, actual API/asset routing, first and
repeated `arm start` and real compatible distinct-version job-preserving replacement
remain separate mandatory gates. No successful parse implies overall completion.
