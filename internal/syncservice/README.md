# Sync operation handoff

`syncservice` is the exact-key control and receipt layer for `arm sync-worker`.
It binds `syncreconcile` output to ARM's existing leased SSH admission and to
`syncfs` native publication. It does not create another transport or grant file
authority. This package is an integration slice; the CLI, Manager, database
proof, and SSH worker are not wired to it yet.

## Direct leased SSH sequence

1. Both endpoints hold reviewed roots and scan the same selection with
   `syncfs.Scan`. The initiating endpoint calls `syncreconcile.Reconcile` and
   reviews its digest, exact actions, conflicts, identities, policies, resource
   promises, and generation. `Binding` must identify the authorized account
   and use `syncfs.Root.NativeID()` for each held root; a path string alone is
   insufficient. Call `syncfs.VerifyPlanRoot` before source materialization
   and publication, and retain the held handles through the operation.
   Unresolved conflicts prevent an intent.
2. `NewIntent` recalculates that exact plan, records the complete selected and
   candidate-name manifest, source and destination CPU/memory/disk promises,
   original sync/SSH key, fixed worker-command SHA-256, and the proposed next
   baseline. Manifest and record bytes are each capped at 64 MiB, and manifest
   construction is limited by the smaller endpoint memory promise. The source
   Manager charges planning and worker resources, then calls `ReserveTx` with
   its own held-root scan and commits before creating or dispatching an SSH
   invocation. It also uses the existing SSH source
   intention fence under that same key.
3. The destination Manager authenticates the original SSH identity/account,
   grant, fixed worker command, and root. Its held-root `syncfs.Scan` must
   independently reproduce the destination manifest and native name keys;
   source-supplied rows are comparison evidence, not destination observations.
   It calls `ReserveTx` with that independent observation **inside the
   transaction that admits the leased SSH process**, charging its destination
   resources there. `ReserveTx` rebuilds the plan from the manifest and the
   destination's currently confirmed baseline. A changed plan or generation
   is refused before any file effect. `WritePreparation`/`ReadPreparation` use a
   versioned length frame, explicit wire byte cap, and counted manifest decode.
   A token preflight charges nested collection, decoded-string, and canonical
   JSON escaping overhead before typed decoding, including the proposed
   baseline maps and action arrays.
   `ReadPreparation` requires the Manager's active memory admission before it
   allocates the frame body; the request's resource promise is not that hold.
4. The workers materialize only reviewed source actions into private staging,
   using `syncfs.BuildSourceManifest`, `SeedReceiverBasis`, and
   `FinalizeStageDirectories` around the bounded rsync exchange. These verify
   source and staged SHA-256 plus selected metadata. `syncfs` requires
   the receiver's workspace disk reservation and verifies all staged effects
   before publication. The receiver uses `Ledger` as `syncfs.Journal` in
   `NewPublisher`/`ApplyAll`. A prepared effect cannot be replayed; a lost
   outcome remains inspectable under the original key.
5. After `ApplyAll`, held-root recheck and worker settlement, each endpoint
   calls `CompleteOwnSide`. The authenticated SSH control stream exchanges
   `SideReceipt` values; `RecordPeerReceipt` persists the peer's exact value.
   Each endpoint calls `ConfirmBaseline` only after both receipts exist. A lost
   confirmation reply is resolved with `Ledger.Lookup`, never another apply.

`Ledger.Lookup` is read-only. It validates each prepared receipt and outcome
against the original intent, and checks a completed side's effect roster and
digest before exposing the original intent, manifest, effect receipts, and
endpoint receipts. It neither retries transport nor cleans stage
or quarantine. Retained preimages and stage remain under the original operation
until a separately reviewed cleanup.
If the exact SSH destination returns a durable nonexecution fence, the source
may call `RecordNonexecution` with that fence. The sealed original remains
lookup-only and permits a separately reviewed first-run key; an unknown or
admitted original never permits a new-key escape.

## Required integration changes outside this package

* **Manager:** add a typed sync admission around existing
  `AcquireSSHCommitted`. The destination sync reservation and SSH admission
  need one SQLite transaction, so merely calling the two existing entrypoints
  in sequence is insufficient. The source needs a durable pre-dispatch sync
  reservation and the existing SSH source intent. Bind each admitted resource
  promise to the actual CPU, memory, disk, TTL, grant, and worker lifecycle.
  `syncservice` checks positive promises and a control/stage/preimage lower
  bound; those checks do not establish that Manager charged the resources.
  Do not dispatch or publish until authoritative admission proves the charge.
* **Database:** install and prove `OperationsSchema`, `OpenGenerationSchema`,
  `EffectsSchema`, and `BaselinesSchema` with a version marker and
  startup/reset/Stop recovery.
  Configure SQLite synchronous=FULL or stronger with WAL or a rollback journal
  on every writing connection; mutating methods verify both settings on their
  transaction. Reject missing
  prior namespaces or partial records as uncertain evidence.
* **SSH worker/control:** use one fixed, reviewed `arm sync-worker` entry and
  compare `Intent.WorkerCommandSHA256` and `Intent.SSHControlSHA256` with the
  existing exact SSH control commitment.
  Send bounded versioned control messages over the authenticated leased stream
  for prepare, stage, apply, exact-key lookup, receipt exchange, and confirm.
  Do not use a shell-selected rsync binary or restart a timed-out worker.
* **Native transfer:** `rsyncengine.Run` currently sends a supplied entire
  root. Give it only the private tree built by `syncfs.BuildSourceManifest`,
  including reviewed `SourcePath` to `Path` remapping. Recheck source hashes,
  stage hashes, root identity, and selected paths. `SeedReceiverBasis` copies
  or clones the reviewed basis under a charged receiver reservation. Retained
  preimages and transfer temporary files also count against that admission.
  The integrated native `syncfs` overlay provides `Root.NativeID`,
  `VerifyPlanRoot`, `BuildSourceManifest`, `SeedReceiverBasis`, and
  `FinalizeStageDirectories`. The local publication test binds both native
  roots, refuses a different receiver and uses `NewBoundPublisher` with the
  durable ledger. It does not exercise authenticated SSH transfer or actual
  Manager charging. Stage sizes are logical lower bounds: admission must
  account for allocation, inode and temporary/preimage overhead on the actual
  source and receiver filesystems; the state-directory disk pool alone cannot
  authorize a different volume.

Manager installs and proves this namespace during startup. Used sync history
currently blocks reset and Stop until exact native cleanup/recovery proof is
integrated; schema installation alone does not authorize public sync dispatch.
