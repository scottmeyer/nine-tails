# Lore: A Model-Native Agent Sidecar

**Status:** Discussion Draft  
**Version:** 0.3  
**Date:** 2026-09-04  
**Working name:** `lore`

## Abstract

Lore is a small, harness-independent CLI sidecar for persistent agent context.
It does not run an agent loop, select a model, manage a conversation, or enforce
agent behavior. It resolves a named agent into a context capsule,
records corrections and useful experience, carries a small versioned working
state, exposes named tools backed by arbitrary executables, and carries
reminders or external signals into future invocations.

Lore is designed around a simple premise: language models are already good at
interpreting loosely structured text. The sidecar should therefore impose
strict structure only where ordinary software must act mechanically—storage,
execution, timing, identity, context snapshots, and context limits. Meaning
remains in text, lightweight metadata, and embedded YAML that an agent can
inspect and repair.

The intended human interface is an agent conversation. Lore's direct interface
is primarily for agents and harness adapters. A user should be able to say,
"inspect the PR review agent and make its comments more concise," and let an
agent inspect and modify Lore rather than maintain a separate configuration UI.

## 1. Motivation

Agent harnesses usually begin with a static definition: a role, a collection of
instructions, and a set of tools. In practice, users repeatedly refine that
definition through small corrections:

- "More concise, with evidence."
- "Do not edit generated mocks in this repository."
- "Check whether the GitHub patch is truncated before reviewing it."
- "Use the existing script instead of rebuilding this query."
- "Recheck this after CI finishes."

It is often easier to correct the current draft and accept the revision than to
stop, find the agent definition, generalize the correction, edit the right
file, and decide where it belongs. Consequently, useful corrections remain
trapped in individual context windows and must be repeated.

Lore captures those corrections with almost no ceremony. Recent entries affect
the next invocation immediately. A model periodically compacts accumulated
entries into a small working brief. The system remains intentionally imperfect:
the resulting context steers the agent but does not guarantee compliance.

Lore also treats named agents as small context modules rather than processes.
One agent may load another agent's capsule for a narrow task. A capable harness
may place that capsule in a subagent; a simpler harness may apply it within the
current context. Lore does not distinguish between those execution strategies.

## 2. Design Thesis

Lore is best understood as an agent's small persistent home:

| Component | Purpose |
| --- | --- |
| Base | Manually established purpose and durable instructions |
| Brief | Metadata-bearing compiled items from accumulated experience |
| State | Small versioned snapshot of what is true now |
| Journal | Recent corrections, observations, and loose memory |
| Toolbox | Named scripts and executable indirections |
| Agent catalog | Other context capsules available for narrow work |
| Inbox | Reminders, scheduled items, and external signals |

At invocation time, Lore assembles these into a context capsule:

```text
base instructions
+ current state
+ eligible items from the active brief generation
+ recent unrepresented guidance
+ relevant tools and agents
+ currently due signals
```

The harness supplies that capsule to a model. The model and harness remain
responsible for interpreting and acting on it.

## 3. Goals

Lore should:

1. Reduce commonly repeated user corrections across agent invocations and
   measure whether those corrections were previously covered.
2. Resolve a short agent name into a useful context capsule whose size is
   reported, never cut.
3. Allow agents to be small and composable without requiring a particular
   harness implementation.
4. Let agents add memories, preferences, scripts, and reminders using a few
   stable CLI operations.
5. Support arbitrary, model-readable scope metadata without requiring a domain
   schema or migrations.
6. Allow a frontier model to periodically compile raw entries into a concise
   brief.
7. Provide stable semantic tool names whose implementations may be any local
   executable or adapter.
8. Make the complete system inspectable and repairable by an ordinary agent
   session.
9. Remain useful with SQLite, a local directory, and no resident service.
10. Degrade cleanly when a harness lacks subagents, scheduling, or native
    context injection.
11. Record exactly which context items were emitted without confusing the
    origin of a correction with its future scope.
12. Carry small model-managed working state across invocations without
    conflating current truth, historical memory, or behavioral guidance.
13. Support selective reflection using ordinary agents and existing write
    operations rather than embedding another agent loop.

## 4. Non-Goals

Lore is not:

- An agent loop or autonomous runtime.
- A workflow engine.
- A model router.
- A replacement for system or developer instructions.
- A policy enforcement or compliance system.
- An authorization, sandboxing, or prompt-injection boundary.
- A provenance, approval, or governance system.
- A human-facing agent management dashboard.
- A vector database or mandatory semantic retrieval system.
- A model training or weight-update system.
- A guarantee that an agent will follow loaded guidance.

The invoking harness and its existing protections remain the operational and
security boundary. Lore stores and returns context and may dispatch configured
executables; it does not make those executables safe.

## 5. Design Principles

### 5.1 Eat its own dogfood

An agent must be able to inspect, explain, and repair Lore state through the
same CLI exposed to other agents. If a normal frontier model cannot understand
and correct a feature from its textual representation, that feature is
presumptively too complicated for Lore.

### 5.2 Keep mechanics rigid and meaning fluid

Software must parse timestamps, locate files, execute argument vectors, update
records, and enforce token limits. Those fields are structured.

Semantic concepts such as repository, task phase, audience, communication
style, or error condition remain arbitrary metadata and text:

```text
repo-id=my_repo
language=go
phase=review
preference=concise-evidence
```

Lore does not require an ontology explaining those keys.

### 5.3 Record history mechanically; infer meaning semantically

Lore should not ask a model to reconstruct facts it can record cheaply. Context
snapshots, record versions, active brief generations, compilation inputs, and
signal leases are mechanical history. Whether two instructions mean the same
thing, whether a correction is general, and how several notes should be
condensed remain inference tasks.

A context is a receipt for what the model saw. It is not a declaration of where
future guidance applies.

### 5.4 The agent is the human interface

Lore should expose machine-readable inspection and mutation, not a second
human workflow. Users manage Lore by instructing an agent:

> Inspect the PR review agent. Explain why it is overexplaining comments, then
> correct the brief.

The agent performs the spelunking and presents a human-sized report.

### 5.5 A named agent is a context capsule

Loading an agent does not imply creating a process or model call. It produces
context. The harness decides whether that context is:

- Applied inline in the current conversation.
- Given to a fresh subagent.
- Passed to another external harness invocation.
- Saved for later scheduled invocation.

### 5.6 Prefer append and compile over configuration surgery

A small correction should be appendable immediately. Semantic cleanup happens
later through model compilation rather than through an elaborate rule editor.

### 5.7 Prefer names and indirection

Agents should call `recall-memory`, not depend on the current memory vendor or
query implementation. Named tools allow implementations to change without
rewriting every agent capsule.

### 5.8 Assume the corpus stays small

Start with direct lookup, exact metadata overlap, recency, and model inference.
Do not introduce embeddings, ranking services, or specialized indexes until
the observed corpus requires them. Better retrieval can later be installed
behind a tool such as `recall-memory` without changing Lore's core.

### 5.9 Failure should remain cheap

Compilation may be imperfect. Metadata may be inconsistent. An agent may fail
to use a relevant tool. These should be correctable by appending a note,
editing text, or recompiling—not by repairing a complex control system.

## 6. Terminology

### Agent

A named collection of base instructions, compiled guidance, current state,
recent entries, available tools, related agents, and signals. An agent is not
necessarily a running process.

### Context capsule

The bounded text and structured receipt returned by `lore load`. The receipt
identifies the ambient invocation metadata and every record actually rendered.

### Record

An immutable persisted semantic unit in Lore. Updating text, YAML, metadata, or
artifact content creates a new record and supersedes the old record.

### Entry

A journal-like record containing a correction, preference, observation,
discovery, or other model-readable text.

### Lane

A small mechanical category controlling how Lore treats a record. Guidance is
rendered and compiled; recall material is retrieved on demand; definitions are
resolved by name; state is loaded as a current snapshot; signals follow
availability and delivery mechanics. `kind` remains open-ended within a lane.

### Brief

A generation of compact, independently selectable model-generated items. Brief
items retain metadata and source-entry relationships. A brief generation is a
cache of semantic work, not the source of truth.

### State

A small, named, model-readable YAML snapshot of what is true now for an agent.
State is loaded directly, never compiled, and replaced through immutable
versioning and compare-and-swap.

### Reflection

A selectively invoked agent behavior that reviews a significant episode and
decides whether to update state, guidance, recall memory, signals, tools, or
nothing. Reflection is not a Lore-owned loop or mandatory per-action log.

### Tool

A named executable indirection. A tool usually resolves to an argument vector,
script, or adapter and may accept structured input.

### Related agent

Another named context capsule advertised as available for a narrower task.

### Signal

A signal that may become available immediately or at a future time,
optionally addressed to one agent. A signal is not a message: an unaddressed
one is seen by every agent that loads, and metadata scopes it. Signals cover
reminders, scheduled work, and external events.

### Compiler

A model-backed operation that rewrites journal material into a bounded brief.
The compiler may be an API call, a harness command, or another Lore agent.

### Context

An immutable receipt of one `load`: agent, task, ambient metadata, estimated
size, parent context, and the exact record IDs emitted. Contexts provide inheritance
for nested operations and evidence for repeated-correction measurement.

### Harness adapter

Thin integration code that decides how to place a context capsule, invoke a
subagent, deliver due signals, or call a model for compilation.

## 7. System Boundary

Lore owns:

- Persistent records and artifact references.
- Agent and tool name resolution.
- Context assembly and size reporting.
- Immutable state resolution and replacement.
- Journal append and inspection.
- Tool definition lookup and optional executable dispatch.
- Signal persistence and due-time lookup.
- Compiler input/output contracts.

The harness owns:

- Model selection and inference.
- Conversation and agent loops.
- Placement and priority of returned context.
- Tool permissions and process isolation.
- Inline versus subagent execution.
- Deciding whether and when an episode merits reflection.
- External credentials and authorization.
- Deciding when an agent has completed a task.

Lore output is ordinary context. Calling it a "top-level agent instruction" is
reasonable at the product level, but it does not acquire system-message
priority unless the harness explicitly gives it that placement.

## 8. Persistent Model

### 8.1 Generic record envelope

All semantically meaningful state should fit within this logical envelope:

```yaml
id: rec_01K4...
agent: pr-review
lane: guidance
kind: note
name: null
body: Generated mocks in this repository must not be edited directly.
created-at: 2026-09-04T16:30:00Z
origin-context: ctx_01K4...
status: active
supersedes: null
meta:
  repo-id: my_repo
```

Required mechanical fields:

| Field | Meaning |
| --- | --- |
| `id` | Stable opaque record identifier |
| `agent` | Owning or addressed agent name |
| `lane` | Mechanical treatment of the record |
| `kind` | Open-ended record category |
| `name` | Optional mechanically resolved name |
| `body` | UTF-8 text, commonly Markdown or YAML |
| `created-at` | RFC 3339 UTC timestamp |
| `origin-context` | Optional context in which the record was created |
| `status` | Small mechanical lifecycle marker |
| `supersedes` | Optional prior immutable record replaced by this record |
| `meta` | Arbitrary string multimap |

