# Three-agent code review

Three independent reviewers loaded the repository's `nine-tails.reviewer`
capsule, inspected the code, and then loaded `nine-tails.builder` for scoped
implementation. Each implemented three areas. Root reviewed their changes and
coordinated cross-review before the final checks.

| Agent | Area | Result |
| --- | --- | --- |
| Sol | Receipt retention | GC removes historical marks with their receipts and cleans older orphan marks. Dry runs preserve them. Subqueries replace per-receipt SQL parameters, so a 33,000-receipt backlog can be collected. |
| Sol | Signal claims | Each update rechecks due/claimable state and returns only a successfully claimed row. Two independent stores compete for one signal in the regression test. |
| Sol | Compiler lifecycle | Unix process groups receive cancellation/interruption and remaining descendants are cleaned after completion. Successful and failing parents with inherited pipes retain the correct result and diagnostics. |
| Terra | MCP failure output | Error streams join without artificial leading newlines; an empty failure is labeled as failed. Successful stdout remains byte-preserving. |
| Terra | MCP request IDs | Fractional IDs are rejected; mathematically integral decimal/exponent forms and large integers retain their exact representation without exponent-sized allocation. |
| Terra | MCP preflight | Name, reference, metadata and state-operation syntax is checked before local-reference lookup. Invalid requests no longer open a store or hide the syntax error behind a missing receipt. Metadata conversion is validated once in deterministic order. |
| Luna | Capability decoding | Run markers reject trailing JSON values and garbage, accept trailing whitespace, and remain unchanged on failed admission or mutation. |
| Luna | Import metadata | Invalid keys are rejected even when their null/empty values would otherwise disappear. The decoder and importer share the store's metadata validator. Document decoding precedes store creation. |
| Luna | YAML key conversion | Distinct keys that convert to the same string are rejected deterministically. Unique scalar keys retain their prior compatibility. |

These are fixes and refinements, not nine independently demonstrated production
failures. Signal claims were already serialized by `BEGIN IMMEDIATE`; the
conditional update makes the invariant explicit at the mutation as well.

## Review and validation

- `make test`, `go vet ./...`, `make build`, and `git diff --check` passed.
- `go test -race ./...` passed for the integrated concurrency changes. The final
  import preflight reorder also passed the targeted import race tests and the
  full ordinary suite.
- Checkout CLI probes exercised compiler success, failure, and timeout with real
  descendants, checked diagnostics, and verified no generation was installed
  after failures.
- One isolated stdio MCP connection loaded two agents, saved guidance, verified
  its appearance on the owning agent's next load and absence from the other,
  and exercised state CAS, explicit empty metadata, and failure framing.
- Additional CLI probes checked GC dry-run/mark cleanup and malformed imports
  without creating their destination home. All fixtures used temporary homes.

Cross-review found incomplete preflight cases, duplicated import error prefixes,
and a subprocess test whose child timer raced its pipe-wait timer. Those were
corrected before completion. An external CLI probe then caught store creation
before import decoding; the final fix and regression cover that boundary.

## What the agent workflow showed

The loaded reviewer guidance included relevant lessons about boundary fidelity,
metadata presence, and scope. That did not guarantee complete follow-through:
some initial reports proposed broad test matrices without concrete findings.
Root requested reproductions and narrower changes, and reviewed the resulting
patches. This is evidence that supplying useful guidance works, while review
quality still depends on the model and supervision. No duplicate instruction
was added merely because an existing instruction was missed.

A separate demonstration captured the complete reviewer load: 646 words,
estimated at 1,474 tokens, including operating protocol, role, learned
adjustments, referenced state, and memory-library navigation. The protocol and
referenced status were substantial relative to the role itself. Current working
state was shortened after this pass; changing the shared protocol needs a
separate design decision.

## Remaining limits

Compiler stdout and stderr still buffer in memory without a byte quota. The
runtime changes bound process lifetime, not output size. Descendant cleanup was
exercised on macOS; platforms without Unix process groups retain direct-process
cancellation and bounded pipe waiting. This review did not execute Windows
process behavior or establish that editorial summaries preserve every semantic
distinction. The installed MCP binary must be restarted by a connected host to
take effect; the verification used fresh stdio processes.
