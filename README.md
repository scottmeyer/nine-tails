<p align="center">
  <img src="docs/nine-tails-banner.png" alt="nine-tails — persistent context for agents" width="960">
</p>

# nine-tails

[![CI](https://github.com/scottmeyer/nine-tails/actions/workflows/ci.yml/badge.svg)](https://github.com/scottmeyer/nine-tails/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/scottmeyer/nine-tails)](https://github.com/scottmeyer/nine-tails/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/scottmeyer/nine-tails)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Persistent, inspectable identities and experience for agents.

`nine-tails` is a small CLI sidecar that resolves a named agent into a Markdown
context capsule. As the agent works, it can record corrections, useful
experience, working state, executable tools, and future signals. The next
invocation starts better informed.

An agent can be a coder, game designer, architect, writer, artist, or companion.
Its base describes the role; using and teaching it builds the durable guidance
and experience supplied on later loads. The harness provides the conversation,
execution, model choice, and subagents.

It is deliberately harness-independent. It does not run an agent loop, choose
a model, proxy prompts, or require a daemon or network service.

```text
base + guidance + relevant recall + state + tools + due signals
                              │
                       nine-tails load
                              ▼
                    context capsule + receipt
                              │
                         agent session
                              ▼
              corrections, experience, state, and capabilities
```

## Why nine-tails?

Agent instruction files are good at stable project rules, but poor at carrying
what an agent learned yesterday. Transcripts contain that history, but they are
large, opaque, and tied to a harness. `nine-tails` keeps the durable parts in a
small local store with a plain-text interface an agent can inspect and repair.

- **Context capsules:** load the role, applicable guidance, state, tools, and
  signals, plus a bounded set of task-relevant recall excerpts.
- **Corrections that take effect immediately:** append a preference or warning
  now; it appears on the next load without waiting for compilation.
- **Bounded working state:** update small YAML documents with compare-and-swap
  protection.
- **Selective memory:** keep durable facts, reminders, and executable
  capabilities without saving every turn or raw tool output.
- **Inspectable history:** records are immutable and exportable; superseded or
  disabled versions remain available for diagnosis and repair.
- **Portable integration:** use the CLI from any harness, or opt into the
  [stdio MCP transport](docs/mcp.md) or included lifecycle adapters.

## Install

Install the latest release with Homebrew on macOS or Linux:

```sh
brew install --cask scottmeyer/tap/nine-tails
nine-tails --version
```

Prebuilt archives are available from
[GitHub Releases](https://github.com/scottmeyer/nine-tails/releases/latest):

| Platform | Architectures | Archive |
| --- | --- | --- |
| macOS | `arm64`, `x86_64` | `.tar.gz` |
| Linux | `arm64`, `x86_64` | `.tar.gz` |
| Windows | `arm64`, `x86_64` | `.zip` |

Each release includes SHA-256 checksums. macOS binaries are Developer ID signed
and notarized by Apple before publication.

To install from source, use Go 1.26 or newer:

```sh
go install github.com/scottmeyer/nine-tails/cmd/nine-tails@latest
```

From a checkout:

```sh
make build      # writes ./bin/nine-tails
make install    # writes $(go env GOPATH)/bin/nine-tails
```

## Quick start

When no capsule has already been injected and no agent was explicitly named,
start with `pilot`, the built-in usage guide and agent catalog:

```sh
nine-tails load pilot \
  --task "Help me add authentication" \
  --meta repo-id=my-project \
  --meta harness=my-harness
```

On a fresh store, that command seeds `pilot` and `reflector` from documents
embedded in the binary. Each load shows a short local reference such as `@42`,
paired with its agent. Use that handle for calls, corrections and delegation.
The original `[nine-tails-context=ctx_...]` marker remains for harnesses.
A reference retains its kind: a record handle cannot stand in for a receipt.

```sh
nine-tails refs --kind context
nine-tails refs --meta repo-id=soccer-chess
nine-tails refs --kind signal --query "follow-up"
nine-tails inspect @42
nine-tails prefer --context @42 "Lead with concrete examples."
```

The numbers above are examples; use the references listed in your store.
References stay assigned to one item, even after context cleanup. They are
local: use canonical IDs when exchanging data between stores. `refs --format
json` includes both. Existing commands and mutation output retain canonical
IDs for compatibility.

When you already know the agent, load it directly with `load --agent <name>`
or `load <name>`. Every capsule includes the learning, tool, state, and
delegation protocol; a prior pilot load is unnecessary.

Create a specialized agent with a base definition:

```sh
nine-tails base pr-review --expect none \
  --meta title="PR Review Agent" --stdin <<'EOF'
## Purpose

Review proposed changes for demonstrable correctness and regression risks.
EOF

nine-tails load --agent pr-review \
  --task "Review PR 1842" \
  --meta repo-id=my-project \
  --meta harness=my-harness
```

Use the returned `pr-review` receipt for calls and corrections made while
reviewing. A delegated child load can add `--context <parent-context-id>` to
inherit metadata and link its work to the parent.
Explicit child `--meta` keys replace their inherited values; other keys stay.
For example, `--context <framework-context-id> --meta repo-id=soccer-chess`
switches project scope while retaining the harness. Repeat an explicit key
to deliberately select several values.

Teach it while it works:

```sh
nine-tails prefer --context <pr-review-context-id> \
  "Lead with concrete evidence; keep prose concise."

nine-tails avoid --context <pr-review-context-id> --meta repo-id=my-project \
  "Editing generated mocks."
```

The next `load` includes those adjustments. Pass metadata on a write only when
the knowledge should be scoped by that metadata; otherwise it remains useful
wherever the agent runs. Correct an active record with `--supersedes`; omitting
`--meta` preserves its complete scope. To repair a wrong scope, provide the
exact replacement set with `--meta`, or use `--clear-meta` to remove all scope.
The two flags are mutually exclusive. Omit new text to keep the prior body.
New records never inherit ambient context metadata. To retire an obsolete active record
without a replacement, use `disable <record-id>`; it remains inspectable by ID
and under its agent's `inspect --all` history. Retiring compiled guidance
invalidates that brief generation as an inseparable cache; surviving sources
appear immediately as recent guidance. Replacing a compiled source with changed
text, instruction kind, or applicability scope does the same: obsolete compiled advice disappears on the next load,
without waiting for a compiler. Recompile when useful to compact the surviving
notes, not to make the correction take effect.

Capture explicit durable corrections during work. At a meaningful pause,
briefly consider whether anything should carry forward. Supported reusable
lessons become guidance; useful experience or uncertainty can be saved with
`remember`. Zero writes is valid, including a playful companion session.
Reflection can happen inline. The optional `reflector` helps with difficult
reconciliation. `nine-tails close <context-id>` is optional bookkeeping and
needs no scoring exercise; unlisted records default to `?`.

The optional [workshop catalog](agents/workshop/README.md) supplies a coordinator,
an `architect`, framework roles, and a game team: `game.designer`,
`game.engineer`, and `game.playtester`. Follow its installation instructions
to add absent roles while preserving personalized agents already in your
store. [The workshop cycle](docs/workshop-cycle.md) describes collaboration and
the Soccer Chess work used to identify friction in ordinary agent handoffs.

When an old phrase finds a superseded lesson, `inspect @N` preserves its
historical text and shows the current replacement separately, including its
short reference and scope. A stale write stays a conflict and points you to
that replacement. Recall shows when an observation was recorded so earlier
conditions are not mistaken for current project state.

[The context graph direction](docs/context-graph.md) describes how these
relationships support fresh context projections. [This friction review](docs/friction-2026-09-06.md)
records the concrete changes and remaining limits.

## What should be persisted?

| Need | Command | Result |
| --- | --- | --- |
| Operating guidance | `note`, `prefer`, `avoid` | Appears in recent adjustments and can later be compiled |
| Useful experience or a fact | `remember` | Relevant excerpts surface on load as data; full records remain searchable with `inspect --query` |
| Small current working state | `state put` | Replaces a named YAML state using compare-and-swap |
| Shared state in a role's next capsule | `state link` | Subscribes to current named state without copying its values |
| A reminder or external event | `signal` | Appears when due and can be leased by a scheduler |
| A reusable executable capability | `tool add` | Adds a validated named tool callable through a context |
| Optional shorter guidance | `compile` | Condenses eligible guidance through a configured model command; original lessons remain authoritative |
| Retire an obsolete record | `disable` | Stops loading, compiling, or calling it without deleting history |

Examples:

```sh
# Compare-and-swap working state.
nine-tails state put pr-review/working --context <pr-review-context-id> \
  --expect none --meta repo-id=my-project --stdin <<'EOF'
status: waiting
waiting-on: ci
next-action: recheck the goroutine finding
EOF

# Read current state and its CAS id before updating it.
nine-tails state get pr-review/working
# Then state put with --expect <current-state-id>; omitted --meta preserves scope.

# Subscribe a role to one authoritative shared project state.
nine-tails state link game.engineer/project workshop/soccer-chess \
  --expect none --meta repo-id=soccer-chess
nine-tails load game.engineer --meta repo-id=soccer-chess

# Schedule work without running a scheduler inside nine-tails.
nine-tails signal pr-review --at +2h \
  --subject "Recheck PR 1842 after CI" \
  --dedupe-key my-project:pr-1842:recheck-ci
nine-tails tick --claim --lease 5m

# Register a script as a named tool, then call it through a loaded context.
nine-tails tool add pr-review complete-pr-diff \
  --script ./complete-pr-diff.sh \
  --description "Fetch complete changed-file contents for a pull request" \
  --context <pr-review-context-id>
nine-tails inspect pr-review --include tools
nine-tails call --context <pr-review-context-id> complete-pr-diff \
  --input '{"pr": 1842}'
```

State links resolve one hop on each load. Both the link's scope and the target's
scope must apply. The capsule shows the current state once as data with its
owner and version; updates by the owner appear on future loads. Missing targets
produce a repair hint. Replace a link using its ID as `--expect`, or retire it
with `disable`; neither operation changes the target. Link metadata follows
definition rules: pass its complete scope when replacing it, because omission
means unqualified. Exporting a role includes its links without copying another
agent's state values.

Use `inspect` as the repair surface:

```sh
nine-tails inspect pr-review --include base,brief,journal
nine-tails inspect <pr-review-context-id>
nine-tails inspect pr-review --lane recall --query "generated mocks"
```

`load` uses the concise `--task` to retrieve up to three matching recall
excerpts, capped at 360 characters each. The lexical search respects metadata
conflicts and labels results as data, with record IDs, truncation notices, and
inspection paths. Guidance is never shortened or evicted to make room.
Override retrieval with `--query "generated mocks"`, or disable it for a load
with `--query ""`. This matches words rather than inferring synonyms; use
`inspect` or a different query when you need other evidence.

Data goes to stdout and diagnostics to stderr. Core data commands are
non-interactive; `hooks run` is the explicit interactive supervisor. Commands
that create one record print its new ID by default; each command documents its
structured output and exceptions. With `--format json`, errors also appear on
stdout as `{"error":"...","code":N}`. `call` is the exception because its
stdout belongs to the invoked tool.

## Agent-harness integration

The portable integration is a short protocol in `AGENTS.md`, `CLAUDE.md`, or
the equivalent instruction file. Replace the repository placeholder with a
literal, stable value before committing it:

```md
Use nine-tails for persistent agent context. If this episode already contains
a `[nine-tails-context=...]` capsule, follow it and do not load it again.
Otherwise, load an explicitly requested agent with
`nine-tails load <agent> --task "<concise non-sensitive purpose>" --meta repo-id=<literal-repo-id> --meta harness=<actual-harness>`.
When no agent was named, load `pilot` the same way and select only an agent it
advertises—or a checked-in role explicitly authorized by the repository
instructions—using `--context <pilot-receipt>` for the child load. Keep each
receipt paired with its agent. The `--task` label is stored on the receipt, so
keep the complete task in the harness conversation. Never write secrets,
authorization, raw external content, or task-only instructions to records,
state, signals, or tools.
```

For a delegated task, put the child load command on the first line of the
child's task, using a concise non-sensitive purpose, and put the complete task
on the following lines. The child runs it and returns its new receipt. This
works with any harness that can invoke a CLI; no special subagent type is
required.

The optional [MCP adapter](docs/mcp.md) gives a connected host a stable native
tool menu for loading, learning, inspection, state, and tool execution.
`nt_tools` discovers the current agent's executable capabilities, and
`nt_call` invokes them with an explicit receipt. Concurrent agents keep their
own receipt identities. Configuring a host to launch `nine-tails mcp` is optional;
the CLI supports the same work.

Claude Code and Codex can also receive capsules through opt-in lifecycle
adapters:

```sh
# Install the merge-preserving adapter once.
nine-tails hooks install --claude
nine-tails hooks install --codex

# Explicitly activate it for one harness process tree. The selected harness
# name is added to metadata automatically.
nine-tails hooks run pilot --meta repo-id=my-project --claude
nine-tails hooks run pr-review --meta repo-id=my-project --codex -- --model MODEL

# Remove only entries owned by nine-tails.
nine-tails hooks uninstall --claude
nine-tails hooks uninstall --codex
```

`hooks run` first verifies that the selected adapter is installed and that its
agent can be loaded; `pilot` is seeded when necessary. Installation alone does
not make ordinary sessions use `nine-tails`. The hook gate stays silent and
does not open the store unless the session inherits a live capability created
by `hooks run`. Codex asks the user to review newly installed hooks in `/hooks`
before trusting them. Native hook mode persists the first submitted prompt as
the context receipt task; use a manual load with a concise purpose instead when
that prompt contains secrets or raw external content.

The adapters inject a fresh capsule at the first real prompt, replay the same
capsule after compaction, and create a new parent-linked receipt after a clear.
They do not read transcripts or trigger unconditional reflection. Exact
lifecycle, size-limit, process, and platform behavior is pinned in
[DESIGN.md](DESIGN.md#15-harness-adapters-spec-172).

## Storage and scope

By default, data lives under `~/.nine-tails`:

```text
~/.nine-tails/
├── nine-tails.db
├── artifacts/
├── exports/
├── runtime/
└── config.yaml       # optional
```

Set `NINE_TAILS_HOME` to move it. There is one store per user—not one store per
repository or worktree. A repository is ambient metadata supplied on load:

```sh
nine-tails load builder \
  --task "Implement the auth endpoint" \
  --meta repo-id=my-project \
  --meta track=auth
```

The long-lived agent is `builder`; this particular invocation is its context
receipt. Two concurrent builders remain one agent so their learnings can roll
up together, while `repo-id`, `track`, or other invocation metadata can target
relevant context and signals. Do not encode sessions in names such as
`builder@session`.

To move an agent to another machine or share it with someone else, use
`export --bundle` and `import`. No repository-aware synchronization is hidden
inside the binary.

## Optional condensation

Recent adjustments remain visible immediately. When they grow large,
`compile` can condense them into a cached brief through any command that reads the
compile document on stdin and writes the result on stdout:

```yaml
# ~/.nine-tails/config.yaml
compiler:
  argv: ["my-model-command", "--noninteractive"]
  timeout: 300s
```

```sh
nine-tails compile pr-review
```

For a manual or custom-model workflow, use `compile-input` followed by
`brief put`. Compiler output is validated for complete dispositions and
installed with compare-and-swap protection.

Original lessons remain authoritative. Compiler input includes the original
source bodies behind existing summaries. If a compiled representation is
inapplicable or cannot render on a load, the eligible source guidance returns
in full. Compilation is never a checkpoint required to activate learning.

## State and handoffs

State updates preserve existing metadata when `--meta` is omitted. Set scope
explicitly on creation; `--context` does not copy it. On update, `--meta`
replaces the complete set and `--clear-meta` explicitly removes it. Bundle
imports remain exact snapshots, including their metadata.

Both `state get` and `state put` accept a qualified `agent/name`, or a bare
name with `--context ctx_...`. The receipt identifies the agent, not a past
state version; an explicit agent must match that receipt's owner.

Keep changing project decisions in one named, scoped state; give other roles
scoped `state link` subscriptions so fresh loads receive the current version. Keep
bases project-neutral and check applicability before carrying a previous
project's decisions into a new one. Capsules display resolved metadata and
state CAS recipes so a handoff need not start with help lookups.
New starter guidance teaches this convention; upgrading the binary does not
overwrite your existing pilot or reflector.

## Project status and development

`nine-tails` currently implements the v0.3 sidecar specification. The
behavioral contract is [lore-sidecar-spec-v0.3.md](lore-sidecar-spec-v0.3.md),
and implementation decisions are binding in [DESIGN.md](DESIGN.md).

```sh
make test
make vet
make build
```

Tests use isolated temporary homes and never touch `~/.nine-tails`.

The repo-owned dogfood agents live in [`agents/`](agents/README.md). Their
names are repository-qualified so importing them cannot silently substitute a
personal `builder` or `reviewer` from the user-wide store.

Maintainers should follow the [release guide](docs/releasing.md) for tagging,
signing, notarization, Homebrew publishing, and release verification.

## License

MIT © 2026 Scott Meyer. See [LICENSE](LICENSE).
