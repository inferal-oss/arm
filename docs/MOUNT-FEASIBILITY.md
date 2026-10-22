# Mounted-share adapter evidence

The approved [access scheme](MESH-ACCESS-PROPOSAL.md) requires unprivileged mounts on Linux and macOS, authenticated local access, destination-owned leases and near-network-speed transfers. No production mounted-share service or adapter is implemented. The results below retain that full requirement; an easier protocol or a fast loopback read does not complete it.

| Candidate | Actual evidence | Outstanding boundary |
| --- | --- | --- |
| Linux FUSE, read-only | Independent non-root mount, synthetic read and unmount passed. | Production origin/attachment leases, confinement, recovery, isolation and performance. Existing OS FUSE support is required. |
| macOS NFS with AUTH_SYS | Native non-root mount/read/unmount passed; a hand-written request claiming UID 0 was accepted. | Client-supplied UID/GID does not authenticate a local caller. This adapter cannot be selected without another proven boundary. |
| macOS NFS with RPCSEC_GSS privacy | Native GSS mutual authentication and encrypted NFS GETATTR/LOOKUP/READ passed in the isolated prototype. Null auth, forged AUTH_SYS UID 0, borrowed handles without GSS and replayed encrypted reads were refused. | A full kernel mount using only its exact private credentials remains unresolved. These are authenticated RPC results, not a mounted filesystem. |
| macOS native SMB | Wrong/absent credentials were promptly refused. | Correct credentials did not produce a usable native mount. Authenticated negotiation alone is insufficient. |
| macOS native WebDAV over TLS | Root independently repeated non-root mount/read/write/unmount, wrong/absent credential and untrusted-leaf refusal, with private per-invocation credentials and leaf trust. | General build-filesystem semantics fail: exclusive flock did not exclude another process; external updates can be stale; symlinks were unsupported. Production isolation, lifecycle and performance remain unverified. |

## Correctly authenticated NFS

The isolated RPCSEC_GSS prototype uses freshly generated synthetic principals, tickets and AES keys in a random `.INVALID` realm. It contacts no KDC and reads no existing/default credential cache. The acceptor holds a service key and checks the exact principal before serving synthetic in-memory data. Native Apple GSS imports only an explicit private FILE cache and verifies mutual authentication, request/reply integrity and privacy.

The prototype also created an exact temporary XCTEMP cache, recorded and synced its UUID before importing the two synthetic credentials, then destroyed only the held cache. It did not enumerate or switch the user's caches. This is credential interoperability evidence, not a shipped native helper or dependency decision.

The remaining problem is selecting that exact credential for the kernel mount. In the inspected Apple implementation, `principal=` takes a user-name path that can search the existing credential collection. The exact-UUID path is available through `NFS_IOC_SET_CRED` after mounting, but a Kerberos NFSv3 mount needs authenticated root metadata before that ioctl can run. A SYS bootstrap mount succeeded while exposing only constant synthetic root metadata and denying listing, but its credential ioctl returned ENOTSUP. Updating the mount did not provide an authentication transition. No protected data was made public to bypass this cycle.

Further research can test NFSv4 security negotiation or another exact-credential native entry point. Before selection it must pass actual correct-credential mount/read/unmount, wrong/absent/expired credential, replay/borrowed-handle and another-real-OS-user denial, plus cancellation, revocation and owned cleanup. The experimental server's context limits, expiry/replay implementation and dependencies also need production review.

Source references inspected by the prototype: [Apple NFS](https://github.com/apple-oss-distributions/NFS/tree/93733ffccece4daee73bf6306b291d1ae69130c0), [gssd](https://github.com/apple-oss-distributions/gssd/tree/e0aaeb03e7eb6a0311e7773a5582e38496f8eaf4) and [Heimdal](https://github.com/apple-oss-distributions/Heimdal/tree/1635de38a813f6e1dd3f8fd270683187ad4f02be). These source observations do not replace runtime gates.

## WebDAV: useful feasibility, limited semantics

The native probe passes random credentials through an immediately unlinked private seekable descriptor, with a single ephemeral certificate supplied to the native agent. Credentials appear in neither URL, argv nor environment. The inspected `-a` path disables keychain lookup; supplied leaf trust applies to that agent rather than the global trust store. UI is suppressed, secure authentication is required, and no system service or configuration is changed. These private native options require OS capability checks before any production use.

The private TLS server authenticates every method before accessing its synthetic in-memory filesystem. Root's independent repeat returned native exit 80 for a wrong password, exit 19 for absent credentials and exit 19 for an untrusted leaf, each without a mount or timeout. Correct credentials mounted in 193 ms. Exact small and 32 MiB reads, write/fsync/readback, rename, truncate and removal passed. Twelve independent wrong/absent-credential method requests returned 401. Fsync here acknowledges a memory-backed fixture, not durable origin storage.

Two processes both acquired exclusive nonblocking flock on the same file concurrently. Symlink creation returned ENOTSUP. A separate prototype observation saw stale data through fresh native opens for 40 seconds after an independent HTTPS write, with updated bytes appearing at 62.28 seconds; root repeated the immediate stale-read result. The inspected [Apple WebDAV source](https://github.com/apple-oss-distributions/webdavfs/tree/a239d37d7407700119e9c92f66f38ab0a296cb0f) corroborates the ineffective flock path and 60-second file validation interval. Cargo/Gradle/SQLite working trees cannot be advertised as safely supported by this adapter.

An explicitly immutable, server-enforced read-only artifact/input share is a possible separate capability. It still needs actual other-user isolation, owned launch/mount receipts, per-request lease checks, anchored filesystem confinement, credential expiry/revocation, native cache accounting, busy-mount recovery and strict reset proof. Delivered cached bytes cannot be retracted. This possible capability does not replace the full mounted-share requirement or authorize writable build shares.

The independent 32 MiB loopback sample took 60.10 ms through the native mount and 63.07 ms through an exact-leaf-pinned direct HTTPS client. Other prototype samples varied substantially. These small memory-backed measurements establish neither physical cold-origin behavior, durable writes, LAN/WAN performance nor the required 85–90% same-host-pair encrypted-stream target. Metadata-heavy builds and simultaneous consumers need separate tests.

All independent fixture mounts were removed, three captured exact native agent identities were absent after unmount, no associated native cache directories remained, the server exited successfully and private credentials/data were removed. Prototype sources and raw evidence remain ignored development artifacts; no experimental protocol dependency or native compiler requirement was added to ARM.
