# nine-tails — implementation design (binding for v0)

This document pins the decisions that `lore-sidecar-spec-v0.3.md` leaves open.
The spec is normative for behavior; this file is normative for *how we build it*.
When the two disagree, the spec wins and this file gets fixed.

Working name: **nine-tails**. Binary: `nine-tails`. Module:
`github.com/scottmeyer/nine-tails`. Language: Go 1.26, no cgo.

Dependencies (keep it to these): `modernc.org/sqlite`, `github.com/spf13/cobra`,
`gopkg.in/yaml.v3`, stdlib.

## 0. Non-negotiables

- Simplicity over completeness. A feature an agent can't explain from its text
  representation is too complicated.
- Data on stdout, diagnostics on stderr, never interactive, never colored.
- Every mutation is an immutable record; only mechanical fields change in place.
- Exit codes exactly as spec §16.4: 0 ok, 2 invalid input, 3 not found,
  4 store failure, 5 tool/adapter failure, 6 unused, 7 CAS/lease
  conflict. `hooks run` is the explicit supervisor exception: after a successful
  launch it preserves the child status (or Unix `128 + signal`).
- Errors: first stderr line is `nine-tails: <summary>`; detail lines may follow,
  indented two spaces. With `--format json` the same error is also written to
  stdout as `{"error": "...", "code": N}` so structured callers can parse it.
  The one exception is `call`: a tool's stderr streams through untouched, so
  on a failed call the summary line follows the tool's own output (§9).
- Every key nine-tails emits in its own JSON or YAML is `snake_case`
  (`created_at`, `origin_context`, `context_id`, `available_at`). The one
  exception is a harness-owned lifecycle response: adapters must reproduce the
  external wire schema exactly (`hookSpecificOutput`, `hookEventName`,
  `additionalContext`). Model-authored inputs (compiler output, import
  documents) are accepted in snake_case or kebab-case.

## 1. Home directory and config

`NINE_TAILS_HOME` if set, else `~/.nine-tails` (deliberately not the platform
data dir: an agent must be able to find it). Layout:

```
$NINE_TAILS_HOME/
├── nine-tails.db      # SQLite, WAL, busy_timeout 5000ms, BEGIN IMMEDIATE writes
├── artifacts/<record-id>/<basename>
├── exports/
├── runtime/           # private, ephemeral `hooks run` capabilities only
└── config.yaml        # optional
```

Every command that accesses the knowledge store creates the home and database
on first use. Harness install/uninstall and an inactive lifecycle gate do not.

### 1.1 One store per user; repositories and worktrees are metadata

There is one store per user. Repositories, clones and worktrees are not
stores and never get their own: keying memory by checkout directory forks it
(a fresh worktree would find no agents and silently create an empty store).

- A repository is ambient metadata on `load`: `--meta repo-id=<name>`. The
  value is written literally in that repository's agent instruction file
  (CLAUDE.md, AGENTS.md or equivalent), so every harness passes the same value
  and every worktree inherits it because the file is checked in. nine-tails
  never computes it; it knows nothing about version control.
- Nested loads, appends and calls carry the repository through `--context`,
  so it is passed once per session.
- Records are unqualified unless a correction is explicitly repo-specific
  (`--meta repo-id=<name>`), exactly as spec §9.1 says. The compiler sees the
  origin context's `repo-id` and the condition-loss lint flags a dropped one.
  A load that carries no `repo-id` sees everything: a key present on only one
  side never excludes (spec §8.2).
- Agent names are global. An agent whose base is specific to one repository
  is a name (`nine-tails.reviewer`), not a store. It is shared with a second
  repository by generalizing the base and scoping the specifics with
  `repo-id`, and only when a second repository actually needs the name.
- A branch or worktree name is invocation metadata (`--meta branch=<name>`)
  that lands on the receipt; it is never scope.
- An agent instance is its context receipt, never a name. Several builders on
  different tracks are one agent, `builder`, whose learnings roll up together;
  each load carries what distinguishes it (`--meta track=auth`,
  `--meta session=<id>`) and the receipt records it as origin. Address the
  ladder with what already exists: `signal` alone reaches everyone,
  `signal builder` every builder, `signal builder --meta track=auth` the
  builder that loaded with that key (the conflict rule does the targeting, so
  every load must pass the key, exactly as with `repo-id`). Names like
  `builder@session` fragment memory and are not used. The binary never reads
  a harness session id from the environment; the adapter or instruction file
  passes it as `--meta` in one place.
- Something every builder must keep knowing is guidance (`note builder`), not
  a signal: a signal has a when and a done, and the first instance to
  acknowledge it removes it for the rest. Something every agent of every type
  must know belongs in the instruction file, the one cross-agent guidance
  channel.
- Tools act on the working directory they are called from, never on a stored
  checkout path, so one definition serves every worktree.
- State is per agent, not per checkout: uniqueness is by name, so two
  checkouts of one repository working on the same agent share `working` and
  CAS keeps one truth. If one agent must hold state for several repositories
  at once, that is the spec §22 "state conventions" tripwire; the smallest
  response is a per-repository name plus `repo-id` meta.
- Sharing an agent with another machine or person is spec §8.5: an export
  bundle, committed alongside the code when that is convenient. Never a
  second store.

In this repository `./nt` selects the freshly built binary and nothing else.

### 1.2 Entry agent and starter

`pilot` is the conventional entry agent: its base is the usage guide, its
related agents are the catalog of what the store offers, and a model session
starts with

```
nine-tails load pilot --task "<task>" --meta repo-id=<repo> --meta harness=<harness>
```

That line is the whole per-repository bootstrap; it is the one thing a
repository's instruction file (or a harness hook) must say, and it never
changes. Everything else a session needs to know is in the capsule it gets
back, so the guide is versioned with the behavior it describes and corrected
like any other agent (`note pilot --context ctx_N`, `base pilot`).

