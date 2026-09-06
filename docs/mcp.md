# Agent tools over MCP

`nine-tails mcp` is an optional local stdio adapter. A host launches the process and consumes newline-delimited JSON-RPC. It adds no network listener, model loop, scheduler, or separate knowledge store. Protocol negotiation supports MCP 2025-11-25; clients requesting another revision receive that supported revision and may disconnect.

Configure any compatible MCP client with:

```json
{"mcpServers":{"nine-tails":{"command":"/absolute/path/to/nine-tails","args":["mcp"]}}}
```

Use the absolute built binary path, or an executable available to the host. The default store is `~/.nine-tails`; an explicit `--home` selects another store. No global client configuration is changed by installation. A running host must connect the server before these operations appear in its native tool menu.

The tool menu stays stable:

| Tool | Purpose |
| --- | --- |
| `nt_load` | Load an agent, record its receipt, and return current guidance plus relevant experience. |
| `nt_learn` | Add, correct, consolidate, or retire knowledge through its owning receipt. |
| `nt_inspect` | Retrieve full records, search history, or inspect an agent. |
| `nt_tools` | Discover current agent-owned and shared executable capabilities using a receipt. |
| `nt_call` | Invoke a discovered capability through its receipt scope. |
| `nt_state` | Read/update named state or subscribe to shared state with compare-and-swap. |
| `nt_close` | Optional receipt bookkeeping; accepts no marks. |

For example, `nt_load({"agent":"game.playtester","task":"Check Soccer Chess passing lanes","meta":{"repo-id":"soccer-chess","harness":"my-host"}})` returns a capsule with a `context_id`. Use that exact id on subsequent calls. `nt_tools({"context":"ctx_..."})` returns tool descriptions, declared input fields and call templates. Discovery does not dynamically add each underlying tool to the host's native menu: execution goes through `nt_call`.

On state updates, omitted `meta` preserves scope and an explicit empty `meta: {}` clears it. Nonempty metadata replaces the complete set, with the same key grammar as the CLI. JSON numbers in tool inputs retain their exact decimal representation. Successful script stdout, including whitespace and empty output, is preserved in the result; script stderr remains on the MCP process stderr.

For `nt_learn` corrections with `supersedes`, omitted `body` preserves the prior
text and omitted `meta` preserves the prior record's complete scope. Explicit
`meta` replaces it exactly, including an empty `meta: {}`. The boolean
`clear_meta: true` also removes all scope and is mutually
exclusive with `meta`; `false` has the same effect as omission. New lessons are
unqualified unless `meta` is provided, regardless of the receipt's ambient scope.
New lessons require a nonempty `body`; ordinary corrections may omit it with a
nonempty `supersedes`. An explicitly empty `body` is invalid, including on corrections.
When both `body` and `kind` are omitted, the exact predecessor's lane and kind
are preserved, including custom guidance or recall kinds. Explicit `kind`
requests a type change subject to the normal same-agent and same-lane checks.
When `body` is supplied, omitted `kind` retains the usual `note` default.

Two other `nt_learn` actions remove the need for a separate maintenance loop:

- `sources: ["@17", "@23"], body: "complete replacement", reason: "why these belong together"`
  consolidates at least two distinct active guidance records with identical
  scope. Omitted `kind` is inferred only when source kinds agree; `remember`
  is invalid for consolidation. Sources cannot accompany `supersedes`, `meta`,
  `clear_meta`, or `forget`. The receipt must own every source. The new record
  retains the reason and historical sources; stale input rejects the operation.
- `forget: "@17", reason: "why this is obsolete"` retires that exact active
  record without a successor. It cannot accompany body, kind, sources,
  supersedes, meta, or clear_meta. History remains inspectable with the decision.

Both require the existing `context` field. `reason` is invalid for ordinary
append/correction actions. Consolidation and forgetting use the same atomic
operations and validation as the CLI; no model service runs inside nine-tails.

