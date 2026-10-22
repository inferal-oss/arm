# Mesh workspaces

A workspace is a named group of ARM mesh nodes with reviewed membership. Its
directory node maintains the approved member list and exact certificate pins.
For example, when a laptop A connects to build nodes B and C, one workspace lets
B discover C through A and exchange host inbox messages through that connection
while authenticating each other end to end.

Each host still owns its resources, leases and expiry. Workspace membership
permits discovery and the workspace's messaging policy. Resource access,
execution, SSH and file sharing need their own grants. Joining never promotes a
discovered node into a trusted direct peer.

## Supported now

The native local CLI supports creation, joining, candidate admission, directory
refresh, inspection, deny/revoke/retirement, explicit recovery and durable host
inbox send/reply/lookup. Commands use
the local backend. Browser sessions, selected remote dashboards and ordinary
peer RPC cannot invoke workspace administration.

Start with an enabled direct mesh and an existing active, proven pair between
the candidate and directory. Use `arm mesh connect` to establish that pair with
its own concrete trust review. Current admission runs as the directory's local
operator. Public delegated-introducer proposals, automatic background directory
refresh, workspace dashboard controls, agent publication and general graph routes
are remaining work. Current messages use a direct pair or one directory relay.
The directory is an explicitly selected authority; automatic
directory failover, workspace merges and identity rotation are also unfinished.

## Inspect a workspace

```sh
arm workspaces list
arm workspaces inspect --workspace WORKSPACE_ID --section members
arm workspaces inspect --workspace WORKSPACE_ID --section trusted
arm workspaces inspect --workspace WORKSPACE_ID --section operations
arm workspaces lookup --workspace WORKSPACE_ID --key OPERATION_KEY
```

Reads do not initialize a node key, listener or workspace schema. `inspect`
supports `--offset` and `--limit` of 1–20, with an explicit next offset. Sections
also include `changes` and `denies`. Replies have a complete 48 KiB limit;
oversized replies refuse instead of silently dropping records. `lookup` reads
retained history without contacting another node or dispatching a proof.

## Create and join

Use a distinct operation key for each new intent and preserve it for inspection.
The placeholders below come from the preceding concrete response. Commands emit
JSON when redirected, or with `--json`.

1. On directory A, prepare creation:

   ```sh
   arm workspaces create --name builds --key CREATE_KEY --reason "Coordinate build nodes"
   ```

   Inspect its workspace ID, revision, policy and `review_sha256`. Creation is
   still pending. Approve that exact review with:

   ```sh
   arm workspaces decide --workspace WORKSPACE_ID --key CREATE_KEY --revision REVISION --review REVIEW_SHA --approve
   ```

   Terminal confirmation defaults to decline. Scripts must explicitly add
   `--yes` after inspecting the review. `decide` without `--approve` declines.

2. On A, export the invitation to a new file, then transfer that file to B:

   ```sh
   arm workspaces invitation --workspace WORKSPACE_ID --output invitation.json
   ```

   Invitations expire after at most five minutes. They describe the directory's
   public identity and policy; their digest detects changed content and is not
   a signature or authorization token. Export creates a private 0600 file and
   refuses to overwrite an existing path.

3. On B, import the invitation and approve the returned exact candidate review:

   ```sh
   arm workspaces join --invitation invitation.json --key JOIN_KEY --reason "Join the build workspace"
   arm workspaces decide --workspace WORKSPACE_ID --key JOIN_KEY --revision REVISION --review REVIEW_SHA --approve
   arm workspaces consent --workspace WORKSPACE_ID --key JOIN_KEY --output consent.json
   ```

   The directory must match B's existing active, proven direct peer. Transfer
   the fresh consent file back to A. Consent describes B's approval; actual
   possession of its pinned key must still be proved over the live connection.

4. On A, preview admission, then commit the exact returned review:

   ```sh
   arm workspaces admit --workspace WORKSPACE_ID --consent consent.json --key ADMIT_KEY --reason "Admit the reviewed build node"
   arm workspaces admit --workspace WORKSPACE_ID --consent consent.json --key ADMIT_KEY --reason "Admit the reviewed build node" --review REVIEW_SHA
   ```

   The second command asks for confirmation, atomically revalidates the review
   and performs one pinned candidate proof. Scripts require `--yes`. Reusing
   an existing key returns its recorded result without dispatching another proof.

5. On B, fetch fresh membership and explicitly finish the join:

   ```sh
   arm workspaces refresh --workspace WORKSPACE_ID
   arm workspaces lookup --workspace WORKSPACE_ID --key JOIN_KEY
   arm workspaces finalize --workspace WORKSPACE_ID --key JOIN_KEY --revision CURRENT_JOIN_REVISION
   ```

