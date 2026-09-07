# Retrieval is not application

Research note, 2026-09-07. This review asks what nine-tails still needs in
order to help Codex and Claude learn through ordinary work without depending
on model weights, embeddings, transcripts, or a feature unique to either
harness.

## Finding

Nine-tails already has the right durable substrate: immutable load receipts,
explicit scope, inspectable correction lineage, exact recall selection, a
bounded lexical fallback, and a browsable memory library. Its main gap is one
stage later. The store can establish that a record was eligible and delivered,
but there is no compact way for the working model to revisit everything that
could have influenced an episode and compare it with the resulting artifact.
Delivery is necessary evidence for application, not evidence of application.

The minimal next step is therefore a read-only episode review packet plus a
harness-led consult/apply/verify protocol. Behavioral evaluation should remain
in repository artifacts and task checks. Nine-tails should expose provenance
for that evaluation, not manufacture an `applied` flag or reward from the
model's own report.

## What the papers establish

| Work | Memory and evidence | What it actually measures | Limit that matters here |
| --- | --- | --- | --- |
| [Generative Agents](https://arxiv.org/abs/2304.03442) | A complete natural-language memory stream is ranked by recency, importance, and relevance; reflection creates higher-level memories used in planning. | Human-rated answers about memory, plans, reactions, and reflections; an ablation; and a two-day social simulation. | The evaluation is short and centered on believability. The authors call for longer observation and stronger robustness testing. Importance scoring and semantic relevance also add model calls and embeddings without proving that a retrieved item caused behavior. |
| [Voyager](https://arxiv.org/abs/2305.16291) | Reusable executable skills are committed only after environment feedback, execution errors, and self-verification indicate success. Skills are later retrieved for related tasks. | Exploration progress, task milestones, map coverage, zero-shot success in a new world, component ablations, and top-5 retrieval accuracy. | The evidence is unusually strong because skills execute, but it is Minecraft-specific and depended on GPT-4 plus embedding retrieval. Its self-verifier can itself be wrong. Nine-tails can borrow verified admission and later task reuse without borrowing that stack. |
| [ExpeL](https://arxiv.org/abs/2308.10144) | Success and failure trajectories yield natural-language insights; similar successful experiences and insights condition later trials without weight updates. | Held-out task success, insight/retrieval ablations, and cross-domain transfer. | The studied tasks are textual and use closed-source models. All learned insights still fit in context; the authors identify selective insight retrieval as future work for a truly lifelong setting. |
| [Agent Workflow Memory](https://arxiv.org/abs/2409.07429) | Successful trajectories are abstracted into reusable workflows, including an online stream where each successful task can teach the next one. | Functional task success, steps, a cumulative online learning curve, and cross-template/site/domain transfer on Mind2Web and WebArena. | The domain is web navigation. When workflows were exposed as new actions, agents invoked them in only 18.5% of tasks, and fixed sequences failed on dynamic intermediate state. This is direct evidence that availability is not use and that application must stay responsive to current state. |
| [LongMemEval](https://arxiv.org/abs/2410.10813) | Separates indexing, retrieval, and reading, then tests extraction, multi-session and temporal reasoning, knowledge updates, and abstention across 500 questions. | End-to-end answer correctness and retrieval-oriented design comparisons under growing histories. | It is a conversational QA benchmark. It sharply diagnoses retrieval and reading, including a 30--60% decline from oracle evidence to full-history reading in reported long-context experiments, but it does not test whether memory changes tool use or an external artifact. |
| [Mem0](https://arxiv.org/abs/2504.19413) | Extracts, consolidates, and retrieves salient conversation facts; a graph variant captures relations. | LoCoMo answer quality, including an LLM judge, plus token use and latency. | The benchmark asks questions about conversation history rather than testing procedural reuse. The reported evaluation excludes LoCoMo's unanswerable category because ground truth was unavailable, and a judge score is still weaker than a task-native behavioral check. |
| [A-MEM](https://arxiv.org/abs/2502.12110) | LLM-generated contextual notes, tags, and links evolve as new memories arrive; dense retrieval supplies top-k memories. | Conversational QA on LoCoMo and DialSim, lexical/semantic answer metrics, token use, retrieval time, and component ablations. | Its quality depends on the underlying model, and its principal results are QA metrics rather than action. Updating old memory representations in place also gives weaker historical auditability than nine-tails' immutable sources and explicit successors. |

Across these systems, three distinct claims are often compressed into
"memory works": the right evidence was found, the model used it in a decision,
and the resulting behavior improved. LongMemEval is strongest on the first,
Voyager and AWM provide the clearest external evidence for the third, and AWM's
low workflow-action use shows why the middle claim cannot be inferred from
delivery.

## Current nine-tails evidence

The implementation in `internal/capsule/recall.go` uses deterministic distinct
whole-word matching over body, name, subject, and title, ranks by match and
scope overlap, and fills a soft 4,096-byte recall budget. Explicit selection
can deliver every chosen applicable record. `internal/capsule/library.go`
exposes the rest as a live, paged index. `internal/capsule/recall_check.go`
reuses the real selector to compare an exact memory with a receipt and today's
query, avoiding a second diagnostic ranking implementation.

The boundary is documented accurately in `docs/learning-cycle.md`: the
historical query override, explicit-selection mode, and exact excerpt bytes are
not retained, and a receipt does not prove that the full body was present or
applied. The store records the exact rendered record IDs and ordinals, but
application evidence lives elsewhere.

The loaded project evidence demonstrates both the strength and the gap:

- Receipt `@844` selected memory `@811`, whose lesson says that delivered
  instructions still need independent behavior and recovery checks. Its exact
  recall check reports `recorded_in_context: true` and `selected: true`, then
  explicitly limits the claim to record delivery rather than full-text
  delivery, relevance, or use.
- Memories `@483`, `@497`, `@505`, and `@506` preserve paired or controlled
  outcomes from earlier experiments: later compliance, fewer calls without
  fewer errors, unsupported attribution despite traceability, and a case with
  no accuracy gain. Each correctly limits itself to small exploratory or
  unblinded trials rather than a general reliability claim.
- Memory `@485` gives stronger application evidence: a stale copied constraint
  caused a concrete bad review; replacing it with current shared state led a
  fresh reviewer to retrieve the current constraint and accept compliant work.
- Memory `@715` connects useful dogfooding to repaired regressions and test
  entry points. This is valuable outcome evidence, but reconstructing the
  episode currently requires knowing and opening several individual records
  and external artifacts.

These receipts show that nine-tails can preserve careful evidence. They also
show why receipt delivery and model-authored recollection should not become a
score.

## Three buildable gaps

### 1. Read-only episode review packet

Add `inspect <receipt> --review` and the equivalent MCP inspection. Return one
bounded, paged stream containing records delivered by that receipt, records
written with that receipt as origin, and retirements decided with that receipt.
For each item provide the exact reference, lane/kind/name/status, a short
whitespace-collapsed preview labeled as a current inspection preview, and an
exact full-inspection recipe. Delivered items also need their receipt section
and ordinal plus the current successor or retirement outcome.

Use a soft byte target, explicit per-category and total counts, and a generated
continuation recipe with an opaque cursor. Do not include full bodies, traverse
child contexts, infer relevance, or label anything applied. The packet answers
"what should I inspect while reviewing this episode?" and keeps the answer
portable between CLI and MCP.

This closes an observability gap with data the store already owns. It follows
LongMemEval's separation of retrieval from reading and preserves nine-tails'
stronger immutable lineage. It also supplies the evidence index needed for the
task-level checks used by Voyager and AWM.

### 2. Event-triggered consult/apply/verify protocol

Put the loop in the loaded, harness-neutral instructions rather than in Codex-
or Claude-specific hooks:

1. Consult selected data or the library when the current decision makes prior
   experience material; inspect the full current source before relying on an
   excerpt.
2. Apply only the parts supported by the current task, project state, and user
   intent. A workflow is guidance, not a fixed macro over changing state.
3. At a material checkpoint or before handoff, inspect the episode review
   packet and compare applicable lessons with artifact diffs, tests, command
   outcomes, or user feedback. Capture a retrieval case only for a real miss;
   correct, consolidate, retire, or remember only when the evidence supports
   it.

The trigger should be a decision or observed outcome, not every tool call and
not mandatory episode closure. This is the lightweight analogue of Voyager's
execute/feedback/verify admission loop and avoids AWM's mistake of treating a
workflow as a blind action sequence.

### 3. Longitudinal application evaluation outside the store

Keep a small repository-owned evaluation artifact that follows the same role
across ordered, fresh episodes. Give each learned item an externally checkable
behavioral predicate, then record:

| Layer | Measure |
| --- | --- |
| Retrieval | eligible, delivered, fully inspected when needed, correct current successor |
| Application | behavioral predicate visible in artifact, tool arguments, or decision; supported exception handling |
| Outcome | task-native test, verifier, or user judgment independent of receipt delivery |
| Retention | predicate still holds after unrelated intervening episodes |
| Transfer | predicate holds on a changed task and, when practical, across Codex and Claude |
| Harm | irrelevant or stale memory is ignored; contradictions and updates resolve to current evidence |
| Efficiency | attempts, tool calls or steps, token/packet bytes, and durable writes per successful task |

Use paired applicable-memory and withheld-memory runs where feasible, plus an
irrelevant-memory control. Keep task inputs and external scoring fixed, vary
order, and report each run rather than only an aggregate. This measures
improvement, retention, transfer, and negative transfer while preserving the
lesson already present in project evidence: call count, delivery, and notebook
traceability alone do not establish better behavior.

## Why embeddings are deferred

LongMemEval, Generative Agents, Voyager, A-MEM, and Mem0 show that semantic
keys can help at scale or across paraphrases. They also add model or embedding
dependencies, and none turns retrieval into application evidence. Nine-tails
already supports exact selection, deterministic lexical retrieval, current
successor inspection, and real retrieval-case capture. First collect misses by
cause: lexical mismatch, scope, stale content, budget, or delivered-but-unused.
Only repeated appropriate misses under different wording support adding an
optional semantic candidate stage. Even then, it should change candidate
selection only; receipts, explicit choices, scope checks, full inspection, and
behavioral evaluation should remain authoritative.
