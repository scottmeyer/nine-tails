# nine-tails repository instructions

This repository builds nine-tails and uses checked-in nine-tails agents to
work on itself. `./nt` runs the binary from this checkout against the shared
user store at `~/.nine-tails`; run `make build` first in a new clone or
worktree. The stable repository identity is `repo-id=nine-tails`.

## Start an episode

- If the current instructions already contain a
  `[nine-tails-context=...]` capsule, it is loaded. Follow it and do not load it
  or `pilot` again.
- Otherwise, when the task explicitly names an agent, run `./nt load <agent>
  --task "<concise non-sensitive purpose>" --meta repo-id=nine-tails --meta
  harness=<actual-harness>`. When no agent is named, load `pilot` the same way.
- The `--task` value is stored on the context receipt. Keep the full task in the
  harness conversation; use only a concise, non-sensitive purpose in the load.
- Choose the next agent only from the pilot capsule or the checked-in repository
  roles listed below. If a listed role is missing, install it by following
  `agents/README.md`, then load that exact role with `--context
  <pilot-receipt>`. Its new catalog entry appears on future pilot loads; do not
  reload pilot merely to refresh the current capsule.
- Use a stable lowercase harness name. Use `unknown` only when the harness
  genuinely cannot be identified.
- A fresh user store contains only `pilot` and `reflector`. Do not substitute a
  similarly named personal agent for a missing checked-in repository role.

Every loaded capsule contains the harness-neutral receipt, writeback,
delegation, data-placement, and persistence-safety protocol. Follow it; do not
duplicate that versioned protocol in repository instructions.

## Repository agents and rules

- `nine-tails.builder` implements a scoped change.
- `nine-tails.reviewer` verifies a completed change without fixing it.
- `nine-tails` carries design guidance and the observation log for this tool.
- Run `make test`; tests must never touch `~/.nine-tails`.
- DESIGN.md is binding for implementation and the specification is normative
  for behavior. When they disagree, fix DESIGN.md.
