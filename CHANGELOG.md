# Changelog

## 0.2.0

The first release after 0.1.0 collects what a month of harness-driven use and
one unattended research-lab soak exposed. Stores written by 0.1.0 need one
explicit `nine-tails migrate`; see the upgrade note in the README.

### Added

- `disable <id> --context <receipt> --reason "..."` retires a record without
  deleting it. The record stays inspectable with the decision; its name is
  free for a new definition; compiled guidance that depended on it is
  invalidated. (`5035124`)
- `--dedupe-key K` on `remember`, `note`, `prefer`, `avoid` and `append`, and
  `dedupe_key` on the MCP `nt_learn` tool. The first write claims the key for
  the agent and lane; a replay returns the existing record, or its current
  successor, with `deduplicated: true` and writes nothing. A key whose record
  was retired conflicts (exit 7). This replaces the lookup-by-body replay
  logic that callers had to write around crash-prone write-then-acknowledge
  sequences.
- `inspect <agent> --meta key=value` filters records by exact metadata pairs,
  also as `meta` on the MCP `nt_inspect` tool. `--query` remains the
  substring search.
- `migrate` upgrades a store's schema explicitly, after copying the database
  to `nine-tails.db.v<old>.bak`. Every other command refuses an older store
  with exit 4 and names `migrate`, so a newer binary pointed at a shared or
  bundled store cannot upgrade it one-way by accident.
- `--version` prints the commit the binary was built from. Release builds set
  it through linker flags; a plain `go build` reads the embedded VCS stamp.
- An optional recall selector (`selector: {argv: [...], timeout: "5s"}` in
  `config.yaml`): `load` asks that executable which recall records to deliver
  when no explicit `--recall` was given and falls back to lexical retrieval
  on any failure. DESIGN §7.1.
- Consolidation of overlapping guidance or recall into one replacement with
  recorded sources and a reason; `inspect --review` for one receipt's
  delivered records, episode writes and retirement decisions; `inspect --page`
  for browsing a large recall library; `close` with per-record marks;
  readable local `@N` references; receipt closure and context marks.

### Changed

- Record and context identifiers are prefix + ULID; the global counter is
  gone. Existing identifiers are preserved by migration.
- Schema version 6. Migrations from any 0.1.0 store (version 2) are covered.
- Subprocess timeout test fixtures tolerate cold starts and still detect a
  surviving child.

### Upgrade

```sh
nine-tails --version        # 0.2.0 (<commit>)
nine-tails migrate          # once per store; writes nine-tails.db.v2.bak first
```
