# nine-tails

A small, harness-independent CLI sidecar for persistent agent context. It
resolves a named agent into a token-bounded **context capsule**, records
corrections and useful experience, carries a small versioned **state**, exposes
named **tools** backed by executables, and carries **signals** (reminders,
external events) into future invocations. It does not run an agent loop, pick
a model, or enforce anything.

Working name for the `lore` spec (`lore-sidecar-spec-v0.3.md`). Implementation
decisions are pinned in `DESIGN.md`.

```
load a named context
append what should be remembered
carry a small versioned working state
inspect and repair through an agent
call named executable indirections
carry signals into later invocations
reflect selectively at meaningful boundaries
occasionally compile the journal
```

## Install

```sh
make install          # go install → $(go env GOPATH)/bin/nine-tails
# or
make build            # ./bin/nine-tails
```

Storage lives in `$NINE_TAILS_HOME` (default `~/.nine-tails`): one SQLite
file, an `artifacts/` directory for registered scripts, and an optional
`config.yaml`. There is one store per user; repositories and worktrees are
metadata, not stores (see below). In this repository `./nt` runs
`./bin/nine-tails` (`make build` first in a fresh clone or worktree) against
that ordinary store, so every checkout shares one memory.

## Five-minute tour

```sh
# 1. Create an agent: base instructions are the only required part.
nine-tails base pr-review --meta title="PR Review Agent" --stdin <<'EOF'
## Purpose
Review proposed changes for demonstrable correctness and regression risks.
EOF

# 2. Load it. The capsule is context-ready markdown; the receipt id is inside.
nine-tails load pr-review --task "Review PR 1842" --meta repo-id=my_repo --budget 1400
#   # PR Review Agent
#   [nine-tails-context=ctx_2]
#   ...

# 3. Record a correction against that context. Origin is recorded; ambient
#    metadata is NOT copied as scope unless you pass --meta explicitly.
nine-tails prefer --context ctx_2 "Lead with concrete evidence; keep prose concise."
nine-tails avoid  --context ctx_2 --meta repo-id=my_repo "Editing generated mocks."

# 4. The next load shows both under "Recent adjustments" immediately.
nine-tails load pr-review --meta repo-id=my_repo

# 5. Carry state across invocations (compare-and-swap, size-capped YAML).
nine-tails state put pr-review/working --expect none --stdin <<'EOF'
status: waiting
waiting-on: ci
next-action: recheck the goroutine finding
EOF
#   state_5      ← the capsule heading shows "## Current state (working, state_5)"

# 6. Register a script as a named tool and call it.
nine-tails tool add pr-review complete-pr-diff --script ./complete-pr-diff.sh \
  --description "Fetch complete changed-file contents for a pull request"
nine-tails call --context ctx_2 complete-pr-diff --input '{"pr": 1842}'

# 7. Leave a reminder; it appears in capsules once due, and `tick --claim`
#    leases it for an external scheduler.
nine-tails signal pr-review --at +2h --subject "Recheck PR 1842 after CI" \
  --dedupe-key my_repo:pr-1842:recheck-ci --meta pr=1842
#    Without an agent a signal is for everyone who loads; scope it with --meta.
nine-tails signal --subject "Pass --meta repo-id=my_repo on load" --meta repo-id=my_repo
nine-tails tick --claim --lease 5m

# 8. Inspect and repair anything from an ordinary agent session.
nine-tails inspect pr-review --include base,brief,journal
nine-tails inspect ctx_2
nine-tails inspect pr-review --query "generated mocks"

# 9. Compile accumulated corrections into a brief (needs a model; see below).
nine-tails compile-input pr-review --budget 1200 > input.json
#   ... give input.json to a model, get output.yaml ...
nine-tails brief put pr-review --expect-generation none --expect-base base_1 --stdin < output.yaml
```

Every mutation prints the new id on one line; add `--format json` for the full
record. Errors begin with a `nine-tails:` summary line on stderr and may add
indented diagnostic lines. Exit codes are 2 (invalid), 3 (not found), 4
(store), 5 (tool), 6 (budget), and 7 (conflict).

## Using nine-tails from an agent harness

Put this in your `AGENTS.md` / `CLAUDE.md` (or equivalent):

