# Private unbound native lifetime

This package has no exported constructor, type, validation or retirement method
and no production callsites. It is not a resident bootstrap, Manager/Server
attachment or worker launcher. No borrowed lifetime or future joined witness is
implemented. `arm start` integration and real API/UI A-to-B job preservation remain
required later gates.

`acquire` itself opens an explicit existing canonical absolute directory and its
existing `daemon.lock`, through real no-follow native directory components. It
requires effective-account ownership, exactly private modes 0700/0600, real
directory/regular lock types and a singly linked lock. Shared ancestors need not
be private, but every component must be a real directory. Source/held identity
checks compare device, inode, owner, group and complete mode. Directory link count
must remain positive; legitimate subdirectory changes do not invalidate it.
Lock link count must remain one. No file contents are read or written; lock-file
size/content is not identity or an ownership proof.

The path is bounded to 4096 bytes and 64 components. At most four native FDs are
owned at once: two retained originals and up to two auxiliary validation opens.
Closed auxiliary records are reduced to checked counters rather than accumulated
history. These are count/path bounds, not a CPU/RSS grant or wall-time guarantee
for kernel/filesystem calls. The package starts no goroutine or process.

Exclusive nonblocking flock occurs only during original construction. Validation
checks held originals and independently reopened path identities without taking
or reacquiring any lock. The private original-pointer registry rejects copied,
foreign, zero and retired receivers. A failed original validation is sticky,
including after pathname/mode restoration. Same-account code remains trusted;
this is not protection from an actor who can interfere with the resident's actual
process memory/native descriptors or arbitrarily mutate its filesystem.

Retirement is private and has no borrowers in this cut. It withdraws the original
before closing any FDs; the original lock close releases the native lock without
a separate unlock/reacquisition path. Every successful original Close is counted.
A close error is uncertain, never retried against a potentially reused number;
its exact original file/slot is retained. Failed acquisition returns a structured
original cleanup report; it returns a nonnil handle with the error if cleanup is
uncertain, which its caller must preserve. Retirement reports only native FD/lock
closure. It cannot certify jobs, protocol tasks, Manager/Server or resource release.

Tests use only owned synthetic temporary files/directories and actual native
opens/flocks/closes. They exercise original acquisition/independent-open
contention, partial open/lock refusals and cleanup, changed/replaced/missing names,
type/mode/link/symlink refusals, copied/foreign/retired handles and concurrent
validate/retire. Privilege-dependent wrong-account fixtures and platform native
Close failure are not exercised; their refusal/retention paths are source-reviewed,
not claimed as native fault evidence. No fabricated positive callback, readiness
flag, FD wrapper or test-only ownership bypass exists.

Future native bootstrap must review actual Manager/Server lifetime binding,
mesh-independent local stop revision, borrower registration/closure dependencies,
format-4 approval and admission. There is deliberately no API to attach them here.
