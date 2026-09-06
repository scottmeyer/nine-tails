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

This iteration repairs one concrete dependency failure: changing a source's
scope could leave its compiled instruction active under the old scope. Body,
kind or metadata-set changes now invalidate a dependent brief atomically. The
corrected source becomes usable immediately; optional condensation can follow.

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

The next small extension should be explicit supporting-source relationships
for ordinary learned records, reusing canonical IDs and the existing inspection
and replacement operations. It should have a concrete caller in the learning
flow and preserve the ability to load and work without a model service inside
nine-tails. A generic graph editor or graph database is unnecessary.

## Remaining learning limits

Today the working model still decides what to retain and which correction
replaces which lesson. Recall uses lexical matching. Explicit eligible guidance
is preserved in full; this iteration does not silently rank away user rules.
Dates identify recalled observations as historical, but do not themselves prove
that an observation is obsolete. A graph supplies traceable relationships; it
does not supply semantic judgment or evidence of correctness.
