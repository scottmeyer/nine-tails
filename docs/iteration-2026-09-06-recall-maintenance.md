# Recall maintenance before embeddings

The user agreed to finish recall consolidation and preserve actual retrieval
misses before adding an embedding dependency. The immediate defects were
concrete: consolidation rejected recall, and inspection could not compare an
expected memory with its original delivery receipt.

## Implemented behavior

`consolidate` now accepts guidance or recall with one owner, one lane and
identical scope. It infers and preserves the lane, keeps exact source lineage,
and rejects stale inputs atomically. Recall consolidation leaves guidance
generations unchanged. The replacement is immediately available to normal
retrieval and the paged library. MCP uses the same operation.

`inspect <memory> --context <receipt> [--query <terms>]` adds a read-only
diagnostic to the existing exact-record view. It separates recorded delivery
from current eligibility, matching and budget selection, using the real
retrieval implementation. CLI and MCP expose the same check. It creates no
memory, receipt, schema or scoring record. A short library recipe makes it
discoverable without expanding the common protocol beyond its existing size
ceiling.

The [actual Soccer Chess case](retrieval-cases/soccer-chess-stale-handoff.md)
captures an obsolete grid handoff delivered during 3D rules work. It supports
content cleanup; it supplies no direct evidence of a vocabulary miss. Historical
query overrides, explicit selection mode and excerpt bytes were not retained,
so the diagnostic discloses that it cannot replay the original selection or
prove a model applied the memory. A caller can save observed evidence with a
relevance rationale as a project artifact that survives ordinary receipt GC.

## What the agents actually did

The builder role implemented and tested recall consolidation. A separate
builder invocation aligned MCP learning. A reviewer exercised the diagnostic,
caught inconsistent wrong-type errors after local-reference resolution, and
verified the fix. Root reviewed the builder and MCP work, ran the full suite,
and exercised the rebuilt binary in an isolated store.

The existing reflector was taught focused recall reconciliation through its
own receipt, without replacing its customized base. A fresh reflector received
that guidance, inspected four complete framework memories and chose to keep
three independent ones separate. Their shared project chronology did not make
their future uses interchangeable. It corrected the fourth memory to describe
recall consolidation and diagnostics rather than adding a duplicate entry.

A fresh design-role invocation naturally retrieved that corrected memory and
used it during the final contract review. Strong semantic review then caught
two overstatements: the replacement conflated lane and kind inference, and it
implied that diagnostics could determine staleness or actual application. The
reflector corrected both, but dropped a valid artifact-placement clause. Root
restored that clause with a targeted edit and verified the unchanged remainder.
Every predecessor remains inspectable. The reflector's existing guidance was
also corrected to require preservation of unaffected claims during narrow
repairs; the starter now carries the same method for fresh stores.

The final graph/workflow memory is `rec_01M1W5AHT5NFHHHHDJ70B3XAXY`; the final
reflector correction is `rec_01M1W5AHVK83PFMA02YWHBYCYQ`. Ordinary retrieval of
the earlier corrected version is recorded by design receipt
`ctx_01M1W56C3YM8VF6ZEENHPH4Q9T`. These prove persistence, delivery and an actual
review/correction sequence. They do not establish that the final wording has
already improved a later task or that semantic preservation is automatic.

## Validation and limits

`make test`, `go vet ./...`, the build and diff checks passed. Regressions cover
same-lane merging, unchanged guidance generations, historical receipt fidelity,
immediate retrieval/library visibility, stale/scope/owner failures, non-revival
after retirement, query presence, real budget parity, and invalid CLI/MCP
arguments before store/reference access. Tests use isolated stores.

The real-binary probe merged two recall sources, verified the exact old receipt
unchanged, diagnosed historical delivery separately from current selection,
and retrieved only the merged memory on a subsequent load. The custom store
path contained spaces and shell metacharacters; discovery retained that binding.

No embeddings, vector index, new database schema, mandatory maintenance pass,
or automatic case collection was added. Genuine overlap can now be repaired
through the normal learning path. Repeated real vocabulary misses remain the
evidence gate for semantic retrieval; semantic correctness still needs review.
