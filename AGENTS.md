# Development workflow

Each delegated implementation task or subagent must use its own Git worktree and branch. Do not let parallel workers edit a shared checkout. Preserve existing work when moving a task, copy only its owned changes and required reviewed dependencies, and integrate focused commits after review and validation. Keep the primary checkout for coordination and integration.

Commit each major feature after reviewing it, fixing the findings, and running the relevant validation. Keep commits focused on the completed feature and describe material limitations truthfully.

The complete project scope is in `SPEC.md`. Keep `docs/PLAN.md`, `docs/IMPLEMENTATION.md`, and `docs/VALIDATION.md` aligned with actual supported behavior and verification; an initial milestone does not complete the full specification.