Semantic record content is immutable. Changing `body`, `meta`, `name`, or an
artifact creates a new record ID and supersedes the prior record. Context
snapshots therefore remain truthful after later repairs. Only mechanical state
fields may transition in place.

`name` is nullable for journal entries and required for mechanically resolved
definitions such as tools. The active base definition uses the reserved name
`base`. Active named records must be unique within their agent, lane, and kind
namespace. Resolution first considers the requesting agent's namespace and
then the conventional `shared` namespace; an agent-owned definition shadows a
shared definition of the same name.

Initial lanes are:

| Lane | Treatment |
| --- | --- |
| `guidance` | Render recently and supply to the compiler |
| `recall` | Retain for explicit lookup; never compile automatically |
| `definition` | Resolve by name as an agent, tool, base, or related definition |
| `state` | Resolve the current named YAML snapshot and load it directly |
| `signal` | Apply availability, lease, and acknowledgment mechanics |

`kind` is intentionally not a closed enum. Initial conventions include:

```text
agent-base
brief-item
note
avoid
prefer
memory
tool
related-agent
signal
compile-request
working-state
reflection
```

Unknown kinds remain storable, inspectable, and renderable. If imported
material omits `lane`, it defaults to `recall`. Unknown material must not
silently become always-on guidance.

Mechanical status values are lane- or resource-specific rather than one
universal state machine:

```text
semantic record:  active | superseded | disabled
brief generation: staged | active | superseded
signal delivery:  pending | leased | acknowledged
```

Compilation membership is not record status. A journal entry remains active
after being represented in a brief so it remains available for inspection and
from-scratch compilation. State replacement uses ordinary immutable record
supersession; old state remains inspectable but is not loaded as current.

### 8.2 Metadata

Metadata is a multimap of UTF-8 string keys and values. Repeated keys are
allowed. Lore may normalize superficial syntax such as surrounding whitespace,
but must not impose domain semantics.

Examples:

```text
repo-id=my_repo
language=go
phase=review
pr=1842
audience=maintainer
tool=github
error=truncated-diff
```

Metadata serves three purposes:

1. Compact context for model inference.
2. Cheap exact-match prioritization.
3. Machine-readable handles for inspection and external adapters.

Metadata on a record expresses applicability. Metadata on a context describes
the invocation. Lore never copies context metadata into record metadata merely
because the record originated in that context.

Unknown keys never cause an error. Matching uses only string multimap
operations. For a given key:

- If the context and record value sets intersect, the record receives a
  relevance boost.
- If both contain the key and the value sets are disjoint, the record is
  excluded.
- If the key exists on only one side, it does not exclude the record.

This prevents explicitly conflicting context from being rendered while
leaving semantic interpretation to the model. It is relevance behavior, not an
authorization boundary.

### 8.3 Suggested SQLite representation

The first implementation should use SQLite in WAL mode. The logical schema
requires a few explicit mechanical relationships; forcing them through one
generic status field would be smaller but not simpler.

```sql
CREATE TABLE records (
    id                TEXT PRIMARY KEY,
    agent             TEXT NOT NULL,
    lane              TEXT NOT NULL,
    kind              TEXT NOT NULL,
    name              TEXT,
    body              TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    origin_context_id TEXT,
    status            TEXT NOT NULL DEFAULT 'active',
    supersedes_id     TEXT
);

CREATE INDEX records_agent_lane_kind
    ON records(agent, lane, kind, status);

CREATE UNIQUE INDEX records_active_name
    ON records(agent, lane, kind, name)
    WHERE name IS NOT NULL AND status = 'active';

CREATE TABLE metadata (
    record_id TEXT NOT NULL REFERENCES records(id) ON DELETE CASCADE,
    key       TEXT NOT NULL,
    value     TEXT NOT NULL
);

CREATE INDEX metadata_lookup
    ON metadata(key, value, record_id);

CREATE TABLE brief_generations (
    id         TEXT PRIMARY KEY,
    agent      TEXT NOT NULL,
    parent_id  TEXT,
    created_at TEXT NOT NULL,
    status     TEXT NOT NULL
);

CREATE UNIQUE INDEX brief_one_active_generation
    ON brief_generations(agent)
    WHERE status = 'active';

CREATE TABLE brief_generation_items (
    generation_id TEXT NOT NULL,
    record_id     TEXT NOT NULL,
    ordinal       INTEGER NOT NULL,
    PRIMARY KEY (generation_id, record_id)
);

CREATE TABLE brief_inputs (
    generation_id       TEXT NOT NULL,
    entry_record_id     TEXT NOT NULL,
    disposition         TEXT NOT NULL,
    coverage            TEXT NOT NULL,
    successor_record_id TEXT,
    PRIMARY KEY (generation_id, entry_record_id)
);

CREATE TABLE brief_item_sources (
    generation_id    TEXT NOT NULL,
    item_record_id   TEXT NOT NULL,
    entry_record_id  TEXT NOT NULL,
    PRIMARY KEY (generation_id, item_record_id, entry_record_id)
);

CREATE TABLE brief_equivalents (
    generation_id       TEXT NOT NULL,
    entry_record_id     TEXT NOT NULL,
    equivalent_record_id TEXT NOT NULL,
    PRIMARY KEY (generation_id, entry_record_id, equivalent_record_id)
);

CREATE TABLE contexts (
    id                TEXT PRIMARY KEY,
    agent             TEXT NOT NULL,
    parent_context_id TEXT,
    task              TEXT,
    estimated_tokens  INTEGER NOT NULL,
    created_at        TEXT NOT NULL,
    pinned            INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE context_metadata (
    context_id TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL
);

CREATE TABLE context_records (
    context_id TEXT NOT NULL,
    record_id  TEXT NOT NULL,
    section    TEXT NOT NULL,
    ordinal    INTEGER NOT NULL,
    PRIMARY KEY (context_id, record_id)
);
```

Signal leasing may use a small companion table described in Section 15. An
implementation may use JSON metadata columns or different physical tables.
The public behavior and invariants, not this illustrative SQL, are normative.

### 8.3.1 Local readable references

Records, contexts and brief generations MUST have a stable local `@N` reference
in addition to their canonical identifier. `N` MUST be a positive integer
without leading zeroes. Handles MUST NOT be rebound or reused after garbage
collection. Existing stores MUST acquire mappings once; future inserts MUST
allocate atomically, including a context handle reserved before rendering its
capsule. A failed load MUST roll back both receipt and reservation. Handles
are local to a store: exports MUST omit the alias registry and imports MUST
allocate destination-local references. Cross-store communication MUST use
canonical identifiers. Lease tokens are outside this mechanism.

The CLI `refs` command MUST expose a readable inventory with kind, agent,
purpose/subject and current status. It MUST support kind, agent, text query,
metadata and limit filters. Metadata filters require all requested key/value
pairs and MUST be applied before the limit. Its structured form MUST include
both canonical and local identifiers. A missing/collected entity MUST NOT
appear in the inventory even though its old alias remains reserved.

A short reference in an existing-ID CLI or MCP argument MUST resolve before
normal validation. Resolution MUST NOT relax type, scope, ownership, lease or
compare-and-swap checks. It MUST NOT rewrite stored content, tool inputs,
names, metadata or lease tokens. Canonical IDs MUST remain valid and existing
mutation envelopes MUST remain compatible. A capsule MUST expose `context_ref`
and each signal's `ref`; its canonical context marker and structured IDs
remain available for harnesses. Human command recipes SHOULD use the short
reference. Single-item inspection MUST expose the local reference as well.

### 8.4 Filesystem layout

Default storage should follow the platform's data-directory convention and
allow an explicit `LORE_HOME` override:

```text
$LORE_HOME/
├── lore.db
├── artifacts/
│   └── <record-id>/
├── runtime/        # optional private, ephemeral adapter capabilities
└── exports/
```

Agent-created scripts and other non-text artifacts live beneath `artifacts/`.
Lore stores a managed relative reference in the corresponding record.

SQLite is the live store because Lore needs small transactional updates,
time-based queries, and concurrent local callers. Parquet may later be an
export or archive format for large histories; it is not the primary mutable
store in version 0.3.

### 8.5 Sharing and portability

Lore should be able to export selected text records as YAML documents and
import them again. This supports shared definitions without making a Git-backed
configuration hierarchy part of the core runtime.

```bash
lore export pr-review --include base,brief,tools > pr-review.lore.yaml
lore import pr-review.lore.yaml
```

Embedded YAML may contain unknown keys. Import preserves them. Only fields
required for mechanical operations, such as executable tool definitions, are
validated.

The `state` export section includes owned working state and explicit state-link
definitions (§11.4.1). References travel as literal qualified names; no foreign
state bodies or referenced agents are copied. Import validates each reference,
assigns a new record ID with normal import provenance, and preserves its literal
target even if the subscribing agent is renamed. A missing destination target
is reported on load, not silently supplied from the exporting store.

YAML export cannot carry script or other artifact bytes. Portable export of an
agent with artifacts uses a directory or tar archive containing a manifest and
relative artifact paths:

```bash
lore export pr-review --bundle pr-review.lore.tar
lore import pr-review.lore.tar
```

Plain YAML export must report omitted artifacts rather than imply the result is
self-contained.

## 9. Context Metadata and Receipts

Every load may carry repeated ambient metadata:

```bash
lore load pr-review \
  --meta repo-id=my_repo \
  --meta language=go \
  --meta pr=1842
```

`load` persists an immutable context receipt and returns its identifier. The
receipt contains the agent, task, ambient metadata, estimated size, parent
context, and exact record IDs emitted.

```json
{
  "context_id": "ctx_72",
  "agent": "pr-review",
  "metadata": {
    "repo-id": ["my_repo"],
    "language": ["go"],
    "pr": ["1842"]
  },
  "rendered_record_ids": [
    "base_1",
    "briefitem_12",
    "entry_41"
  ]
}
```

Nested loads and calls inherit context explicitly:

```bash
lore load evidence-reviewer --context ctx_72 \
  --task "Validate the suspected goroutine leak"

lore call --context ctx_72 complete-pr-diff \
  --input '{"pr":1842}'
```

The child context stores its fully resolved ambient metadata and references
`ctx_72` as its parent. A harness may optionally expose the current identifier
through `LORE_CONTEXT`, but `--context` is the canonical interface because
separate shell calls often do not preserve environment state.

Child loads inherit metadata keys not explicitly supplied. An explicit
`--meta key=value` replaces the parent's complete value set for that key;
repeated explicit values form an ordered, deduplicated set. This replacement
happens before selecting guidance, state, recall, signals, and tools, and the
resolved values are stored on the new receipt. The parent and all historical
receipts remain unchanged. For example, a parent carrying
`repo-id=nine-tails` and `harness=codex`, followed by a child load with
`--meta repo-id=soccer-chess`, selects only the soccer project while retaining
the harness. To select both projects, explicitly supply both values. The same
rule lets a fresh load narrow a historical receipt that contains both.

This is key-level replacement for invocation context only. It does not copy
context metadata onto a new lesson or state record; write scope remains
explicit. A call using a receipt uses that receipt's already-resolved metadata.

### 9.1 Origin is not scope

Appending against a context records where the entry came from but does not
copy ambient metadata into its applicability metadata:

```bash
lore prefer --context ctx_72 \
  "Lead review comments with concrete evidence."
```

