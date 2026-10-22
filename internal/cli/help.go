package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type commandHelp struct {
	usage, summary, example string
	min, max                int
}

var commandHelps = map[string]commandHelp{
	"ssh":         {usage: "NODE --reason TEXT [OPTIONS] [-- COMMAND_STRING]", summary: "Execute one command string, or a shell, through pinned destination-authorized embedded SSH using the system OpenSSH client. Requires destination arm ssh setup and directional ssh-exec plus lease-own grants. Every channel gets a fresh destination lease; same-account execution is not a sandbox. No forwarding, SFTP or file/share access. Raw stdout/stderr and exit status are preserved; --json execution is rejected. Command text and private key are never persisted. Unknown outcomes require explicit lookup, never automatic execution retry.", min: 1, max: 1},
	"ssh setup":   {summary: "Review and explicitly enable a destination-account SSH service and private host key. Requires initialized mesh and running backend. Default keeps unchanged; --dry-run is read-only, scripts require --yes."},
	"ssh disable": {summary: "Review and disable the exact destination SSH service revision, then close its current streams. Keys and audit evidence are retained; unresolved leases keep normal cleanup obligations."},
	"ssh pending": {summary: "Read exact retained source SSH intentions offline without starting a backend, lookup, cleanup or replay. Pending evidence remains private."},
	"ssh lookup":  {usage: "NODE --key EXACT_KEY [--cleanup --yes] [--dry-run]", summary: "Explicitly inspect the original destination enrollment/key without dispatching execution. A missing result remains unknown. Only an exact terminal/nonexecution receipt and absent verified local artifacts can settle source intent. --cleanup separately reviews removal of a complete unchanged private artifact roster after the original worker is proved absent; partial/changed evidence requires manual preservation. --dry-run performs no writes.", min: 1, max: 1},
	"ssh seal":    {usage: "NODE --key EXACT_KEY --reason TEXT [--dry-run] [--yes]", summary: "Review destination-side nonexecution sealing of an exact pending original key. Sealing and admission serialize in one destination transaction. Admitted work remains admitted; a lost seal acknowledgement is lookup-only. No execution retry or automatic cleanup.", min: 1, max: 1},

	"mesh connector list":   {summary: "Read reviewed local connector IDs without starting a backend or creating state."},
	"mesh connector add":    {usage: "ID SSH_DEST --reason TEXT [OPTIONS]", summary: "Save an immutable native-only connector to an exact installed helper and optional private remote home. The dashboard can select this ID; it cannot edit executable/home/SSH options. No peer trust or installation occurs. Confirmation required.", min: 2, max: 2},
	"mesh artifacts":        {summary: "Read/import trusted local platform companions used for separately reviewed missing-node installation; no release downloads or remote writes.", min: 0, max: 0},
	"mesh artifacts list":   {summary: "List private local artifact descriptors without creating state or starting a backend.", min: 0, max: 0},
	"mesh artifacts import": {usage: "FILE --reason PURPOSE [--dry-run]", summary: "Verify a CGo-free ARM Go executable and copy it into a private immutable SHA256 catalog. The original project binary is unchanged. --dry-run hashes/inspects only; foreign binaries are never executed locally.", min: 1, max: 1},
	"mesh operations":       {usage: "[KEY]", summary: "Read durable operator workflow receipts and bounded recovery attempts offline; no contact, resend or backend startup.", min: 0, max: 1},

	"node":                {summary: "Read the same reported OS, architecture, ARM build, hostname and bounded inventory/capability schema for localhost or a direct peer (--host). Inventory timestamps and stale flags describe the original cached observations; reported metadata is not authentication or a resource guarantee."},
	"database stats":      {summary: "Read exact database/WAL/shared-memory sizes, SQLite pages/reusable space and table/lease counts. Uses a separate read-only online connection, or a coherent stopped copy; no compaction, repair or permission changes. APFS allocated blocks are not exclusive reclaimable space."},
	"database check":      {summary: "Read-only bounded SQLite quick_check by default (10 seconds); --full explicitly runs integrity_check (two minutes). Up to100 findings; incomplete/corrupt checks do not report success. Structural checks do not certify provider obligations or process liveness."},
	"database backup":     {usage: "DESTINATION [OPTIONS]", summary: "Create a consistent private standalone SQLite backup at an unused explicit path. Parent must be private and contain no symlinks. Works online through the separate reader or from a stopped coherent copy. Source remains unchanged; no overwrite, compaction or repair. Mesh keys/configuration, managed artifacts and pending client intents are not included.", min: 1, max: 1},
	"database remove":     {summary: "Review and permanently erase only a stopped, fully settled local SQLite ledger. Default keeps unchanged; scripts require --yes and --reason. --dry-run is read-only. Configured mesh/history needs explicit --retire-mesh local archival and re-pairing; remote claims retain destination authority/expiry. Artifacts/operator configuration and private receipts remain. Interrupted work fences startup; --continue OPERATION reviews and explicitly finishes the same immutable intent. No force bypass, remote revocation or automatic restart."},
	"budget list":         {summary: "Page local authenticated-owner policy definitions. These ceilings do not reserve host resources. --parent self shows direct children of your UID root."},
	"budget inspect":      {usage: "BUDGET_ID|self [OPTIONS]", summary: "Inspect immutable ancestry, current revision, overlapping ancestor charges, deficits and effective policy headroom. Use consumers for paged lease reasons. No reservation reuse is implied.", min: 1, max: 1},
	"budget consumers":    {usage: "BUDGET_ID|self [OPTIONS]", summary: "Page charged and queued descendant leases with reasons. Queued amounts describe requested quantities and charge zero. Unknown/ending provider obligations remain charged.", min: 1, max: 1},
	"budget history":      {usage: "BUDGET_ID|self [OPTIONS]", summary: "Page the durable policy change journal, exact actor UID, attribution, reason and before/after revisions.", min: 1, max: 1},
	"budget lookup":       {summary: "Inspect an exact durable policy mutation by --key without changing or retrying it. Not-found is a snapshot and cannot disprove an in-flight commit."},
	"budget create":       {usage: "NAME --reason TEXT [OPTIONS]", summary: "Create a policy envelope under --parent self or a child ID. No resources are reserved; leases use --budget ID and still require ordinary host capacity. --limits is complete typed JSON with optional cpu, memory_bytes, disk_bytes and pool-ID/count ceilings. Missing/null means no extra ceiling, zero forbids a positive claim.", example: `arm budget create builds --limits '{"cpu":2,"memory_bytes":1073741824}' --reason "Coordinate local builds"`, min: 1, max: 1},
	"budget set":          {usage: "BUDGET_ID|self --limits JSON --revision N --reason TEXT [OPTIONS]", summary: "Replace the COMPLETE limits object under a revision guard. Omitted/null fields clear that extra ceiling; explicit zero is a zero ceiling (including memory). '{}' explicitly clears all ceilings. Existing grants keep their deadlines; reductions may create a deficit that blocks relevant admission/renewal.", example: `arm budget set self --limits '{"cpu":4}' --revision 1 --reason "Limit local CPU commitments"`, min: 1, max: 1},
	"budget rename":       {usage: "BUDGET_ID NAME --revision N --reason TEXT [OPTIONS]", summary: "Rename a policy without changing its immutable ID/parent or lease bindings.", min: 2, max: 2},
	"budget describe":     {usage: "BUDGET_ID TEXT --revision N --reason TEXT [OPTIONS]", summary: "Change a policy description with a durable attributed journal entry.", min: 2, max: 2},
	"budget drain":        {usage: "BUDGET_ID|self --revision N --reason TEXT [OPTIONS]", summary: "Refuse new descendant bindings and renewals. Already queued requests retain their place and continue under normal policy/host checks.", min: 1, max: 1},
	"budget resume":       {usage: "BUDGET_ID|self --revision N --reason TEXT [OPTIONS]", summary: "Reopen a draining policy to new work.", min: 1, max: 1},
	"budget delete":       {usage: "BUDGET_ID --revision N --reason TEXT [OPTIONS]", summary: "Retain a tombstone only after children and queued/charged descendants are gone. The authenticated owner root cannot be deleted.", min: 1, max: 1},
	"mesh lookup":         {usage: "KEY", summary: "Inspect a durable local connect/disconnect intent, exact SSH identity, pins, phase history and unresolved outcome offline. Does not retry or contact peers.", min: 1, max: 1},
	"mesh connect":        {usage: "SSH_DEST [OPTIONS]", summary: "Start/reuse both ARM backends and prepare private identities and exact direct-route listeners, then review pinned trust and directional grants. Preparation may restore previously approved enabled listeners when restarting ordinary backends; NEW listeners/endpoints/trust require confirmation. Decline preserves existing trust/listeners. --dry-run writes nothing; scripts require --yes. Verified SSH known_hosts required. Missing nodes can use a trusted local target-platform companion (--artifact FILE or mesh artifacts import): separately review immutable private installation/start, then concrete TLS pins/grants. No global overwrite, root/services or invented release downloads; partial outcomes require exact lookup. Compatible existing nodes retain their settings; transitive trust is unsupported.", min: 1, max: 1},
	"mesh disconnect":     {usage: "HOST [OPTIONS]", summary: "Revoke the exact automatic connection locally first, then matching target trust over verified SSH. Offline target remains remote-pending with an exact retry key. Existing leases and other peers are preserved.", min: 1, max: 1},
	"mesh renew":          {summary: "Explicitly renew/recover the local node certificate using its existing key; revokes peer grants and requires reviewed SSH re-pair. --reason and confirmation required; no automatic key/pin replacement."},
	"mesh message-lookup": {usage: "HOST --key ORIGINAL_KEY", summary: "Inspect this peer’s recorded send without sending or refreshing contact identity.", min: 1, max: 1},
	"mesh identity":       {summary: "Show this initialized node's public certificate, pins, stable ID and advertised address."},
	"mesh peers":          {summary: "Inspect directional peer grants, pending/active/revoked state and certificate expiry."},
	"mesh init":           {summary: "Create a private node identity and explicitly enable a separate pinned TLS1.3 listener. Requires --name, --listen literalIP:PORT and --address reachableHOST:PORT; --yes for scripts."},
	"mesh join":           {summary: "Pair with an initialized node over strictly verified SSH. Directional --grant defaults observe-host; reverse defaults none. lease-own needs ceilings; run grants target OS-user code execution. Review --dry-run and --reason; scripts require --yes."},
	"mesh revoke":         {usage: "HOST --reason TEXT [OPTIONS]", summary: "Block a direct peer and close its sessions. Existing local leases retain expiry/cleanup obligations.", min: 1, max: 1},
	"mesh call":           {usage: "HOST OPERATION [--params JSON] [--key KEY]", summary: "Call the closed direct-peer operation allowlist. Mutations require a stable exact-retry key; no transitive dispatch or local administrative RPC.", min: 2, max: 2},
	"mesh contacts":       {usage: "HOST", summary: "Read only explicitly allowed target-local contacts with a contact-read grant.", min: 1, max: 1},
	"mesh send":           {usage: "HOST --body TEXT --key KEY [--to CONTACT --registration REVIEWED]", summary: "Send to the granted peer operator inbox or exact allowed contact. Target host ID is authoritative; ambiguous names fail. Requires explicit message/contact permissions.", min: 1, max: 1},
	"mesh bootstrap":      {summary: "Bounded stdin helper used only through verified operator SSH; not a mesh RPC."},

	"dashboard": {usage: "[BROWSER] [OPTIONS]", summary: "Open the local web console for monitoring and reviewed lease controls. BROWSER selects a macOS application (Safari or an .app path) or an executable on PATH; Linux accepts executables. Use terminal for the interactive terminal dashboard with the same six views, live feed and reviewed lease/message controls. It requires terminal input/output, rejects --json and --no-open, and leaves leases running when closed. A one-use link creates an eight-hour session. Requires a running backend. Piped/JSON output and --no-open never launch a browser, even with BROWSER.", example: "arm dashboard\n  arm dashboard Safari\n  arm dashboard firefox\n  arm dashboard terminal\n  arm dashboard --no-open\n  arm dashboard Safari --json", max: 1},
	"monitor": {summary: "Show physical host CPU, memory/swap, filesystem space, per-device disk I/O and network rates with bounded history. --follow streams through the shared backend. Unknown counters and history gaps remain explicit; these are separate from lease budgets.", example: `arm monitor
  arm monitor --follow --json`},
	"agents":         {summary: "Read the agent operating guide and list embedded workload knowledge cards. Works offline without creating state. Use arm agents card --list for metadata, arm agents card NAME to read a card and arm agent list to list contacts.", example: "arm agents --json\n  arm agents card --list\n  arm agents card rust-build"},
	"agents card":    {usage: "[NAME|list] [OPTIONS]", summary: "Read an embedded workload card explaining code-backed behavior and its limits, or use --list (alias: card list) for catalog metadata without bodies. Terminals show Markdown for a card; pipes and --json return metadata plus Markdown. --markdown emits a named card as plain Markdown in a pipe. Works offline without creating state.", example: "arm agents card --list\n  arm agents card list --json\n  arm agents card rust-build --json\n  arm agents card rust-build --markdown", max: 1},
	"start":          {summary: "Start the local backend in the background. Shortcut for arm daemon start; an existing backend keeps its budgets.", example: `arm start --cpu 4 --memory 8GiB --disk 20GiB`},
	"stop":           {summary: "Stop an idle backend, --wait to drain, or --force to release leases. --wait --timeout SECONDS releases after the deadline. Queued requests are cancelled during drain; unresolved resources keep the backend available."},
	"incidents":      {summary: "Show the latest 100 durable CPU/memory usage incidents, warning message IDs, evidence, deadlines and outcomes. Filter with --lease. Observed overage policy is opt-in for supervised run."},
	"usage":          {usage: "LEASE_ID [OPTIONS]", summary: "Observe CPU cores over an interval and platform-specific accounted memory for ARM-supervised live group members. Unknown or stale measurements remain explicit; observation is not a hard quota.", example: `arm usage LEASE_ID`, min: 1, max: 1},
	"pool create":    {usage: "NAME --capacity COUNT --reason TEXT [--scope host|global | --parent ID --parent-revision N] [OPTIONS]", summary: "Create a root pool or carve a child from an existing pool. Children inherit scope/host/unit and never create extra permits. Reductions retain unresolved backing. Hierarchy mutation controls are native CLI only. Mutations return before/after state and a durable journal sequence.", example: `arm pool create browser-slots --scope host --capacity 3 --reason "Coordinate browsers"`, min: 1, max: 1},
	"pool lookup":    {summary: "Inspect a durable pool mutation by --key without retrying it. Missing is an observation, not proof that a disconnected request cannot commit."},
	"pool roots":     {summary: "Page root capacity pools and disjoint partition accounting. Child capacities overlap their parents and must not be summed."},
	"pool children":  {usage: "POOL_ID [OPTIONS]", summary: "Page direct child partitions, retained backing and deficits.", min: 1, max: 1},
	"pool consumers": {usage: "POOL_ID [OPTIONS]", summary: "Page direct consumers or --descendants to include actual use through child partitions. Queued requests reserve no capacity.", min: 1, max: 1},
	"pool list":      {summary: "List counted resources, host/global scope, exact capacities, consumers and queue. --all includes deleted definitions."},
	"pool inspect":   {usage: "POOL_ID [OPTIONS]", summary: "Inspect a counted resource, revision, availability/deficit, consumers and queued reasons.", min: 1, max: 1},
	"pool history":   {usage: "POOL_ID [OPTIONS]", summary: "Browse the transactional change journal: actor, attribution, time, reason and before/after state.", min: 1, max: 1},
	"pool set":       {usage: "POOL_ID COUNT --reason TEXT [OPTIONS]", summary: "Set nonnegative capacity using a revision guard. Reductions below reservations record a deficit without changing existing leases.", min: 2, max: 2},
	"pool adjust": {usage: "POOL_ID DELTA --reason TEXT [OPTIONS]", summary: "Atomically increase or reduce capacity by a signed integer. A repeated --key never applies the adjustment twice.", example: `arm pool adjust POOL_ID --reason "Free one more device" 1
  arm pool adjust POOL_ID -1 --reason "Device unavailable"`, min: 2, max: 2},
	"pool rename":   {usage: "POOL_ID NAME --reason TEXT [OPTIONS]", summary: "Rename a resource while preserving its ID, claims and history.", min: 2, max: 2},
	"pool describe": {usage: "POOL_ID TEXT --reason TEXT [OPTIONS]", summary: "Change a pool description with an attributed journal entry.", min: 2, max: 2},
	"pool drain":    {usage: "POOL_ID --reason TEXT [OPTIONS]", summary: "Refuse new claims. Already queued requests continue under normal capacity and fairness rules.", min: 1, max: 1},
	"pool resume":   {usage: "POOL_ID --reason TEXT [OPTIONS]", summary: "Reopen a draining pool to new claims.", min: 1, max: 1},
	"pool delete":   {usage: "POOL_ID --reason TEXT [OPTIONS]", summary: "Preserve a historical tombstone. Queued or consuming claims prevent deletion.", min: 1, max: 1},
	"arm":           {usage: "[--home DIR] COMMAND [OPTIONS]", max: -1},
	"acquire":       {usage: "[OPTIONS]", summary: "Reserve CPU, memory, disk and/or ports without starting a command. --wait uses FIFO admission; inspect shows current blockers and an advisory or unknown estimate. --start-deadline is an absolute RFC3339 latest start time on the local host; TTL starts on grant, and a missed start is durably rejected.", example: `arm acquire --cpu 2 --memory 1GiB --reason "Analysis workers"`},
	"run": {usage: "[OPTIONS] -- COMMAND [ARG...]", summary: "Run a command under a supervised lease. ARM options precede COMMAND; everything after -- belongs to the command. Local --start-deadline RFC3339 prevents authorization after the latest start time; queued requests reserve no capacity and TTL begins on grant. For owned persistent Gradle, gradle-service --detach returns a starting lease: inspect workload.state until ready. gradle-build --service ID uses that exact ready service with a separate client/output lease; service CPU/memory stays charged between clients. Read arm agents card gradle-build for supported options and uncertainty/cleanup limits.", example: `arm run --cpu 2 --reason "Compile release binaries" -- cargo build --release
  arm run --profile gradle-service --detach --storage STORAGE_ID --cpu 2 --memory 1GiB --ttl 20m --reason "Owned Gradle service" -- gradle
  arm run --profile gradle-build --service SERVICE_ID --storage OUTPUT_ID --cwd /absolute/project --cpu 0.25 --memory 256MiB --reason "Build requested JAR" -- gradle --offline jar`, min: 1, max: -1},
	"storage create":            {usage: "SIZE [OPTIONS]", summary: "Create a managed disk-only lease. SIZE accepts units such as 10GiB. --disk SIZE is an alternative.", example: `arm storage create 10GiB --reason "Retain build outputs"`, max: 1},
	"storage list":              {summary: "List managed storage leases and their retained obligations."},
	"storage inspect":           {usage: "PATH [OPTIONS]", summary: "Measure logical and allocated storage without adopting or deleting it. Exclusive/reclaimable bytes may be unknown.", example: `arm storage inspect ./target`, min: 1, max: 1},
	"storage cleanup":           {usage: "LEASE_ID [OPTIONS]", summary: "Reclaim verified managed storage after its consumers stop. --dry-run only previews. Unverified reclamation stays charged.", example: `arm storage cleanup LEASE_ID --dry-run`, min: 1, max: 1},
	"storage handoff":           {usage: "LEASE_ID [OPTIONS]", summary: "Return files and unresolved storage responsibility to the operator. Preserves remaining data and ends ARM's cleanup authority.", example: `arm storage handoff LEASE_ID`, min: 1, max: 1},
	"storage export":            {usage: "LEASE_ID DEST_DIR --file SOURCE[=OUTPUT] --reason TEXT [OPTIONS]", summary: "Copy explicit owned, idle managed files into a new independent operator-owned bundle. Never overwrites or adopts shared/unmanaged sources. A durable operation records copy/publication evidence; source cleanup remains a separate release.", example: "arm storage export STORAGE_ID ./dist/app --file target/release/app --reason \"Preserve the requested executable\" --wait", min: 2, max: 2},
	"storage exports":           {summary: "List the latest 100 export operations; optionally filter by --lease. Use inspect EXPORT_ID for evidence, reconcile EXPORT_ID to verify an already-published bundle, or handoff EXPORT_ID --reason TEXT to assume remaining uncertain output responsibility without deleting it.", example: "arm storage exports\n  arm storage exports inspect EXPORT_ID\n  arm storage exports reconcile EXPORT_ID\n  arm storage exports handoff EXPORT_ID --reason \"I will inspect and dispose of partial output\""},
	"storage exports inspect":   {usage: "EXPORT_ID [OPTIONS]", summary: "Inspect the durable export operation, selected files, identities, content digests, state and output paths.", min: 1, max: 1},
	"storage exports reconcile": {usage: "EXPORT_ID [OPTIONS]", summary: "Verify an already-published bundle against recorded identity/content evidence. Does not resume partial copying, publish staging output or delete uncertain paths.", min: 1, max: 1},
	"storage exports handoff":   {usage: "EXPORT_ID --reason TEXT [OPTIONS]", summary: "Assume responsibility for retained partial/uncertain output. Preserves all paths and records the decision; source cleanup becomes eligible under ordinary lease rules.", min: 1, max: 1},
	"storage retain":            {usage: "LEASE_ID --ttl DURATION --reason TEXT [OPTIONS]", summary: "Shorten managed intermediate retention to a justified lifetime, at most 24h. Cannot extend the current expiry or cut across an attached consumer's deadline; ordinary renew handles extensions.", example: "arm storage retain STORAGE_ID --ttl 10m --reason \"One incremental rebuild remains\"", min: 1, max: 1},
	"ports reserve": {usage: "[PORT_SPEC...] [OPTIONS]", summary: "Hold port bindings. With no PORT_SPEC or --port, reserve one OS-selected TCP port on 127.0.0.1.", example: `arm ports reserve --reason "Preview server"
  arm ports reserve 8080 --reason "Preview server"
  arm ports reserve web=tcp6://[::1]:8000-8010 --reason "IPv6 preview"`, max: -1},
	"ports list":           {summary: "List ARM port reservations, consumers and reasons, including its protected backend listener."},
	"ports discover":       {summary: "Observe native TCP listeners and bound UDP sockets. Permission restrictions and unknown ownership remain explicit."},
	"inspect":              {usage: "LEASE_ID [OPTIONS]", summary: "Inspect one lease, its current outcome, and structured queue blockers/position when waiting. Start estimates are advisory or explicitly unknown, never future reservations.", min: 1, max: 1},
	"release":              {usage: "LEASE_ID | --all [OPTIONS]", summary: "End a lease early, or all outstanding leases at this local authority. Unresolved resources remain charged; bulk reports list each result. Use --key for exact bulk retries.", max: 1},
	"cancel":               {usage: "LEASE_ID | --all [OPTIONS]", summary: "Cancel one lease or all outstanding leases at this local authority. Bulk operations retain provider safeguards and report partial failures.", max: 1},
	"reconcile":            {usage: "LEASE_ID [OPTIONS]", summary: "Verify that an uncertain process group or port allocation is gone. Never signals a recovered PID.", min: 1, max: 1},
	"renew":                {usage: "LEASE_ID --ttl DURATION [OPTIONS]", summary: "Extend a live lease when queue priority and parent lifetimes permit.", example: `arm renew LEASE_ID --ttl 20m`, min: 1, max: 1},
	"status":               {summary: "Show host budgets, leases and capabilities."},
	"resources":            {summary: "Show live memory/state-filesystem disk headroom, full outstanding promises, optional ceilings, consumers and reasons. Unknown availability pauses only relevant new grants."},
	"messages":             {summary: "Show the operator inbox and message delivery status."},
	"events":               {summary: "Read durable events, or stream with --follow (JSON lines when redirected).", example: `arm events --follow`},
	"daemon start":         {summary: "Start the local backend in the background. An already running backend keeps its existing budgets.", example: `arm daemon start --cpu 4 --memory 8GiB --disk 20GiB`},
	"daemon stop":          {summary: "Stop an idle backend; --wait drains existing work, --force releases leases now, and --wait --timeout SECONDS releases after its deadline. Unresolved obligations remain charged and available for reconciliation."},
	"daemon status":        {summary: "Show the running backend's status and budgets."},
	"serve":                {summary: "Run the local backend in the foreground until interrupted.", example: `arm serve --cpu 4 --memory 8GiB --disk 20GiB`},
	"agent probe":          {summary: "Inspect installed agent CLI versions and messaging capabilities."},
	"agent register":       {summary: "Register one exact supported agent session for attribution and messages.", example: `arm agent register --id builder --adapter codex --session SESSION_UUID`},
	"agent list":           {summary: "List registered agent contacts."},
	"agent send":           {summary: "Send as your exact registered contact/session/registration to one reviewed agent recipient or operator. The durable sender identity supports replies; no broadcast or remote agent forwarding. --key pins retries; lookup never delivers.", example: `arm agent send --id codex-worker --session SESSION_UUID --registration REGISTRATION_ID --to claude-worker --text "Please inspect this result" --key SEND_KEY`},
	"agent reply":          {summary: "Reply to a message received by your exact registration. ARM uses its recorded sender and refuses replacements; no --to is accepted. Native delivery remains adapter-specific. Native CLI responses and acknowledgement text use the sender's durable polling inbox to avoid automatic turn loops.", example: `arm agent reply --id claude-worker --session SESSION_UUID --registration REGISTRATION_ID --message MESSAGE_ID --text "Reviewed" --key REPLY_KEY`},
	"agent message-lookup": {summary: "Read a saved send/reply by original key and exact sender selectors, without native delivery. Not found is a snapshot, not proof that an in-flight request cannot commit.", example: `arm agent message-lookup --id codex-worker --session SESSION_UUID --registration REGISTRATION_ID --key SEND_KEY`},
	"agent inbox":          {summary: "Read messages for an exact registered session. Reading never acknowledges them. Returns a page with an opaque resume cursor.", example: `arm agent inbox --id builder --session SESSION_UUID --pending`},
	"agent watch":          {summary: "Watch an exact recipient inbox (JSON lines when redirected or with --json). Reconnect with the last emitted cursor; use --once for one bounded batch. Output never acknowledges messages.", example: `arm agent watch --id builder --session SESSION_UUID`},
	"agent receive":        {summary: "Explicitly record that a registered session received a message. This is separate from acknowledgement.", example: `arm agent receive --id builder --session SESSION_UUID --message MESSAGE_ID`},
	"agent ack":            {summary: "Explicitly acknowledge a message. Optional response text reaches the original agent sender's polling inbox, or the operator inbox for operator-origin messages. An acknowledgement does not prove resource compliance.", example: `arm agent ack --id builder --session SESSION_UUID --message MESSAGE_ID --text "Reduced build concurrency"`},
	"message operator":     {summary: "Send a durable message to the operator inbox.", example: `arm message operator --text "The build needs another five minutes."`},
	"message send":         {summary: "Send to one exact registered contact snapshot. --key deduplicates exact retries; changed content or recipients conflict. --reply-to links a reply to that contact's operator-inbox message.", example: `arm message send --to builder --text "Please reduce build concurrency." --key OPERATION_ID`},
	"message broadcast":    {summary: "Send to the exact current contact set. Recipient changes during submission reject the send. --key deduplicates exact retries; verify delivery reports before requesting a different send.", example: `arm message broadcast --text "Please release idle leases." --key OPERATION_ID`},
	"install":              {summary: "Install verified managed agent instructions. Preview with --dry-run. Backend startup is separate."},
	"uninstall":            {summary: "Remove unchanged instructions using the saved installation manifest, preserving edits and resource obligations. The executable and state remain."},
	"version":              {summary: "Print ARM, Go and platform versions."},
}
var groupHelps = map[string]string{
	"mesh artifacts": "  list                       Read trusted local companion descriptors\n  import FILE                Copy a verified target-platform companion (--reason required)\n",
	"mesh connector": "  list                       Read local reviewed connector IDs offline\n  add ID SSH_DEST             Review an immutable helper/home mapping (--reason required)\n",
	"database":       "  stats                     Read sizes, SQLite pages and known obligation counts\n  check [--full]            Run a read-only SQLite structural check\n  backup DESTINATION        Write a consistent private standalone SQLite backup\n  remove [--dry-run]        Review a stopped, settled ledger reset (default: keep)\n",
	"budget":         "  list|inspect ID|consumers ID    Browse local owner policy ceilings and lease reasons\n  create NAME --parent ID         Add an immutable child policy (default: self root)\n  set ID --limits JSON            Replace complete typed ceilings; --revision required\n  history ID|lookup --key KEY      Inspect durable change evidence without replay\n  rename ID NAME|describe ID TEXT  Change metadata without rebinding leases\n  drain ID|resume ID|delete ID     Manage admission policy under revision guards\n",
	"mesh":           "  connect SSH_DEST         Review missing-node install/start, then pinned peer connection\n  artifacts list|import    Manage trusted local platform companions (native only)\n  disconnect HOST          Revoke this enrollment locally first, then remotely\n  lookup KEY               Inspect a durable connection/disconnection outcome\n  operations [KEY]         Read operator workflow/attempt receipts offline\n  connector list|add       Manage native-reviewed dashboard connector IDs\n  identity|peers           Inspect configured identities and directional grants\n  init                     Advanced explicit listener and identity setup\n  join --ssh DEST          Pair initialized nodes with reviewed directional grants\n  revoke HOST              Revoke peer trust and close sessions\n  call HOST OP             Dispatch an allowed operation with an exact-retry key\n  contacts HOST            Inspect granted remote contact identities\n  send HOST                Send an explicitly authorized host/contact message\n",

	"pool":    "  create NAME --capacity COUNT               Root --scope or child --parent/--parent-revision\n  roots|children ID|consumers ID              Page partition hierarchy and consumers\n  list|inspect ID|history ID                  Browse definitions, claims and journal\n  lookup --key KEY                           Inspect a durable change without retrying\n  set ID COUNT|adjust ID DELTA                Set capacity or apply a signed delta\n  rename ID NAME|describe ID TEXT             Update resource metadata\n  drain ID|resume ID                          Close or reopen new admission\n  delete ID                                  Tombstone a pool with no outstanding claims\n",
	"storage": "  create SIZE           Create a managed disk lease\n  list                  List storage leases\n  inspect PATH          Measure a directory without changing it\n  export ID DEST --file SOURCE  Preserve selected outputs in a new bundle\n  exports               List export operations (inspect|reconcile|handoff ID)\n  retain ID --ttl TIME   Shorten intermediate retention with --reason\n  cleanup LEASE_ID      Reclaim managed storage (--dry-run previews)\n  handoff LEASE_ID      Preserve remaining files and return responsibility\n",
	"ports":   "  reserve [PORT_SPEC...]  Hold bindings (default: TCP loopback, OS-selected port)\n  list                    List ARM allocations, consumers and reasons\n  discover                Observe native bindings and visible owners\n",
	"daemon":  "  start   Start the backend in the background\n  stop    Stop an idle backend\n  status  Inspect backend status\n",
	"agent":   "  probe     Inspect installed CLIs and messaging capabilities\n  register  Register an exact session (--delivery poll for an inbox)\n  list      List contacts\n  send      Send with your registered sender identity\n  reply     Reply to the recorded original sender\n  message-lookup  Inspect a keyed agent send without delivery\n  inbox     Read a recipient's durable messages\n  watch     Stream inbox messages with reconnect\n  receive   Record receipt of one message\n  ack       Acknowledge one message and optionally reply\n",
	"message": "  operator   Send to the operator inbox\n  send       Send to one contact (--to ID)\n  broadcast  Send to all registered contacts\n",
}

