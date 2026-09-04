<p align="center">
  <img src="docs/nine-tails-banner.png" alt="nine-tails — persistent context for coding agents" width="960">
</p>

# nine-tails

[![CI](https://github.com/scottmeyer/nine-tails/actions/workflows/ci.yml/badge.svg)](https://github.com/scottmeyer/nine-tails/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/scottmeyer/nine-tails)](https://github.com/scottmeyer/nine-tails/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/scottmeyer/nine-tails)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Persistent, inspectable context for coding agents—without becoming an agent
framework.

`nine-tails` is a small CLI sidecar that resolves a named agent into a Markdown
context capsule. As the agent works, it can record corrections, useful
experience, working state, executable tools, and future signals. The next
invocation starts better informed.

It is deliberately harness-independent. It does not run an agent loop, choose
a model, proxy prompts, or require a daemon or network service.

```text
base + compiled brief + recent adjustments + state + due signals
                              │
                       nine-tails load
                              ▼
                    context capsule + receipt
                              │
                         agent session
                              ▼
              corrections, state, tools, and signals
```

## Why nine-tails?

Agent instruction files are good at stable project rules, but poor at carrying
what an agent learned yesterday. Transcripts contain that history, but they are
large, opaque, and tied to a harness. `nine-tails` keeps the durable parts in a
small local store with a plain-text interface an agent can inspect and repair.

- **Context capsules:** load only the definition, brief, adjustments, state,
  tools, and signals relevant to this invocation.
- **Corrections that take effect immediately:** append a preference or warning
  now; it appears on the next load without waiting for compilation.
- **Bounded working state:** update small YAML documents with compare-and-swap
  protection.
- **Selective memory:** keep durable facts, reminders, and executable
  capabilities without saving every turn or raw tool output.
- **Inspectable history:** records are immutable and exportable; superseded or
  disabled versions remain available for diagnosis and repair.
- **Portable integration:** use the CLI from any harness, or opt into the
  included Claude Code and Codex lifecycle adapters.

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
embedded in the binary. The returned Markdown contains a marker such as
`[nine-tails-context=ctx_01M1PJF95JW91BHBS7QWHBAP8W]`. Context IDs are opaque:
keep the exact value and pair it with the agent that returned it. IDs printed by
mutations, such as `base_...` or `rec_...`, are not context IDs.

Create a specialized agent with a base definition:

```sh
nine-tails base pr-review --expect none \
  --meta title="PR Review Agent" --stdin <<'EOF'
## Purpose

Review proposed changes for demonstrable correctness and regression risks.
EOF

nine-tails load pr-review \
  --task "Review PR 1842" \
  --context <pilot-context-id>
```

That second load returns a different receipt. Use the `pr-review` receipt—not
the earlier `pilot` receipt—for calls and corrections made while reviewing.

Teach it while it works:

```sh
nine-tails prefer --context <pr-review-context-id> \
  "Lead with concrete evidence; keep prose concise."

nine-tails avoid --context <pr-review-context-id> --meta repo-id=my-project \
  "Editing generated mocks."
```

The next `load` includes those adjustments. Pass metadata on a write only when
the knowledge should be scoped by that metadata; otherwise it remains useful
wherever the agent runs. To repair a wrong scope without editing history, pass
the active record to `--supersedes`; omit new text to keep its body and provide
the exact replacement scope with `--meta`. To retire an obsolete active record
without a replacement, use `disable <record-id>`; it remains inspectable by ID
and under its agent's `inspect --all` history. Retiring compiled guidance
invalidates that brief generation as an inseparable cache; surviving sources
appear as recent guidance until the next compile.

## What should be persisted?

| Need | Command | Result |
| --- | --- | --- |
| Operating guidance | `note`, `prefer`, `avoid` | Appears in recent adjustments and can later be compiled |
| A fact worth retrieving later | `remember` | Stays in the recall lane and is searchable with `inspect --query` |
| Small current working state | `state put` | Replaces a named YAML state using compare-and-swap |
| A reminder or external event | `signal` | Appears when due and can be leased by a scheduler |
| A reusable executable capability | `tool add` | Adds a validated named tool callable through a context |
| A durable shorter brief | `compile` | Condenses eligible journal records through a configured model command |
| Retire an obsolete record | `disable` | Stops loading, compiling, or calling it without deleting history |

Examples:

```sh
# Compare-and-swap working state.
nine-tails state put pr-review/working --expect none --stdin <<'EOF'
status: waiting
waiting-on: ci
next-action: recheck the goroutine finding
EOF

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
nine-tails call --context <pr-review-context-id> complete-pr-diff \
  --input '{"pr": 1842}'
```

Use `inspect` as the repair surface:

```sh
nine-tails inspect pr-review --include base,brief,journal
nine-tails inspect <pr-review-context-id>
nine-tails inspect pr-review --lane recall --query "generated mocks"
```

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

## Compiling the journal

Recent adjustments remain visible immediately. When they grow large,
`compile` can turn them into a durable brief through any command that reads the
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