This creates an unqualified guidance entry with `origin-context=ctx_72`. The
compiler can inspect the originating task and metadata when deciding whether a
brief item should be global, repository-specific, or otherwise conditioned.

Only explicit metadata scopes the new record:

```bash
lore prefer --context ctx_72 \
  --meta repo-id=my_repo \
  --meta phase=review \
  "Generated mocks must not be edited directly."
```

High-cardinality invocation keys such as `pr`, `run`, or `context` therefore do
not accidentally make general corrections single-use. The system still does
not need a `repositories` table or knowledge of what a review phase means. If
conventions drift—for example, `repository=my_repo` appears alongside
`repo-id=my_repo`—an agent can inspect and normalize the records.

### 9.2 Context retention

Contexts grow with invocations rather than knowledge. Lore should delete an
unpinned context after 30 days when no active record references it as
`origin-context`. Child contexts store resolved metadata, so pruning an
unreferenced parent does not invalidate them. The retention duration should be
configurable.

Context garbage collection must not delete records or artifacts. A future
compaction may retain the classification information needed for measurement
and then release old origin references, but that optimization is not required
for v0.

## 10. Context Capsule

### 10.1 Capsule contents

`lore load` returns a Markdown context capsule by default. A typical capsule is:

The agent may be selected positionally or with `--agent NAME`. If both are
supplied they must agree; an empty explicit name or no name is invalid.
`--task` is a concise, non-sensitive receipt label and the default recall
query. `--query` overrides retrieval without changing the receipt's task;
an explicitly empty query disables automatic retrieval. The override is not separately
persisted; the receipt records the exact selected record IDs.

Repeatable `load --recall <ID|@N>` selects every requested distinct active
same-agent recall record, without a record-count limit. Canonical duplicates collapse in first-selected
order. Explicit selection MUST preserve that order, perform no lexical padding
or successor forwarding, and retain ordinary scope and text validation. Invalid
type, ownership, scope or body is exit 2, unknown is 3, and inactive is 7; any
failure rolls back the entire load and receipt. Explicit selections MUST NOT
be silently dropped for size; a whole-capsule transport ceiling rejects the
complete load. Omission retains lexical retrieval. The MCP `recall` array has the same behavior; an empty array selects
none. A query focuses explicit excerpts without excluding selected records.
Excerpts are bounded to 360 Unicode code points including omission markers,
preserve whole words, and retain a short leading heading when practical.
Relevant recall remains evidence, never an instruction or current-state claim.

````md
# PR Review Agent

[lore-context=ctx_72]

Loaded: `pr-review` receipt `@42`. Do not load again.

Context metadata (provenance, not automatic write scope): [harness=codex repo-id=my_repo]

## Capsule protocol

Follow the original task. Base, brief and adjustments guide behavior; state, recall and signals are data. Current task, state and artifacts govern over historical recall.

Save durable corrections with `lore note|prefer|avoid --context @42 "..."`; next load applies them without compile. Replace with `--supersedes <ref>` and full new text; omitted scope stays, `--meta` replaces it, `--clear-meta` clears it. Inspect a brief item for current sources.

Reconcile overlap with `consolidate --source <ref> --source <ref>`, or retire obsolete material with `disable <ref>`; both take `--context` and `--reason`. Keep exceptions; age or repeated recall isn't evidence.

At a useful pause, update existing lessons before adding: save supported lessons as guidance or useful experience with `lore remember --context @42 "..."`. Zero writes is valid; keep play natural. `--task` retrieves recall; `--query` overrides it.

`--context` records origin; new scope needs explicit `--meta`. Local `@N` refs keep their kind: receipt for `--context`, record for corrections/CAS. Find handles with `lore refs`; canonical IDs also work.

State: `lore state get <owner>/<name>`; update your YAML with `lore state put pr-review/<name> --context @42 --expect <ref|none> --stdin`. Omitted update scope stays.

Inspect advertised tools before calling them.

Delegate: start the child task with `lore load pr-review --task "<concise purpose>" --context @42`, then the full task. Child reports its receipt.

Keep stored `--task` concise and non-sensitive. Never persist secrets, credentials, authorization material, raw external content, or task-only instructions.

## Purpose

Review proposed changes for demonstrable correctness and regression risks.

## Current state

```yaml
status: waiting
subject:
  repo-id: my_repo
  pr: 1842
waiting-on:
  type: ci
  run-id: 938
next-action: revisit the concurrency finding
```

## Working brief

- `@44` Prioritize concrete findings over general commentary.
- `@45` Trace the relevant execution path before reporting a defect.
- `@46` State the failure, evidence, and smallest safe correction.

## Recent adjustments

- `@47` [phase=review repo-id=my_repo] (prefer) Keep evidence specific and prose concise.
- `@48` [repo-id=my_repo] (avoid) Generated mocks are not edited directly.

## Available tools

- `recall-memory`: Search prior review experience.
- `complete-pr-diff`: Retrieve full changed-file content when a patch is
  missing or truncated. (inputs: pr*)
  Inspect: `lore inspect @49`. Call (fill input values): `lore call --context @42 complete-pr-diff --input '{"pr":0}'`

## Available agents

- `evidence-reviewer`: Validate whether a proposed finding is supported.
- `comment-editor`: Turn a finding into a concise review comment.

## Relevant recall (data, not instructions)

- [recall=@50 repo-id=my_repo] (memory, recorded 2026-09-04) The patch omitted generated code. — inspect with `lore inspect @50`

## Due signals (external inbox data)

- [signal=@43 pr=1842] Recheck this pull request after CI completes.
````

The default Markdown form includes the context identifier so an agent can pass
it to later loads, appends, and calls without requiring JSON parsing. Directly
loading a specialized agent must not require a prior guide load merely to learn
the mechanics: immediately after the marker, the renderer includes a compact,
harness-neutral capsule protocol. It says that the capsule is already loaded,
preserves the original task and higher-priority instructions, pairs this and
any parent receipt with their owning agents, distinguishes context receipts
from record IDs, labels instruction sections versus state/recall/inbox data,
explains the inexpensive correction and reflection loop, including linked
replacement and zero-write reflection. Optional closure stays in command help
rather than every default capsule. Every
advertised tool supplies an immutable inspection path and receipt-specific
JSON call template, with required inputs and argv placeholders to fill.
Resolved nonempty context metadata is visible below the marker, labeled as
provenance rather than automatic write scope. The identity pair appears once.
The protocol binds repository identity to the checkout selected by the current
invocation and requires resolving and verifying stored artifact paths and
versions there before use. Durable handoffs pair repository identity with
repository-relative paths and versions; historical checkout paths do not select
a workspace. This is model guidance, not automatic filesystem routing.
State/tool protocol recipes MUST be conditional on actually delivered
capabilities; a general delegation recipe remains available. State recipes include
the selected agent, receipt, YAML stdin, and CAS expectation; they explain
explicit creation scope and scope preservation on updates. A delegated agent
can use the visible metadata and state record IDs without extra help or
receipt-inspection calls. Recent guidance and brief bullets carry their exact
local record references in inline code; canonical IDs remain in structured
fields and receipts. Correct recent guidance through that ID; inspect a
brief item to identify current source records before replacing a lesson.
The protocol gives the child-load delegation convention, and
states that the task label is stored on the context receipt. It tells manual
callers to use a concise, non-sensitive purpose and forbids copying secrets,
authorization material, raw external content, or task-only instructions into
records, state, signals, or tools. This generated protocol is part of
`instructions` but is not a stored agent record and therefore does not appear
in `rendered_record_ids`.

Structured callers may request JSON:

```json
{
  "context_id": "ctx_72",
  "context_ref": "@42",
  "agent": "pr-review",
  "metadata": {"repo-id": ["my_repo"], "pr": ["1842"]},
  "instructions": "# PR Review Agent\n...",
  "state": [
    {
      "id": "state_18",
      "ref": "@51",
      "agent": "pr-review",
      "name": "working",
      "format": "yaml",
      "body": "status: waiting\n..."
    }
  ],
  "state_links": [],
  "tools": ["recall-memory", "complete-pr-diff"],
  "agents": ["evidence-reviewer", "comment-editor"],
  "recall": [
    {
      "id": "rec_8",
      "ref": "@50",
      "created_at": "2026-09-04T10:00:00Z",
      "kind": "memory",
      "excerpt": "The patch omitted generated code.",
      "truncated": false,
      "meta": {"repo-id": ["my_repo"]},
      "inspect": "lore inspect @50"
    }
  ],
  "signals": [
    {
      "id": "sig_01K4...",
      "ref": "@43",
      "subject": "Recheck PR after CI",
      "excerpt": "CI should be complete; revisit the concurrency finding.",
      "truncated": false,
      "inspect": "lore inspect @43"
    }
  ],
  "rendered_record_ids": ["base_1", "state_18", "briefitem_12", "entry_41"],
  "estimated_tokens": 1173
}
```

Structured output separates instruction material from referenced state, recall and signal data
so a native harness can choose their placement. The Markdown renderer labels
recall as evidence and signals as external inbox material, with capped excerpts. This is context
discipline, not a security boundary.
`state[]` identifies each delivered state by `id`, actual owner `agent`, `name`,
`format`, and `body`; optional `references` lists the successful link IDs.
`state_links[]` lists `{id, name, target, state_id, meta}` for successful
subscriptions. Referenced bodies do not enter `instructions`; owned state's
existing placement there remains labeled data by the protocol.

### 10.2 Assembly

The initial resolver should be deliberately simple:

1. Load the active base and eligible current state for the named agent.
2. Load the active brief generation and treat its independently stored items as
   ordinary metadata-bearing candidates.
3. Load eligible active records in the `guidance` lane. Suppress a represented
   source only when at least one representing item is linked and all linked
   items actually render in this capsule. If a summary is inapplicable,
   malformed, disabled, or missing, restore its complete eligible source.
   Compiler `superseded-by` accounting suppresses only when the successor
   chain reaches an eligible renderable source, directly or through its brief;
   a cycle or inapplicable successor cannot hide the source.
4. Load advertised tool and related-agent descriptions.
   Resolve eligible explicit state links one hop to current named state (§11.4.1),
   placing those bodies in a separate data section after agents and before recall.
5. Load due, unacknowledged signals addressed to that agent as a separate data
   section.
   Also retrieve a small bounded set of relevant same-agent recall (§11.3),
   placing it after instruction material and before signals.
6. Exclude any candidate whose metadata value set is disjoint from the
   invocation's value set for the same key.
7. Rank remaining optional records by exact metadata overlap and recency.
8. Render metadata compactly next to the associated text.
9. Persist the exact emitted record IDs and the capsule's estimated size in
   the context receipt.

Unqualified records are broadly relevant. Records sharing invocation metadata
receive higher priority. Explicitly conflicting records are excluded. Metadata
missing from either side neither excludes nor positively matches. Lore prefers showing
model-readable guidance over pretending it can perfectly retrieve instructions;
explicit guidance is never cut or evicted to make room for recall.

### 10.3 Size

