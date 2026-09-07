# Learning through ordinary work

The working model makes the semantic decisions while the evidence and the
user's intent are available. Nine-tails stores those decisions, protects their
scope and lineage, and projects the current result on the next load. It does
not need a second model service or a successful compilation to learn.

## Apply a lesson while the evidence is available

When a failure, user correction, or changed plan exposes a useful decision,
consult relevant guidance and recall before repeating the attempt. Inspect the
complete source when a preview is insufficient; check its conditions against
the current task and artifacts. Choose one concrete change to the next action,
then check the result against user feedback, a test, a tool result, or another
observable outcome. A retrieved memory is a candidate, not proof that its
advice applies.

Capture a supported correction while that evidence is still available. Prefer
updating the existing lesson over adding a second account. Keep the trigger,
useful action, evidence, and exception that affect a future decision. An
unverified idea stays uncertain recall; changing project status belongs in
state. An explicit user preference does not need an experiment to establish
what the user wants. Routine success need not produce a memory.

This is a behavior of the working harness model. Codex, Claude, or another
harness can perform it inline, or delegate a focused review with the parent
receipt and a bounded summary of the actual feedback. It requires no lifecycle
hook, resident reflector, transcript recorder, or separate model service.

The research behind this change is collected in the
[reflection](research-learning-reflection.md),
[retrieval](research-learning-retrieval.md), and
[architecture](research-learning-architecture.md) reviews. Those papers motivate
feedback-grounded iteration; their benchmark gains do not establish gains for
nine-tails.

## What to do when

| What happened | Action | What carries forward |
| --- | --- | --- |
| The user gives a durable preference | `prefer`, `avoid`, or `note --context <receipt>` | Applicable guidance on the next load |
| A known instruction needs correction | Inspect it, then write with `--supersedes <ref>` | Replacement wording; old scope stays unless explicitly changed |
| Several instructions or experiences express the same lesson | `consolidate --source <ref> --source <ref> --context <receipt> --reason "..." "replacement"` | One same-lane replacement with its exact predecessors and the decision |
| An old defect report or instruction is obsolete | Inspect it, then `disable <ref> --context <receipt> --reason "..."` | Inspectable history and reason; no future automatic surfacing |
| An experience might help but is not a general rule | `remember --context <receipt> "..."` | Task-relevant evidence, with its date and provenance |
| A project decision or result changes | `state put` with the current expected ID | Current named state; linked roles receive it on their next load |
| Several roles should use the same standing guidance | `agent follow <role>/<alias> <owner> --expect none --meta repo-id=<project>` | The owner's current applicable guidance, with its source and correction history |
| Nothing durable changed | Keep working | No write |

Explicit user instruction is evidence of what the user wants. A model's guess
about that preference is not equivalent evidence. Store an uncertain experience
as uncertain recall, including the circumstance that made it useful. Avoid
promoting one successful workaround into a universal instruction.

Before adding a lesson, check the current capsule and, when necessary, search
the agent's existing records. Correct or consolidate the existing rule if it
already covers the case. Do not accumulate a new contradictory paragraph and
expect the next agent to resolve it again.

## Handing work to another invocation

Deliver the complete capsule, not only its `instructions` field. Markdown is
already complete. A JSON/YAML consumer must also deliver referenced state,
selected recall, library navigation and signals as labeled data; keep those
boundaries and avoid copying owned state twice. Omitting those sections gives
the model less context than its receipt records. Render the original result
once rather than loading again to repair the handoff.

Keep a changing handoff in the role's named state and update it with CAS.
Record the stable repository identity, commit or version, repository-relative
implementation and contract paths, completed validation, and remaining work.
The next invocation binds that repository identity to its selected checkout,
resolves the stored paths from that repository's root, and verifies the version
and artifacts before relying on them. Missing or renamed artifacts need fresh
resolution; an old absolute checkout path never selects the next workspace.

Store selection is separate from artifact location. The normal user store is
shared across projects. When deliberately using another store through `--home`
or `NINE_TAILS_HOME`, generated capsule and library commands carry that selected
home so a copied command cannot silently use another store's local reference.
That absolute command path belongs to the current invocation and is regenerated
on load; keep durable artifact references repository-relative. Preserve the
store selection when translating a recipe to a project wrapper.

