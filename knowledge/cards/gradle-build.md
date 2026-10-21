---
schema_version: 1
id: gradle-build
title: "Gradle builds and owned persistent services"
tags: [gradle, java, jvm, build, daemon, cleanup]
resources: [cpu, memory, storage]
adapter: gradle-build
---
# Gradle builds and owned persistent services

Gradle work can continue inside a persistent JVM after a command-line client
exits. Use an ARM-owned service instead of adopting a user's existing daemon.
The initial adapter requires an installed `gradle` executable and JDK, an
explicit project root, local supervision and separate managed storage leases.
It does not download tools or use a project wrapper. Read the returned lease's
`workload` evidence; an active service lease alone does not establish readiness.

## Start a service with a justified lifetime

```sh
arm storage create 4GiB --ttl 30m --reason "Private Gradle service state and caches for this task"
arm run --profile gradle-service --detach --storage SERVICE_STORAGE_ID --cpu 2 --memory 1GiB --ttl 20m --reason "Owned Gradle daemon and workers for the requested builds" -- gradle
arm inspect SERVICE_LEASE_ID --json
```

Replace returned IDs with their actual values. `--detach` returns the initial
service lease; it is accepted only for this profile. Inspect until
`workload.state` is `ready`. ARM first launches the foreground daemon through
its ordinary gated process supervision, then runs a bounded ARM-authored probe
in the same verified process group. The probe uses a private generated project;
it does not configure the user's project. Ready evidence binds native process
identity, JVM start time, JDK, Gradle home/version, worker count and memory flags.
Failure is visible and no application consumer is authorized while unverified.

The service promise remains charged while the daemon is idle between clients.
It covers the daemon, its workers and the bounded startup probe. Whole service
CPUs, rounded down, set Gradle's maximum workers; parallel project execution is
disabled. ARM first allows 256MiB of the service claim for its startup probe,
then sets daemon JVM heap to half and metaspace to one quarter of the remainder,
in whole MiB, with a 64MiB initial heap. These are JVM settings,
not a total-memory or CPU guarantee. Native allocations, threads and plugin
processes consume additional memory. Request enough for the observed workload.

The home is an exclusive private directory inside the service storage lease.
Never run another client against it, add init scripts to it, or share its
registry/cache with another service or user daemon. ARM does not run `gradle
--stop` against that home or a user's home. It signals only its currently owned
supervised groups; receipt or log PIDs grant no authority to kill a process.

## Give each build a separate client and output lease

```sh
arm storage create 4GiB --ttl 15m --reason "Temporary Gradle build outputs until the requested JAR is retained"
arm run --profile gradle-build --service SERVICE_LEASE_ID --storage OUTPUT_STORAGE_ID --cwd /absolute/project --cpu 0.25 --memory 256MiB --ttl 10m --reason "Build the requested executable JAR using the owned daemon" -- gradle --offline jar
```

The service must outlast its consumers and its own storage lease must cover
that lifetime. Only one consumer may use a service at a time; busy or unresolved
work refuses a second consumer rather than starting another authorized JVM.
The client promise covers its separate launcher JVM. Project tasks and workers
run under the separately charged service promise. Between clients, only that
service execution promise remains. Service release/expiry ends attached
consumers before reclaiming its owned process scope and storage.