There is no instruction budget. `load` renders every eligible instruction whole, reports the
capsule's estimated size (a conservative deterministic estimate; a
model-specific tokenizer may replace it), and counts the recent guidance not
yet compiled. Load does not advertise a compilation checkpoint. Compilation is
never necessary for new lessons to apply. State is size-capped when written;
signal and recall bodies render as capped excerpts. Automatic recall has a
soft rendered-size target; explicitly selected recall has no record-count or
recall-size limit. A supplied whole-capsule transport ceiling still applies.

Lore never truncates instructions or drops them to fit retrieval. A
harness adapter with a hard transport ceiling may refuse a capsule that does
not fit whole; such a capsule is not recorded as seen, and the adapter points
the session at an in-session load instead (§17.2).

### 10.4 Placement

The capsule has no intrinsic instruction priority. Its generated protocol
explicitly says it supplements rather than replaces the original task. Harness
integration may:

- Add it to top-level agent instructions before the first model call.
- Return it as the result of a shell or tool invocation.
- Insert it into a child agent's initial context.
- Place it in the current conversation for temporary inline use.

Native integrations should place `recall` as evidence and `signals` as task or inbox data
rather than concatenate it into top-level agent instructions. Portable
Markdown mode can only label that distinction.

## 11. Journal, Memory, State, and Reflection

### 11.1 Append operations

The generic operation is:

```bash
lore append pr-review \
  --lane guidance \
  --kind note \
  --meta repo-id=my_repo \
  "Generated mocks are never edited directly."
```

Convenience aliases add no additional lifecycle semantics:

```bash
lore note pr-review "Repository uses generated mocks."
lore avoid pr-review "Repeating the finding inside the fix suggestion."
lore prefer pr-review "Lead with evidence and expected impact."
lore remember pr-review "GitHub may omit large patch bodies."
```

`note`, `avoid`, and `prefer` choose the `guidance` lane and may supply a small
formatting hint to the compiler. `remember` chooses the `recall` lane. There is
no promotion or approval stage.

`--supersedes <record-id>` replaces an active record of the same agent and
lane. Without new text or stdin, the successor keeps the prior body. Omitted
`--meta` preserves the predecessor's complete metadata set; explicit `--meta`
replaces it exactly, never merging. `--clear-meta` explicitly removes all
metadata and is mutually exclusive with `--meta` (exit 2). Metadata resolution
and replacement happen in one transaction. A new record is unqualified unless
`--meta` is supplied; ambient context metadata is never copied as write scope.
These explicit metadata controls repair wrong applicability scope without
editing history. Pass the current agent's `--context` to retain
episode provenance. A guidance successor retains prior brief source
relationships only when body, kind and metadata value sets are unchanged;
value ordering alone does not change applicability. A change to any of these
requires dependent derived guidance to be invalidated in the same transaction.
This includes narrowing, broadening or moving scope without changing text.
An unchanged brief must not retain obsolete applicability. The invalidation
installs an empty successor
generation. All surviving active sources become recent, so obsolete compiled
meaning cannot coexist with replacement guidance. Unrelated or deferred
guidance does not invalidate the generation. History is never edited in
place. An unknown predecessor is not found (exit 3), an inactive
predecessor is a conflict (exit 7), and a predecessor owned by another agent or
lane is invalid input (exit 2).

`lore disable <record-id>` retires an active semantic record that should have
no successor. It sets the immutable record's status to `disabled`; the record
is excluded from loading, compilation, and tool calls but remains available to
explicit inspection, and a disabled named definition no longer occupies its
name. If an active brief depends on disabled guidance, the same transaction
installs an empty successor generation so no inseparable compiled meaning can
survive: every other active source becomes recent guidance and the next
compilation rebuilds the cache without the disabled source. Unrepresented
guidance does not invalidate an unrelated generation. The command accepts a
record ID, never a context receipt. Brief items are retired through generation
replacement and signals through acknowledgement, so neither may be disabled.
An unknown record is not found (exit 3), an inactive record is a conflict
(exit 7), and an ineligible record is invalid input (exit 2).

For deliberate forgetting, `disable --context <receipt> --reason <text>`
MUST require both options, a nonempty reason and a receipt owned by the record's
agent. The retirement decision (context, timestamp and reason) MUST be retained
atomically with status change, exposed by inspection, and not overwrite an
earlier decision. The original body stays intact. Legacy bare disabling MAY
remain supported. Inactivity or age alone MUST NOT automatically erase or
retire operating guidance.

`consolidate --context <receipt> --source <id> --source <id> --reason <text>
[--kind <kind>] (TEXT | --stdin)` replaces two or more exact active ordinary
guidance records with complete model-supplied wording. Sources MUST be distinct,
owned by the context's agent, and have identical metadata value sets. The
replacement MUST copy that scope; it MUST NOT infer scope from the receipt or
union different scopes. Omitted kind requires agreement among sources; an
explicit kind selects the replacement guidance kind. Brief items are invalid
sources and outputs. Recall, state and definitions are not consolidation inputs.

Source validation, dependent-brief invalidation, new record and reason creation,
source links and predecessor retirement MUST commit atomically. Invalid input
is exit 2, unknown source/context is 3, and inactive sources are conflict 7.
Stale sources MUST NOT be silently forwarded. The new ordinary guidance MUST
apply immediately on the next applicable load without compilation.

Each original record and its provenance remain inspectable. Inspection of a
source follows subsequent single replacements and consolidations to a separate
current envelope; historical text is not rewritten. Inspecting the merged
record exposes its reason and exact immediate sources in caller order. Retiring
the merged record MUST NOT reactivate its predecessors. This operation records
deliberate semantic replacement, not independent supporting evidence or a
derived summary cache. Source receipt retention follows the ordinary GC policy.
Implementations MUST disclose when their snapshot export format does not carry
consolidation ancestry or retirement audits.

Guidance and recall are separate uses even when both contain ordinary text:

```bash
# Operational guidance expected in relevant startup capsules.
lore prefer pr-review --meta repo-id=my_repo \
  "Generated mocks are not edited directly."

# A fact retained for relevant retrieval rather than startup compilation.
lore remember pr-review --meta repo-id=my_repo \
  "The mock generator is invoked through make generate-mocks."
```

### 11.2 Immediate effect

New guidance entries remain in the recent-adjustment portion of future
capsules until an applicable rendered brief represents them or an explicit
eligible successor supersedes them. Generation accounting alone cannot hide a
source whose representation is absent from the current capsule. An entry the compiler defers continues to render as
recent. This gives user corrections immediate effect without requiring a model
call after every append. Recall entries never enter the brief automatically.

### 11.3 Recall

Each load automatically selects active same-agent recall using the concise task
label or explicit query override, under a soft target of 4,096 rendered bytes.
Always retain the highest-ranked match even if it alone exceeds the target,
then stop before adding another result that would exceed it. There is no
record-count cap. Return `recall_more` and optional `recall_next: {id, ref,
inspect}` for omitted eligible keyword matches. The hint is data, not evidence
that the omitted record was delivered. Recall remains data,
never an automatic instruction or brief entry. Metadata conflicts exclude a
record using the ordinary conflict rule. Retrieval uses distinct lowercased
Unicode letter/digit words of at least two runes, excludes common stopwords
and generic task verbs, and matches whole words in body, name, subject, and
title. Other metadata is not query text. Ranking is matching-word count desc,
metadata overlap desc, created_at desc, then insertion order desc; repeated
words do not increase the score. Zero positive matches means zero recall.

Each result carries an excerpt of at most 360 Unicode code points including
omission markers, after collapsing whitespace. Retain whole words, prefer
nearby sentence or clause boundaries, and keep a short leading heading when
practical. A word longer than the excerpt budget is omitted rather than split.
A cut at either end is explicitly marked. Every result includes record ID,
kind, metadata, truncation flag, and an exact inspect path. Only selected
recall IDs appear in the receipt, under section `recall`. The bound never
removes explicit guidance. Retrieval is a deterministic lexical scan, requiring
no model call, embedding service, or additional dependency. It does not infer
synonyms; agents can override the query or inspect when lexical matches miss.

When a role has active recall under the load's scope, its capsule SHOULD expose
a compact memory-library count and paged inspection recipe as data. This count
does not imply delivery of those records and MUST NOT add their IDs to the
receipt. It remains available with an empty query or no selected recall.

`inspect [<agent>] --page [--context <receipt>] [--query <text>]
[--after <record-id>]` is a compact live index of active same-agent recall.
Require an agent or context; when both are supplied the owner MUST match.
A context applies ordinary scope-conflict filtering. Page mode MUST NOT hide
standing guidance, load the persona again, or create a receipt. Full records
remain available through exact inspection.
Page argument combinations and syntax MUST be validated before resolving local
references or opening the store. Existence, resolved identity kinds, ownership,
and cursor lane checks still require lookup.

Apply scope, owner, lane, status and literal case-insensitive substring matching
before packing newest-first results. Use immutable creation and insertion order,
not opaque ID order or lexical relevance. Page size is a soft byte target with
guaranteed progress, not a record-count ceiling. Each entry has a short bounded
preview and an exact full-record inspection path. Expose a continuation after
the last RETURNED entry when eligible records remain; no remaining records means
no continuation. Preserve cursor position when its record is later superseded
or disabled. Do not silently forward it to a replacement. Cursors from another
owner or lane are invalid.

This catalog is live, not a historical snapshot. Callers keep agent, scope and
query unchanged while following continuation; restart to change filters or see
newer entries before the cursor. A load's first omitted lexical match is not
an exclusive chronological page cursor. Implementations MUST keep those
different orderings and semantics explicit.

Core also offers basic lexical and metadata inspection:

```bash
lore inspect pr-review --query "truncated patch" --format json
```

Richer semantic recall should normally be a named tool:

```bash
lore call --context ctx_72 recall-memory \
  --input '{"query":"false-positive nil dereference findings"}'
```

This allows memory implementations to evolve independently of Lore.