The starter is two ordinary export documents embedded in the binary,
`internal/starter/pilot.yaml` and `internal/starter/reflector.yaml`. `load
pilot` on a store that lacks an agent named there imports that document and
says so on stderr; an agent that already exists, whoever made it, is never
touched, and nothing else ever seeds. `pilot` is not a reserved name: it is
edited, exported and imported like any agent. `brief-compiler` is not seeded
because the built-in compiler instructions already exist.

The harness is a facet of pilot, not a name: `--meta harness=<harness>` on
the load and on the notes that are harness-specific. One pilot learns to use
nine-tails; each harness's quirks stay scoped to it.

Foreign agent definitions (a subagent file, an AGENTS.md, a catalog entry)
are adopted by the model following the recipe in pilot's base: `base` for
the instructions, `tool add` for real executables only, `agent add pilot`
to advertise, then load and read back. There is no markdown importer:
which part is base, guidance or tool is a semantic judgment (spec §5.2), and
the mechanical part is already `base`, `agent add` and `import --stdin`. The
starter files are the template for agent packs: one document per agent,
imported with `import`.

`config.yaml` (all optional, defaults shown; the spec calls these configurable):

```yaml
compile_advice_tokens: 4000 # `load` advises a compile above this estimated size; 0 = never
signal_excerpt_chars: 300
state_max_bytes: 8192
context_retention_days: 30
compiler:
  argv: []                  # e.g. ["claude", "-p"]; see §10
  timeout: 300s
```

Flags override config; config overrides defaults. `nine-tails config` prints
the effective values as JSON so an agent can see them.

Configuration is validated before the store is opened. Byte caps, retention
days, excerpt lengths, and compiler timeouts must be positive; the compile
advice threshold must be zero or positive. Invalid configuration is exit 2.

`NINE_TAILS_NOW` (RFC 3339), when set, is "now" for every timestamp and
comparison. Tests use it; humans never need it.

## 2. Identifiers and names

One global integer counter (table `seq(n)`, starts at 0 so the first ID is
`<prefix>_1`), allocated inside the write transaction. IDs are `<prefix>_<n>`:

| Thing | Prefix |
| --- | --- |
| record, definition/agent-base | `base` |
| record, kind brief-item | `item` |
| record, lane state | `state` |
| record, definition/tool | `tool` |
| record, definition/related-agent | `rel` |
| record, lane signal | `sig` |
| any other record | `rec` |
| context receipt | `ctx` |
| brief generation | `gen` |
| signal lease token | `lease` |

The counter is global so numbers never collide. IDs are opaque; the prefix is
a readability aid. Anything matching `^[a-z]+_[0-9]+$` is always an ID, never
a name.

**Names** (agent, tool, state, related-agent, brief-item key) match
`^[a-z0-9][a-z0-9.-]*$` — no `_`, no `/`, no whitespace. Reserved names:
`shared` (namespace), `base` (the base definition), `ack`, `none`. Exit 2
otherwise. Title-casing an agent name uppercases the first byte of each
`-`/`.`-separated word.

**Recency** is `created_at` (UTC, second precision, `Z`), ties broken by
`rowid`. "Newest first" = `ORDER BY created_at DESC, rowid DESC`. Every list
output uses creation order unless stated. All stored timestamps are normalized
to UTC with `Z` before storage so lexical comparison is chronological.

## 3. Schema

Exactly the spec §8.3 tables plus:

```sql
CREATE TABLE seq (n INTEGER NOT NULL);           -- single row
CREATE TABLE signal_delivery (                    -- spec §15.3
    record_id       TEXT PRIMARY KEY,
    agent           TEXT NOT NULL,
    available_at    TEXT NOT NULL,
    dedupe_key      TEXT,
    state           TEXT NOT NULL,                -- pending | leased | acknowledged
    lease_token     TEXT,
    leased_until    TEXT,
    acknowledged_at TEXT
);
CREATE UNIQUE INDEX signal_dedupe ON signal_delivery(agent, dedupe_key)
    WHERE dedupe_key IS NOT NULL AND state != 'acknowledged';
PRAGMA user_version = 2;   -- 2: contexts.token_budget became estimated_tokens
```

`records.name`: required for lane=definition, lane=state, and kind=brief-item;
null for every other guidance/recall/signal record. Signals put `subject` in
`meta`.

Metadata is `metadata(record_id, key, value)`. Keys and values are trimmed;
exact duplicate (key, value) pairs collapse to one, preserving first-insertion
order. Keys may not contain whitespace, `=`, `[`, `]`. Values are arbitrary.

**Bodies** must be valid UTF-8 text and are stored verbatim except that exactly
one trailing `\n` is removed if present. A body empty after that is exit 2
(`signal` body may be empty; its arbitrary external data is still text).
Body outputs in yaml format append exactly one trailing newline.

## 4. Record envelope (JSON/YAML)

```yaml
id: rec_41
agent: pr-review
lane: guidance
kind: prefer
name: null
body: Lead with evidence.
created_at: "2026-09-04T16:30:00Z"
origin_context: ctx_72
status: active
supersedes: null
meta:
  repo-id: [my_repo]      # always list-valued (multimap)
```

Same shape everywhere: `inspect`, `export`, compile input. A signal envelope
embedded in inspect output adds `delivery: {state, available_at, dedupe_key,
lease_token, leased_until, acknowledged_at}`.

## 5. Package layout and ownership

