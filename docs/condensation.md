# Optional condensation

Recent adjustments remain visible immediately. When they grow large,
`compile` can condense them into a cached brief through any command that reads the
compile document on stdin and writes the result on stdout:

```yaml
# ~/.nine-tails/config.yaml
compiler:
  argv: ["my-model-command", "--noninteractive"]
  timeout: 300s
```

```sh
nine-tails compile pr-review
```

For a manual or custom-model workflow, use `compile-input` followed by
`brief put`. Compiler output is validated for complete dispositions and
installed with compare-and-swap protection.

Review the wording before installation with `brief put ... --dry-run`. Its
`proposed_items` include complete instructions, scopes, and original source
bodies. `remaining_guidance` shows the full entries that will remain recent,
including deferred guidance and anything restored by dropping a partial
representation. These are global accounting results; an individual load can
also restore sources when a summary does not apply to its scope.

Every proposed item needs a guidance source, assigned in this response or
inherited from a same-key active item. A base duplicate or an equivalence hint
does not provide that support. Previously imported or legacy items without
sources remain readable, but their next compilation must omit them or first
save supported meaning as ordinary guidance. Source links prevent unsupported
items; reviewers still need to check whether the new wording is faithful.

For example, if two items jointly represent one source, dropping either item
without reaccounting for the source restores its complete original text. A
shorter item list can therefore leave more raw guidance in circulation. Review
`remaining_guidance` alongside the proposed items before judging the change.

Original lessons remain authoritative. Compiler input includes the original
source bodies behind existing summaries. If a compiled representation is
inapplicable or cannot render on a load, the eligible source guidance returns
in full. Compilation is never a checkpoint required to activate learning.

Treat the reflector and compiler as editors. Keep the future action, its
trigger, and the exception that changes the decision. Remove incident narration,
personal challenges, changing status, and explanation that adds no useful
instruction. Preserve explicit user intent and uncertainty; one incident does
not establish a universal preference. There is no required number of edits.

Cut unwanted sources before condensing. Inspect the complete source, replace or
consolidate useful lessons, and retire material with no future role using
`disable <ref> --context <owner-receipt> --reason "..."`. Deferral leaves the
original guidance visible, so it cannot serve as a removal operation. Original
text and the retirement decision remain inspectable.

The compiler can learn too. Its `brief-compiler` base customizes the editorial
method, while the required output contract always comes from nine-tails.
Corrections saved to this role appear in the next `compile-input` document's
`editorial_guidance`, with their original scope and identity. These records
guide editing; they are separate from the target agent's preferences and input
accounting. Input assembly neither starts a model nor records a new receipt.

Existing customized bases are not silently upgraded. Inspect and reconcile a
base that embeds an older compiler contract; otherwise that old text will
duplicate the current fixed contract. Keep the role definition short and save
method corrections as ordinary guidance.