```md
When asked to use a nine-tails agent:

1. Run `nine-tails load <name> --task "<task>" --meta repo-id=<repo> ...`
   with useful ambient metadata; `repo-id` is this repository's fixed name.
   Apply the returned capsule to the task.
2. Keep the `[nine-tails-context=ctx_N]` id. Use
   `nine-tails load <other> --context ctx_N --task ...` when the capsule
   advertises a narrower agent under "Available agents".
3. Record recurring user corrections with `nine-tails prefer|avoid|note
   --context ctx_N "..."`. Add `--meta` only when explicitly scoping them.
4. Call advertised tools with `nine-tails call --context ctx_N <tool> --input '{...}'`.
5. At a meaningful episode boundary (a correction, a recovered failure, a
   completed or blocked task) load `reflector` with `--context ctx_N` and
   apply it inline: update state, guidance, recall, a signal, or a tool — or
   nothing. Zero writes is a valid outcome.
6. Use `nine-tails inspect` when asked to explain or repair an agent.
```

## Repositories and worktrees

One store per user. A repository is not a store; it is ambient metadata:

```sh
nine-tails load pr-review --task "Review PR 1842" --meta repo-id=my_repo
```

Write the `repo-id` value into the repository's agent instruction file so every
harness, clone and worktree passes the same one; nine-tails never derives it
from version control. Corrections stay unqualified unless you scope them
(`--meta repo-id=my_repo`), so an agent learns across repositories by default
and per repository on request. Nothing else is needed: no per-repo store, no
sync step, no repository awareness in the binary. To hand an agent to another
machine or person, `export --bundle` it and `import` it there.

## Content agents the spec expects

These are ordinary agents, created with `base`; nothing about them is built in.

```sh
nine-tails base reflector --stdin <<'EOF'
Review the episode for information worth carrying forward.

Write only when the episode changes current state, future operating guidance,
durable recall memory, a future signal, or reusable executable capability.
Prefer zero to three precise updates. Do not summarize the episode merely
because it occurred. Do not store raw tool output when a concise fact or
recovery procedure is sufficient.

Write through: nine-tails state put | prefer | avoid | note | remember | signal | tool add.
EOF
```

`brief-compiler` is the compiler as an agent: when it has a base,
`compile-input` uses that base as the compiler instructions instead of the
built-in text, so the compiler is editable with the same commands as any other
agent. See `nine-tails compile-input <agent>` for the output contract.

## Compiling with a model

`compile <agent>` pipes the compile-input JSON to a configured command and
installs whatever comes back on stdout:

```yaml
# $NINE_TAILS_HOME/config.yaml
compiler:
  argv: ["claude", "-p", "--output-format", "text"]
  timeout: 300s
```

or `nine-tails compile pr-review --compiler "claude -p"`, or
`NINE_TAILS_COMPILER="claude -p"`. Any command that reads the document on
stdin and writes the output document on stdout works, including a script that
runs another harness. The output is validated (every input entry must get
exactly one disposition) and installed with compare-and-swap; the
condition-loss lint prints warnings but never blocks.

## Recall memory

`inspect <agent> --lane recall --query "..."` is the built-in lexical recall.
Richer recall is a *tool* named `recall-memory` so the backend can change
without touching any agent:

```yaml
# nine-tails put shared --lane definition --kind tool --name recall-memory --stdin
version: 1
description: Search prior experience for relevant recall entries.
exec:
  argv: ["artifacts/tool_N/recall.sh"]     # any executable; materialize with
  stdin: json                              # `inspect <agent> --lane recall --format json`
input:
  query: {type: string, required: true}
  limit: {type: integer}
output:
  format: json                             # items carry nine-tails record ids
```

## Layout

```
cmd/nine-tails/      cobra commands, one file per group; cli_test.go harness
internal/store/      all SQL: records, metadata, contexts, generations, signals
internal/capsule/    assembly, ranking, budget, rendering
internal/tool/       tool YAML parse/validate/exec
internal/compile/    compiler contract, coverage, install, lint
internal/bundle/     export/import
internal/cli/        flags, config, errors
```

`make test` runs everything. Tests never touch `~/.nine-tails`.
