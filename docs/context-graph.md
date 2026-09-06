# Evolving knowledge, fresh context

The intended agent is a durable role that changes through use. Its next capsule
should be a fresh projection of applicable knowledge, rather than a new static
agent file or a checkpoint that waits for compilation.

## The graph that already exists

Nine-tails already stores a small directed graph in SQLite:

| Relationship | Meaning |
| --- | --- |
| Record → originating context | The episode that recorded it |
| New record → superseded record | A deliberate correction with preserved history |
| Consolidated guidance or experience → same-lane sources | A reasoned many-to-one replacement with all predecessors intact |
| Retired record → decision and episode | Why knowledge stopped surfacing, without erasing it |
| Brief item → source records | Evidence used to derive a condensed instruction |
| Context → delivered records | Exactly what that invocation received |
| Role → state link → named current state | One authoritative value shared without copies |
| Role → named tool or related role | Capabilities and collaborators available to it |

A correction chain is a linked list, but the whole system is a graph. Several
observations can support one derived instruction; a single correction can
invalidate several derived items. Several roles can share one project state.

Two views must remain distinct. A historical receipt names the exact versions
that were delivered. A new load follows current replacements and eligible state
links, then selects applicable guidance and relevant historical evidence. New
knowledge must not rewrite an old receipt, and being connected to a node does
not mean that node belongs in every capsule.

Body, kind or metadata-set changes invalidate a dependent brief atomically.
Many-to-one consolidation now connects ordinary guidance or recall records as deliberate
replacements. A working model supplies the merged meaning and reason; the store
preserves each source and rejects stale or differently scoped inputs. Retiring
a record can also retain the reason and deciding episode. Neither operation
requires a successful compiler run.

Explicit recall selection lets a caller choose relevant evidence for a new
load. It does not traverse every connected node into context. A receipt still
records only what that invocation actually received.

## Where the design should go

This is a direction for further implementation, not a claim that the following
semantic relationships already exist:

- Ordinary lessons should be able to name the specific observations or user
  corrections that support them, beyond the broad originating episode.
- An explicit contradiction or resolution should connect the relevant records.
  A model establishes that relationship from evidence; word similarity alone
  must not declare one lesson false or erase it.
- A generated summary should remain a disposable view with explicit sources.
  If a dependency changes, invalidate or regenerate the affected view. Retain
  the original knowledge without requiring a successful compiler run.
- Retrieval should use those relationships to explain why a lesson was selected
  and recover its evidence. Traversal needs scope and size limits; adjacency
  alone is insufficient relevance.

Consolidation edges mean “replaces these records”; they do not mean “is
supported independently by these observations.” Keep that distinction when
adding evidence relationships. More edge types need concrete learning callers,
not a generic graph editor. SQLite remains sufficient for the present graph.

## Remaining learning limits

Today the working model still decides what to retain and which correction
replaces which lesson. Automatic recall uses lexical matching; callers may explicitly select memories. Explicit eligible guidance
is preserved in full; this iteration does not silently rank away user rules.
Dates identify recalled observations as historical, but do not themselves prove
that an observation is obsolete. A graph supplies traceable relationships; it
does not supply semantic judgment or evidence of correctness.

See [the learning cycle](learning-cycle.md) for operational decisions. Graph
ancestry and retirement audits are currently store-local; snapshot export/import
does not preserve them. Source receipts can be collected under ordinary GC,
while the original source bodies and recorded origin IDs remain inspectable.
