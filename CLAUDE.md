# nine-tails (repo instructions)

This repository builds `nine-tails` and uses it on itself. `./nt` runs the
freshly built binary against the repo-local store in `.nine-tails/`.

- Before implementing anything here, load the relevant agent and use its
  capsule as your instructions: `./nt load builder --task "..."`,
  `./nt load reviewer --task "..."`. Keep the `[nine-tails-context=ctx_N]` id.
- Record corrections you receive while working with
  `./nt prefer|avoid|note --context ctx_N "..."`. Do not add `--meta` unless
  the correction is genuinely scoped.
- Observations about nine-tails as a tool go to the `nine-tails` agent:
  `./nt remember nine-tails "Observation: ..."` (recall) or
  `./nt note nine-tails "..."` (guidance about its design).
- `make test` must stay green; tests never touch `~/.nine-tails`.
- DESIGN.md is binding for how things are built; the spec is normative for
  behavior. When they disagree, fix DESIGN.md.
