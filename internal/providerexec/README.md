# Owned local diagnostic-worker primitive — checkpoint

Internal native runtime only. Read `formal/provider-worker/README.md` for the exact
implemented boundary, actual admission/SQLite fixtures, scoped model/trace/proof
validation and remaining P1b/P1c/P2/P3 work. No public provider registration/provisioning command
is available. Trusted selection constructors validate/copy inputs; they do not
establish authority. Raw decoded provider claims never become ordinary held-worker
admission, a new executable selection, or a cleanup receipt.

A diagnostic result is confirmed only by the conjunction of actual matched valid
response, zero provider exit, exact stdout frame+EOF and positive scoped native
closure. ProtocolSuccess alone describes the response; it does not settle worker
ownership, compensation or an external operation. Full attempt settlement additionally
requires positive managed-temp cleanup and ordinary worker release. Unknown keeps
its handle, durable concurrency slot and future finalization bytes.

Executable namespace policy: every canonical component is opened relative to its
native parent with O_NOFOLLOW, with at most64components/255bytes per component and
4096bytes per full path. Root/current-UID directories must be non-group/world
writable. The sole exception is a root-owned sticky directory with an exact
root/current-owned non-group/world-writable next child. Complete native directory
birth identities/modes and fd-native ACL evidence are saved and rechecked before/after bounded image reads
and at existing selection boundaries. Literal symlink aliases (including macOS
/var or /tmp aliases) are refused; reviewed callers must select the canonical
native path explicitly. Missing native birth identity is a refusal.

This protects selected names from other UIDs under trusted root/OS assumptions.
Reopen/check followed by exec is not atomic executable-handle launch. The helper
cannot authenticate its already mapped text against hostile replacement. Dynamic
libraries and hostile same-UID/root mutation remain outside this primitive's
isolation claim.

P1b live-hold gate: Prepare checks a detached admission snapshot and its deadline;
that old snapshot cannot retain or revalidate actual leases at BeginStart/grant.
The future typed manager adapter MUST verify and durably retain current ordinary
worker/temp obligations at those boundaries, including unknown outcomes/expiry.
No public registration/diagnostic dispatch may expose this primitive before that
adapter and the explicit cleanup dependency are reviewed and implemented.

ACL selection breadth is deliberately fail-closed. Darwin APFS/HFS use bounded
libSystem descriptor ACL APIs: only empty/absent or deny-only ACLs are supported;
all allow ACEs, unknown framing/flags, unavailable/errors and over128entries refuse.
Exact native absence requires the known supported descriptor/filesystem plus
nil+ENOENT captured after clearing errno on one locked OS thread. Nil+zero/stale
or other errors are never absence. Leaf and each ancestor/next-child ACL evidence
are bound and rechecked. Actual APFS controls/regressions have been run; HFS is
source/structural scope only until a real HFS fixture is available.

The initial Linux scope is ext-family/XFS/Btrfs/tmpfs/overlay with positively absent
bounded extended access/default POSIX ACLs. Nonempty ACLs, retrieval errors and
other filesystem authorization models refuse. NFSv4/rich ACLs and broader safe ACL
support remain unfinished; POSIX modes alone are not a universal ACL policy.