```
cmd/nine-tails/main.go          root; wiring; exit-code mapping; `agents`, `config`
cmd/nine-tails/cmd_append.go    append, note, avoid, prefer, remember, base
cmd/nine-tails/cmd_load.go      load
cmd/nine-tails/cmd_inspect.go   inspect
cmd/nine-tails/cmd_put.go       put
cmd/nine-tails/cmd_state.go     state get|put
cmd/nine-tails/cmd_context.go   context list|pin|unpin|gc
cmd/nine-tails/cmd_tool.go      tool add, agent add
cmd/nine-tails/cmd_call.go      call
cmd/nine-tails/cmd_signal.go    signal, signal ack, tick
cmd/nine-tails/cmd_compile.go   compile-input, brief put, compile
cmd/nine-tails/cmd_export.go    export, import
cmd/nine-tails/cmd_hooks.go     harness install/uninstall, explicit run, dispatch gate
internal/store/                 all SQL: records, metadata, contexts, generations,
                                signals, state CAS, RecentGuidance (the ONLY
                                implementation of §7 rule 4)
internal/capsule/               assembly, ranking, size reporting, markdown + json render
internal/tokens/                deterministic estimate
internal/tool/                  YAML tool body parse/validate/exec
internal/compile/               compile-input, output validate, coverage, install, lint
internal/bundle/                export/import
internal/starter/               embedded starter documents (pilot, reflector); seeded by `load pilot`
internal/cli/                   flags, config, body reading, output helpers, errors
internal/harness/               shared adapter contract, reversible JSON merge,
                                ephemeral capability/session binding
```

Error mapping: `store.ErrNotFound → 3`, `store.ErrConflict → 7`,
`store.ErrInvalid → 2`, `cli.ExitError` carries its code, anything else → 4.
Concrete assignments: `--expect` naming a missing/superseded ID → 7; unknown
`--context` → 3; `signal ack` unknown id → 3, wrong token / not leased /
expired → 7; tool cannot start or times out → 5, otherwise the tool's exit code
verbatim; compiler cannot start / nonzero / timeout → 5; compiler output
unparsable or failing validation → 2; malformed `--at`/`--meta`/
unknown flag or command → 2; a stored `user_version` higher than ours → 4.

An agent **exists** iff any record (any status) carries its name. `load`,
`inspect`, `export`, `compile-input`, `compile`, `state get` on a nonexistent
agent → 3. `append`/`put`/`base`/`signal`/`tool add`/`agent add` create it
implicitly.

## 6. Command surface (v0)

```
nine-tails load <agent> [--task T] [--context ctx] [--meta k=v]... [--format md|json|yaml]
nine-tails append [<agent>] --lane guidance|recall [--kind K] [--meta k=v]... [--context ctx] (TEXT | --stdin)
nine-tails note|avoid|prefer|remember [<agent>] [--meta]... [--context ctx] (TEXT | --stdin)
nine-tails base <agent> [--expect ID|none] [--meta]... (TEXT | --stdin)
nine-tails put <agent> --lane definition|state --kind K --name N [--expect ID|none] [--meta]... [--context ctx] (TEXT | --stdin)
nine-tails state get <agent>/<name> [--format yaml|json|id]
nine-tails state put [<agent>/]<name> --expect ID|none [--context ctx] [--meta]... (TEXT | --stdin)
nine-tails inspect <agent | id> [--include a,b] [--lane L] [--kind K] [--name N] [--query Q] [--all]
                                [--coverage C] [--lint condition-loss] [--format json|yaml]
nine-tails tool add <agent> <name> --script PATH (--description D | --stdin) [--meta]...
nine-tails agent add <agent> <name> --description D [--meta]...
nine-tails call [--context ctx | --agent A] <tool> [--input JSON | --stdin]
nine-tails signal [<agent>] --subject S [--body B | --stdin] [--at RFC3339|+5m] [--dedupe-key K] [--meta]... [--context ctx]
nine-tails signal ack <sig-id> --lease <token>
nine-tails tick [--claim] [--lease 5m] [--agent A]
nine-tails context list [--agent A] [--limit N] | pin <ctx-id> | unpin <ctx-id> | gc [--older-than 30d] [--dry-run]
nine-tails compile-input <agent> [--format json|yaml]
nine-tails brief put <agent> --expect-generation gen_11|none --expect-base base_4 --stdin [--dry-run]
nine-tails compile <agent> [--compiler "claude -p"]
nine-tails export <agent> [--include base,brief,journal,state,tools,agents] [--bundle FILE.tar] [--all]
nine-tails import (FILE.yaml | FILE.tar | --stdin)
nine-tails agents [--format text|json]
nine-tails config
nine-tails hooks install (--claude|--codex)
nine-tails hooks uninstall (--claude|--codex)
nine-tails hooks run <agent> [--meta k=v]... (--claude|--codex) [-- HARNESS_ARGS...]
```

**Mutation output.** Every mutating command prints exactly one line on stdout:
the primary new ID (`rec_41`, `state_18`, `sig_9`, `gen_12`, `tool_7`).
With `--format json` it prints the new record's envelope, plus command-specific
extras: `signal` adds `"deduplicated": true|false` (a dedupe hit also writes
`nine-tails: deduplicated against sig_44` to stderr); `brief put` prints
`{generation, items: [ids], warnings: [...]}`; `import` prints one new ID per
line (JSON: `{ids: {old: new}}`); `signal ack`, `pin`, `unpin` print the
affected ID; `context gc` prints one deleted ID per line (JSON: `{deleted}`).

**`--context` implies the agent.** On `append`, `note|avoid|prefer|remember`
and `state put`, `<agent>` is optional when `--context` is given and defaults
to the context's agent. Rule: with `--context`, if two or more positionals are
given the first is the agent (and must match the context's agent, else exit 2
`nine-tails: ctx_72 belongs to pr-review, not evidence-reviewer`); if one is
given it is the TEXT; with `--stdin` there are no positionals. `signal` takes
an optional agent and defaults to `shared`: a signal is a signal, not a
message, and every agent that loads sees a shared one (§7 rule 7). Name an
agent only when a wake-up must start that agent.

**TEXT vs --stdin**: exactly one. TEXT beginning with `-` needs `--` before it
(cobra convention); the usage line shows it.

**Lanes per command**: `append` accepts `--lane guidance|recall` only (default
`recall`; `--kind` defaults to `note` for guidance, `memory` for recall) and
rejects `--kind brief-item`; `put` accepts `--lane definition|state` only. The
state lane has exactly one kind, `working-state` (anything else is exit 2, so
`state get`, `state put` and `load` always agree on what a named state is);
`put` runs state validation (§8) for state and tool validation (§9) for
definition/tool. No v0 command produces `status=disabled`.

