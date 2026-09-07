# Agent tools over MCP

`nine-tails mcp` is an optional local stdio adapter. A host launches the process and consumes newline-delimited JSON-RPC. It adds no network listener, model loop, scheduler, or separate knowledge store. Protocol negotiation supports MCP 2025-11-25; clients requesting another revision receive that supported revision and may disconnect.

Request IDs follow that revision's [string-or-integer requirement](https://modelcontextprotocol.io/specification/2025-11-25/basic/index).
Fractional values are invalid requests. Integral decimal/exponent forms and
large integers retain their exact input representation in replies.

Configure any compatible MCP client with:

```json
{"mcpServers":{"nine-tails":{"command":"/absolute/path/to/nine-tails","args":["mcp"]}}}
```

Use the absolute built binary path, or an executable available to the host. The default store is `~/.nine-tails`; an explicit `--home` selects the store. Installing the binary alone does not change global client configuration. A running host must connect the server before these operations appear in its native tool menu.

For an installed binary at `~/.nine-tails/bin/nine-tails`, register it with Codex:

```sh
codex mcp add nine-tails -- "$HOME/.nine-tails/bin/nine-tails" --home "$HOME/.nine-tails" mcp
codex mcp get nine-tails --json
```

This explicitly binds the executable and shared store without depending on a
checkout path. The desktop app and CLI share the host's configuration. Refresh
the desktop connection in **Settings → MCP servers → nine-tails → Restart**,
or start a fresh CLI session. Verify that `nt_load`, `nt_learn`, `nt_inspect`
and the other tools appear; an enabled config entry alone is not proof that
the current conversation connected. See the
[official Codex MCP setup](https://learn.chatgpt.com/docs/extend/mcp).

Once connected, prefer the native tools for ordinary work. If a capsule is
already loaded, reuse its receipt through MCP; connecting is not a reason to
load the agent again. Forward the whole load result, including data sections.
Use the checkout binary and isolated stores to verify code changes; an existing
MCP process keeps its installed executable until the connection is restarted.

Agent-owned scripts execute in the MCP process's launch directory. A different
`repo-id` on a load does not change that directory. Inspect capabilities before
calling them and resolve their repository inputs explicitly; load, learning,
inspection and named-state operations use the selected store independently.

When the server uses a nondefault home, generated CLI recipes in capsules and
library pages carry its absolute, shell-quoted `--home` selection. Copying a
recipe outside the MCP launch environment therefore keeps the same store.
MCP calls themselves continue to use the server's selected store. A local `@N`
from another store is unrelated even when its number happens to match.

Successful operations return their stdout payload unchanged. Informational
diagnostics, including first-use seeding and skipped state links, go to server
stderr. A successful load therefore remains parseable JSON, and tool output
keeps its whitespace or empty body. Failed operations still return useful
error details and leave the connection available for subsequent calls.
Malformed arguments are rejected before local-reference lookup or store access.
Failure text joins nonempty stdout and stderr with one newline; an otherwise
empty failure receives an explicit failure message.

Forward the whole `nt_load` result to the working model. Its `instructions`
field excludes referenced state, recall, library navigation and signals;
preserve those sections as data. The receipt describes the complete projection,
so extracting only instructions would silently drop selected context.

The tool menu stays stable:

For a focused episode review, call
`nt_inspect({"target":"@42","review":true})`. This read-only packet contains
the receipt's delivered records, episode-originated writes, and retirement
decisions. Full-source inspection recipes and current successors accompany the
historical references. Follow a non-null `next` by supplying its `review_after`
with the same target and `review: true`. Counts expose remaining entries. The
packet does not contain the conversation or prove that a memory was applied;
the working model supplies actual feedback before drawing that conclusion.

| Tool | Purpose |
| --- | --- |
| `nt_load` | Load an agent, record its receipt, and return current guidance plus relevant experience. |
| `nt_learn` | Add, correct, consolidate, or retire knowledge through its owning receipt. |
| `nt_inspect` | Retrieve full records, search history, inspect an agent, or review an episode's recorded evidence. |
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
  consolidates at least two distinct active guidance or recall records with the
  same owner, lane, and identical scope. Omitted `kind` is inferred only when
  source kinds agree; ordinary `memory` sources therefore produce a recall `memory`.
  `kind: "remember"` is invalid here because `remember` is the ordinary recall
  write shorthand; omit `kind` to preserve the recall lane. Sources cannot
  accompany `supersedes`, `meta`, `clear_meta`, or `forget`. The receipt must
  own every source. The new record retains the reason and historical sources;
  stale input rejects the operation.
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

When memories exist, `nt_load` also includes `library: {count, inspect, check}`. Browse
without reloading the agent using `nt_inspect({"page":true,"context":"@42"})`,
optionally adding `query`. Alternatively supply an agent `target`; a supplied
context must own it and supplies scope. Pages contain bounded previews, exact
full-record inspection paths and a nullable `next` with its last-returned
`after` reference. Continue with `nt_inspect` using that `after` and the same
target/context/query. Page mode accepts only recall, and rejects include;
`after` is invalid outside page mode. Ordinary full inspection still requires
`target`.
Page syntax is checked before local reference resolution or store access.
Incompatible arguments remain protocol errors; invalid selector, context, or
cursor syntax is an operation failure with `isError`, as with other tool errors.

This is a live chronological catalog, not load's lexical ranking or a saved
snapshot. A retired cursor retains its position; restart for newer entries or
changed filters. Page previews and inventory counts are data, never delivered
standing instructions, and browsing does not create a receipt.

For unexpected recall, use
`nt_inspect({"target":"@50","context":"@42","query":"undo"})` with page
absent or false. This returns the exact recall record and `recall_check`:
immutable delivery evidence alongside today's lexical selection under the
receipt's scope. Omitted query uses the receipt task; explicit empty query
remains empty. Lane, include and after are invalid in this mode. Syntax is
validated before reference resolution. The operation neither creates a receipt
nor changes memory. Save its JSON as a project artifact with a relevance
rationale when a real problem occurs. It discloses historical limits and
cannot reconstruct an original query override or prove that guidance was used.

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