For caller-selected recall, `nt_load({"agent":"architect", "recall":["@17"]})`
loads every requested eligible memory in the supplied order, with no
record-count limit. Omission uses the lexical task/query fallback; `recall: []`
selects none. Automatic recall uses a soft rendered-size target and reports
`recall_more` plus an optional `recall_next` inspection hint for omitted matches.
Wrong owner, lane, scope or invalid body rejects the whole load;
inactive IDs conflict and are never silently redirected. Only the selected
evidence is recorded on the receipt. `task`/`query` still focuses the excerpts.

When memories exist, `nt_load` also includes `library: {count, inspect}`. Browse
without reloading the agent using `nt_inspect({"page":true,"context":"@42"})`,
optionally adding `query`. Alternatively supply an agent `target`; a supplied
context must own it and supplies scope. Pages contain bounded previews, exact
full-record inspection paths and a nullable `next` with its last-returned
`after` reference. Continue with `nt_inspect` using that `after` and the same
target/context/query. Page mode accepts only recall, and rejects include;
`context` and `after` are invalid outside page mode. Ordinary full inspection
still requires `target`.
Page syntax is checked before local reference resolution or store access.
Incompatible arguments remain protocol errors; invalid selector, context, or
cursor syntax is an operation failure with `isError`, as with other tool errors.

This is a live chronological catalog, not load's lexical ranking or a saved
snapshot. A retired cursor retains its position; restart for newer entries or
changed filters. Page previews and inventory counts are data, never delivered
standing instructions, and browsing does not create a receipt.

To include shared facts automatically in future loads, use
`nt_state({"name":"game.engineer/project","target":"workshop/soccer-chess","expect":"none","meta":{"repo-id":"soccer-chess"}})`.
`target` selects an immutable state-link definition and is mutually exclusive
with `body`; `expect` is the current link ID or `none`. A bare link name requires
the subscribing agent's `context`. Links use exactly the supplied metadata;
omitted or empty `meta` is unqualified, including replacement. Both link and
target scopes must apply on load. Resolution is one hop, and the returned state
names its real owner and exact version. Missing targets give a nonfatal skipped
diagnostic; link writes never update the target. Inspect or disable link records
through their normal CLI operations. This extends `nt_state`, keeping the native
tool menu stable.

Each invocation carries its own receipt. Loading a second agent does not change the first agent's calls or mutate connection-wide persona state. Current definitions and metadata filtering are shared with CLI behavior. Responses include `isError` for operation failures; invalid protocol requests and unknown tool arguments use JSON-RPC errors. Initialization and discovery of the fixed MCP menu do not open the knowledge store.

For `nt_load` with a parent `context`, each supplied `meta` key replaces all
inherited values for that key. Unspecified keys inherit. For example,
`meta: {"repo-id":"soccer-chess"}` switches a framework parent to the game
project while retaining its `harness`. An array deliberately selects multiple
values and exact duplicates collapse. Omitted metadata or `meta: {}` inherits
unchanged. Parent receipts are immutable; only the new receipt gets the resolved
scope, which governs its capsules, tool discovery, and calls. This invocation
inheritance rule does not copy ambient scope onto lessons or new state.

Scripts run from the MCP server's launch directory. Launch the server in the project where its scripts should operate, or use a separate server process per working directory. A receipt carries applicability, not a filesystem working directory or authorization grant. Tools keep their declared execution timeouts. This first adapter processes requests sequentially and treats cancellation notifications as advisory; it does not implement HTTP transport, progressive output, or server-initiated model calls.

Bind `repo-id` to the checkout selected for this invocation. Portable handoffs
store repository identity, repository-relative artifact paths, and a commit or
version; resolve and verify them in that checkout before use. Historical paths
in state or recall do not change the MCP server's launch directory.

The authoritative learning operations remain the CLI/store functions. The adapter never infers permissions, copies transcripts into memory, or changes the user's host configuration.

Protocol references: [Lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle), [stdio transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports), [tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools).

Receipt arguments accept the capsule's `context_ref` (for example `@42`) as
well as its canonical `context_id`. `nt_inspect` accepts any local `@N`
reference; correction `supersedes` and state `expect` arguments also accept
record references. Resolution preserves the usual type, ownership, CAS and
lease rules. Task text, content, metadata and tool input are never rewritten.
Use `nine-tails refs --meta repo-id=soccer-chess` to browse a readable project
inventory. References belong to one local store; exports use canonical IDs.