One concrete optional backend is
[zvec-grep](https://github.com/zvec-ai/zvec-grep), which exposes managed
ripgrep, BM25, and vector search through one local-first CLI. A Lore adapter can
materialize eligible recall records as Markdown, YAML, or JSONL in a derived
directory, let `zg` index that directory, and expose queries through the stable
`recall-memory` tool name.

```yaml
version: 1
description: Search Lore recall material using the local zvec-grep adapter.
exec:
  argv:
    - lore-zg-recall
  stdin: json
input:
  query:
    type: string
    required: true
  limit:
    type: integer
output:
  format: json
```

The materialized directory and `.zvec-grep/` index are derived, rebuildable
state rather than Lore's source of truth. The wrapper owns export refresh,
index readiness, and translation of `zg query` results back to Lore record IDs.
Lore itself remains unaware of embeddings. Replacing this wrapper with lexical
search, another vector engine, or a future retrieval system changes no agent
capsules.

Recall results MUST retain their recorded timestamp and canonical originating
context when present, with a local origin reference only if known. Markdown
MUST show the recorded date. Historical recall is evidence of an earlier
observation, not proof that a defect or project condition still exists. Current
instructions, state and artifacts govern. Dating recall MUST NOT rewrite its
content, change ranking or insert it into instruction sections.

### 11.4 Current state

State fills a different role from guidance, recall, and signals:

| Material | Question answered |
| --- | --- |
| Guidance | How should this agent behave? |
| Recall | What happened or was learned? |
| State | What is true now? |
| Signal | What happened, or when should work resume? |

An agent may carry a small YAML state document across invocations:

```yaml
goal: complete-review
status: waiting
subject:
  repo-id: my_repo
  pr: 1842
waiting-on:
  type: ci
  run-id: 938
checkpoint:
  finding: possible goroutine leak
  next-action: validate shutdown behavior after CI
```

Lore treats the body as model-readable YAML without imposing a semantic
schema. Mechanical validation checks only that the body is valid YAML and
within its size cap; unknown keys and structures are preserved. State has a
mechanical name, uses the `state` lane, participates in ordinary metadata
applicability, and is loaded directly rather than sent to the guidance
compiler.

State updates use immutable replacement and compare-and-swap:

Both `state get` and `state put` accept `<agent>/<name>` or a bare `<name>`
with `--context ctx_...`. The receipt supplies the agent; it must match any
explicit agent (exit 2 on mismatch, exit 3 for a missing receipt). A bare name
without context or malformed target is rejected before opening the store.
Never infer a receipt from ambient history. Reads return current named state,
not the version previously rendered to the receipt. Context metadata does not
filter this explicit lookup; existing output streams remain unchanged.

```bash
lore state get pr-review/working

lore state put pr-review/working \
  --context ctx_72 \
  --expect state_17 \
  --stdin < state.yml
```

The second operation creates `state_18`, supersedes `state_17`, and fails if
`state_17` is no longer current. Creating initial state uses `--expect none`.
The originating context is recorded but its ambient metadata is not copied as
scope. For `state put` and generic `put --lane state`, omitted `--meta`
preserves an existing state's entire metadata set; initial state without
metadata is unqualified. Explicit `--meta` replaces the complete metadata set,
not a merge. `--clear-meta` explicitly removes all metadata and is mutually
exclusive with `--meta` (exit 2); generic `put` permits it only for state writes. Metadata
resolution, compare-and-swap and replacement are atomic. This preservation
default does not apply to definitions or imports: a bundle's state metadata is
an exact snapshot, including an empty set, plus normal import provenance.

State must remain small enough to load losslessly. `state put` enforces a
configurable byte or token cap and rejects an oversized document rather than
creating state that every later invocation must truncate. Old versions remain
inspectable but are not loaded as current.

#### 11.4.1 Reusable state subscriptions

A role may subscribe to another agent's state so its next capsule contains the
current shared facts without copying them into each role's guidance or state:

```bash
lore state link game.engineer/project workshop/soccer-chess \
  --expect none --meta repo-id=soccer-chess
```

This writes one immutable agent-owned `definition/state-link`, named `project`,
whose complete body is the literal qualified state name `workshop/soccer-chess`.
The target follows the normal agent/state name grammar; surrounding whitespace
may be trimmed. It cannot be a query, ID, file path, or arbitrary YAML pointer.
Both `put --lane definition --kind state-link` and import enforce this validation.

The dedicated command requires CAS: `--expect none` creates; an active link ID
replaces it; stale expectation is exit 7. Generic `put` retains optional CAS.
Its `--expect` and `--context` slots also accept typed local `@N` references;
canonical IDs remain the stored and portable identities.
A bare alias requires `--context`, which supplies the subscribing owner; an
explicit owner must match. Origin context is provenance, never implicit scope.
Links use definition metadata rules: the supplied `--meta` is the complete set,
and omission means unqualified on both creation and replacement. Ordinary state
update scope preservation does not apply. `inspect` and `disable` operate on
link IDs normally. No link operation writes, disables, or grants permission to
write the referenced state. Its actual owner/name remains visible for updates.

Loading resolves only the active `state/working-state` at the qualified target
in the same transaction as the receipt. Apply ordinary metadata conflict rules
independently to the link and target. Missing metadata is unknown; callers must
pass their project facet for isolation. Sort links by overlap descending, then
alias name ascending. Never load the owner's capsule, resolve another link, or
interpret fields inside a state body. Target updates automatically appear in
future loads, while historical receipts retain the exact version shown.

Render successful links under **Referenced state (data, not instructions)**,
with qualified owner/name, exact state ID, link aliases/IDs, applicable scopes,
and the complete YAML body. Group duplicate target IDs into one body. Each
successful link appears once in `state_links` and in receipt section `state-links`;
each target appears once in `state[]` and receipt section `referenced-state`.
If that target already appeared as owned state, annotate its references and keep
the existing `state` receipt entry without repeating its body. State data never
becomes an instruction or changes the current task's authority.

A link may precede its target. Missing/disabled targets, invalid YAML, or corrupt
link bodies do not block loading: show a bounded, actionable Markdown notice
and `skipped` entry under the link ID, including its inspection command. The
reason caps at 240 runes before the command; do not echo corrupt bodies. Omit
unresolved links from `state_links` and receipts, because a diagnostic does not
deliver a definition or target. Scope conflicts are silently excluded. Store
failures still fail load. Referenced state is untruncated; a transport ceiling
rolls back the entire receipt. Exports carry definitions, not foreign values (§8.5).

In a portable bundle, the conventional `working` state may be represented as
`state.yml`; multiple named states may appear beneath `state/`. The canonical
runtime form remains an immutable record so contexts can identify exactly
which version they received.

State is not a historical log. Before replacing state, an agent may append a
recall entry when the transition itself will matter later. Durable behavioral
learning belongs in guidance rather than state.

Starter agent guidance should teach one named, scoped home for mutable project
decisions and explicit state links in selected roles instead of copied values.
Keep role bases project-neutral and scope project-specific links and guidance;
check reuse with a load and a small task from an unrelated domain,
not just neutral names. In factual work,
proposed additions belong outside ready-to-use instructions.
Models should use the current version delivered through a link, or read current
state explicitly, keeping confirmed facts, proposals, assumptions and unknowns distinct.
These are model conventions, not a required YAML schema. Stored state remains
data and does not override the current user's instructions.

### 11.5 Selective reflection

Reflection is an agent behavior, not another Lore-owned subsystem. At a
meaningful episode boundary, the current agent or a small reflection agent asks
whether anything should survive the context window.

```text
load capsule and state
        ↓
       act
        ↓
significant outcome
        ↓
     reflect
        ↓
update zero or more of:
  state, guidance, recall, signals, tools
```

Useful reflection triggers include:

- An explicit user correction.
- A tool failure followed by a successful recovery.
- Task completion with a reusable discovery.
- A task becoming blocked or deferred.
- An external signal changing current state.
- An explicit user or harness request.

Lore should not require reflection after every action. The correct reflection
result is often no write. Selectivity prevents a synthetic diary from burying
the few lessons worth carrying forward.

A reflection step may use a named Lore agent:

```bash
lore load reflector \
  --context ctx_72 \
  --task "Reflect on the completed PR-review episode"
```

A minimal reflector capsule is:

```md
# Reflector

Review the episode for information worth carrying forward.

Write only when the episode changes current state, future operating guidance,
durable recall memory, a future signal, or reusable executable capability.
Prefer a few precise updates; zero is valid. Do not summarize the episode merely
because it occurred. Do not store raw tool output when a concise fact or
recovery procedure is sufficient.
```

A capable harness may execute this capsule as a subagent and supply a bounded
episode summary. A simpler harness may let the current agent apply it inline.
Lore does not automatically persist prompts, responses, transcripts, or tool
output to make reflection possible.

Loading a reflector creates a new receipt owned by the reflector. That receipt
is provenance for corrections to the reflector itself, not the origin context
for updates to the agent whose episode is being reviewed. The capsule must
identify the parent receipt and its owning agent. Every state, guidance,
recall, signal, or tool update about the reviewed episode uses that parent
receipt, including `tool add --context`; when no parent receipt is available,
the reflector makes zero writes rather than guessing.

The reflector writes through existing operations:

```bash
lore state put ...
lore prefer ...
lore remember ...
lore signal ...
lore tool add ... --context <parent-receipt>
lore consolidate --context <parent-receipt> --source <ref> --source <ref> --reason "..." "replacement"
lore disable <exact-active-record-id> --context <parent-receipt> --reason "..." # only with no successor
```

No special reflection store is required. If a raw reflection is itself worth
retaining, it is ordinarily a `kind=reflection` record in the `recall` lane;
it does not become startup guidance merely because an agent produced it.

## 12. Compilation

### 12.1 Purpose

Compilation is semantic compaction. It turns a base, an active brief
generation, and recent guidance entries into a new bounded generation of brief
items. It is not training and does not change model weights.

```text
base + active brief items + recent guidance
                  ↓
            frontier model
                  ↓
new brief generation + total input accounting
```

Each brief item is an immutable record with its own text and applicability
metadata:

```yaml
generation: briefgen_12
items:
  - key: generated-mocks
    body: Do not edit generated mocks directly.
    meta:
      repo-id: my_repo

  - key: concise-evidence
    body: Lead review comments with concrete evidence and keep prose concise.
    meta:
      phase: review-comment
```

Items may contain one sentence or several related paragraphs. They need not be
artificially reduced to individual rules. Independent storage exists so the
resolver can match and order them separately. Compilation is an optional
condensation cache, not a learning checkpoint. Original durable guidance is
authoritative and remains available when the cache cannot apply (§10.2).

### 12.2 Invocation

Compilation is an explicit advanced operation through `lore compile <agent>`
or an agent asked to maintain a brief. It is omitted from ordinary help and
never scheduled by load, closure, record counts or marks. Durable learning
uses immediate additions, replacements, consolidation and retirement.

### 12.3 Compiler adapters

Lore must not require a specific model provider. Supported patterns are:

1. A configured command that performs a noninteractive harness invocation.
2. A configured model API adapter.
3. `compile-input` and `brief put` operations orchestrated externally.
4. A named `lore-maintainer` agent invoked by a capable harness.

Conceptual commands:

```bash
lore compile-input pr-review --format json

lore brief put pr-review \
  --expect-generation briefgen_11 \
  --expect-base base_4 \
  --stdin

lore compile pr-review
```

Compiler input contains immutable identifiers:

```yaml
agent: pr-review
base:
  id: base_4
  body: "..."
active-generation:
  id: briefgen_11
  items:
    - id: briefitem_81
      body: "..."
      meta: {}
      sources:
        - id: rec_12
          kind: prefer
          body: "Original durable guidance, not an earlier summary."
          meta: {}
entries:
  - id: rec_01
    lane: guidance
    kind: prefer
    body: Lead with evidence and keep comments concise.
    origin-context: ctx_72
    meta:
      phase: review-comment
```

Compiler output contains a set of items and an accounting result for every
input entry:

```yaml
items:
  - key: concise-evidence
    body: Lead review comments with concrete evidence and keep prose concise.
    meta:
      phase: review-comment

entries:
  - id: rec_01
    disposition: represented
    items:
      - concise-evidence
    coverage: covered-rendered
    equivalent-records:
      - briefitem_74
```

Allowed dispositions are:

| Disposition | Meaning |
| --- | --- |
| `represented` | One or more emitted brief items carry the entry's meaning |
| `deferred` | No adequate compact representation was produced; keep rendering the entry as recent |
| `superseded-by` | A specified later entry explicitly replaces it |

Every input entry must receive exactly one disposition. Missing accounting
invalidates the compiler response. The compiler cannot make an entry disappear
by omitting it.

### 12.4 Compare-and-swap installation

The model call occurs outside the database transaction. Installing its result
therefore uses compare-and-swap rather than merely "a transaction."

Installation succeeds only if:

- The expected brief generation is still active.
- The expected base record is still active.
- Every referenced input and successor record still exists with the expected
  immutable ID and usable state.
- Every input entry has a valid disposition.
- Every referenced output item key exists in the response.

Within one transaction Lore:

1. Creates immutable brief-item records.
2. Creates the new generation in `staged` state.
3. Writes generation membership and source-accounting relationships.
4. Activates the new generation.
5. Supersedes the prior generation.

New guidance entries created while the compiler runs do not invalidate the
installation if they were not part of its input; they remain recent for the
next capsule. If compare-and-swap fails, no partial generation becomes active
and the caller must obtain fresh compiler input.

Journal entries are not marked consumed. `brief_inputs`,
`brief_item_sources`, and `brief_equivalents` record how a generation accounted
for them while leaving them available for inspection and from-scratch
reconstruction.

### 12.5 Coverage classification

When a guidance entry originates from a recorded context, the compiler can
compare it against both the active store and the exact record IDs emitted in
that context. The model proposes semantic equivalence; Lore verifies whether
the referenced records were actually rendered.

Initial classifications are:

| Classification | Meaning |
| --- | --- |
| `novel` | No semantically equivalent active guidance existed |
| `covered-unrendered` | Equivalent guidance existed but was absent from the originating capsule |
| `covered-rendered` | Equivalent guidance appeared in the originating capsule |
| `refinement` | Related guidance existed, but the correction adds or changes a condition |
| `unknown` | No originating context or confident semantic match exists |

`covered-unrendered` points to Lore's selection layer: exclusion, truncation,
ranking, the wrong lane, or a stale generation. `covered-rendered` only proves
that coverage was present; it does not claim whether the cause was wording,
placement, conflict, attention, or compliance.

The repeated-correction rate partitioned by these classifications is the
primary cheap measure of whether Lore is reducing repeated user requests.

### 12.6 Condition-loss lint

Source relationships allow Lore to detect suspicious scope generalization. If
every explicit source scope contains `repo-id=my_repo` and the resulting brief
item omits `repo-id`, Lore should flag the item before activation.

Lint strength depends on the source:

- Dropping metadata explicitly attached to every source entry is a strong
  warning.
- Dropping a value merely shared by every origin context is a weak heuristic.
- If at least one source was already unqualified, no condition-loss warning is
  implied.
- Adding a scope that no source carried and that the sources' origin contexts
  do not all share is a strong warning: an invented condition hides the item
  from every load that passes that key.

The lint does not automatically reject semantic generalization. A correction
first observed in one repository may genuinely be general. The warning makes
the inference visible to the compiler or maintenance agent.

### 12.7 Compiler behavior

The default compiler instructions should be short and inspectable:

- Preserve concrete user corrections.
- Account for every supplied guidance entry.
- Merge equivalent entries and retain their source relationships.
- Keep independently changeable instructions in separate items so each can be
  corrected on its own; retain necessary conditions with its rule.
- Retain conditions that explain apparent contradictions.
- Prefer instructions that describe the desired behavior, not only what to
  avoid.
- Defer material that cannot be represented safely and concisely.
- Remove redundant wording.
- Do not invent preferences absent from the material.
- Keep the whole brief concise; it is loaded on every invocation.

Compiler input MUST NOT contain practice tallies or marks. Repeated retrieval
or a prior model's usefulness rating is not evidence of correctness. Preserve
explicit corrections and rarely needed safeguards unless supported evidence
changes their applicability. `close <receipt>` is optional bookkeeping only;
it accepts no marks, records no new mark rows, and cannot activate learning.
Historical marks MAY remain available through explicit inspection.

The compiler may itself be represented as a small Lore agent. This is the
preferred dogfooding path once harness adapters exist.

### 12.8 Briefs are replaceable caches

Raw entries remain inspectable after compilation. A later agent may rebuild the
brief from the full guidance journal, normalize metadata, or correct a poor
generalization. Old generations remain immutable and may be retained according
to ordinary history policy. Repair activates a new generation rather than
rewriting an old one.

## 13. Tool Definitions and Scripts

### 13.1 Semantic indirection

An agent should depend on a stable name and description:

```text
recall-memory
complete-pr-diff
trace-go-callers
```

It should not need to know whether a tool is implemented using a vector store,
`rg`, a shell script, an MCP adapter, or another CLI.

### 13.2 Executable tool YAML

Tool bodies use YAML because it is compact, readable by models, and easy to
embed in exported bundles:

```yaml
version: 1
description: Search long-term memory for relevant prior experience.
exec:
  argv:
    - qmd
    - search
    - --json
    - --
    - "{{ query }}"
  stdin: none
  timeout: 30s
input:
  query:
    type: string
    required: true
output:
  format: json
```

The record envelope supplies the tool's mechanically resolved `name`; it is not
duplicated inside the body. Only fields necessary to execute the tool require
validation. Unknown fields are preserved and may be shown to agents.

Implementations should execute an argument vector directly rather than
construct a shell string. A shell script may still be the named executable.
Lore provides no sandbox; the harness determines which tools an agent may
reach.

Argument-vector execution prevents shell-string interpolation but does not
prevent an executable from interpreting a value beginning with `--` as an
option. Placeholders must occupy an entire argument; Lore must not split one
input into several arguments or interpolate a value inside another argument.
The tool author inserts `--` where the executable supports it or passes
arbitrary data through stdin. Lore must not add `--` automatically because the
convention is not universal.

### 13.3 Registering an agent-authored script

An agent may create and test a script in its workspace, then register it:

```bash
lore tool add pr-review complete-pr-diff \
  --script ./complete-pr-diff.sh \
  --description "Retrieve every changed file when a PR patch is truncated" \
  --context ctx_72 \
  --meta tool=github \
  --meta condition=truncated-diff
```

Lore copies the script into a managed artifact directory and stores an
immutable, named tool record referencing the managed path. Updating the script
creates a new record and artifact and supersedes the old definition. Contexts
that rendered the older definition continue to refer to its original record.
Optional `--context` records the episode that created or updated the tool and
must belong to the owning agent.
The context capsule includes the name, description, declared input names and
required markers, and useful metadata. Executable details and script contents
are loaded only when inspected or executed.

### 13.4 Shared tools

Tools may be associated with one agent, multiple agents, or a conventional
shared namespace. The first implementation may express this through metadata
or explicit names without introducing a separate role model:

```yaml
agent: shared
lane: definition
kind: tool
name: recall-memory
meta:
  available-to: pr-review
  available-to: evidence-reviewer
```

The exact sharing convention is intentionally revisable. It must remain
visible to and repairable by a model. Resolution always checks an agent-owned
active name before the `shared` namespace, allowing a local definition to
shadow the shared tool without changing other agents.

### 13.5 Calling tools

```bash
lore call --context ctx_72 complete-pr-diff \
  --input '{"repo":"acme/payments","pr":1842}'
```

Behavior:

1. Resolve the named tool visible to the context's agent, checking its own
   namespace before `shared`.
2. Validate only its mechanically required input.
3. Substitute each value as one whole argument or through the declared stdin
   mode, without shell-string interpolation.
4. Execute through the configured adapter or directly.
5. Return stdout on stdout and diagnostics on stderr.
6. Preserve the executable's meaningful nonzero outcome.

Lore may optionally record a compact call result as an entry, but automatic
tool telemetry is not required in v0.

## 14. Agent Composition

### 14.1 Related-agent catalog

An agent capsule may advertise smaller agents:

```md
## Available agents

- `evidence-reviewer`: Validate whether a proposed finding is demonstrably
  supported.
- `comment-editor`: Rewrite a finding as a concise review comment.
```

Only the name and short description appear initially. Full instructions are
loaded on demand:

```bash
lore load evidence-reviewer \
  --context ctx_72 \
  --task "Validate a suspected goroutine leak"
```

The child inherits ambient metadata through `ctx_72`, records that identifier
as its parent, replaces explicitly supplied keys as described in §9, and
produces a new context receipt. Guidance later appended
against the child records the child as its origin without automatically using
the inherited metadata as scope.

### 14.2 Invocation modes

The returned capsule is valid in three modes.

**Inline mode:** The current model temporarily applies the capsule and returns
the requested narrow output in the same context window.

**Subagent mode:** The harness starts a fresh model context using the capsule
and returns the result to the parent.

**External mode:** An adapter starts another harness or process with the
capsule.

Lore itself implements none of these inference modes. Harness capability
determines which one is used.

### 14.3 Encapsulation

Small agents should generally define:

- A narrow purpose.
- A compact method or decision lens.
- Relevant tools.
- A simple expected output shape.

Example:

```md
# Evidence Reviewer

Determine whether a proposed finding is demonstrably supported.

Return one of:

- `CONFIRMED` — include evidence and consequence.
- `REJECTED` — explain why the claim does not hold.
- `UNCERTAIN` — identify the missing evidence.
```

This is linguistic encapsulation, not process isolation. It is intentionally
good enough rather than perfect.

## 15. Signals, Reminders, and External Invocation

### 15.1 Unified signal model

Reminders, scheduled work, and external events are the same primitive: a
signal with an optional addressee and an optional future availability time.
Without an addressee it belongs to the conventional `shared` namespace and
every agent's capsule carries it; an addressee is needed only for wake-up.

```bash
lore signal pr-review \
  --at 2026-09-04T18:00:00Z \
  --dedupe-key "my_repo:pr-1842:recheck-ci" \
  --meta repo-id=my_repo \
  --meta pr=1842 \
  --subject "Recheck PR after CI" \
  --body "CI should be complete; revisit the concurrency finding."
```

An external system may emit an immediate signal:

```bash
lore signal pr-review \
  --subject "CI completed" \
  --dedupe-key "github:my_repo:pr-1842:ci-run-938" \
  --meta repo-id=my_repo \
  --meta pr=1842 \
  --stdin < ci-result.json
```

External emitters may supply an opaque dedupe key. Lore treats recipient plus
dedupe key as unique among nonterminal signals, allowing retrying producers to
avoid duplicate wake-ups without Lore interpreting the key.

Signal bodies may contain arbitrary or very large external data. Structured
`load` output includes the subject, a capped excerpt, a truncation indicator,
and an inspection command. The complete body remains available through
`inspect`. Native harnesses decide where the signal belongs in model context.

### 15.2 Awareness without wake-up

On `lore load`, due unacknowledged signals appear in the context capsule with
their current delivery state. Loading is side-effect-free and never leases a
signal. This requires no daemon and satisfies schedule awareness for agents
that are invoked regularly.

### 15.3 Wake-up

Actual future invocation requires an external clock and harness adapter:

```bash
lore tick --claim --lease 5m --format json
```

An OS timer, cron job, or small resident process calls `tick`. With `--claim`,
Lore atomically leases due signals and returns their envelopes and lease
tokens. The caller may start the addressed agents through a configured harness.
Lore does not keep an agent loop alive.

The minimal delivery lifecycle is:

```text
pending -> leased -> acknowledged
             |
             +-- lease expires -> pending
```

A slow or failed harness launch therefore does not permanently strand a
signal, and concurrent tickers do not launch the same signal while a lease is
valid. A leased signal is acknowledged using its lease token:

```bash
lore signal ack sig_01K4... --lease lease_91
```

An implementation may represent the mechanical state separately from the
immutable signal body:

```sql
CREATE TABLE signal_delivery (
    record_id     TEXT PRIMARY KEY,
    agent         TEXT NOT NULL,
    available_at  TEXT NOT NULL,
    dedupe_key    TEXT,
    state         TEXT NOT NULL,
    lease_token   TEXT,
    leased_until  TEXT,
    acknowledged_at TEXT
);

CREATE UNIQUE INDEX signal_dedupe
    ON signal_delivery(agent, dedupe_key)
    WHERE dedupe_key IS NOT NULL;
```

Only `tick --claim` leases signals. Ordinary `load` and read-only `tick` calls
must not modify delivery state.

Recurring schedules are optional after v0. One-shot signals plus an agent that
creates the next signal are sufficient to validate the model.

## 16. CLI Surface

### 16.1 Core commands

```text
lore load       Resolve an agent into a context capsule.
lore append     Add a generic record to an agent journal.
lore inspect    Return raw or filtered agent state.
lore put        Create an immutable named version and supersede its predecessor.
lore disable    Retire an active semantic record without deleting history.
lore call       Invoke a named executable tool.
lore signal     Create or acknowledge an inbox item.
lore tick       Read or lease due signals for external delivery.
lore context    Inspect, pin, or garbage-collect context receipts.
lore hooks      Install, remove, or explicitly activate a harness adapter.
```

### 16.2 Convenience commands

```text
lore note
lore avoid
lore prefer
lore remember
lore state get
lore state put
lore compile
lore tool add
lore brief put
lore export
lore import
```

Convenience commands should remain explainable as compositions of core
operations. `note`, `avoid`, and `prefer` default to the `guidance` lane;
`remember` defaults to `recall`. `state get` resolves the active named state;
`state put` is a capped, compare-and-swap specialization of immutable `put`.

### 16.3 Agent-oriented output

- Structured commands default to JSON or provide `--format json`.
- `load` defaults to context-ready Markdown.
- Structured `load` output separates `instructions` from `recall` and `signals`.
- Data goes to stdout.
- Diagnostics go to stderr.
- Core data commands are noninteractive. An explicit harness supervisor may
  attach to and wait for an interactive child.
- No colors, paging, prompts, or decorative tables are emitted in agent mode.
- Identifiers remain stable and opaque.
- Mutations accept `--stdin` to avoid shell-escaping large model output.

### 16.4 Suggested exit codes

| Code | Meaning |
| ---: | --- |
| 0 | Success |
| 2 | Invalid invocation or input |
| 3 | Agent, record, tool, or signal not found |
| 4 | Store unavailable or transaction failed |
| 5 | Tool or external adapter failed |
| 6 | Unused; capsules are never cut for size |
| 7 | Compare-and-swap or lease conflict |

Exact numeric assignments are less important than stable, documented
behavior. An explicit harness-supervisor command may preserve the launched
harness's normal status and use the shell convention `128 + signal` on Unix.

## 17. Harness Integration

### 17.1 Generic instruction-file integration

A portable `AGENTS.md` or equivalent may contain this ordered protocol:

```md
If this episode already contains a `[lore-context=...]` capsule, it is loaded:
follow it and do not load it again. Otherwise load the explicitly requested
agent with a concise, non-sensitive task purpose and useful ambient metadata.
Keep the complete task in the harness conversation. When no agent was
requested, use the implementation's discovery agent if it defines one.
Repository instructions may additionally authorize exact, checked-in agent
names; a missing definition may be installed from that repository pack and
loaded with the original discovery receipt without reloading the immutable
discovery capsule.

Apply the capsule without replacing the original task or higher-priority
instructions. Keep every context receipt paired with the agent that produced
it; record and state IDs are not context receipts. Use the target agent's
receipt for calls and reusable corrections. Add metadata to a write only when
explicitly scoping it. The task label is stored on its context receipt; never
copy secrets, credentials, authorization material, raw external content,
transcripts, or task-only instructions into records, state, signals, or tools.

To delegate, begin the child's task with
`lore load <agent> --task "<concise non-sensitive purpose>" --context <parent-receipt>`
and put the complete task on the following lines. The child runs that prelude
and returns its new receipt. At a meaningful boundary, reflection may produce
zero durable updates; updates to the parent episode use the parent agent's
receipt, not a new reflector receipt. Use `lore inspect` to explain or repair
an agent.
```

This mode works anywhere an agent can invoke a CLI. It relies on ordinary model
compliance and makes no assumption about the harness's subagent tool or role
naming.

### 17.2 Native adapter

A native adapter may:

- Call `load` before the first model inference.
- Place the capsule in the harness's top-level agent instructions.
- Translate related-agent loads into real subagent calls.
- Carry ambient metadata and context identifiers automatically without turning
  origin metadata into record scope.
- Preserve instruction and signal placement as separate inputs.
- Supply a bounded episode result to a configured reflector when a harness
  chooses to trigger reflection.
- Trigger compile operations through its configured model.
- Deliver due signals as new harness invocations.

Native integration improves ergonomics but does not change Lore's data model.

An implementation that installs user-scope lifecycle hooks must distinguish
global invocation from activation. The harness may invoke a cheap gate for
every session, but Lore work is conditional on an explicit wrapper such as:

```text
lore hooks install (--claude|--codex)
lore hooks uninstall (--claude|--codex)
lore hooks run <agent> [--meta key=value]... (--claude|--codex) [-- HARNESS_ARGS...]
```

Before launching the harness, the wrapper verifies that every required
lifecycle event has the current Lore executable's complete canonical,
unfiltered handler group in the selected settings file. An owned handler under
a restrictive parent matcher is not sufficient. A missing, partial, filtered,
or stale installation is an actionable adapter failure and the harness is not
started; silent launch without injection is never success. When the selected agent is the
implementation's bootstrap agent, the wrapper performs the same idempotent
starter seeding as a manual bootstrap load before checking its base.

The adapter selection supplies authoritative `harness=<name>` ambient
metadata. An absent value is added, an identical explicit value is accepted
without duplication, and a conflicting explicit value is invalid input. The
known harness facet therefore cannot disappear from native loads or be
misstated by a copied example.

The wrapper owns a short-lived unguessable capability, binds it atomically to
the first eligible harness session identifier, and attempts revocation when
the process or session ends. Admission must combine process-tree-inherited
proof, best-effort owner-PID liveness, and a bounded renewable expiry rather
than trusting an ambient activation flag. Because a PID is not a process-birth
identity, adapters must state the bounded PID-reuse caveat when a surviving
descendant can retain and renew the proof. Ordinary startup in a nested harness
that inherits the environment must not displace the bound session. Outside
that live binding, the installed gate exits successfully and emits no bytes
before it decodes lifecycle input or opens Lore's config/store.

If a harness changes session identifiers for clear/resume without a preceding
transition reason, its lifecycle JSON may be insufficient to distinguish a
root transition from the same transition in an inheriting nested process. An
adapter may accept that documented transition only if it states the resulting
process-tree activation scope and does not claim stronger process identity.

For a bound session, `UserPromptSubmit` is the reliable task boundary. The
first real prompt in an episode atomically claims and loads a fresh capsule
with the exact prompt as the receipt task and emits it through the harness's
documented model-context response. Concurrent delivery must not create
duplicate receipts. Later prompts in that episode do not duplicate the full
capsule. When a harness reports compaction, the adapter may replay an ephemeral
cached copy without loading the store or creating a new receipt. A resume may
replay only a cache from the same still-live wrapper. Clearing a session starts
a new episode that waits for its first real prompt; the latest context
identifier may parent that new load.

In an activated run, `Stop` may give one brief reminder per stop cycle while
the receipt is open: save any already-known durable correction or changed
state using that receipt; zero writes is valid and no reflection task should
be manufactured; plain `lore close <receipt>` is optional bookkeeping, and
the session may stop without closing. The reminder must not assign record
inspection, scoring, reflector loading, or compilation. It performs no writes,
background reflection, or model calls. A stop following the hook's own
continuation (`stop_hook_active`), a closed receipt, or a run that never loaded
is silent. Closing a receipt still ends the episode: the next real prompt
loads afresh with that receipt as parent.

Because native integration persists that first prompt verbatim, callers whose
prompt contains secrets, credentials, authorization material, or raw external
content must use portable manual loading with a concise, non-sensitive purpose
instead. Implementations must disclose this distinction in hook help.

The wrapper may accept repeatable `--meta key=value` ambient metadata. It must
validate that multimap before opening or mutating the store, keep it in the
ephemeral harness-neutral run state, and apply it to every fresh episode load.
It therefore participates in ordinary record filtering/ranking and is recorded
on each context receipt; replaying a cached compact/resume capsule performs no
new metadata or receipt operation. Implementations must bound its encoded size
before launch, reserve sufficient marker space for the adapter's maximum cached
capsule, and reject every over-limit state mutation before replacement. A
successful marker write must always remain readable by the next hook.

Installation must be idempotent, use recognizable ownership markers, merge
with unrelated JSON settings and hook handlers, and remove only owned entries.
Harness-specific paths, event schemas, and wire casing stay behind a shared
adapter contract rather than entering the knowledge model. An adapter should
not read unstable transcript files, capture a transcript by default, or turn
session-end into unconditional reflection. Harness trust review and hook
permissions remain the harness's security boundary.

An adapter with a hard output or cache ceiling refuses a capsule that does not
fit whole rather than cutting it: nothing is recorded as seen, and the session
is pointed at an in-session load. The ceiling is an adapter concern; core
`load` has no budget.

### 17.3 Graceful degradation

If a harness cannot create subagents, another agent capsule may be applied
inline. If it cannot wake on signals, due items appear on the next manual load.
If no compiler is configured, recent entries continue to appear directly. If
no semantic memory backend exists, `inspect` provides lexical access. If no
reflection adapter exists, the current agent can apply the reflector capsule
inline or omit reflection entirely.

## 18. Inspection and Self-Repair

Lore intentionally provides no required human management surface. Instead it
offers complete, machine-readable inspection:

```bash
lore inspect pr-review --include base,brief,journal,state,tools,signals,contexts --format json
lore inspect pr-review --query "comment length" --format json
lore inspect shared --kind tool --format yaml
lore inspect pr-review --coverage covered-unrendered --format json
lore inspect pr-review --lint condition-loss --format json
```

Inspecting a superseded record MUST preserve that record's historical envelope
and expose a separate `current` envelope for the latest replacement, including
its local `ref`, complete body, scope and actual status. A disabled successor
MUST NOT be presented as active. A record without successors omits `current`.
Following a corrupt cycle MUST terminate with an error. A stale replacement
write MUST remain a conflict and provide an inspection path to the successor;
it MUST NOT silently apply the write to a different identity. This forward
path lets a search for obsolete wording recover the current lesson.

Inspecting a brief-item record also returns `sources: {recorded: [], current: []}`.
Both arrays contain complete record envelopes. Recorded links retain the
historical evidence (including retags); current sources follow replacement
chains and deduplicate successors in first-source order. Status and metadata
remain explicit, so a disabled source is not presented as an active repair
target. A standalone imported item has empty arrays. Current source text is
not a claim that a historical summary still represents it.

Example user interaction:

> Inspect the PR review agent. It has started overexplaining findings. Tell me
> why.

An agent may discover that several requests for stronger evidence were compiled
into "provide detailed explanations." The user responds:

> Fix it. Evidence should be detailed; prose should be concise.

The agent can then append the correction, create a superseding definition, or
run the compiler:

```bash
lore prefer pr-review \
  "Keep evidence concrete and detailed, but keep explanatory prose concise."

lore compile pr-review
```

The model is the configuration explorer and editor. Lore only needs sufficient
read and write primitives for that model to operate.

## 19. End-to-End Example

### 19.1 Start

The user says:

> Use the Lore PR review agent on PR 1842.

The harness or current agent calls:

```bash
lore load pr-review \
  --task "Review PR 1842" \
  --meta repo-id=my_repo \
  --meta language=go \
  --meta pr=1842
```

Lore returns `context_id=ctx_72` with the capsule and its exact rendered-record
snapshot.

### 19.2 Narrow consultation

The PR agent finds a possible leak and loads a narrower capsule:

```bash
lore load evidence-reviewer \
  --context ctx_72 \
  --task "Determine whether this goroutine can outlive the request"
```

The harness may run that capsule inline or in a subagent.

### 19.3 Draft correction

The user reviews the proposed comment and says:

> More concise, with evidence.

The current agent revises the comment and records:

```bash
lore prefer --context ctx_72 \
  --meta phase=review-comment \
  "Lead with concrete evidence and default to concise review comments."
```

The entry records `ctx_72` as its origin. Only the explicitly supplied
`phase=review-comment` value scopes it; `repo-id` and `pr` are available to the
compiler through the origin context but are not copied onto the entry.

### 19.4 Tool creation

The agent discovers that the standard GitHub patch is truncated, writes a
small recovery script, tests it through the harness, and registers it:

```bash
lore tool add pr-review complete-pr-diff \
  --script ./complete-pr-diff.sh \
  --description "Fetch complete changed-file contents for a pull request" \
  --context ctx_72 \
  --meta tool=github \
  --meta error=truncated-diff
```

### 19.5 State and reflection

The review is blocked on CI. At this meaningful episode boundary, the current
agent loads the reflector capsule inline or through a subagent:

```bash
lore load reflector \
  --context ctx_72 \
  --task "Reflect on the review now blocked on CI"
```

The reflector decides the current status should survive the invocation and
updates named state through compare-and-swap:

```bash
lore state put pr-review/working \
  --context ctx_72 \
  --expect state_17 \
  --stdin <<'YAML'
goal: complete-review
status: waiting
subject:
  repo-id: my_repo
  pr: 1842
waiting-on:
  type: ci
next-action: recheck shutdown evidence after CI
YAML
```

No guidance or recall write is required merely because reflection ran.

### 19.6 Reminder

The agent schedules a later check:

```bash
lore signal pr-review \
  --at 2026-09-04T18:00:00Z \
  --dedupe-key "my_repo:pr-1842:recheck-ci" \
  --subject "Recheck PR 1842 after CI" \
  --meta repo-id=my_repo \
  --meta pr=1842
```

### 19.7 Later compaction

After several corrections accumulate:

```bash
lore compile pr-review
```

The compiler emits a new generation of metadata-bearing brief items, accounts
for every input entry, and installs the generation through compare-and-swap.
Future invocations receive eligible items from that generation plus guidance
added or deferred after the compilation.

## 20. Minimal Implementation Plan

### Phase 1: Persistent capsules

- SQLite store in WAL mode.
- Immutable records, fluid kinds, mechanical lanes, and metadata operations.
- `load`, `append`, `inspect`, and `put`.
- Named, size-capped state with immutable replacement and compare-and-swap.
- Base, itemized brief generation, and recent-guidance rendering.
- Context identifiers and exact emitted-record snapshots.
- Explicit origin-versus-scope behavior.
- Metadata conflict exclusion and overlap prioritization.
- Deterministic token estimation, brief floors, and recent-entry caps.
- Basic context retention and garbage collection.

This phase establishes the receipt needed to determine whether later
corrections were novel, unrendered, or already rendered.

### Phase 2: Tools and small agents

- YAML executable tool records.
- Managed script artifacts.
- `call` and `tool add`.
- Whole-argument substitution and stdin input modes.
- Agent-owned-before-shared name resolution.
- Related-agent summaries and nested `load`.
- Ambient context inheritance for nested operations.
- A small reflector capsule and selective episode-boundary invocation.
- A stable `recall-memory` adapter contract; an optional `zvec-grep` backend
  can be installed without changing Lore's schema.
- Generic integration instructions for existing harnesses.

### Phase 3: Compilation

- `compile-input` and `brief put`.
- Total input accounting and source relationships.
- Coverage classification and condition-loss linting.
- Compare-and-swap generation installation.
- Configurable external compiler command.
- Optional `lore-maintainer` agent.
- Automatic compilation thresholds only after explicit compilation works.

### Phase 4: Signals

- Immediate and future-addressed signals.
- Dedupe keys and capped signal excerpts.
- Side-effect-free due-signal inclusion in `load`.
- `tick --claim`, expiring leases, and acknowledgment.
- Optional harness wake-up adapters.

No phase requires a daemon, an embedding-aware Lore schema, a web service, or
a human UI. An external recall adapter may use embeddings without changing
that boundary.

An optional foreground stdio MCP adapter may expose the ordinary operations
through a stable tool list, taking explicit agent names or context receipts.
It must not depend on connection-local current-agent state or turn a tool call
into a change to the advertised list. This transport adds no daemon, network
service, or harness requirement. Its concrete contract may live in a separate
transport document such as [docs/mcp.md](docs/mcp.md).

## 21. Acceptance Criteria for v0

An implementation is sufficient when it can demonstrate all of the following:

1. A named agent can be created with base instructions.
2. `load` returns a coherent capsule with all eligible instructions intact and reports its
   estimated size.
3. A correction appended in one invocation appears in the next invocation.
4. `load` returns an immutable context receipt containing every emitted record
   ID.
5. A correction records its origin context without inheriting ambient metadata
   as scope.
6. A named state update is size-capped, uses compare-and-swap, preserves its
   prior immutable version, and appears losslessly on the next eligible load.
7. Arbitrary metadata is preserved and rendered without prior schema
   definition; disjoint value sets on a shared key exclude a record.
8. The active brief is a generation of independently selectable,
   metadata-bearing items that a burst of recent guidance never evicts.
9. Guidance, recall, and state remain mechanically distinct, and unknown kinds
   default to recall.
10. A compiler adapter accounts for every input entry and installs a generation
   through compare-and-swap without marking source entries consumed.
11. Corrections can be classified as novel, covered-unrendered,
    covered-rendered, refinement, or unknown using their originating capsule.
12. Condition loss from source entries to brief items can be surfaced as a
    lint warning.
13. A YAML-defined tool backed by an immutable script artifact can be
    registered and called without splitting substituted arguments.
14. Agent-owned named definitions shadow shared definitions predictably.
15. One agent can load another agent's capsule with inherited ambient context
    without Lore deciding whether a subagent exists.
16. A reflection episode can make zero writes or update state, guidance,
    recall, signals, or tools solely through existing documented operations.
17. A future signal appears after its availability time, can be deduplicated
    and leased by `tick --claim`, and returns to pending after lease expiry.
18. A separate agent session can inspect the complete state, explain an
    undesirable behavior, and repair it using only documented CLI operations.
19. Corrupt optional records do not prevent the remaining agent capsule from
    loading; mechanical errors are returned clearly.
20. Concurrent local invocations do not corrupt the store or activate
    conflicting brief generations.

## 22. Deferred Decisions and Tripwires

Each open question remains deferred until a concrete observation reopens it.
A tripwire supplies evidence, not an implementation mandate. When one fires,
preserve the observation as a reproducible case, choose the smallest response
that handles that case, and record both the observation and the decision.
Aesthetic preference, anticipated scale, and vague discomfort are not
tripwires.

| Deferred question | Tripwire | Smallest likely response |
| --- | --- | --- |
| Product name | The first public repository, installable package, or persistent external reference | Check package and search collisions before committing; sharing a draft with another model does not count. |
| Metadata normalization | The same materially identical repair is made twice in real records | Make the third repair a named script or tool; only move behavior into core after that indirection proves insufficient. |
| Compilation adapter | External-command setup causes an observed failure or workaround | Fix the demonstrated friction with documentation, dependency checks, or one narrow built-in adapter while retaining external commands as the general path. |
| Tool-output recording | A later episode needs mechanically available call facts that were lost, or recorded output produces measured noise, latency, or exposure | Record a compact receipt or selected failure for the implicated tool. Keep full output explicit and capped; do not default to transcript capture. |
| Recurring schedules | A requested recurrence cannot be reliably expressed as a one-shot signal that schedules its successor | Add a next-occurrence helper first. Add a core recurrence parser only after repeated misses or a need to schedule without a model. |
| Shared bundle storage | A bundle is used by another machine or person and actual drift or synchronization pain occurs | Use ordinary Git when it fits. A registry requires separate evidence that Git is the source of friction. |
| Recall backend | A known record is missed by a real lexical and metadata query | Save the miss as a retrieval fixture, then try query repair, FTS, or an external backend such as `zvec-grep`. Core embeddings require a stable need that cannot remain behind `recall-memory`. |
| Capsule entry and exit conventions | A later invocation demonstrably misreads a capsule's outputs | Tighten that capsule's output contract first. Promote markers into a protocol only after the ambiguity repeats across capsules or harnesses. |
| Aggregate export | Someone manually exports data to answer a real counts, trends, or frequency question | Add the smallest query or export shaped by that question. SQLite, CSV, JSONL, DuckDB, or Parquet remain implementation choices. |
| Reflection cadence | Durable state or learning is repeatedly missed at the same meaningful episode boundary | Add an explicit harness trigger at that boundary; do not introduce unconditional per-action reflection. |
| State conventions | The same malformed or ambiguous state pattern requires repeated repair | Add a state-specific agent, validator, or migration tool while keeping the stored YAML semantically open. |

These are extension points, not prerequisites. Tripwire observations and their
decisions are themselves ordinary Lore history: use recall for the case,
guidance for a durable operating rule, and state only for what is true now.

## 23. Summary

Lore is intentionally an embarrassingly small sidecar:

```text
load a named context
append what should be remembered
carry a small versioned working state
inspect and repair through an agent
call named executable indirections
carry signals into later invocations
reflect selectively at meaningful boundaries
occasionally compile the journal
```

Its central constraint is recursive simplicity: the system stores the kinds of
text, metadata, YAML, and scripts that its own agents can understand and edit.
If Lore becomes too complicated for an ordinary model session to spelunk and
repair, it has violated the premise that gives it value.

Lore should infer meaning, but never infer history it could have recorded
mechanically.
