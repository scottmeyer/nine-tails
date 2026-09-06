# Retrieval evidence from ordinary work

Keep a case only when an actual task exposes unexpected recall. These artifacts
are review evidence, not agent memory, a benchmark campaign or a required loop.

The [Soccer Chess handoff case](soccer-chess-stale-handoff.md) records delivered
but obsolete content. It is a counterexample to treating every learning failure
as a reason to add embeddings.

Inspect the exact memory against the original load receipt. `--query` supplies
the actual non-sensitive terms when known; omission checks the stored task:

```sh
./nt inspect <memory-ref> --context <original-receipt> --query "<actual terms>" > <case-name>.json
```

Use a descriptive filename and a short adjacent Markdown note with:

- What the task needed and why this specific memory mattered.
- Whether it was absent, delivered but inappropriate, or delivered but unused.
- The actual query when known; otherwise say that the receipt task is a proxy.
- Which repair the evidence supports, and what remains unknown.

Inspect the source and choose what to preserve before saving an artifact: the
JSON includes the full memory and stored receipt. Use canonical IDs in durable
notes; local `@N` aliases belong to their store. Resolve artifact paths from the
repository identity, never from a remembered absolute checkout path.

`recall_check.recorded_in_context` is immutable exact-ID delivery evidence.
`recall_check.current` is a current lookup, with these possible reasons:

| Reason | What to inspect next |
| --- | --- |
| `no-word-match` | Does different wording hide a useful, applicable experience? |
| `no-query-terms` | Was retrieval disabled or was the task too generic? |
| `outside-recall-budget` | Were higher-ranked entries more useful? |
| `scope-conflict` | Does the stored applicability describe the intended use? |
| `superseded` / `disabled` | Inspect the separate current successor or retirement decision |
| `invalid-text` | Repair damaged stored content |
| `selected` | Inspect the excerpt and distinguish relevant evidence from obsolete facts |

The check does not reconstruct historical query overrides, explicit selections,
excerpt bytes or the historical candidate corpus. A saved artifact remains
readable after receipt GC; rerunning the command still requires the receipt.

Only repeated real vocabulary misses provide direct evidence for an embedding
layer. Preserve word matching, existing scope/lifecycle rules and immediate
learning if that layer is eventually added. Until then, correct stale content,
consolidate overlapping experience, and select exact useful recall when needed.