`--meta k=v` may repeat; splits at the first `=`; missing `=` or empty key → 2.

`--at` accepts RFC 3339 (any offset; stored as UTC) or `+<integer><unit>` with
unit `s|m|h|d`.

`agents` prints one name per line by default; `--format json` gives
`[{name, has_base, active_records}]`.

## 7. Load / capsule assembly (spec §10)

Whole load runs in one `BEGIN IMMEDIATE` transaction: allocate the context ID
first (so the header cost is exact), read, render, write the receipt, commit.

Resolved metadata = parent context's metadata ∪ explicit `--meta`.

Candidates:

1. **Base**: active `definition/agent-base/base`. Missing → exit 3.
2. **State**: active lane=state records that pass the conflict rule, sorted by
   score desc then name asc. Never truncated. A state body that is not valid
   YAML is skipped (see *skipped* below).
3. **Brief items**: item records of the active generation with status active
   that pass the conflict rule. Sort: score desc, then generation ordinal asc.
   Render order is the sort order.
4. **Recent guidance**: `store.RecentGuidance(agent)` — active lane=guidance
   records, excluding kind=brief-item, whose `brief_inputs` row in the *active
   generation* is absent or `deferred`. A `represented` or `superseded-by` row
   suppresses an entry only while that generation remains active. If a later
   generation drops the corresponding item and does not account for the source
   entry, the still-active source entry renders as recent again. Sort: score
   desc, then newest first. Coverage inspection still uses each entry's newest
   accounting row across all generations.
5. **Tools**: active definition/tool records owned by the agent, plus `shared`
   tools whose `available-to` meta contains the agent or that have no
   `available-to`. Agent-owned shadows shared by name. Sort: score desc, name
   asc. A tool body that fails `tool.Parse` is skipped.
6. **Related agents**: active definition/related-agent records owned by the
   agent. Sort: score desc, name asc.
7. **Signals**: `signal_delivery` rows for the agent and for `shared` (those
   whose `available-to` names the agent or is absent), state != acknowledged,
   `available_at <= now`, joined to records, passing the conflict rule. Sort:
   score desc, then available_at asc, rowid asc. Load never mutates delivery.

`shared` is an ordinary agent name for storage and inspect. The cross-agent
visibility is shared tools (rule 5) and shared signals (rule 7), both honoring
`available-to`; `call` applies the tool filter. `available-to` on any other
record is ordinary metadata.

Conflict rule (2–7): for each key present on BOTH the record and the resolved
metadata, if the value sets are disjoint, exclude the record.
Score = count of distinct (key, value) pairs shared with resolved metadata.

**Skipped**: an optional record (state, tool, related-agent, brief item) whose
body cannot be rendered is omitted, excluded from the receipt, reported on
stderr as `nine-tails: skipped <id>: <reason>`, and listed in JSON under
`skipped: [{id, reason}]`. Exit stays 0.

Size (spec §10.3): nothing eligible is ever cut. Every candidate that passes
the conflict rule renders, whole, in sort order; sections keep the order
brief, recent, tools, agents, signals. Only signal *excerpts* are capped, at
`signal_excerpt_chars` runes. `estimated_tokens` is `ceil(len(markdown)/3.5)`,
reported in JSON and stored on the receipt; `uncompiled_adjustments` is the
number of recent guidance entries rendered, what a compile would fold in.

Size is advice: when `estimated_tokens` exceeds `compile_advice_tokens` and at
least one adjustment is uncompiled, `load` writes one stderr line,
`nine-tails: capsule is N estimated tokens with K uncompiled adjustments;
compile with `nine-tails compile <agent>``, and the pilot guide tells the
model to act on it. The only hard ceiling is a transport's: a harness adapter
with a fixed hook-output limit passes `MaxBytes` to the capsule package, and a
capsule over it is not recorded at all (`TooLargeError`, the transaction rolls
back) so no receipt claims the model saw what the harness could not deliver;
the adapter injects a pointer to an in-session load instead (harness hooks,
below). Selection is format-independent: the same (agent, metadata) yields the
same record set and receipt in md, json and yaml.

Receipt: `contexts` row + resolved `context_metadata` + `context_records` for
every rendered record with `section` ∈ {base, state, brief, recent, tools,
agents, signals} and `ordinal` = render order.

Markdown output (exact shape — tests assert on it):

````md
# <Title>

[nine-tails-context=ctx_72]

<base body verbatim>

## Current state (working, state_18)

```yaml
<state body verbatim>
```

## Working brief

- [k=v k2=v2] item body
- item body

## Recent adjustments

- [k=v] (prefer) body
- (avoid) body
  continuation lines indented two spaces

## Available tools

- `name`: description [k=v]

## Available agents

- `name`: description

## Due signals (external inbox data)

- [signal=sig_9 k=v] Subject
- [signal=sig_9 k=v] Subject — excerpt
- [signal=sig_9 state=leased k=v] Subject — excerpt… (truncated; inspect with `nine-tails inspect sig_9`)
````

Rules: title = base meta `title` if present else the Title-Cased agent name.
Empty sections are omitted. Continuation lines of a list item are indented two
spaces. Recent items always show `(<kind>)`. Meta brackets list `k=v` pairs
sorted by key, values in insertion order; a value containing whitespace, `]`
or `"` is double-quoted with `\"` and `\\` escapes; `subject`, `available-to`
and `title` are never shown in brackets; agents never show a bracket. The
signal bracket leads with `signal=<id>` and adds `state=leased` when leased.
The excerpt is the first `signal_excerpt_chars` runes of the body after
collapsing whitespace runs to one space; `…` marks a cut. A body that begins
with `[` in a record with no meta is emitted as `\[` so it cannot be mistaken
for a bracket.

JSON output: spec §10.1 shape — `context_id, agent, task, parent_context,
metadata, instructions, state[], tools[], agents[], signals[],
rendered_record_ids, estimated_tokens, uncompiled_adjustments, skipped[]`.
`instructions` is byte-identical to the markdown minus the `## Due signals`
section. `signals[]` = `{id, subject, excerpt (without …), truncated, state,
leased_until?, meta, inspect}`.