## Sharing guidance without copying a persona

Choose one owner for shared user preferences. A role can opt in with a named,
scoped subscription:

```sh
./nt agent follow game.engineer/project workshop --expect none --meta repo-id=soccer-chess
```

On the next game.engineer load, applicable workshop guidance appears with its
source. Correct it at workshop once; subsequent subscribers receive the current
version. The subscription includes neither workshop's identity nor its tools,
recall, state, or subscriptions. Keep changing project facts in named state and
use state links for those. Scope both the subscription and the guidance; supply
the invocation's stable repository identity on load.

Use `inspect <role> --include agents` to find subscriptions. Updating one needs
its current record as `--expect`; disabling it stops future delivery without
changing the source. Before retiring a copied local instruction, verify that
the shared source preserves its whole meaning and that the subscription covers
every context where it should apply. Keep role-specific exceptions local.

## Consolidation is semantic work

Read complete sources, including their kind, scope, conditions and exceptions.
Ask whether the same future decision would apply every source. Related subject
matter alone does not make two instructions interchangeable. Keep independently
changeable rules separate. Preserve the user's concrete intent; remove repeated
wording and superseded implementation claims.

Write the complete replacement and a concise reason identifying what was
combined or corrected. Nine-tails checks same owner, active exact sources and
identical lane and scope, then installs the entire replacement atomically. It cannot
mechanically prove that the replacement preserved meaning. The caller should
compare each original clause with the replacement and inspect the result.

Recall can use the same operation. Retain the situation, useful response,
supporting observation and valid exceptions in the replacement. Remove obsolete
test counts and changing project status; current facts belong in named state.
Recall stays experience, and guidance stays instruction. The new memory is
immediately available to retrieval and library browsing; there is no compile
checkpoint and no guidance-generation change.

For difficult overlap, give `reflector` the owning episode as parent and the
specific topic or source references in its task. It inspects full sources,
repairs supported overlap, and leaves independent meanings separate. A focused
review does not need to read the whole library or produce a minimum number of
writes. Its output explains changed and retained meanings and links the sources.

The source records remain intact. Their references lead through further
consolidations and corrections to current knowledge. Historical receipts keep
their exact delivered versions. This is a small graph of deliberate replacement
decisions, not a chain of summaries that gradually loses its evidence.

Optional briefs remain disposable projections. When a source was split across
several summary items, keeping only some of them in the next generation restores
the complete source unless it is explicitly accounted for again. Replacing a
correction with unchanged wording still keeps its obsolete predecessor out of
the current projection. Neither transition rewrites historical evidence.

## Forgetting has two different meanings

An observation can be irrelevant to this task without being obsolete. Leave it
out of this capsule and keep it available for other work. When preparing a child
or a new episode, the caller can search recall and select exact relevant records
with `load --recall <ref>`; every eligible selection is delivered, without an
arbitrary record-count limit. A harness-supplied whole-capsule transport ceiling can reject
an oversized load, but never silently drop a chosen memory. Without explicit picks,
lexical task/query search uses a soft context-size target and reports remaining
keyword matches with an inspection hint. Excerpts remain bounded; inspect
complete records when the decision requires their full evidence.

Retire a record only when evidence supports it: the defect is resolved with no
remaining recovery lesson, the user withdrew a preference, or the workflow no
longer exists. If a useful replacement exists, supersede or consolidate instead.
Age, absence from recent tasks, and repeated exposure are not evidence that a
conditional preference became wrong. Retirement preserves history and a reason;
it does not revive an earlier instruction automatically.

## Reviewing one episode

At a useful review or handoff, gather the episode's recorded evidence with:

```sh
nine-tails inspect <receipt> --review
```

The packet groups exact delivered records, writes originating in that episode,
and retirement decisions. It includes inactive writes and follows corrections
separately to their current versions. Short previews help identify a lesson;
use its inspection recipe to read full evidence before editing it. Follow the
returned continuation to see remaining entries. The response is a live view;
restart after new writes when needed.

Supply actual feedback from the current conversation or artifacts alongside the
packet when delegating a review. The packet contains neither that feedback nor
unsaved discoveries, and it does not establish that a delivered lesson was
applied. It reviews the exact receipt only; inspect a child's own receipt for
its work. A focused review may conclude that no durable change is warranted.

