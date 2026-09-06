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