## 8. State (spec §11.4)

`state put` validates: valid YAML (any top-level shape), byte length <=
`state_max_bytes`. `--expect` is required: `none` to create, else the current
record ID. Mismatch → exit 7 `nine-tails: expected state_17 but state_18 is
active`. Generic `put --lane state` runs the same validation but `--expect` is
optional (omitted = supersede whatever is active).

`state get` prints the body verbatim (plus one trailing newline) and writes
`nine-tails: state_18 (use --expect state_18 to replace)` to stderr;
`--format id` prints just the ID; `--format json` the envelope.

## 9. Tools (spec §13)

Body YAML:

```yaml
version: 1
description: Fetch complete changed-file contents for a pull request
exec:
  argv: ["artifacts/tool_12/complete-pr-diff.sh", "{{ pr }}"]
  stdin: none | json | text      # default none
  timeout: 30s                   # default 60s
input:
  pr: {type: string, required: true}   # type is informational; only required is enforced
output:
  format: json                   # informational
```

Validation (at `put`, `tool add`, `import`): `exec.argv` non-empty list of
non-empty strings; `exec.stdin` ∈ {none, json, text}; `exec.timeout` a Go
duration if present; `version` absent or 1; placeholders match
`^\{\{\s*([a-z0-9_.-]+)\s*\}\}$` and must each be an entire argv element (any
position, including argv[0]); every placeholder must be declared in `input`.
Unknown keys preserved. `description` is required.

`tool add <agent> <name> --script PATH --description D`: copies PATH to
`artifacts/<new-id>/<basename>`, chmod +x, body = `{version: 1, description,
exec: {argv: ["artifacts/<id>/<basename>"], stdin: json}}`. With `--stdin`
instead of `--description`, the YAML body is read from stdin, the artifact
path is prepended to its `exec.argv` (which may be absent or contain only
placeholders), `version` is filled with 1 if absent, `exec.stdin` is left as
written, and only then is the final body validated. Unreadable PATH → 2. The
artifact directory and the allocated id are rolled back if anything fails.
`tool add` is `put --lane definition --kind tool` without `--expect`
(last-writer-wins). Every literal argv element beginning with `artifacts/`
(any position, e.g. `[/bin/sh, artifacts/tool_2/x.sh]`) resolves relative to
`NINE_TAILS_HOME` at call time; substituted input values are never resolved.
`exec.timeout` must be a positive duration.

`agent add <agent> <name> --description D` = `put --lane definition --kind
related-agent --name <name> "<D>"`.

`call`: the agent is `--agent`, else the `--context`'s agent, else `shared`;
when both flags are given they must agree (else exit 2, same rule as
`state put`). Resolve agent-owned first, then `shared` honoring
`available-to`. `--input` must be exactly one JSON object (default `{}`;
trailing data → 2), decoded with `UseNumber`; `--stdin` reads it instead.
Validate `required` only. `call` has no `--format`: its stdout belongs to the
tool. A tool body that no longer parses → 4 with a repair hint. Substitute each placeholder
element with the value: strings verbatim, numbers as their JSON literal text,
booleans `true`/`false`, objects/arrays as compact JSON; an element whose
placeholder input is absent (and not required) is removed from argv. Never add
`--`. stdin: `json` = the whole input object as JSON (unknown keys forwarded);
`text` = the string value of input key `text` (empty if absent); `none` =
closed. Run with `exec.Command` in its own process group; env inherits plus
`NINE_TAILS_HOME`, `NINE_TAILS_AGENT`, and `NINE_TAILS_CONTEXT` when given.
stdout and stderr are the tool's: both stream through untouched, never
buffered or re-indented, and a failed call's summary line follows the tool's
output. Exit code passed through; cannot start / timeout → 5, and a timeout
kills the whole group. SIGINT, SIGTERM or SIGHUP received by nine-tails while
the tool runs is forwarded to the group (a second one kills it) and nine-tails
then exits 128+signal with a summary line, so Ctrl-C ends the tool exactly as
it would had the tool shared the terminal's group. What a successful tool
leaves running is its own business: a descendant that keeps stdout or stderr
open keeps the caller waiting, exactly as with any other program, so a tool
that daemonizes must redirect both.

## 10. Compilation (spec §12)

`compile-input <agent>` (default json):

```yaml
agent: pr-review
instructions: |            # built-in default compiler instructions (§12.7) + output contract
  ...
expect_generation: gen_11  # or "none"
expect_base: base_4
base: {id: base_4, body: "..."}
active_generation:         # null when none
  id: gen_11
  items: [{id: item_81, key: concise-evidence, body: "...", meta: {...}}]
input_entries: [rec_41, rec_42]         # exactly the ids in entries[]
entries:                                 # RecentGuidance(agent), oldest first
  - id: rec_41
    kind: prefer
    body: ...
    meta: {...}
    origin_context: ctx_72
    origin_context_metadata: {repo-id: [my_repo], pr: ["1842"]}
    origin_context_rendered: [base_4, item_81]   # so the compiler can judge coverage
```

Compiler output (YAML or JSON, auto-detected; keys snake or kebab):

```yaml
input_entries: [rec_41, rec_42]   # echoed unchanged
items:
  - key: concise-evidence
    body: ...
    meta: {phase: [review-comment]}   # scalar values also accepted
entries:
  - id: rec_41
    disposition: represented | deferred | superseded-by
    items: [concise-evidence]         # required iff represented
    successor: rec_50                 # required iff superseded-by
    refinement: true                  # optional hint
    equivalent_records: [item_81]     # optional: prior records judged equivalent
```

