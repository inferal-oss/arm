# Shell completion

ARM generates Bash, Zsh and Fish completion directly from its public command
and flag registrations. No backend is needed to generate or load a script.

For the current shell session:

```bash
# Bash (requires bash-completion)
source <(arm completion bash)
```

```zsh
# Zsh
autoload -Uz compinit && compinit
source <(arm completion zsh)
```

```fish
# Fish
arm completion fish | source
```

To provision future sessions, generate a script into a user-owned location:

```bash
# Bash: bash-completion loads this location on supported installations.
mkdir -p ~/.local/share/bash-completion/completions
arm completion bash > ~/.local/share/bash-completion/completions/arm
```

If your Bash setup does not load that directory, add a source line for the
created file to your own shell configuration. On macOS with Homebrew
bash-completion, use the completion directory documented by that installation.

```zsh
# Zsh: generate once, then put this directory on fpath before compinit.
mkdir -p ~/.zsh/completions
arm completion zsh > ~/.zsh/completions/_arm
```

Add `fpath=(~/.zsh/completions $fpath)` before the existing `compinit` call in
your own `.zshrc`, or initialize completion with `autoload -Uz compinit &&
compinit` after setting `fpath`.

```fish
# Fish automatically loads this directory.
mkdir -p ~/.config/fish/completions
arm completion fish > ~/.config/fish/completions/arm.fish
```

These are explicit user provisioning instructions. ARM does not edit shell
profiles or install scripts as a side effect of completion. Regenerate the
script after upgrading ARM.

Commands and flags complete offline. Host, pool, lease and run suggestions use the
existing authenticated backend through read-only requests, with a shared
250 ms deadline per completion. They never start a backend or initialize an
ARM state directory. Missing, disconnected, slow or unauthorized backends return
no dynamic suggestions. Use `ARM_HOME`, or `--home DIR` before the command, to
select an existing authority.

Only identifier tokens and fixed descriptions enter the completion protocol;
credentials, reasons and other private record content are omitted. Suggestion
tokens contain 1–128 ASCII letters, digits or `._:-`, and cannot start with `-`.
Other supplied names (including spaces or Unicode) remain usable when quoted
for execution; their generated readable aliases and exact IDs still complete.
Ambiguous names are omitted. A suggestion is a discovery aid, never an access
grant. Execution performs its normal authorization and concurrency checks.

The supported dynamic selectors include `--host`, pool selectors and lease
selectors on the public resource commands, and the first `RUN` argument of
`arm log` (including with `-f`, `--follow` or `--offset`). Run suggestions include
proxy and leased runs from the local run inventory, using run IDs and readable
run names. They do not read logs or command output. Inventory errors or more
than 4,096 runs produce no suggestions; at most 256 sorted suggestions are
returned after checking the complete inventory for ambiguous names.

IDs remain valid. Arguments after
`run`'s or `proxy`'s command boundary belong to the executed program; ARM does not discover
or run external plugins to obtain their completion candidates.

The implementation uses [Cobra's shell completion protocol](https://github.com/spf13/cobra/blob/main/site/content/completions/_index.md)
and imports the compatibility handlers' flag registrations, preserving ARM's
existing single-dash long options, argument boundaries and output contracts.