## Consulting a growing library

The capsule includes a memory-library count and a browse recipe when memories
exist, even when the current task retrieves none. The agent can consult that
library during work:

```sh
nine-tails inspect --page --context @42
nine-tails inspect --page --context @42 --query "passing lanes"
```

Each page contains short previews, full-record inspection links and a
`next.inspect` continuation command. Follow it to retrieve additional pages;
there is no arbitrary record-count ceiling. The continuation follows the last
returned entry, so the first omitted entry appears on the next page. Reading
full evidence uses ordinary `inspect <ref>`. Browsing never reloads the persona
or creates a receipt.
Page syntax errors are rejected before reference lookup or store access, so
an incompatible flag is still invalid input when the supplied receipt is unknown.

Pages are a live index, newest first. A retired cursor record still anchors its
original position; it is never redirected to a replacement. Keep scope and
query fixed while paging. Restart to change the query or include newer entries
before the cursor. Library substring search and load's keyword ranking are
different lookups; a load's next omitted match is not a library page cursor.

Standing instructions remain immediately applicable. This library is for
situational evidence and experience, not an excuse to quietly hide a user rule
because the agent has accumulated many of them.

## Capturing a real retrieval problem

When a useful memory did not surface, or a surfaced memory was inappropriate,
inspect it against the original receipt:

```sh
nine-tails inspect <memory-ref> --context <original-receipt> --query "<actual terms>"
```

The result retains the complete exact memory and receipt. `recorded_in_context`
answers whether that exact memory was delivered; `current` checks today's
lexical retrieval with the receipt's scope. It distinguishes no word match,
scope exclusion, supersession/retirement and a result outside the context budget.
It uses the real selector, so diagnostic and load cannot develop separate
ranking rules. The memory-library pointer advertises this check when needed.

The original query override, explicit-selection mode and excerpt bytes were
not retained. Supply the actual non-sensitive query when known, and do not
present today's selection as a historical reconstruction. A delivery receipt
also does not prove the full memory body was present or the model applied it.

Save the returned JSON/YAML as a repository artifact alongside a short rationale
for why the memory mattered. This preserves useful evidence after receipt GC
without creating more recall content or another store schema. See
[retrieval cases](retrieval-cases/README.md). Capture only problems encountered
in ordinary work; do not manufacture examples or make this a closing ritual.

An appropriate memory missed through different wording can justify semantic
retrieval. A stale handoff needs content repair, a scope mistake needs scope
repair, and a delivered lesson ignored by the model needs application/review
work. Embeddings cannot be inferred as the fix merely because learning failed.

## What this iteration removed

Marks no longer participate in learning. `close` accepts only a receipt and
creates no marks. Compiler input and scope lint no longer receive practice
tallies. Historical marks stay inspectable. Compilation remains an advanced
cache operation, omitted from ordinary help. Closing and compiling are never
required pauses in the learning flow.

## Actual repairs in this iteration

The nine-tails corpus contained overlapping instructions about brief repair,
tool discovery, and the product's domain. One repair instruction still said a
same-body scope change should preserve a compiled brief; its corrected version
said scope changes must invalidate it. Another tool instruction still described
MCP as a future proposal. These require source-aware reconciliation, not an
age threshold or a higher retrieval score.

A recalled defect report claimed receipt JSON omitted empty task and parent
fields, although current output includes them. That report can be retired with
the fix as its reason. Meanwhile “semantic learning” matched old semantic-search
experiments: those are different meanings of the same word. Explicit memory
selection addresses the caller's judgment; lexical fallback still has that
limitation.

## Remaining boundaries

Nine-tails does not yet detect paraphrases, infer contradictions, extract lessons
from a transcript, or rerank memories semantically on its own. The loaded model
must do that work. It now has atomic operations and a short protocol that make
those decisions durable. Better automation should propose inspectable decisions
with their evidence, then use these same operations.

Consolidation accepts guidance or recall within one lane and equal scope; ordinary supporting
or contradicting observation edges are not implemented. Source bodies remain
durable, but source receipts follow ordinary retention. Snapshot export/import
does not transport consolidation ancestry or retirement audits; use a store
backup when preserving that history is required.
