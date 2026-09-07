# Harness-driven learning: evidence and a small next step

Research date: 2026-09-07. Implementation baseline:
`6079afcd19bd01b294f78174c4d5ce2c18d7016d`. This is an architectural
assessment and implementation proposal, not a claim that a new learning loop
has already improved task outcomes. The specification and DESIGN remain the
behavioral and implementation authorities.

Nine-tails already has most of the storage mechanics needed for continuous
learning. The highest leverage change is to help the working model complete
the sequence from a concrete surprise to a better action, then preserve only
the supported lesson. Codex and Claude should perform that semantic work in
their current conversation. The portable core should supply reliable evidence
and exact update operations.

## What the papers support

**Reflexion** distinguishes acting, evaluating an attempt, and producing verbal
feedback for subsequent attempts. Its experiments use task outcomes, heuristics,
tests, and sometimes model evaluation; its memory contains reflections rather
than updated model weights. Its practical memory buffer is commonly only one
to three experiences. The relevant result is that feedback can change a later
attempt through context. It does not establish that every completed task needs
a saved reflection, that self-evaluation is always reliable, or that a small
episodic buffer is an appropriate long-term user preference store.
[Shinn et al., 2023, §3–4](https://arxiv.org/html/2303.11366v4).

**Agentic Context Engineering (ACE)** separates generation, reflection, and
curation. It stores itemized knowledge and merges local updates rather than
rewriting an entire accumulated prompt. Its context-collapse example and
incremental-update ablation motivate preserving detail and updating individual
entries. ACE also uses helpful/harmful counters and embedding-based
deduplication; those are mechanisms in its evaluated system, not requirements
for nine-tails. Its limitations acknowledge dependence on extracting useful
insights, and its evaluation-stage token accounting shows that larger contexts
can increase input cost even when adaptation becomes cheaper. These findings
support local edits and selective consultation, without proving this particular
harness workflow will improve.
[Zhang et al., 2026 revision, §2–3, §5 and Appendix A](https://arxiv.org/html/2510.04618v3).

**MemoryBank** combines stored interaction information, retrieval, updates, and
summaries for long-term conversational continuity. Its forgetting mechanism
depends on elapsed time and memory significance. Its demonstrated setting is
a companion chatbot, with real-dialog qualitative examples and simulated-dialog
quantitative analysis. This supports treating persistence and useful retrieval
as separate responsibilities. It does not justify deleting an explicit user
instruction because it was old or rarely relevant. Adopting its decay policy
would conflict with nine-tails' deliberate separation of task irrelevance and
evidence-backed retirement.
[Zhong et al., 2023](https://arxiv.org/pdf/2305.10250).

The recommendations below are our inference from those mechanisms and the
repository evidence. No paper directly evaluates nine-tails, its user store,
or its current Codex/Claude integrations.

## What the implementation already does

| Learning responsibility | Existing implementation | Remaining gap |
| --- | --- | --- |
| Carry instructions forward | Capsule renders applicable guidance immediately; compilation is optional | Delivery alone does not establish application |
| Preserve local corrections | Supersession, same-scope consolidation, retirement audits, immutable source bodies | Model must still judge meaning, conditions, and evidence |
| Consult experience | Lexical recall, explicit selection, paged library, exact inspection | Model must notice when its initial context is insufficient |
| Explain retrieval | Exact record versus receipt check uses the actual current selector | Old query override, selection mode, and excerpt bytes are unavailable |
| Maintain current facts | Named state with CAS and scoped subscriptions | Temporary project status can still be misclassified as recall |
| Selectively reflect | Portable protocol, reflector starter, optional hook reminder | Generic pause language does not explicitly connect an outcome to a changed next action |

These observations come from [capsule assembly](../internal/capsule/capsule.go),
[recall selection](../internal/capsule/recall.go),
[retrieval diagnosis](../internal/capsule/recall_check.go),
[the library](../internal/store/recall_library.go),
[consolidation](../internal/store/consolidations.go),
[retirement](../internal/store/retirements.go), and
[reflector](../internal/starter/reflector.yaml).

There is already concrete evidence that content maintenance matters:
[the Soccer Chess handoff case](retrieval-cases/soccer-chess-stale-handoff.md)
records delivery of a memory containing obsolete grid-game status during a
later 3D task. It is not evidence for a semantic-search failure or proof that
the memory caused a coding mistake. That distinction should survive every
improvement to the loop.

## Two changes worth implementing now

### 1. Give the loaded model an actionable learning trigger

Refine the common capsule protocol and reflector instructions around meaningful
events: a user correction, a failed attempt with a useful recovery, a changed
plan, or an outcome that contradicts existing guidance. The working model should:

1. Consult the relevant memory or instruction in full when its decision needs
   evidence; search the current library when initial recall is insufficient.
2. Identify what the observed result changes about the next action, with its
   conditions and uncertainty. Apply that change during the current work when
   it helps, then verify the result using the task's existing checks.
3. Correct an existing lesson or save bounded experience only if something
   useful should survive. Keep changing facts in named state. An explicit user
   preference is already evidence of intent and need not await a test.

This should replace vague wording rather than grow into a mandatory checklist
on every action. Successful persistence is evidence that a write worked;
successful application requires separate task evidence. Zero writes remains a
valid result. The model can do this inline; a focused reflector is useful when
reconciliation benefits from another pass.

Suggested ownership: one builder edits common protocol rendering and its
behavioral tests in `internal/capsule`, plus the reflector starter in
`internal/starter`. Contract edits stay with the integrating parent so parallel
branches do not compete over DESIGN, the specification, and the learning guide.

### 2. Offer a small read-only episode review packet

Proposed surface: `inspect <receipt> --review`, mirrored by
`nt_inspect` with an exact receipt target and `review: true`. The command gathers
existing evidence for the current model or a delegated reflector. It neither
asks another model to reflect nor produces a durable lesson automatically.

The useful packet contains the receipt's owner, purpose and scope, followed by
three explicitly labeled evidence groups:

| Group | Exact source | Interpretation |
| --- | --- | --- |
| Delivered records | `context_records`, in recorded order | Historical record identity; today's status or successor is a separate field |
| Episode writes | `records.origin_context_id` equals this receipt, across statuses | Includes ordinary corrections and consolidation replacements; full inspection supplies ancestry |
| Episode retirements | `record_retirements.context_id` equals this receipt | Includes audited removals that a query of newly created records would miss |

Use bounded previews and full-record inspection recipes. Page or explicitly
expose omitted entries and continuations; never imply a truncated packet is
complete. Query and render a bounded page rather than loading the entire corpus
and shortening the final JSON. A continuation should preserve the receipt,
section, and stable position. Resolve current successors as current evidence;
never substitute them for the historical delivered identity. Full source bodies
and consolidation ancestry already have an inspection surface and need not be
copied into every preview.

Keep episode attribution exact. Automatically including every descendant
receipt would combine separate delegated roles and ambiguous ownership. A
parent can request another receipt explicitly. Origin-linked writes also do
not include unsaved discoveries, writes without provenance, or changes made
under other receipts. Legacy unaudited disables cannot be attributed reliably.
The command must not claim to reconstruct a transcript, what excerpt the model
saw, what it understood, or whether a lesson was useful. The harness supplies
the actual relevant user feedback and task outcome from its conversation.

Suggested ownership: a second builder adds a small review projection and tests,
with minimal `cmd_inspect.go` and `cmd_mcp.go` dispatch changes. It can reuse the
existing records, contexts, successor links and retirement tables without a
schema migration. This packet reduces mechanical lookup work; the first change
provides the behavioral reason to use it.

## Verify mechanics and usefulness separately

Mechanical verification should cover CLI/MCP parity, explicit receipt
ownership, page continuation, successor labeling, consolidation and retirement
visibility, custom store recipes, and the absence of review-side writes. Use
isolated stores and the checkout's built binary, then run `make test`.

For useful work, retain a short case only when a real event warrants one:
what failed or changed, which source mattered, what action changed, and what
task check established the result. A later invocation applying the lesson is
stronger evidence than merely seeing it in a capsule. Neither a synthetic
success score nor a minimum count of saved memories is needed. Independent
review should check whether a proposed durable lesson preserves conditions and
whether the observed result actually supports its breadth.

Do not expand this iteration into transcript capture, a model backend,
automatic reflection schedules, marks, or age-based forgetting. Exact future
recall provenance could improve historical diagnostics, but it would require
additional stored data and cannot repair old receipts. Semantic retrieval
should follow a real different-wording miss, not the mere presence of a growing
library. Ordinary supporting/contradicting evidence edges and history-portable
exports remain separate design questions.
