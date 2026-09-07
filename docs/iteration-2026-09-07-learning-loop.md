# Learning at the next decision

Baseline: `6079afcd19bd01b294f78174c4d5ce2c18d7016d`.

The core was mechanically sound in the baseline test suite. It already carried
applicable guidance, preserved immutable corrections and consolidation ancestry,
retained retirement decisions, and offered task recall and exact inspection.
The gap was the connection between an observed result and a better next action.
The generic pause reminder did not explain when to consult a lesson, how to
apply it, or what evidence supports preserving a change. Reviewing one episode
also required several separate history lookups.

Three research agents examined primary papers and the implementation in
separate worktrees. Their reports cover
[reflection and experience learning](research-learning-reflection.md),
[retrieval and evaluation](research-learning-retrieval.md), and
[the architecture](research-learning-architecture.md). The shared recommendation was selective,
feedback-grounded adaptation driven by the working harness model. The papers
support that direction; they do not establish performance gains for nine-tails.

## Implemented direction

The common capsule protocol now connects a failure or changed plan to consulting
relevant lessons, a concrete next action, and an observable check. Explicit user
preferences remain direct evidence of intent. Supported corrections are captured
while the evidence is available; uncertain experience stays recall and changing
facts stay state. Fresh starter and repository roles reinforce the same loop.

`inspect <receipt> --review` and MCP `nt_inspect` assemble an episode's recorded
evidence: delivered records, originating writes, and retirement decisions.
Historical IDs remain exact and current successors are separate. Pages contain
short previews, full inspection recipes, counts and continuations. There is no
transcript capture, new model service, scoring, or automatic write.

Codex and Claude remain the primary drivers. Both receive the common protocol
through the CLI or MCP; native hooks are optional conveniences. Existing
personalized starter definitions are preserved. A running MCP server continues
using its installed binary until restarted, so checkout validation uses `./nt`
in isolated stores.

## Review-driven refinements

The first packet draft built all previews and successor chains before slicing
the response. Review identified that this would make every small page pay for
the whole episode. The builder changed it to collect lightweight positions and
materialize only the page and its overflow candidate. SQLite returns bounded
source prefixes for previews; unchanged successors reuse the same projection.
A regression case puts a corrupt successor chain at the far end of a large
episode: early pages must remain readable, and the corruption must be reported
when reached.

Review also caught an explicitly empty CLI continuation being treated as a
first-page request, and a local reference of the wrong resource kind reaching
receipt lookup. Both now use typed validation before ordinary lookup can obscure
the error. These are direct applications of the existing builder guidance about
validating syntax before reference resolution and preserving reference kinds.

The independent binary review then found that SQLite text-prefix functions stop
at embedded NULs, even though a stored UTF-8 body may contain them. A preview
could consequently hide a meaningful suffix while claiming to be complete.
The fix follows the existing memory library's bounded BLOB-prefix approach and
preserves valid UTF-8 boundaries for bodies, names, and retirement reasons.

The shared nine-tails learning preference was corrected through its existing
source, preserving scope and lineage. The research reports remain repository
artifacts; raw papers and task transcripts were not added to the memory store.

The builder also retained the verified SQLite prefix lesson as scoped experience
after checking for an existing relevant memory. The discovery, code change,
regression, and independent recheck form one concrete dogfooding cycle. They
establish this repair, not a general improvement in agent task performance.

## Validation

`make test`, `make vet`, and `make build` passed on the integrated change.
The cross-harness integration test verifies a correction originating under
Codex, later delivery under Claude, preserved repository scope, and immutable
historical receipts. A complete pagination test compares every page across CLI
and MCP, retaining inactive writes and retirement entries without duplicates
or missing records.

Independent review used the real checkout binary in isolated stores. It checked
46 entries across four pages, exact CLI/MCP agreement, consolidation and retired
successors, foreign-owned delivered guidance, excluded child writes, a 4 MB
Unicode source, quoted custom-store paths, and unchanged logical SQLite
contents. Malformed CLI/MCP inputs failed without creating a store. The reviewer
then reproduced and verified the NUL fix across text, names, retirement reasons,
and a mixed-width UTF-8 boundary. No finding remained open.

Regression entry points:

- `cmd/nine-tails/cmd_learning_cycle_test.go`
- `cmd/nine-tails/cmd_review_test.go`
- `internal/store/episode_review_test.go`
- `internal/capsule/capsule_test.go`
- `cmd/nine-tails/starter_test.go`

## Evidence standards for the next iteration

The clearer protocol has a measurable context cost. Identical minimal-role loads
in isolated custom stores measured 687 estimated tokens at the baseline and
908 after the change, a 221-token increase using nine-tails' byte-based
estimator. This is not a tokenizer measurement or a latency benchmark. The
default-store protocol ceilings increased from 1,650/2,000 bytes to 2,400/2,750
bytes without/with state and tools. The review operation is opt-in; its text
processing is bounded to a page while lightweight episode IDs are still
collected to locate the cursor and report counts. Large histories can therefore
still incur indexing work. End-to-end task efficiency remains to be established.

Mechanical tests establish that a correction persists, keeps its scope, appears
in later capsules, and can be reviewed without rewriting history. They do not
establish that a model understood or applied it.

For an actual repeated failure, retain a small project artifact naming the
observed failure, relevant exact lesson, changed action, and verification result.
Use the episode packet to recover recorded sources and changes. A later task
that applies the lesson provides stronger evidence than another successful
load. Include cases where a retrieved lesson was irrelevant or an attempted
adjustment failed. Keep changing status out of the recall corpus.

No embedding service or autonomous reflection loop was added: neither was
justified by this audit. Historical query overrides and delivered excerpt bytes
remain unavailable. Semantic relevance, evidence quality, and the decision to
retain a lesson still depend on the invoking model and the task's feedback.