func (a *app) group(name string, args []string) bool {
	if len(args) != 0 && !(len(args) == 1 && (args[0] == "--help" || args[0] == "-help" || args[0] == "-h")) {
		return false
	}
	text, ok := groupHelps[name]
	if !ok {
		return false
	}
	fmt.Fprintf(a.out, "Usage: arm %s COMMAND [OPTIONS]\n\nCommands:\n%s\nUse arm %s COMMAND --help for options.\n", name, text, name)
	return true
}
func (a *app) usage(f *flag.FlagSet) {
	if f.Name() == "arm" {
		a.helpTo(f.Output())
		return
	}
	h := commandHelps[f.Name()]
	fmt.Fprintf(f.Output(), "%s arm %s", a.paint("1;36", "Usage:"), f.Name())
	if h.usage != "" {
		fmt.Fprint(f.Output(), " ", h.usage)
	} else {
		fmt.Fprint(f.Output(), " [OPTIONS]")
	}
	fmt.Fprintln(f.Output())
	if h.summary != "" {
		fmt.Fprintln(f.Output(), "\n"+h.summary)
	}
	if f.NFlag() > 0 || hasFlags(f) {
		fmt.Fprintln(f.Output(), "\nOptions:")
	}
	f.VisitAll(func(v *flag.Flag) {
		name, usage := flag.UnquoteUsage(v)
		if v.Name == "json" {
			usage = "force JSON output (default: automatic terminal detection)"
		}
		isBool := false
		if b, ok := v.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = b.IsBoolFlag()
		}
		label := "--" + v.Name
		if !isBool {
			if name == "string" || name == "value" {
				name = "VALUE"
			}
			label += " " + strings.ToUpper(name)
		}
		fmt.Fprintf(f.Output(), "  %-30s %s", label, usage)
		if v.DefValue != "" && v.DefValue != "0" && v.DefValue != "0s" && v.DefValue != "false" && v.DefValue != "[]" {
			if isBool && v.DefValue == "true" {
				fmt.Fprint(f.Output(), " (default)")
			} else {
				fmt.Fprintf(f.Output(), " (default %s)", v.DefValue)
			}
		}
		fmt.Fprintln(f.Output())
	})
	if h.example != "" {
		fmt.Fprintln(f.Output(), "\nExamples:\n  "+h.example)
	}
}
func hasFlags(f *flag.FlagSet) bool {
	found := false
	f.VisitAll(func(*flag.Flag) { found = true })
	return found
}