Validation (all → exit 2, each problem as a detail line): the set of
`entries[].id` equals `input_entries` exactly (no missing, extra or duplicate)
and every one is still an active lane=guidance record of the agent; every
referenced item key exists; `items` present iff `represented`; `successor`
present iff `superseded-by` and names an active lane=guidance record of the
agent; every `equivalent_records` id exists (any status); item keys are valid
names, unique, with non-empty bodies. An empty `items` list is allowed.
Entries not in `input_entries` (appended during the compile) are untouched.

Coverage, computed by nine-tails:

```
if refinement == true                       → refinement
elif equivalent_records non-empty:
    if origin_context is null               → unknown
    elif any equivalent ∈ context_records(origin) → covered-rendered
    else                                    → covered-unrendered
elif origin_context is null                 → unknown
else                                        → novel
```

Install (`brief put`), in one transaction: check `--expect-generation` is the
active generation (or `none` and there is none) and `--expect-base` is the
active base → else exit 7 naming the active one; set the prior generation's
item records to `superseded`; insert item records (lane=guidance,
kind=brief-item, name=key; a key matching a prior-generation item sets
`supersedes` to it); create the generation `staged`; write membership, inputs,
sources, equivalents; activate; supersede the prior generation. Re-emitting a
prior item key mechanically carries that item's earlier source relationships
and represented accounting into the new generation (unless the compiler
accounts for the same entry again). Dropping all of an entry's representing
item keys does not carry its accounting, so the active source becomes recent
guidance again. `superseded-by` accounting is carried across generations.
Source entries stay active. `--dry-run` validates, computes coverage and lint,
prints what would be installed, writes nothing.

Condition-loss lint (computed on demand from `brief_item_sources`; returned by
`brief put` and `inspect --lint condition-loss`):

```
for each item with ≥1 source:
  if any source has an empty meta multimap → no warning
  for each key K present on every source:
     V = intersection of the sources' value sets for K
     if V non-empty and item.meta lacks K → STRONG {item, key, values: V, sources}
  if every source has an origin context:
     for each key K present on every origin context's metadata and on no source:
        V = intersection of those value sets; if V non-empty and item lacks K → WEAK
```

Items with zero sources produce no warnings. The lint never blocks install.

`compile <agent>`: compile-input → run the compiler (`--compiler` flag, else
`NINE_TAILS_COMPILER` env, else `config.compiler.argv`; none → exit 2 with the
config snippet) with the compile-input JSON on stdin and expect the output
document on stdout → `brief put`. The compiler inherits the environment plus
`NINE_TAILS_HOME` and `NINE_TAILS_AGENT`. `compile` additionally checks that
the echoed `input_entries` equals its own document's list. Warnings go to
stderr. `instructions` comes from the `brief-compiler` agent's active base
when that agent exists, else the built-in text in
`internal/compile/instructions.go`; either way `compile-input` shows it.

Further pins: there is no `--budget`; the instructions ask for a concise
brief and nothing measures it. Metadata
keys in compiler output are validated by the §3 key rule, not the name regex.
A duplicate inside `input_entries` is a validation problem. `brief put` on a
nonexistent agent → 3. `brief put --stdin` is mandatory. `--dry-run` prints
the plan (`dry_run: true`, provisional ids, inputs with coverage) and rolls
back, so no id is consumed. Non-dry-run JSON is exactly
`{generation, items, warnings}`.

## 11. Signals (spec §15)

`signal [<agent>] ...` creates one record (lane=signal, kind=signal, body,
meta with `subject=<S>` plus user meta) and one `signal_delivery` row
(`available_at` = `--at` or now, state pending). The agent defaults to
`shared`: every agent's load renders it (subject to the conflict rule and
`available-to`), so "pass repo-id, not repo, on load" reaches whoever loads
next without naming anyone. If `(agent, dedupe-key)` exists nonterminal, print
the existing ID, write `nine-tails: deduplicated against sig_44` to stderr,
exit 0.

`tick` lists shared signals like any other, with agent `shared`. A wake-up
adapter has nothing to start for them and leaves them unclaimed; a person or
coordinator retires a stale broadcast with `tick --claim --agent shared` and
`signal ack`.

`tick`: rows with state pending, or leased with `leased_until <= now`, and
`available_at <= now`; live leases are not listed; ordered by available_at
asc, rowid asc; expired leases shown as `state: pending` with empty lease
fields. Without `--claim`: read-only. With `--claim`: in one transaction set
state=leased, lease_token=`lease_<n>`, leased_until=now+lease (default 5m).
Output: JSON array of `{id, agent, subject, body, meta, available_at, state,
lease_token, leased_until}`; `[]` when empty.

`signal --format json` prints the envelope plus `delivery` (so a caller sees
`available_at`) and `deduplicated`. `signal ack <id> --lease <token>`: leased,
unexpired, matching token → acknowledged; prints the id (`--format json`: the
envelope). Unknown id → 3; otherwise → 7. `load` includes live-leased signals
(with `state=leased` in the bracket) because awareness is not delivery.

## 12. Inspect (spec §18)

`inspect <agent>`: JSON `{agent, base, state[], brief{generation, items[],
inputs[]}, journal[], tools[], agents[], signals[]}`; `--include` restricts
sections and may add `contexts` (off by default; each is a receipt, newest
first, at most 20). `journal[]` = active guidance + recall records excluding
brief items. Any of `--query`, `--lane`, `--kind`, `--name` switches to the
flat shape `{agent, records: [envelopes]}` and `--include` is ignored;
`--query` is a case-insensitive substring over body, name and meta values.
`--all` includes superseded/disabled in either shape. In the full shape it
keeps `base` and `brief` as the active singleton values and adds the
self-contained arrays `base_history` and `brief_history`; it also includes
acknowledged signals and visible shared-tool history in their existing arrays.
`--coverage C` gives
`{agent, coverage: [{entry, disposition, coverage, items, equivalent_records}]}`
using each entry's latest brief_inputs row. `--lint condition-loss` gives
`{agent, lint: [{item, key, strength, values, sources, message}]}`.

`inspect <id>`: the record envelope with `rendered_in: [ctx ids]` (and
`delivery` for signals); `ctx_N` gives the receipt; `gen_N` gives
`{generation, items, inputs}`. A well-formed ID that does not exist → 3.