ARM pins the client to the service's installed executable, JDK, JVM flags,
version and home. It controls init scripts, project cache, output directories,
workers, configuration cache, watch mode and parallelism. Configuration caching is
explicitly disabled because the current receipt gate uses Gradle’s
[`buildFinished` callback](https://docs.gradle.org/current/javadoc/org/gradle/api/invocation/Gradle.html#buildFinished(groovy.lang.Closure)),
which Gradle does not support with configuration caching. Only explicit task
names and the supported diagnostic/offline flags are accepted; conflicting
daemon, JDK, JVM, path, init and worker options are refused. Each build needs
exactly one regular `settings.gradle` or `settings.gradle.kts` in its explicit
root. Daemon JVM criteria files, `buildSrc`, wrappers, composite/included builds,
continuous execution and remote service/consumer attachments are unsupported.
The generated init gate refuses nested settings/included builds. Ordinary
multi-project output paths are assigned under the consumer's managed `build`
directory and locked before project configuration.

## Completion and uncertainty

Before project settings/tasks run, an init gate checks that Gradle selected the
exact owned JVM instance. This matters because Gradle's public client can spawn
a fallback daemon when the expected daemon is busy or incompatible. ARM cannot
prevent every fallback spawn through the public CLI. Unexpected JVM evidence
fails before authorizing project work, retains client/service/storage promises
and blocks service reuse. It never treats that PID as an owned process.

A client exit alone does not prove its task stopped inside the service. ARM
requires matching private start/completion receipts and the client's own group
to be absent. Missing, changed, malformed or replaced evidence stays charged
as `needs_attention`, and the service becomes blocked. A later valid completion
from the original operation can settle the client and restore a still-owned
service; cancellation does not silently return capacity while its task runs.
Service promises survive backend crashes conservatively. After restart ARM
does not regain signal authority from saved PIDs. Inspect and reconcile only
after the owned service and dependent work are known idle; unresolved state
must remain visible. Do not invent reclaimed bytes or stop unrelated daemons
to remove a warning.

## Retain the requested output, then release intermediates

Use the completed client's `workload.build_directory` and the storage lease's
`managed_dir` to locate the actual output. Paths are per consumer; do not assume
the project-local `build/` directory contains the artifact. Export only the
requested JAR and explicitly chosen runtime files to a new approved bundle:

```sh
arm storage export OUTPUT_STORAGE_ID /approved/output/app --file RELATIVE_MANAGED_PATH/app.jar=app.jar --reason "Retain the requested runnable JAR" --key EXPORT_KEY --wait
java -jar /approved/output/app/app.jar
arm storage cleanup OUTPUT_STORAGE_ID --dry-run
arm release OUTPUT_STORAGE_ID
arm release SERVICE_LEASE_ID
arm storage cleanup SERVICE_STORAGE_ID --dry-run
arm release SERVICE_STORAGE_ID
```

Export requires idle owned files and no attached consumers. Verify the retained
result before cleanup. Add actual runtime dependencies explicitly; ARM does not
discover them automatically. If another build has a concrete need for retained
artifacts, shorten their TTL with `arm storage retain ... --ttl ... --reason
...` rather than leaving them for the whole agent session. Retention cannot
extend entitlement or cut across an attached consumer's deadline. Cleanup and
APFS/shared-block accounting have the same conservative guarantees described
in the Rust card; namespace deletion is not exclusive physical reclamation.

## Limits and further reading

This is cooperative build regulation, not a sandbox for hostile Gradle scripts.
Project settings/plugins/tasks remain executable user code and can create
custom outputs, child services or detached processes. ARM redirects standard
Gradle project outputs and observes its owned groups; arbitrary plugin output,
toolchain daemons, escaped process trees and dependency download activity need
additional provider knowledge. Such scopes must not be claimed as verified
reclaimed solely because a client exited. Initial real-tool acceptance covers
installed Gradle 9.8.1 with JDKs on macOS and Linux; other versions require their
own compatibility evidence. There is no adoption of unrelated daemon homes,
cross-version registry reuse or automatic fallback-daemon cleanup.

Gradle's official [daemon guide](https://docs.gradle.org/current/userguide/gradle_daemon.html)
describes compatibility and persistent JVM behavior. Its
[command-line reference](https://docs.gradle.org/current/userguide/command_line_interface.html)
documents foreground/daemon, worker and home options. Detection, ownership,
gating and accounting above are implemented by ARM; this card cannot add
enforcement or authorize cleanup.
