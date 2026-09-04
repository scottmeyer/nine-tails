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
`config.yaml`. In this repository `./nt` is a wrapper that uses `./bin/nine-tails`
with a repo-local store in `.nine-tails/`.

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
`hooks run` preserves its launched harness's exit status (or Unix
`128 + signal`) after a successful launch.

## Using nine-tails from an agent harness

For native prompt injection in Claude Code or Codex, install the small user
hook adapter once:

```sh
nine-tails hooks install --claude   # $CLAUDE_CONFIG_DIR/settings.json
nine-tails hooks install --codex    # $CODEX_HOME/hooks.json

# Then explicitly launch one mechanically active agent session:
nine-tails hooks run pr-review --meta repo-id=my_repo --claude
nine-tails hooks run pr-review --meta repo-id=my_repo --codex -- --model MODEL
```

The default config locations are `~/.claude/settings.json` and
`~/.codex/hooks.json`; the shown environment variables relocate their whole
config homes and are useful for isolated setups. Codex requires a separate
trust review for new or changed non-managed hooks: open `/hooks` after
installation and approve the three nine-tails entries. See the current
[Claude Code hooks reference](https://code.claude.com/docs/en/hooks) and
[Codex hooks reference](https://learn.chatgpt.com/docs/hooks).

Installation makes the harness invoke a tiny gate globally. It does **not**
make every Claude/Codex chat a nine-tails run. Unless the current harness is in
a process tree started by `hooks run` and its session binds that live
capability, the gate exits 0 without output, decoding hook input, or opening
the nine-tails store. Installation is idempotent and merge-preserving; remove
only its recognizable entries with `nine-tails hooks uninstall --claude` or
`--codex`. Existing settings are replaced atomically: Unix retains the file
mode, while Windows `ReplaceFileW` retains an existing destination's DACL and
attributes; a newly created Windows settings file inherits its directory ACL.

Within an active run, the first real user prompt atomically claims and loads a
fresh capsule and uses that exact prompt as its task. Later prompts in the same
episode are silent. Compaction re-injects the cached capsule without creating
another receipt; a same-run resume can do the same. `/clear` waits for the next
real prompt and starts a new receipt parented to the last one. Repeatable
`hooks run --meta key=value` values are parsed as the usual string multimap and
become ambient metadata on every fresh episode load, including its filtering,
ranking, and context receipt; cached compact/resume replay does not create or
change metadata. The encoded activation metadata is capped at 128 KiB and
rejected as invalid input before config or store access. Every marker update
also enforces reserved-envelope and 1 MiB total encoded limits, so a successful
write cannot make the next hook silently reject its own state. Claude capsules
are loaded at no more than 2,800 estimated
tokens so its current
10,000-character hook-output ceiling cannot replace the capsule with a file
preview. Codex capsules are capped at 40,000 estimated tokens so their escaped
cache stays below the private marker's 1 MiB read limit. The adapter never
reads transcripts or triggers unconditional reflection, and it runs no daemon
or network service.

Claude currently reports `SessionStart` sources `startup`, `resume`, `clear`,
`compact`, and `fork`, plus transition-capable `SessionEnd` reasons `clear` and
`resume`. Codex reports the first four start sources but only `other` at
session end, so a live wrapper accepts Codex's cross-session-id `clear` and
`resume` starts directly. An ordinary nested Codex startup remains inert, but
hook JSON has no process identity: a nested Codex that inherits the capability
could claim it after its own cross-id clear/resume while the root is still
live. Avoid nested Codex sessions inside one active wrapper when that boundary
matters; wrapper cleanup, random proof, owner liveness, and expiry still limit
the capability to that explicit process tree and lifetime.

Owner-PID liveness is best-effort rather than a process-birth identity. If the
wrapper crashes while its explicitly launched descendant survives, PID reuse
inside the marker's rolling 24-hour window can make that inherited capability
appear live again; under normal inheritance the random proof stays within that
process tree.

On Unix, the ephemeral runtime directory and marker are restricted to 0700 and
0600. On Windows they inherit the ACL on `NINE_TAILS_HOME`, which therefore
must not be a shared home. The wrapper stays alive on foreground Ctrl-C while
Claude/Codex handles the interrupted turn, then removes the marker when the
harness exits. On Windows it launches native `.exe` harnesses directly and
rejects `.cmd`/`.bat` shims with exit 5 because Windows PowerShell 5.1 cannot
preserve arbitrary arguments through the batch remarshal; install a native
Claude/Codex executable for `hooks run`.

The portable state lock is an atomic directory held only for a short critical
section. It is not stolen automatically: killing a hook process inside that
section can leave a stale `.lock` directory, causing later events to time out
inactive until that lock is removed.

The portable manual integration remains useful for other harnesses. Put this
in your `AGENTS.md` / `CLAUDE.md` (or equivalent):

```md
When asked to use a nine-tails agent:

1. Run `nine-tails load <name> --task "<task>" --meta k=v ...` with useful
   ambient metadata. Apply the returned capsule to the task.
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
internal/harness/    Claude/Codex hook config, lifecycle gate, run capability
internal/cli/        flags, config, errors
```

`make test` runs everything. Tests never touch `~/.nine-tails`.