## 13. Export / import (spec §8.5)

Export (yaml):

```yaml
nine_tails_export: 1
agent: pr-review
records: [<envelope>...]          # active only unless --all; oldest first
omitted_artifacts: [tool_12]      # tools referencing artifacts/ when no --bundle
```

`--include` defaults to `base,brief,journal,state,tools,agents`. `--bundle
FILE.tar` writes `manifest.yaml` plus `artifacts/<id>/<file>`.

Import: one transaction, any validation failure → 2 and nothing written. An
import document describes exactly one agent: the top-level `agent` is the
target, and a record that names a different agent is exit 2 (edit the record
to move it; nothing is rewritten silently). Every record gets a new id;
`supersedes` and `origin_context` are cleared; `meta` gains
`imported-from=<old-id>`; lane defaults to `recall` when missing (kind to
`working-state` for the state lane); a definition or state whose name is active
in the target agent is superseded; artifacts are copied under the new id and
argv paths rewritten. A tool whose artifact the document does not carry (a
plain YAML export) is skipped with a warning and the active definition is
kept, so a re-import never replaces a working tool with a broken one. Imported brief
items are ordinary kind=brief-item guidance records outside any generation and
therefore never render (rule 4 excludes the kind) until a compile installs
them — export reports this. Contexts, generations and delivery rows are never
exported. A document body is already the stored envelope body, so import does
not apply §3 newline normalization a second time; this keeps export/import
lossless. Structured, non-scalar metadata values are rejected rather than
stringified.

## 14. Context GC (spec §9.2)

`context gc` deletes contexts where `pinned = 0`, `created_at` older than the
retention, and no active record has `origin_context_id` = that context.
Children are unaffected. Never touches records.

## 15. Harness adapters (spec §17.2)

`hooks install` writes user-scope command hooks for `SessionStart`,
`UserPromptSubmit`, and `SessionEnd`. Claude Code uses
`$CLAUDE_CONFIG_DIR/settings.json` (default `~/.claude/settings.json`) and
exec-form `command` + `args`; Codex uses `$CODEX_HOME/hooks.json` (default
`~/.codex/hooks.json`) and a safely quoted command string plus an absolute
System32 `commandWindows` when installed natively on Windows. Off-Windows
installation omits that platform-bound override, preventing a shared config
home from later resolving a cwd-controlled `powershell.exe`. These shapes
follow the current official lifecycle contracts:
<https://code.claude.com/docs/en/hooks> and
<https://learn.chatgpt.com/docs/hooks>.

The currently consumed lifecycle enums are adapter-specific. Claude accepts
`SessionStart.source` = `startup|resume|clear|compact|fork` and
`SessionEnd.reason` = `clear|resume|logout|prompt_input_exit|other`. Codex
accepts the same start sources except `fork`, while its end reason is currently
only `other`. Unknown events or enum values are rejected as an active adapter
failure; they are never decoded on the inactive path.

Installation parses JSON before changing it, preserves unknown top-level
settings and every non-owned group/handler, and atomically replaces only
handlers carrying the recognizable `nine-tails.hooks/v1` command marker.
Reinstall removes prior owned handlers before adding one current handler per
event. Uninstall removes only marked handlers; because the harness schemas
provide no legal ownership field for parent hook objects, harmless empty
parent arrays/objects are retained rather than guessing that they are ours.
A missing config is already uninstalled. Install/uninstall print the resolved
settings path. Codex's non-managed hook trust is outside nine-tails: after
install or any changed command hash the user must review and trust the entries
with `/hooks` before Codex will run them.

Replacement is platform-specific. Unix uses a same-directory rename and
retains the prior file mode. Windows uses `ReplaceFileW` for an existing
settings file, retaining its destination DACL and attributes, and write-through
`MoveFileExW` for a new file, which inherits the directory ACL. `ReplaceFileW`
always receives a unique same-directory backup name; the documented partial
failure that moves the original to that backup is restored synchronously, or
the retained recovery path is reported without deleting the original.

An installed hook means **the harness invokes a tiny gate globally**, not that
nine-tails work runs globally. `hooks run <agent> --claude|--codex` is the sole
activation surface. It verifies a loadable agent base, closes that database
connection, creates a random 256-bit capability in an ephemeral file beneath
`runtime/` (Unix mode-restricted or protected by the Windows home directory's
inherited ACL), launches the selected harness with its path/token/home in the
environment, waits as the capability's live owner, and removes it when the
child exits. Repeatable `--meta key=value` is validated with the same string
multimap parser as `load` before any config/store access. Its JSON encoding is
limited to 128 KiB at that boundary; `BeginRun` repeats the check for
programmatic callers. The marker records that ambient metadata with the owner
PID, harness, agent, creation/expiry, and session state. An atomic cross-process
lock binds the first eligible
`SessionStart.session_id`; an ordinary nested same-harness startup that merely
inherits the environment has a different session id and remains inactive. Owner
liveness, a rolling 24-hour expiry, private path/mode checks, the random token,
and exact harness/home matching reject fabricated markers and ordinarily age
out crash leftovers. PID liveness is best-effort: if the wrapper crashes but
an explicitly launched descendant retains the secret environment, rapid OS
reuse of the wrapper PID within that 24-hour window can make its marker appear
live again. That descendant remains inside the explicitly activated process
tree, but PID alone is not a process-birth proof. A live but idle run must
receive a lifecycle event at least once per 24 hours to renew its capability.

On Unix, `runtime/` and marker permissions are enforced as 0700 and 0600. Go's
Windows file modes do not expose ACL privacy, so Windows validates ordinary
non-reparse directory/file types and relies on the ACL inherited from
`NINE_TAILS_HOME`; a shared Windows home is not a supported activation
boundary. The wrapper handles foreground Ctrl-C/Ctrl-\\ without exiting or
duplicating the signal, so the child receives the terminal's group delivery
and may continue. Parent-received TERM/HUP is forwarded with a bounded grace
period, after which wrapper cleanup still runs; group delivery can also have
reached the child directly.

