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
