# Evidence for learning through reflection and experience

This note asks what published agent research supports for nine-tails: a
harness-independent store and projection layer whose primary driver is the model
already working in Codex, Claude, or another harness. It is not a proposal for a
resident agent loop or model service. The papers below test context-level
adaptation without changing model weights, but their evidence is narrower than
the phrase “continuous learning” often suggests.

The useful common result is an **act → observe feedback → choose a concrete
change → retry or transfer** loop. Reflection helps when it converts a real task
signal into an actionable change. Self-generated critique without a reliable
signal can degrade results. Durable cross-task learning needs an additional
decision: preserve a grounded episode as experience, or promote a supported
lesson into guidance. Nine-tails already has the storage primitives for that
decision; its runtime protocol does not yet make the act/feedback/change/verify
sequence explicit.

## What the papers actually establish

### Reflexion: feedback-grounded retries, not free-standing introspection

[Reflexion](https://arxiv.org/abs/2303.11366) stores natural-language reflections
after a task attempt and supplies them to later attempts without updating model
weights. Its results cover repeated trials on bounded benchmarks, not indefinite
learning in an open-ended work history.

- In ALFWorld, a ReAct agent reflected after a concrete trigger: the environment
  reported completion, a repeated action/response loop was detected, or the
  trajectory exceeded 30 actions. Memory was limited to the last three
  reflections. Across 134 environments and at most 12 trials, ReAct + Reflexion
  solved 130 tasks; the paper reports a 22-point improvement over its baseline.
- On HumanEval Python, the reported pass@1 result was 91% versus 80% for the
  GPT-4 baseline in that experiment. This depended on generated tests and their
  execution. On the 50-problem HumanEval Rust subset, reflection without test
  generation scored 52%, below the 60% base model; test generation without the
  verbal reflection also remained at 60%. The paper explicitly attributes false
  updates and premature acceptance to inaccurate generated tests.
- The authors bound memory and retries and describe local-minimum and
  test-oracle limitations. WebShop was a negative case: repeated reflection did
  not supply the exploration diversity the task required.

For nine-tails, this supports using user corrections, test failures, tool output,
and reviewer findings as triggers for a focused next attempt. It does not support
asking for an unconditioned retrospective at every boundary, treating a model's
self-assessment as evidence, or persisting each reflection.

### Self-Refine: specific feedback helps the current artifact

[Self-Refine](https://arxiv.org/abs/2303.17651) alternates feedback and refinement
with the same LLM, using at most four iterations in its experiments. It improves
one output in one task; it does not implement memory across episodes.

- Across seven generation tasks, the paper reports roughly 20 percentage points
  of average improvement over one-step generation. The metrics and evaluators
  differ by task, so this is not one uniform measure of agent reliability.
- Its ablation found specific, actionable feedback better than generic or absent
  feedback: for example, code-optimization scores were 27.5, 26.0, and 24.8,
  respectively. Gains were largest in early rounds and were not always
  monotonic.
- In a manual analysis of 35 successful and 35 unsuccessful code/math cases,
  most failures came from bad feedback: 33% localized the error incorrectly and
  61% proposed an inappropriate fix; only 6% were failures to apply good
  feedback. The paper also found larger math gains when an external source first
  identified an incorrect answer.

This supports concise protocol language that asks for a concrete diagnosis and
next action, followed by a relevant check. It is evidence for within-episode
repair, not evidence that the resulting critique should become a standing rule.

### ExpeL: contrast episodes before extracting cross-task lessons

[ExpeL](https://arxiv.org/abs/2308.10144) separates an experience pool from
cross-task insights. It gathers successful and failed trajectories, compares a
failure with a success on the same task, extracts or edits natural-language
insights, and retrieves successful trajectories by task similarity. Evaluation
used deterministic text environments, four-fold validation, and
`gpt-3.5-turbo-0613` for task execution.

- ExpeL's first-attempt ALFWorld result was 59.0%; ReAct + Reflexion reached
  54.4% only after three retry rounds in the reported comparison. Adding retries
  to ExpeL raised it to 64.2% by round three. This is evidence that cross-task
  experience and same-task repair are distinct and can complement each other.
- On HotpotQA, the insight extractor scored 39.0% success. Supplying its earlier
  free-form Reflexion outputs as additional extraction input reduced this to
  29.0%; the authors suggest hallucinated reflections misled extraction. The
  ReAct baseline was 28.0%.
- On ALFWorld, task-similarity retrieval scored 59.0%, compared with 42.5% for
  random retrieval and 48.5% for reasoning-similarity retrieval. On transfer
  from HotpotQA to the related FEVER task, adapted insights with target examples
  scored 70%, versus 63% for ReAct. These are limited benchmark transfers between
  tasks chosen to share a Wikipedia tool and knowledge.
- The paper did not test an unbounded history: all extracted insights fit in the
  model context, and the authors identify additional retrieval as future work.

This aligns with nine-tails' distinction between situational recall and standing
guidance. A failed episode can remain uncertain recall. Promotion is safer when
the model can compare the failed action with a verified successful action and
name the condition under which the lesson applies. ExpeL motivates relevant
retrieval, but it does not establish that embeddings are the next fix for this
repository; `docs/learning-cycle.md` is right to require a captured real miss
before choosing a retrieval mechanism.

### ACE: preserve context with local, inspectable edits

[Agentic Context Engineering (ACE)](https://arxiv.org/abs/2510.04618)
uses Generator, Reflector, and Curator roles to add localized “delta” entries to
an evolving playbook rather than repeatedly rewrite the whole context. The paper
reports both offline and online adaptation on AppWorld and domain-specific
reasoning benchmarks.

- On AppWorld with DeepSeek-V3.1, ACE reported a 10.6-point average improvement
  over selected baselines. Its incremental-update ablation averaged 70.3 on the
  normal test split, versus 56.9 without incremental updates and 53.3 for ReAct.
  The paper's interpretation is that whole-context rewriting drops useful detail.
- Results depended on feedback quality. In the financial tasks, online ACE
  without ground-truth feedback decreased FiNER accuracy from 70.7 to 67.3,
  while Formula improved. The authors explicitly warn that unreliable feedback
  can pollute the playbook.
- ACE used up to five reflection-refinement rounds and specialized model calls.
  Its cost and latency comparisons are against other multi-rollout adaptation
  methods, not against a lightweight human/model decision made in an existing
  harness. It also finds that some fixed-strategy tasks need only one reusable
  rule and do not benefit from a rich playbook.

Nine-tails already implements the most applicable ACE result: immutable local
replacement, source-aware consolidation, retained history, and optional
source-grounded condensation. A mandatory Generator/Reflector/Curator pipeline
would add cost and a harness dependency without addressing the paper's core
failure mode. The working model can curate a small delta while direct evidence
is in its conversation, then use nine-tails' existing operations to preserve
lineage and scope.

### Voyager: verified executable skills transfer, within a narrow environment

[Voyager](https://arxiv.org/abs/2305.16291) combines a Minecraft curriculum, an
executable skill library, and iterative program repair from environment output,
execution errors, and GPT-4 self-verification. A skill enters the library only
after verification. The paper reports 3.3 times as many unique items, 2.3 times
the travel distance, and faster technology-tree progress than adapted baselines;
the learned library also helped on four tasks in a reset world.

The ablation reported a 73% reduction in discovered items without
self-verification. But the study used high-level Mineflayer APIs, three runs for
the technology-tree and transfer results, and Minecraft-specific feedback. Its
self-verifier sometimes accepted or rejected the wrong outcome, and its
curriculum proposed nonexistent objects. This supports keeping verified,
reusable executables in nine-tails' tool definitions and feeding execution
errors into the next attempt. It does not support an automatic curriculum for
ordinary software or knowledge work.

### Generative Agents: useful memory architecture, weak evidence for a trigger

[Generative Agents](https://arxiv.org/abs/2304.03442) stores observations,
retrieves by recency/relevance/LLM-rated importance, and generates higher-level
reflections when accumulated importance reaches 150. Reflections cite source
memories. In a 25-agent social simulation, 100 evaluators ranked interview
answers from the full architecture and ablations; removing reflection and then
planning reduced perceived believability.

That study evaluated simulated social coherence, not task correction or
cross-episode work quality. It did not compare its importance threshold with
event-triggered or user-triggered review, and its ablations reused memories
created by the full architecture rather than rerunning each architecture. The
citation graph is useful precedent for nine-tails' provenance, but the numeric
importance score and automatic threshold are application heuristics, not
evidence for runtime marks or compulsory reflection.

## Fit with the current design

The current design already gets several consequential choices right:

- `DESIGN.md` makes the harness a facet, keeps semantic judgment in the working
  model, and rejects a daemon or required second model service.
- `docs/learning-cycle.md` separates explicit preferences, uncertain recall,
  mutable state, replacement, consolidation, and reasoned retirement. It warns
  that delivery is not proof of application and that one incident is not a
  universal rule.
- Receipt accounting, record provenance, current-successor inspection, and
  source-aware compilation protect the incremental-edit property supported by
  ACE and the source citation pattern used by Generative Agents.
- Task-focused recall, explicit selection, and paged inspection provide a
  deterministic baseline for the relevance result in ExpeL. The retrieval-case
  workflow can identify whether a failure was selection, scope, stale content,
  or application before the project adds semantic retrieval.
- Tool definitions can hold reusable executables, the transferable artifact in
  Voyager, while the harness still supplies native capabilities.

The concrete gap is the connection between those pieces. The generated capsule
currently says to capture supported lessons and briefly reflect at meaningful
boundaries. It does not tell the working model to consult a relevant existing
lesson when a task fails or the plan changes, inspect full evidence before
relying on an excerpt, choose one changed action, and run an external check.
Likewise, receipt inspection exposes delivery history, but there is no compact
read-only view organized for an evidence-based episode review. The system can
store a conclusion well; it gives less help in forming and testing that
conclusion.

## Buildable priorities

1. **Add an event-triggered consult/apply/verify protocol.** On concrete failure,
   user correction, reviewer finding, or material plan change, the capsule should
   tell the working model to consult relevant delivered or searchable recall,
   inspect full evidence when an excerpt may affect the decision, select a
   specific next action, and verify it with the available user/tool/environment
   signal. Keep the loop inline in the current harness. A successful task need
   not trigger reflection, a retry need not trigger a write, and an unsupported
   inference stays uncertainty rather than guidance.

2. **Provide a read-only episode review packet.** At a useful review or handoff,
   an inspect operation should organize the receipt's role, task, scope, parent,
   exact delivered records, and each record's current successor/status. It must
   make no model call, infer no outcome, read no transcript, and write nothing.
   The working model combines that packet with feedback already visible in the
   harness, identifies the evidence for any proposed change, and uses existing
   replacement/consolidation/retirement commands. This reduces provenance search
   without creating a hidden reflection pipeline.

3. **Evaluate both immediate repair and delayed transfer.** Use small, fixed
   cases drawn from ordinary repository work. For immediate repair, retain the
   attempted action, direct feedback, proposed change, next attempt, and external
   result. For delayed transfer, separately record whether a relevant lesson was
   retrieved, whether the model applied it, and whether the task outcome
   improved. Include negative cases where feedback is misleading or an old
   lesson is inapplicable. Runtime marks are unnecessary: evaluation metrics and
   artifacts belong in the experiment, with repeated trials and blinded review
   where practical.

These priorities preserve nine-tails as a harness-neutral learning scaffold.
Codex, Claude, or another capable harness remains responsible for acting,
reading feedback, making the semantic edit, and verifying the next attempt.
Nine-tails supplies durable evidence, retrieval, provenance, and safe mutation
operations; it should not manufacture a reason to reflect or claim learning
from a stored note alone.