The inactive path is exit 0 with no stdout or stderr. It checks only the
environment and tiny capability file, before decoding hook stdin, parsing
`NINE_TAILS_NOW`, loading config, or opening SQLite. Admitted lifecycle behavior
is deliberately narrow:

1. The first eligible `SessionStart` binds the session and is silent. A fresh
   wrapper around a resumed harness also waits for a real prompt.
2. The first `UserPromptSubmit` in an episode performs a fresh capsule load,
   using the exact submitted `prompt` as the receipt task and the latest run
   context as parent. The wrapper's metadata is supplied as explicit ambient
   metadata on every such load, so normal filtering/ranking and the resulting
   receipt use it; multi-values retain their order. An atomic, expiring load
   claim prevents concurrent hook deliveries from creating duplicate receipts
   and is released on failure. It emits the harness's JSON `additionalContext`
   response and caches that Markdown plus its context id in the private run
   file. Each adapter has one hard transport ceiling (`CapsuleMaxBytes`):
   9,800 bytes for Claude, whose 10,000-character hook output otherwise spills
   to a file preview, and 140 KiB for Codex's 1 MiB marker. A capsule over the
   ceiling is not recorded (§7); the hook injects a pointer to an in-session
   load and to `compile` instead. Every write limits the non-capsule envelope to 192
   KiB and the complete encoded marker to 1 MiB, so even the cache's worst-case
   Go JSON escaping remains readable; an over-limit lifecycle field or update
   fails before atomic replacement and leaves the prior marker intact.
3. Later prompts in the same episode are silent. `SessionStart` with
   `source=compact` re-emits the cached capsule without opening the store or
   creating another receipt. A resume re-emits it only when the same live run
   already has a cache.
4. `clear` starts a new episode and waits for its next real prompt; the prior
   context id remains only as that next receipt's parent. Claude `SessionEnd`
   reasons `clear` and `resume` permit exactly their matching next source to
   bind; other ends revoke. Codex has no transitional end reason, so a
   cross-session-id `SessionStart` with `clear` or `resume` rebinds directly;
   `clear` resets the episode and `resume` may replay only the live cache.
5. Delegation is a convention, not a hook. The pilot begins a child's task
   with the load the child must run first (`nine-tails load <agent> --task
   "..." --context ctx_N`, task text on the following lines); the child runs
   it and works from its own receipt under the parent's. No event is
   installed for the native subagent launch. A `PreToolUse` rewrite that
   hands the child its capsule before it exists was built and verified
   (branch `nt-delegate`): Claude Code 2.1.260 delivers it, validating
   `updatedInput` as the whole tool input rather than a merge; Codex 0.153.2
   never emits `PreToolUse` for `spawn_agent` (openai/codex issue 20204). One
   working harness plus one inert adapter is a per-harness carve-out, which
   this design refuses; the branch waits until both harnesses host the same
   hook.

Codex's cross-id transition is the narrow limit of session-id-only isolation:
its hook input has no process identity that distinguishes a root clear/resume
from the same transition in a nested Codex process that inherited the wrapper
environment. Normal nested startup/prompt/end events remain inert, but a
nested cross-id clear/resume could claim the live capability. The activation
scope is therefore the explicit wrapper's process tree, not a proof of one OS
process. Do not nest Codex inside an active wrapper when that distinction is a
security boundary.

Adapters never read `transcript_path`, capture tool traffic, contact a network
service, start a daemon, or trigger reflection. Reflection remains an explicit
choice at a meaningful episode boundary. Every active dispatch failure maps
to adapter exit 5, never blocking exit 2, and therefore fails open under both
harnesses' command-hook rules; inactive or mismatched sessions stay
byte-silent. The wrapper streams the harness's stdio, returns a child's normal
exit status (or shell-conventional `128 + signal` on Unix), and reports
install, uninstall, launch, and cleanup failures as
external-adapter exit 5. Agent/config/store failures found before launch retain
their ordinary CLI code.

On Windows the wrapper resolves the harness first: `.exe` targets execute
directly, while `.cmd`/`.bat` shims are rejected as adapter exit 5 with a native
`.exe` installation hint. Windows PowerShell 5.1 remarshal through `cmd.exe`
cannot preserve arbitrary argv safely, so the wrapper does not pretend batch
launch is equivalent to `exec` form. Codex's installed `commandWindows` is a
different path: it invokes the native nine-tails executable through an
explicit noninteractive UTF-16LE-encoded PowerShell command resolved beneath
the real System32 directory.

The portable state lock is an atomic directory and has no stale-owner stealing.
A hook killed during its millisecond-scale critical section can leave the
`.lock` directory behind; subsequent events time out inactive and wrapper
cleanup can report exit 5 until the stale lock is removed. Expiry and
best-effort owner liveness still keep the capability marker from admitting an
unrelated process.

## 16. Testing

- `internal/*`: Go unit tests on `t.TempDir()`.
- `cmd/nine-tails/cli_test.go`: in-process harness with a temp home and a fixed
  clock; `cmd/nine-tails/ac_test.go` holds `TestAC01`…`TestAC20`, one per spec
  §21 criterion, each a black-box CLI scenario. AC19 injects a corrupt tool
  body via `internal/store`. AC20 runs N goroutines each opening its own store
  and installing a generation; exactly one is active afterward.
- `make test`, `make build` (→ `./bin/nine-tails`), `make install`.
- Harness tests set `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, and
  `NINE_TAILS_HOME` to `t.TempDir()` fixtures and feed simulated lifecycle JSON;
  they never install into or invoke the user's real harness.

## 17. Deliberately not built in v0

Recurring schedules, automatic compile thresholds, tool-call telemetry,
embeddings, a daemon, a TUI, colored output, per-agent permissions, any notion
of approval. The reflector and `brief-compiler` agents are content, not code:
they are created with `base` (see README) and are part of dogfooding.
