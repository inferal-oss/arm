# Agent Resource Manager

ARM is a Go resource manager for agents, coordinating time-bounded CPU, memory,
storage and other resource leases with durable ownership, admission, recovery,
CLI, web and terminal interfaces, and explicitly authorized mesh operations.
The complete scope and current delivery boundaries are in SPEC.md and docs/.

# Development workflow

## Coordinator operation

The coordinator owns planning, task routing, review and integration. Keep the primary checkout for coordination and integration. For each new non-tiny user request, create a separate Codex chat in this project with a concrete objective, acceptance criteria and these operating instructions. Tiny, localized changes may be handled directly. Reuse an existing chat when the request continues its scope; do not create duplicate work. Carry forward the user's authorization and constraints in task prompts.

Before assigning work, inspect the current Git status, branches/worktrees, project chats and delivery plan. Identify existing completed or in-progress work that needs review or integration. Preserve staged, unstaged and untracked work; never reset or overwrite it to make integration convenient. Record task ownership, dependencies, commit IDs, review status and remaining gates in `docs/PLAN.md`.

Give each implementation task a bounded scope and an isolated branch/worktree. Coordinate shared CLI, backend, schema, module and documentation changes centrally. A task must return focused commits, a summary of behavior, review findings and fixes, validation evidence and material limitations. Monitor assigned chats through completion or a concrete blocker; an idle chat or an author's completion report does not establish acceptance.

Review each completed step before integration, fix the findings and run relevant validation against the composed result. Use an isolated reviewer checkout for delegated reviews. Inspect the actual final diff and dependencies, including security, resource accounting, lifecycle, cancellation and recovery behavior where applicable. Integrate focused reviewed commits; do not merge unrelated divergent history or claim acceptance from tests of an earlier source state. Recheck affected integration boundaries after fixes.

Commit each major completed feature separately after review, fixes and validation. Update `docs/PLAN.md`, `docs/IMPLEMENTATION.md` and `docs/VALIDATION.md` to distinguish implemented behavior, scoped evidence, unsupported behavior and remaining work. Report integrated commit IDs and unresolved gates truthfully. Continue authorized work without repeatedly asking for permission; honor explicit user-review requirements and explain concrete blockers when they apply.

## Implementation isolation and completion

Each delegated implementation task or subagent must use its own Git worktree and branch. Do not let parallel workers edit a shared checkout. Preserve existing work when moving a task, copy only its owned changes and required reviewed dependencies, and integrate focused commits after review and validation. Keep the primary checkout for coordination and integration.

Commit each major feature after reviewing it, fixing the findings, and running the relevant validation. Keep commits focused on the completed feature and describe material limitations truthfully.

The complete project scope is in `SPEC.md`. Keep `docs/PLAN.md`, `docs/IMPLEMENTATION.md`, and `docs/VALIDATION.md` aligned with actual supported behavior and verification; an initial milestone does not complete the full specification.
