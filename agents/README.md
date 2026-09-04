# Repository agent pack

These export documents define the repo-owned agents used to build nine-tails.
Their names are namespaced because the nine-tails store is shared by every
repository and worktree for one user.

Seed the built-in guide and list the global store before importing anything:

```sh
./nt load pilot \
  --task "Initialize the nine-tails repository agents" \
  --meta repo-id=nine-tails \
  --meta harness=<actual-harness>
./nt agents
```

Keep the `ctx_...` receipt returned by that initial pilot load.

Import only a target that is absent from that list. These are intentionally
separate commands so a model does not overwrite an existing base while trying
to initialize an unrelated missing role:

```sh
./nt import agents/nine-tails.builder.yaml
./nt import agents/nine-tails.reviewer.yaml
./nt import agents/nine-tails.yaml
```

After the three targets exist, install their entries in pilot's catalog:

```sh
./nt import agents/pilot.catalog.yaml
```

Load the needed checked-in role directly with the original pilot receipt:

```sh
./nt load nine-tails.builder \
  --task "<concise non-sensitive purpose>" \
  --context <pilot-receipt>
```

The catalog update is visible to future pilot loads. Do not reload pilot only
to refresh the already-issued capsule. The task label is stored on its context
receipt; keep the complete task in the harness conversation rather than
copying sensitive or raw content into that label.

Import gives every record a new ID. Reimporting a definition installs its
checked-in base as a new immutable version while retaining journal history, so
it is an explicit update operation—not an idempotent setup command. Inspect and
reconcile an existing agent before choosing to reimport it.
