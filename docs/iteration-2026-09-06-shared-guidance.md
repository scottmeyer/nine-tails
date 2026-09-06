# Shared guidance, fresh invocations, and playable practice

This iteration used nine-tails roles for actual product and framework work.
Terra implemented shared guidance; Luna implemented the closer game camera and
physical feedback. Stronger reviewers inspected their code and exercised the
result. The game work stayed in the repository identified as `soccer-chess`;
Ravel's operational work stayed in his private experiment identified as `ravel`.
Resolve the paths below within those repositories, not a presumed checkout path.

## The plan and resulting changes

The game needed room to experiment, a readable view of the ball and players,
and a way to inspect what happened. It gained open and defended kickabout with
unlimited touches, reproducible new formations, close/overview cameras, and a
half-speed replay of the actual last play. Replay leaves the live result and
undo history unchanged. Larger controls improve tablet input targets.

The framework problem was repeated project guidance. Shared state already
carried current facts, but a correction on workshop did not become another
role's standing instruction. `agent follow` now makes that sharing explicit:

```sh
./nt agent follow game.engineer/project workshop --expect none --meta repo-id=soccer-chess
```

Seven game roles now follow workshop's scoped project guidance. Updates resolve
on each later load. Links retain their own scope and exact source provenance;
they do not inherit the source persona, state, tools, recall or further links.
The core change is commit `25c9780`. The source records and old receipts remain
inspectable after corrections or retirement.

The removal pass shortened duplicated designer and engineer directions and
retired the fully duplicated game-feel preference after checking the shared
source covered every clause. Overgeneralized design advice about mandatory
skills and coaching was made conditional on the actual requested game. Local
exceptions and independent engineering lessons remain.

## What review changed

Sharing active raw records initially missed the source's successor accounting.
That would reintroduce advice the source projection already considered replaced.
Subscriptions now reuse the lifecycle resolver, restoring full represented
sources when no source brief is delivered while respecting applicable successors.
Three-way metadata intersection also prevents pairwise overlaps from combining
incompatible scopes. Syntax errors are checked before resolving local references.

Game review caught lingering kick/reception effects after timeline rollback,
camera behavior that did not respond to a changed reduced-motion preference,
and Escape being ignored when the replay button retained focus. These were
repaired. The final game-feel review found no gameplay blocker; it identified
inconsistent kick/touch wording for the final polish pass.

Existing builder guidance already described some relevant lifecycle and syntax
pitfalls. Its presence did not prevent initial omissions; review still supplied
the necessary cue. Storing a rule is not a guarantee that a model will apply it.

## Evidence from later work

A fresh Luna visual-designer invocation received the corrected shared project
direction and the prior renderer experience through ordinary task retrieval.
Its receipt records both deliveries. The resulting change reused the existing
live reduced-motion state to suppress decorative goal motion, retaining physical
movement. No new memory was needed for that round. A fresh game-feel review also
received the shared direction after its copied local preference was retired.
This supports successful delivery and aligned subsequent work, not a general
ranking of models or proof that the memory caused the change.

Ravel completed four fresh exploratory rounds and one corrected follow-up.
He kept one evolving state, made a return note, chose independent new work,
then reopened the source when a later adaptation needed details absent from its
summary. The adaptation preserved the source facts. His four exploratory rounds
created no new guidance or recall; temporary decisions remained revisable state.

An unnecessarily broad sibling-project search led to one narrow, inspected
correction. The follow-up received it automatically and confined its actual
task searches to the experiment while completing the chosen work. Applicable
ancestor instructions remained allowed. The supervising controller authored
the correction. A separately cancelled launch after a rejected write is retained
and excluded from successful-correction evidence.

The rejected writes also exposed avoidable CLI friction: an explicit agent
matching the receipt was rejected with `--context --stdin`. Append operations
now accept that unambiguous owner assertion as well as the shorter canonical
form. A different owner or extra arguments still fail without a write; syntax
errors precede local-reference lookup. The controller's premature launch remains
an orchestration mistake even though the unnecessary syntax restriction is fixed.

Ravel's evidence is in repository `ravel`, path
`self-learning/operational-loop/runs/20260906T185120567168Z/README.md`.
Its adjacent `verify-evidence.py` checks distinct invocations, exact prompts,
state origins and transitions, logged reads, and source fidelity without model
or store access. Root independently ran it and inspected the cited source,
adaptation, discovery commands and successful state write. Fictional character
behavior is not evidence of agent learning; the retrieval, editing and
persistence actions are. Human reader benefit and general reliability remain
unmeasured.

## Validation and the remaining hard part

The framework suite, vet and isolated real-binary checks passed. Independent
probes exercised source corrections, cross-project isolation, foreign-write
rejection, alternate-store recipes, link retirement and concurrent CAS with one
winner. The game's physical and React tests, built-worker HTML check, production
build, TypeScript and focused lint passed. Browser checks covered laptop/tablet
layouts, actual aiming, practice, goals, replay, dribbling, runs and recovery.
The game repository carries its own detailed playtest evidence.

The useful loop is a concrete observation, a semantic decision about what should
persist, a scoped correction, and an inspected later invocation doing real work.
Nine-tails now makes shared delivery less repetitive and preserves replacement
semantics across another context surface. It still depends on the model to
distinguish a durable instruction from a temporary plan and to apply relevant
knowledge. Review and real use remain necessary; additional records alone are
not the next improvement.
