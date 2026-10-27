# Paired-preparation v1 dependency matrix

The original inert proposal was reviewed against `180c86e`; its compile-only
evidence does not establish runtime support. The current consumer acceptance is
independent. V0 now has a separate ordinary-format baseline guard candidate;
all later rows remain proposed work. The inert central patch remains unapplied.

| ID | Intersection / exact current source | Required rule before any factory | Proposed status |
| --- | --- | --- | --- |
| V0 | `internal/database/workspace_format.go`; `reset_install.go: StartupAuthority`; `startup_read.go`; restart CAS and settled proof | Two-stage [baseline rollout](PROVIDER-V0-BASELINE.md): distribute the format-1-only guard before explicit format-2 activation, exclude pre-baseline writers, never borrow reset/sync markers. | Baseline guard patch only; released-baseline/deployment evidence and activation protocol remain producer gates. |
| V1 | `internal/providerpreparation/{types,schema,queries}.go` | Fixed inert profile, bounded exact rows and markers; no operational API. | Data/string declarations only. |
| V2 | `internal/model/model.go` | Optional exact LeaseRef and accounting display type; plain data is never capability. | Un-applied patch. |
| V3 | `internal/database/proof_schema.go`, `proof_provider_preparations.go`, `proof.go` | Literal four-object group, two exact autoindexes, marker 1; absence supported, partial/version mismatch refused. Catalog +6 only. Any row or held-field presence blocks default reset. | Un-applied read-only catalog patch; no installer. |
| V4 | `internal/database/proof_json.go`, `proof_ssh_shapes.go`; `internal/sshservice/receipt.go` | Raw presence at exact Lease node rejects null/false/unknown presentations before decoding; typed attachments and independent recursive shape inventory remain closed. | Narrow un-applied patch; broader history/raw-path audit still required. |
| A0 | `internal/manager/manager.go: acquireWithAdmission`; `budget_admission.go`, `budget_accounting.go`, `budget_model.go` | One transaction for both leases, budget sidecars, pair index/header and future bytes before any native work. One bounded pair event; no generic Acquire/put/event helper bypass. | Not implemented; disabled producer. |
| A1 | `internal/manager/manager.go: get/list/put`; proposed Manager raw reader | Protected tuple/type/length/context validation, not remarshal of usage-attached values. Initial/rows immutable, marker CAS with one successor ACK. | Queries/type contract only. |
| A2 | `internal/manager/scheduling.go`, `manager.go: Status/Active/Acquire/tick`, `disk.go`, `budget_accounting.go`, `subpool_accounting.go` | Valid members count once through the paired projection. Missing/terminal member never frees a promise. Corruption/overflow/bounds produce unavailable accounting and fence new grants/renewals/activation. | Not implemented; blocks factory. |
| L0 | `internal/manager/manager.go: end/reconcile/renew` | Pair guard precedes terminal/ending shortcuts, generic non-job release, PID logic and ordinary put. Mark/revoke pair without member writes; signal only original live owner outside bookkeeping locks even on write error. | Not implemented. |
| L1 | `internal/manager/storage.go: endStorage/storageCleanup/storageHandoff`; `storage_export.go` | Pair guard precedes native cleanup/handoff/export or retained-storage rewrites. Neither member may independently disappear. | Not implemented. |
| L2 | `internal/manager/bulk.go`; `shutdown.go`; `manager.go: Close` | Deduplicate by pair, cancel original contexts, retain both; Close handles pairs even though they are not m.jobs. Never wait on native joins or callbacks under Manager.mu. | Not implemented. |
| L3 | `internal/manager/lease_clock.go`; `manager.go: Open/tick` | Intercept before generic clock retention/end/recovery puts. Read one accepted sample after bounded raw queries; boot/sticky/error ends eligibility, not charges. Reopen mints no owner. | Not implemented. |
| L4 | `internal/manager/operator.go`, `ports.go`, `gradle_lifecycle.go`, `storage_export.go` | Shared dependent-release, attachment, deletion and handoff paths must not bypass pair membership or create unsupported dependencies. | Audit/guards required. |
| R0 | `internal/database/proof.go`, `proof_reset.go`, `proof_namespaces.go`; `reset_apply.go`, `reset_install.go` | Default reset proves only exact empty namespace/no refs. Stopped exclusive --forget remains explicit abandonment; no settlement is inferred. | Narrow proposed catalog patch; composed proof review required. |
| R1 | `internal/sshservice/state_records.go`, `history_decode.go`, `history_validate.go`, `history_catalog.go`, `history_archive.go`, `history_worker.go`; `internal/historypack` | Receipt/history/pack compaction cannot strip or reinterpret pair obligations as bare terminal resources. Preserve unsupported/unsettled evidence; no parser-generated proof from typed defaults. | Not implemented; blocks factory. |
| O0 | `internal/manager/peer_snapshot.go`, `cpu_warning.go`, `cpu_credit.go`; native resource views | Missing/terminal member stays represented or projection is unavailable. No warning/snapshot data creates admission or signal credit. Do not hide an unknown pair behind ordinary state-only filters. | Not implemented. |
| O1 | `internal/cli/output.go`; `internal/server/{server,cpu,disk,mesh}.go`; `internal/tui/{capacity_budget,cpu_budget,presentation}.go`; `internal/server/web/semblance/{backend,remote,main,pools}.mjs`, `app.ttl` | Carry accounting-unavailable state; do not advertise capacity minus a partial retained floor as free. Preserve the distinction between physical observations and reservable budgets. Complete the renderer/subscription audit when routing implementation. | UI/backend owners review before producer; no new UI in this diff. |
| F0 | Proposed `manager.ProviderPrepareIntent` | Private original Manager/epoch/pointer/expected row context; no ctor or restoration from bytes. | Type declaration in un-applied patch only. |
| F1 | Later preparing factory and finalizer | Exact predecessor/ACK semantics, no native verification until charged; actual opaque producer/owner registration and compiling sealed settlement bridge before dispatch or slot release. | Deliberately absent. |

The exact raw/typed proof, accounting, lifecycle and scope boundaries must be
reviewed together before A0/F1 is implemented. V3/V4 alone do not grant retention
or settlement authority. Proposed checks are not evidence of passing behavior.
After A0/F1 source approval, route scoped actual transaction/row substitution/cancelled
read/End/storage/Close/expiry/reopen/default-reset/history cases against the composed
source, including required Linux boundaries. Do not run a new queue or import an
older divergent paired-retention branch as an acceptance shortcut.