Repeat the candidate flow for C. B can then explicitly refresh and inspect
`trusted` to discover C. This does not create a B–C direct peer or grant execution.

## Interrupted admission

A lost reply remains uncertain. Inspect the original key on the relevant nodes;
do not create a new admission to guess whether the first one committed. A
backend restart invalidates old process approvals and cached authorization.

`resume-join` and `resume-admission` prepare separately keyed recovery reviews
linked to the immutable original. `recovery-decide` approves the exact fresh
review; `recovery-run` executes that child. Candidate recovery first reads its
own exact directory result. A completed directory admission can be reconciled
read-only. A pending proof needs a fresh candidate review with `--permit-proof`,
fresh candidate consent and a separately reviewed directory recovery. Ordinary
lookup and preparation never silently retry a proof. See `arm help workspaces`
and command-specific help for exact selectors.

Local `deny`, `revoke` and `retire` require an exact revision, reason and
confirmation. They retain audit history and make no remote cleanup claim.
Cancellation commands preserve the original intent. Unresolved membership or
recovery history continues to prevent settled database removal.

## Send and reply through the directory

After admission and a fresh membership read, B can send to C's exact stable host
ID without a B–C direct pair:

```sh
arm workspaces send --workspace WORKSPACE_ID --to HOST_C_ID --key MESSAGE_KEY --text "Build is ready" --ttl 1h
arm workspaces outbox --workspace WORKSPACE_ID
```

On C, inspect the host inbox and reply using the original local message ID:

```sh
arm workspaces inbox --workspace WORKSPACE_ID
arm workspaces reply --workspace WORKSPACE_ID --message ORIGINAL_MESSAGE_ID --key REPLY_KEY --text "Received"
```

The reply goes to the original sender, B. The relay A does not become the sender
and cannot read the encrypted body. This is a host/operator inbox; publishing
agent contacts and delivering into Codex/Claude sessions across routes remain
separate work. No native command, resource, SSH or file operation is forwarded.

A send persists the immutable intention before dispatch. Repeating the exact
key inspects that intention and never sends it again; changed content conflicts.
After an interrupted response, preserve the key and inspect it:

```sh
arm workspaces message-lookup --workspace WORKSPACE_ID --key MESSAGE_KEY
arm workspaces message-lookup --workspace WORKSPACE_ID --key MESSAGE_KEY --reconcile
```

`--reconcile` explicitly asks the pinned destination for its retained receipt. It
does not resend the message. An absent receipt, timeout or expired message is not
proof of non-delivery. Only a never-dispatched `prepared` intention can be
cancelled with `message-cancel --revision REVISION`; terminal confirmation or
`--yes` is required. Dispatched or unknown sends must settle before ordinary
workspace retirement, so retirement cannot remove their live lookup authority.
If the destination becomes permanently unreachable, revoked or replaced,
uncertain evidence remains charged and blocks settled reset; historical-receipt
recovery and destination nonexecution sealing are not implemented yet.

Inbox/outbox pages contain at most 20 complete records and 96 KiB. Pass the
returned routing revision to `--revision` on subsequent pages; restart the read
if it changes. Retained history is bounded to 256 incoming and 256 outgoing
messages per workspace, eight unresolved sends and a complete 32 MiB routing
ledger. New work refuses at capacity. Routine archival remains unfinished.
Bodies are at most 16 KiB UTF-8 and the encoded envelope must also fit 56 KiB;
highly escaped bodies can reach that limit first. Message deadlines are at most
24 hours and never silently settle uncertainty.

After all outgoing obligations settle and the parent workspace is explicitly
retired, `messages-retire --workspace WORKSPACE_ID --key RETIRE_KEY --revision
REVISION` records local routing retirement. It requires confirmation and claims
no remote cleanup. Use command-specific help for the exact selectors.

## Connection and freshness limits

Admission, directory refresh and own-admission status use a closed direct-peer
transport with pinned mutual TLS, followed by independent pinned TLS 1.3 between
the exact workspace endpoints. It accepts no arbitrary target address, generic
RPC, TCP forwarding, resource, execution, SSH or file operation.

Membership authorization expires within 60 seconds from request start. Streams
last at most 30 seconds and stop earlier when their authority expires. Persisted
observations after restart are display evidence only. A partition or directory
outage eventually pauses new messaging; it never creates capacity or new grants.
The implementation checks clocks, current pair/revision, membership and denies,
and bounds records, bytes, pending streams and I/O duration.

See [the approved routing scheme](MESH-ROUTING-PROPOSAL.md),
[implementation details](IMPLEMENTATION.md) and [verification evidence](VALIDATION.md).
