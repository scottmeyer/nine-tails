# nine-tails (repo instructions)

This repository builds `nine-tails` and uses it on itself. `./nt` runs the
freshly built binary from this checkout (`make build` first in a new clone or
worktree) against the ordinary store, `~/.nine-tails`. Every clone and worktree
shares that store; this repository is identified by `repo-id=nine-tails` on
`load`, never by a directory (DESIGN.md §1.1).

- New to this store or harness: `./nt load pilot --task "..." --meta
  repo-id=nine-tails --meta harness=<harness>` first; its capsule is the usage
  guide and the catalog of agents.
- Before implementing anything here, load the relevant agent and use its
  capsule as your instructions:
  `./nt load builder --task "..." --meta repo-id=nine-tails`,
  `./nt load reviewer --task "..." --meta repo-id=nine-tails`.
  Keep the `[nine-tails-context=ctx_N]` id; later loads, appends and calls
  inherit the repository through `--context ctx_N`.
- Record corrections you receive while working with
  `./nt prefer|avoid|note --context ctx_N "..."`. Do not add `--meta` unless
  the correction is genuinely scoped (`--meta repo-id=nine-tails` for one that
  applies only here).
- Observations about nine-tails as a tool go to the `nine-tails` agent:
  `./nt remember nine-tails "Observation: ..."` (recall) or
  `./nt note nine-tails "..."` (guidance about its design).
- `make test` must stay green; tests never touch `~/.nine-tails`.
- DESIGN.md is binding for how things are built; the spec is normative for
  behavior. When they disagree, fix DESIGN.md.
