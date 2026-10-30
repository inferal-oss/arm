# Private untrusted worker package manifest

Pure, unexported `parse`, `canonical` and `commitment` helpers only. No production
package imports this package. The private candidate/file types are declared data,
not approved packages, native handles, compatibility evidence or resource claims.
There is no filesystem, network, archive, build-info, signature/provenance,
approval, staging, admission or execution API/effect.

V1 requires exactly schema/os/arch/build/protocol/entrypoint/files, with schema 1,
four declared darwin/linux × amd64/arm64 combinations and entrypoint `arm`. Each
file has exactly path/size_bytes/sha256. Build/protocol identifiers are ASCII
alphanumeric first, followed by alphanumeric or `._+-`, at most 128/64 bytes.
Protocol is a declaration label; it does not negotiate a private worker channel.

The raw 64 KiB cap precedes UTF-8/JSON work. Incremental fixed-field parsing checks
each decoded key, duplicate and file count before accepting further objects.
Decoded escaped keys may canonicalize; unknown/case/Unicode-fold keys cannot.
Integers must be canonical nonnegative decimal digits, with checked uint64 parsing;
schema is exactly 1. Invalid UTF-8, trailing values and structural/type aliases
refuse. No JSON object is decoded into an unbounded map or ignored field set.

Files number 1..128 and include exactly one `arm` of at least one declared byte.
Other files are under exact `assets/`, may be empty, and use canonical nonempty
slash components. Paths are printable ASCII (including spaces/quotes), excluding
backslash, colon, percent, controls and absolute paths. Components `.` / `..`,
empty components, case-fold duplicates and file/directory prefix conflicts refuse.
Path/component/component-count maxima are 1024/128/64. Every digest is exactly
64 lowercase hex digits. Each file and checked total declares at most 512 MiB;
no bytes are read, reserved or allocated for that declared size.

Canonical JSON has fixed root/file field order, no extra whitespace/newline,
exact decimal integers and bytewise sorted file paths. It validates manually
constructed candidates too, sorts a copy, bounds output to 64 KiB and returns no
partial output on refusal. SHA256 is over these canonical declaration bytes.
It does not verify any declared content hash, executable/build/protocol, physical
size, package approval or native profile; existing mesh artifact validation is
untouched. The parser can represent the first local executable-plus-assets shape,
but cannot authorize its use.

Unit evidence covers golden bytes/hash, all four declared platforms, empty asset,
roundtrip/order/input preservation, changed-data commitments, exact cap controls,
strict objects/keys/numbers/UTF-8/paths/digests and malformed manual candidates.
No native fixture, benchmark, large payload, worker/backend or installation is
created. `arm start` and real compatible A-to-B API/UI job preservation remain
unimplemented feature gates.
