// Package store is the only package that touches SQL. It owns the schema,
// identifier allocation, immutable records with their metadata multimap,
// context receipts, brief generations, signal delivery state, and the
// compare-and-swap rules for named records. It does no rendering.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Sentinel errors mapped to exit codes by the cli layer.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid")
)

// Clock supplies "now" for every timestamp and comparison. main replaces it
// when NINE_TAILS_NOW is set or a test injects a fixed time.
var Clock = time.Now

// Store wraps one SQLite database under a nine-tails home directory.
type Store struct {
	DB   *sql.DB
	Home string
}

// HomeDir returns NINE_TAILS_HOME or ~/.nine-tails.
func HomeDir() (string, error) {
	if h := os.Getenv("NINE_TAILS_HOME"); h != "" {
		return h, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".nine-tails"), nil
}

const userVersion = 2 // 2: contexts.token_budget became estimated_tokens

// Open opens (creating if needed) the store under home.
func Open(home string) (*Store, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("create home: %w", err)
	}
	for _, d := range []string{"artifacts", "exports"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	dsn := "file:" + filepath.Join(home, "nine-tails.db") +
		"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// One connection: every write is BEGIN IMMEDIATE, reads inside a Tx use
	// the same connection, and other processes coordinate through WAL +
	// busy_timeout.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Home: home}
	// Changing journal mode and creating the schema both need database-wide
	// locks. Several first-time callers may arrive together (for example a
	// harness starting multiple agents), so retry SQLITE_BUSY around the
	// idempotent initialization sequence. busy_timeout handles ordinary lock
	// waits; this loop also covers PRAGMA journal_mode, which can report BUSY
	// immediately while another connection is changing it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = s.migrate()
		if err == nil {
			break
		}
		if !isBusy(err) || !time.Now().Before(deadline) {
			db.Close()
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return s, nil
}

func isBusy(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "database is locked") || strings.Contains(s, "sqlite_busy")
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS seq (n INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS records (
    id                TEXT PRIMARY KEY,
    agent             TEXT NOT NULL,
    lane              TEXT NOT NULL,
    kind              TEXT NOT NULL,
    name              TEXT,
    body              TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    origin_context_id TEXT,
    status            TEXT NOT NULL DEFAULT 'active',
    supersedes_id     TEXT
);
CREATE INDEX IF NOT EXISTS records_agent_lane_kind ON records(agent, lane, kind, status);
CREATE UNIQUE INDEX IF NOT EXISTS records_active_name ON records(agent, lane, kind, name)
    WHERE name IS NOT NULL AND status = 'active';

CREATE TABLE IF NOT EXISTS metadata (
    record_id TEXT NOT NULL REFERENCES records(id) ON DELETE CASCADE,
    key       TEXT NOT NULL,
    value     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS metadata_lookup ON metadata(key, value, record_id);
CREATE INDEX IF NOT EXISTS metadata_record ON metadata(record_id);

CREATE TABLE IF NOT EXISTS brief_generations (
    id         TEXT PRIMARY KEY,
    agent      TEXT NOT NULL,
    parent_id  TEXT,
    created_at TEXT NOT NULL,
    status     TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS brief_one_active_generation ON brief_generations(agent)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS brief_generation_items (
    generation_id TEXT NOT NULL,
    record_id     TEXT NOT NULL,
    ordinal       INTEGER NOT NULL,
    PRIMARY KEY (generation_id, record_id)
);

CREATE TABLE IF NOT EXISTS brief_inputs (
    generation_id       TEXT NOT NULL,
    entry_record_id     TEXT NOT NULL,
    disposition         TEXT NOT NULL,
    coverage            TEXT NOT NULL,
    successor_record_id TEXT,
    PRIMARY KEY (generation_id, entry_record_id)
);
CREATE INDEX IF NOT EXISTS brief_inputs_entry ON brief_inputs(entry_record_id);

CREATE TABLE IF NOT EXISTS brief_item_sources (
    generation_id    TEXT NOT NULL,
    item_record_id   TEXT NOT NULL,
    entry_record_id  TEXT NOT NULL,
    PRIMARY KEY (generation_id, item_record_id, entry_record_id)
);

CREATE TABLE IF NOT EXISTS brief_equivalents (
    generation_id        TEXT NOT NULL,
    entry_record_id      TEXT NOT NULL,
    equivalent_record_id TEXT NOT NULL,
    PRIMARY KEY (generation_id, entry_record_id, equivalent_record_id)
);

CREATE TABLE IF NOT EXISTS contexts (
    id                TEXT PRIMARY KEY,
    agent             TEXT NOT NULL,
    parent_context_id TEXT,
    task              TEXT,
    estimated_tokens  INTEGER NOT NULL,
    created_at        TEXT NOT NULL,
    pinned            INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS context_metadata (
    context_id TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS context_metadata_ctx ON context_metadata(context_id);

CREATE TABLE IF NOT EXISTS context_records (
    context_id TEXT NOT NULL,
    record_id  TEXT NOT NULL,
    section    TEXT NOT NULL,
    ordinal    INTEGER NOT NULL,
    PRIMARY KEY (context_id, record_id)
);
CREATE INDEX IF NOT EXISTS context_records_record ON context_records(record_id);

CREATE TABLE IF NOT EXISTS signal_delivery (
    record_id       TEXT PRIMARY KEY,
    agent           TEXT NOT NULL,
    available_at    TEXT NOT NULL,
    dedupe_key      TEXT,
    state           TEXT NOT NULL,
    lease_token     TEXT,
    leased_until    TEXT,
    acknowledged_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS signal_dedupe ON signal_delivery(agent, dedupe_key)
    WHERE dedupe_key IS NOT NULL AND state != 'acknowledged';
`

func (s *Store) migrate() error {
	var v int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > userVersion {
		return fmt.Errorf("store schema version %d is newer than this binary supports (%d)", v, userVersion)
	}
	var mode string
	if err := s.DB.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("enable WAL: sqlite selected journal mode %q", mode)
	}

	// Schema creation, sequence initialization, and version publication are
	// one immediate transaction. INSERT ... WHERE NOT EXISTS avoids the
	// check-then-insert race that could otherwise create two seq rows.
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > userVersion {
		return fmt.Errorf("store schema version %d is newer than this binary supports (%d)", v, userVersion)
	}
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if v == 1 {
		// v2: a receipt records the capsule's estimated size, not a budget.
		if _, err := tx.Exec(`ALTER TABLE contexts RENAME COLUMN token_budget TO estimated_tokens`); err != nil {
			return fmt.Errorf("migrate contexts to v2: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO seq(n) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM seq)`); err != nil {
		return fmt.Errorf("initialize sequence: %w", err)
	}
	if v < userVersion {
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, userVersion)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Now returns the current time formatted as RFC 3339 UTC with second precision.
func Now() string { return FormatTime(Clock()) }

// FormatTime formats t as RFC 3339 UTC with second precision.
func FormatTime(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

// Tx runs fn inside a BEGIN IMMEDIATE write transaction, committing on nil error.
func (s *Store) Tx(fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Querier is satisfied by both *sql.DB and *sql.Tx.
type Querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// NextID allocates the next identifier with the given prefix. Must be called
// inside a write transaction so the allocation is atomic with the insert.
func NextID(tx Querier, prefix string) (string, error) {
	if _, err := tx.Exec(`UPDATE seq SET n = n + 1`); err != nil {
		return "", err
	}
	var n int64
	if err := tx.QueryRow(`SELECT n FROM seq`).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%d", prefix, n), nil
}

// Prefix returns the identifier prefix for a record in the given lane/kind.
func Prefix(lane, kind string) string {
	switch {
	case lane == "definition" && kind == "agent-base":
		return "base"
	case kind == "brief-item":
		return "item"
	case lane == "state":
		return "state"
	case lane == "definition" && kind == "tool":
		return "tool"
	case lane == "definition" && kind == "related-agent":
		return "rel"
	case lane == "signal":
		return "sig"
	}
	return "rec"
}

var (
	idRe   = regexp.MustCompile(`^[a-z]+_[0-9]+$`)
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
)

// IsID reports whether s has the shape of a nine-tails identifier.
func IsID(s string) bool { return idRe.MatchString(s) }

// Reserved names that cannot be agents or record names.
var reservedNames = map[string]bool{"shared": true, "base": true, "ack": true, "none": true}

// ValidName checks a tool/state/related-agent/brief-item name.
func ValidName(what, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w: %s name %q must match ^[a-z0-9][a-z0-9.-]*$ (lowercase, no _ or /)", ErrInvalid, what, name)
	}
	if IsID(name) {
		return fmt.Errorf("%w: %s name %q looks like an identifier", ErrInvalid, what, name)
	}
	if reservedNames[name] {
		return fmt.Errorf("%w: %q is a reserved name", ErrInvalid, name)
	}
	return nil
}

// ValidAgentName checks an agent name; "shared" is allowed here because it is
// a real storage namespace, the other reserved words are not.
func ValidAgentName(name string) error {
	if name == "shared" {
		return nil
	}
	return ValidName("agent", name)
}

// ValidRecordName checks a name for a named record (definition/state/item).
func ValidRecordName(what, name string) error {
	// The one intentional use of a reserved name is the base definition's
	// conventional definition/agent-base/base tuple.
	if what == "agent-base" && name == "base" {
		return nil
	}
	return ValidName(what, name)
}

// ValidNamedRecord checks a name with the lane/kind tuple available. The
// reserved base name is valid only for the one conventional base definition;
// using kind agent-base elsewhere must not bypass the reservation.
func ValidNamedRecord(lane, kind, name string) error {
	if kind == "agent-base" {
		if lane != "definition" || name != "base" {
			return fmt.Errorf("%w: agent-base must be definition/agent-base named %q", ErrInvalid, "base")
		}
		return nil
	}
	if lane == "state" && kind != "working-state" {
		return fmt.Errorf("%w: state records use kind working-state (got %q); state get and state put resolve only that kind", ErrInvalid, kind)
	}
	return ValidRecordName(kind, name)
}

// NormalizeBody strips exactly one trailing newline. Bodies are otherwise
// stored verbatim.
func NormalizeBody(body string) string {
	return strings.TrimSuffix(body, "\n")
}

// Meta is a string multimap. Values keep insertion order; exact duplicate
// pairs are collapsed.
type Meta map[string][]string

// ValidateMeta enforces the persisted metadata contract. Keeping this check
// in the store prevents programmatic callers from bypassing the CLI parser:
// metadata is a UTF-8 string multimap and keys follow the mechanical key
// grammar from DESIGN.md §3.
func ValidateMeta(m Meta) error {
	for _, k := range SortedKeys(m) {
		if !utf8.ValidString(k) {
			return fmt.Errorf("%w: metadata key %q must be valid UTF-8", ErrInvalid, k)
		}
		if k == "" {
			return fmt.Errorf("%w: metadata key may not be empty", ErrInvalid)
		}
		if strings.IndexFunc(k, unicode.IsSpace) >= 0 || strings.ContainsAny(k, "[]=") {
			return fmt.Errorf("%w: metadata key %q may not contain whitespace, '=', '[' or ']'", ErrInvalid, k)
		}
		for _, v := range m[k] {
			if !utf8.ValidString(v) {
				return fmt.Errorf("%w: metadata value for %q must be valid UTF-8", ErrInvalid, k)
			}
		}
	}
	return nil
}

// ValidateBody enforces the record-envelope requirement that bodies are
// UTF-8 text. Artifact bytes do not pass through this function and remain
// unrestricted.
func ValidateBody(body string) error {
	if !utf8.ValidString(body) {
		return fmt.Errorf("%w: body must be valid UTF-8 text", ErrInvalid)
	}
	return nil
}

// Add appends value under key, trimming surrounding whitespace on both and
// ignoring an exact duplicate pair.
func (m Meta) Add(key, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if m.Contains(key, value) {
		return
	}
	m[key] = append(m[key], value)
}

// Has reports whether key has any values.
func (m Meta) Has(key string) bool { return len(m[key]) > 0 }

// First returns the first value for key or "".
func (m Meta) First(key string) string {
	if v := m[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Contains reports whether value is among key's values.
func (m Meta) Contains(key, value string) bool {
	for _, v := range m[key] {
		if v == value {
			return true
		}
	}
	return false
}

// Clone returns a deep copy.
func (m Meta) Clone() Meta {
	out := make(Meta, len(m))
	for k, vs := range m {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// Merge unions other into m (value sets per key, preserving order, no dups).
func (m Meta) Merge(other Meta) {
	for _, k := range SortedKeys(other) {
		for _, v := range other[k] {
			m.Add(k, v)
		}
	}
}

// ParseMeta parses "key=value" strings into a Meta. The split is at the first
// "="; a missing "=" or an empty key is ErrInvalid; keys may not contain
// whitespace, "=", "[" or "]".
func ParseMeta(pairs []string) (Meta, error) {
	m := Meta{}
	for _, p := range pairs {
		if !utf8.ValidString(p) {
			return nil, fmt.Errorf("%w: metadata must be valid UTF-8", ErrInvalid)
		}
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("%w: metadata must be key=value, got %q", ErrInvalid, p)
		}
		if strings.IndexFunc(k, unicode.IsSpace) >= 0 || strings.ContainsAny(k, "[]=") {
			return nil, fmt.Errorf("%w: metadata key %q may not contain whitespace, '=', '[' or ']'", ErrInvalid, k)
		}
		m.Add(k, v)
	}
	if err := ValidateMeta(m); err != nil {
		return nil, err
	}
	return m, nil
}

// Conflicts reports whether rec's metadata conflicts with ctx: some key is
// present on both sides with disjoint value sets (spec §8.2).
func Conflicts(rec, ctx Meta) bool {
	for k, rv := range rec {
		cv, ok := ctx[k]
		if !ok || len(cv) == 0 || len(rv) == 0 {
			continue
		}
		hit := false
		for _, v := range rv {
			if ctx.Contains(k, v) {
				hit = true
				break
			}
		}
		if !hit {
			return true
		}
	}
	return false
}

// Overlap counts distinct (key, value) pairs shared between rec and ctx.
func Overlap(rec, ctx Meta) int {
	n := 0
	for k, rv := range rec {
		for _, v := range rv {
			if ctx.Contains(k, v) {
				n++
			}
		}
	}
	return n
}

// Record is the generic immutable envelope (spec §8.1).
type Record struct {
	ID            string `json:"id" yaml:"id"`
	Agent         string `json:"agent" yaml:"agent"`
	Lane          string `json:"lane" yaml:"lane"`
	Kind          string `json:"kind" yaml:"kind"`
	Name          string `json:"name,omitempty" yaml:"name,omitempty"`
	Body          string `json:"body" yaml:"body"`
	CreatedAt     string `json:"created_at" yaml:"created_at"`
	OriginContext string `json:"origin_context,omitempty" yaml:"origin_context,omitempty"`
	Status        string `json:"status" yaml:"status"`
	Supersedes    string `json:"supersedes,omitempty" yaml:"supersedes,omitempty"`
	Meta          Meta   `json:"meta" yaml:"meta"`
}

// RecordEnvelope is the stable machine-readable representation of Record.
// The database-facing Record uses strings for nullable columns so callers do
// not have to unwrap pointers. The external contract requires the optional
// fields to be present as explicit nulls (DESIGN §4).
type RecordEnvelope struct {
	ID            string  `json:"id" yaml:"id"`
	Agent         string  `json:"agent" yaml:"agent"`
	Lane          string  `json:"lane" yaml:"lane"`
	Kind          string  `json:"kind" yaml:"kind"`
	Name          *string `json:"name" yaml:"name"`
	Body          string  `json:"body" yaml:"body"`
	CreatedAt     string  `json:"created_at" yaml:"created_at"`
	OriginContext *string `json:"origin_context" yaml:"origin_context"`
	Status        string  `json:"status" yaml:"status"`
	Supersedes    *string `json:"supersedes" yaml:"supersedes"`
	Meta          Meta    `json:"meta" yaml:"meta"`
}

func stringPointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Envelope converts the convenient in-memory form to the stable external
// form used by inspect, export, and compile input.
func (r Record) Envelope() RecordEnvelope {
	meta := r.Meta
	if meta == nil {
		meta = Meta{}
	}
	return RecordEnvelope{
		ID:            r.ID,
		Agent:         r.Agent,
		Lane:          r.Lane,
		Kind:          r.Kind,
		Name:          stringPointer(r.Name),
		Body:          r.Body,
		CreatedAt:     r.CreatedAt,
		OriginContext: stringPointer(r.OriginContext),
		Status:        r.Status,
		Supersedes:    stringPointer(r.Supersedes),
		Meta:          meta,
	}
}

// MarshalJSON keeps the record envelope identical wherever a Record appears.
func (r Record) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Envelope())
}

// MarshalYAML keeps the YAML contract identical to the JSON contract.
func (r Record) MarshalYAML() (any, error) {
	return r.Envelope(), nil
}

// NewRecord holds the caller-supplied fields for InsertRecord.
type NewRecord struct {
	ID            string // optional: an id already allocated with NextID in this tx (tool add needs it for artifact paths)
	Agent         string
	Lane          string
	Kind          string
	Name          string // "" means NULL
	Body          string
	OriginContext string // "" means NULL
	Supersedes    string // "" means NULL; the superseded record is marked in the same tx
	Meta          Meta
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// InsertRecord inserts a new active record (allocating its ID) and, when
// Supersedes is set, marks that record superseded. Must run inside Tx.
func InsertRecord(tx Querier, nr NewRecord) (*Record, error) {
	if nr.Agent == "" || nr.Lane == "" || nr.Kind == "" {
		return nil, fmt.Errorf("%w: agent, lane and kind are required", ErrInvalid)
	}
	if err := ValidateBody(nr.Body); err != nil {
		return nil, err
	}
	if err := ValidateMeta(nr.Meta); err != nil {
		return nil, err
	}
	if nr.Supersedes != "" {
		res, err := tx.Exec(`UPDATE records SET status = 'superseded' WHERE id = ? AND status = 'active'`, nr.Supersedes)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil, fmt.Errorf("%w: %s is not an active record", ErrConflict, nr.Supersedes)
		}
	}
	id := nr.ID
	if id == "" {
		var err error
		id, err = NextID(tx, Prefix(nr.Lane, nr.Kind))
		if err != nil {
			return nil, err
		}
	}
	now := Now()
	_, err := tx.Exec(`INSERT INTO records(id, agent, lane, kind, name, body, created_at, origin_context_id, status, supersedes_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
		id, nr.Agent, nr.Lane, nr.Kind, nullable(nr.Name), nr.Body, now, nullable(nr.OriginContext), nullable(nr.Supersedes))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("%w: an active %s/%s named %q already exists for %s", ErrConflict, nr.Lane, nr.Kind, nr.Name, nr.Agent)
		}
		return nil, err
	}
	if err := insertMeta(tx, id, nr.Meta); err != nil {
		return nil, err
	}
	rec := &Record{
		ID: id, Agent: nr.Agent, Lane: nr.Lane, Kind: nr.Kind, Name: nr.Name, Body: nr.Body,
		CreatedAt: now, OriginContext: nr.OriginContext, Status: "active", Supersedes: nr.Supersedes,
		Meta: nr.Meta.Clone(),
	}
	if rec.Meta == nil {
		rec.Meta = Meta{}
	}
	return rec, nil
}

func insertMeta(tx Querier, id string, m Meta) error {
	for _, k := range SortedKeys(m) {
		for _, v := range m[k] {
			if _, err := tx.Exec(`INSERT INTO metadata(record_id, key, value) VALUES (?, ?, ?)`, id, k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetStatus changes a record's mechanical status in place (active|superseded|disabled).
func SetStatus(tx Querier, id, status string) error {
	res, err := tx.Exec(`UPDATE records SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: record %s", ErrNotFound, id)
	}
	return nil
}

// SetBody overwrites a record body in place. This bypasses immutability and
// exists ONLY so tests can inject corrupt records (AC19). Never call it from
// a command.
func SetBody(tx Querier, id, body string) error {
	_, err := tx.Exec(`UPDATE records SET body = ? WHERE id = ?`, body, id)
	return err
}

const recordCols = `id, agent, lane, kind, COALESCE(name, ''), body, created_at, COALESCE(origin_context_id, ''), status, COALESCE(supersedes_id, '')`

func scanRecord(sc interface{ Scan(...any) error }) (*Record, error) {
	r := &Record{Meta: Meta{}}
	if err := sc.Scan(&r.ID, &r.Agent, &r.Lane, &r.Kind, &r.Name, &r.Body, &r.CreatedAt, &r.OriginContext, &r.Status, &r.Supersedes); err != nil {
		return nil, err
	}
	return r, nil
}

// GetRecord loads one record with its metadata.
func GetRecord(q Querier, id string) (*Record, error) {
	r, err := scanRecord(q.QueryRow(`SELECT `+recordCols+` FROM records WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: record %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	if err := loadMeta(q, []*Record{r}); err != nil {
		return nil, err
	}
	return r, nil
}

// RecordExists reports whether id is any record.
func RecordExists(q Querier, id string) (bool, error) {
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM records WHERE id = ?`, id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// AgentExists reports whether any record (any status) carries the agent name.
func AgentExists(q Querier, agent string) (bool, error) {
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM records WHERE agent = ?`, agent).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// loadMeta fills Meta for every record in recs in one query.
func loadMeta(q Querier, recs []*Record) error {
	if len(recs) == 0 {
		return nil
	}
	byID := make(map[string]*Record, len(recs))
	args := make([]any, 0, len(recs))
	ph := make([]string, 0, len(recs))
	for _, r := range recs {
		byID[r.ID] = r
		args = append(args, r.ID)
		ph = append(ph, "?")
		if r.Meta == nil {
			r.Meta = Meta{}
		}
	}
	rows, err := q.Query(`SELECT record_id, key, value FROM metadata WHERE record_id IN (`+strings.Join(ph, ",")+`) ORDER BY rowid`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, k, v string
		if err := rows.Scan(&id, &k, &v); err != nil {
			return err
		}
		if r := byID[id]; r != nil {
			r.Meta[k] = append(r.Meta[k], v)
		}
	}
	return rows.Err()
}

// Filter selects records. Empty fields mean "any". Status "" means active only;
// "*" means all statuses.
type Filter struct {
	Agent  string
	Lane   string
	Kind   string
	Name   string
	Status string
	IDs    []string
}

// ListRecords returns matching records in creation order (created_at, rowid),
// with metadata loaded.
func ListRecords(q Querier, f Filter) ([]*Record, error) {
	var where []string
	var args []any
	add := func(cond string, v any) { where = append(where, cond); args = append(args, v) }
	if f.Agent != "" {
		add("agent = ?", f.Agent)
	}
	if f.Lane != "" {
		add("lane = ?", f.Lane)
	}
	if f.Kind != "" {
		add("kind = ?", f.Kind)
	}
	if f.Name != "" {
		add("name = ?", f.Name)
	}
	switch f.Status {
	case "":
		add("status = ?", "active")
	case "*":
	default:
		add("status = ?", f.Status)
	}
	if len(f.IDs) > 0 {
		ph := make([]string, len(f.IDs))
		for i, id := range f.IDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "id IN ("+strings.Join(ph, ",")+")")
	}
	query := `SELECT ` + recordCols + ` FROM records`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at, rowid"
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := loadMeta(q, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ActiveNamed returns the active record with the given agent/lane/kind/name,
// or ErrNotFound.
func ActiveNamed(q Querier, agent, lane, kind, name string) (*Record, error) {
	recs, err := ListRecords(q, Filter{Agent: agent, Lane: lane, Kind: kind, Name: name})
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("%w: no active %s/%s named %q for agent %s", ErrNotFound, lane, kind, name, agent)
	}
	return recs[0], nil
}

// Resolve finds a named definition visible to agent: the agent's own active
// definition first, then the conventional "shared" namespace (spec §13.4).
// For shared records with an `available-to` metadata key, the agent must be
// listed; shared records without that key are visible to everyone.
func Resolve(q Querier, agent, kind, name string) (*Record, error) {
	if r, err := ActiveNamed(q, agent, "definition", kind, name); err == nil {
		return r, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if agent == "shared" {
		return nil, fmt.Errorf("%w: no %s named %q", ErrNotFound, kind, name)
	}
	r, err := ActiveNamed(q, "shared", "definition", kind, name)
	if err != nil {
		return nil, fmt.Errorf("%w: no %s named %q visible to %s", ErrNotFound, kind, name, agent)
	}
	if r.Meta.Has("available-to") && !r.Meta.Contains("available-to", agent) {
		return nil, fmt.Errorf("%w: %s %q is shared but not available to %s", ErrNotFound, kind, name, agent)
	}
	return r, nil
}

// PutNamed creates a new active named record superseding the current one.
// expect: "" = supersede whatever is active (or create); "none" = must not
// exist; otherwise the ID that must currently be active. Must run inside Tx.
func PutNamed(tx Querier, nr NewRecord, expect string) (*Record, error) {
	if nr.Name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	cur, err := ActiveNamed(tx, nr.Agent, nr.Lane, nr.Kind, nr.Name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	switch {
	case expect == "none":
		if cur != nil {
			return nil, fmt.Errorf("%w: expected no active %s/%s named %q but %s is active", ErrConflict, nr.Lane, nr.Kind, nr.Name, cur.ID)
		}
	case expect == "":
	default:
		if cur == nil {
			return nil, fmt.Errorf("%w: expected %s to be active but nothing is", ErrConflict, expect)
		}
		if cur.ID != expect {
			return nil, fmt.Errorf("%w: expected %s but %s is active", ErrConflict, expect, cur.ID)
		}
	}
	if cur != nil {
		nr.Supersedes = cur.ID
	}
	return InsertRecord(tx, nr)
}

// RecentGuidance is the ONLY implementation of DESIGN §7 rule 4: active
// lane=guidance records for the agent, excluding brief items, that are absent
// from the active generation's accounting or deferred by it. Accounting in an
// old generation must not hide source guidance after a replacement generation
// drops the corresponding item. Returned oldest first.
func RecentGuidance(q Querier, agent string) ([]*Record, error) {
	recs, err := ListRecords(q, Filter{Agent: agent, Lane: "guidance"})
	if err != nil {
		return nil, err
	}
	represented := map[string]bool{}
	if gen, err := ActiveGeneration(q, agent); err == nil {
		represented, err = RepresentedEntryIDs(q, gen.ID)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var out []*Record
	for _, r := range recs {
		if r.Kind == "brief-item" {
			continue
		}
		if represented[r.ID] {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// Agents lists distinct agent names having at least one record (any status).
func Agents(q Querier) ([]string, error) {
	rows, err := q.Query(`WITH ranked AS (
		SELECT agent, created_at, rowid,
			ROW_NUMBER() OVER (PARTITION BY agent ORDER BY created_at, rowid) AS ordinal
		FROM records
	)
	SELECT agent FROM ranked WHERE ordinal = 1 ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SortedKeys returns m's keys in sorted order.
func SortedKeys(m Meta) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