// parse permits flags around positional arguments, except root commands and
// run: once their command begins, the remaining arguments belong to it.
type argumentError struct {
	err     error
	command string
}

func (e *argumentError) Error() string {
	command := "arm " + e.command
	if e.command == "arm" {
		command = "arm"
	}
	return fmt.Sprintf("%v; see %s --help", e.err, command)
}
func (e *argumentError) Unwrap() error { return e.err }
func parseQuiet(f *flag.FlagSet, args []string) error {
	out := f.Output()
	f.SetOutput(io.Discard)
	err := f.Parse(args)
	f.SetOutput(out)
	return err
}
func (a *app) parse(f *flag.FlagSet, args []string, stopAtCommand bool) (err error) {
	a.commandName = f.Name()
	defer func() {
		f.Visit(func(v *flag.Flag) {
			if v.Name == "json" {
				a.forceJSON = v.Value.String() == "true"
			}
		})
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			err = &argumentError{err, f.Name()}
		}
	}()
	options, positionals := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "--help" || arg == "-help" || arg == "-h" {
			if err := parseQuiet(f, options); err != nil {
				return err
			}
			out := f.Output()
			f.SetOutput(a.out)
			err := f.Parse([]string{"--help"})
			f.SetOutput(out)
			return err
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			if stopAtCommand {
				positionals = append(positionals, args[i:]...)
				break
			}
			positionals = append(positionals, arg)
			continue
		}
		if f.Name() == "pool adjust" {
			if _, err := strconv.ParseInt(arg, 10, 64); err == nil {
				positionals = append(positionals, arg)
				continue
			}
		}
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		v := f.Lookup(name)
		if v == nil {
			return parseQuiet(f, append(options, arg))
		}
		options = append(options, arg)
		isBool := false
		if b, ok := v.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = b.IsBoolFlag()
		}
		if !inline && !isBool {
			if i+1 == len(args) || args[i+1] == "--" {
				return fmt.Errorf("--%s requires a value", name)
			}
			nextName, _, _ := strings.Cut(strings.TrimLeft(args[i+1], "-"), "=")
			if strings.HasPrefix(args[i+1], "-") && (f.Lookup(nextName) != nil || nextName == "help" || nextName == "h") {
				return fmt.Errorf("--%s requires a value before %s", name, args[i+1])
			}
			i++
			options = append(options, args[i])
		}
	}
	if err := parseQuiet(f, append(append(options, "--"), positionals...)); err != nil {
		return err
	}
	if h, ok := commandHelps[f.Name()]; ok {
		n := len(f.Args())
		if n < h.min || (h.max >= 0 && n > h.max) {
			return fmt.Errorf("expected usage: arm %s %s", f.Name(), h.usage)
		}
	}
	return nil
}

func (a *app) helpTo(out io.Writer) { original := a.out; a.out = out; a.help(); a.out = original }
