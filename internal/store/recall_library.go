package store

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"modernc.org/sqlite"
)

// SQLite's built-in lower() folds ASCII only. This deterministic function
// retains inspect's Unicode strings.ToLower substring semantics without a
// persisted text index or loading the complete recall corpus into Go memory.
// A search may still scan eligible text in SQLite, one candidate at a time.
func init() {
	err := sqlite.RegisterDeterministicScalarFunction("nine_tails_contains", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			text, textOK := args[0].(string)
			query, queryOK := args[1].(string)
			if textOK && queryOK && strings.Contains(strings.ToLower(text), strings.ToLower(query)) {
				return int64(1), nil
			}
			return int64(0), nil
		})
	if err != nil {
		panic(fmt.Sprintf("register recall substring function: %v", err))
	}
}

// RecallIndexRequest selects a live recall library. Meta is already resolved
// applicability metadata, not a scope to write. After accepts an exact record
// ID or local reference and locates its original position, even if inactive.
type RecallIndexRequest struct {
	Agent string
	Meta  Meta
	Query string
	After string
}

// RecallIndexEntry carries a small preview, never a complete record envelope.
// The full immutable record remains available through ID or Ref inspection.
// Name and Kind are bounded display labels; inspect retains their exact values.
type RecallIndexEntry struct {
	ID        string `json:"id" yaml:"id"`
	Ref       string `json:"ref" yaml:"ref"`
	Name      string `json:"name,omitempty" yaml:"name,omitempty"`
	Kind      string `json:"kind" yaml:"kind"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	Excerpt   string `json:"excerpt" yaml:"excerpt"`
	Truncated bool   `json:"truncated" yaml:"truncated"`
}

// recallEligibility is shared by the count and streaming index. SQL follows
// Conflicts exactly: each context key present on a record must share at least
// one value; keys absent on either side do not conflict. r is the records alias.
func recallEligibility(agent string, meta Meta) (string, []any, error) {
	if err := ValidAgentName(agent); err != nil {
		return "", nil, err
	}
	if err := ValidateMeta(meta); err != nil {
		return "", nil, err
	}
	conditions := []string{"r.agent = ?", "r.lane = 'recall'", "r.status = 'active'"}
	args := []any{agent}
	for _, key := range SortedKeys(meta) {
		values := meta[key]
		if len(values) == 0 {
			continue
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		conditions = append(conditions, `NOT (
			EXISTS (SELECT 1 FROM metadata present WHERE present.record_id = r.id AND present.key = ?)
			AND NOT EXISTS (SELECT 1 FROM metadata matching WHERE matching.record_id = r.id AND matching.key = ? AND matching.value IN (`+placeholders+`))
		)`)
		args = append(args, key, key)
		for _, value := range values {
			args = append(args, value)
		}
	}
	return strings.Join(conditions, " AND "), args, nil
}

// CountEligibleRecall counts the whole scoped library independently of task or
// query, without reading bodies. The count includes all matching active recall
// records, including records whose body might have been corrupted out of band.
func CountEligibleRecall(q Querier, agent string, meta Meta) (int, error) {
	where, args, err := recallEligibility(agent, meta)
	if err != nil {
		return 0, err
	}
	var count int
	err = q.QueryRow(`SELECT COUNT(*) FROM records r WHERE `+where, args...).Scan(&count)
	return count, err
}

// WalkRecallIndex streams eligible rows newest first (created_at, rowid DESC).
// Every filter applies in SQL before the caller stops the stream. Only bounded
// text prefixes are projected; an unqueried library page never reads full
// bodies. Query is a literal Unicode case-insensitive substring, not ranking.
//
// The callback returns true to continue, false to stop. It must not query q
// while rows are open; all aliases and cursor coordinates are read beforehand
// or joined into this query. The caller can hold a transaction for a coherent
// page, including context resolution, count and byte-budget decisions.
//
// Cursors are positions in live data, not snapshots. Inactive cursor records
// remain valid. Newly inserted records before the cursor appear on a restart;
// later retirements disappear. Keep agent, query and resolved scope unchanged
// while traversing. No source is silently forwarded to a newer replacement.
func WalkRecallIndex(q Querier, req RecallIndexRequest, visit func(RecallIndexEntry) (bool, error)) error {
	where, args, err := recallEligibility(req.Agent, req.Meta)
	if err != nil {
		return err
	}
	if visit == nil {
		return fmt.Errorf("%w: recall index requires a visitor", ErrInvalid)
	}
	if !utf8.ValidString(req.Query) {
		return fmt.Errorf("%w: recall query must be valid UTF-8", ErrInvalid)
	}
	if req.After != "" {
		id := req.After
		if strings.HasPrefix(id, "@") {
			id, err = ResolveReference(q, id)
			if err != nil {
				return err
			}
		}
		if !IsID(id) || strings.HasPrefix(id, "ctx_") || strings.HasPrefix(id, "gen_") {
			return fmt.Errorf("%w: recall cursor %q must identify a recall record", ErrInvalid, req.After)
		}
		var agent, lane, created string
		var rowid int64
		err := q.QueryRow(`SELECT agent, lane, created_at, rowid FROM records WHERE id = ?`, id).Scan(&agent, &lane, &created, &rowid)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: recall cursor %s", ErrNotFound, req.After)
		}
		if err != nil {
			return err
		}
		if agent != req.Agent || lane != "recall" {
			return fmt.Errorf("%w: recall cursor %s belongs to %s/%s, not %s/recall", ErrInvalid, req.After, agent, lane, req.Agent)
		}
		where += ` AND (r.created_at, r.rowid) < (?, ?)`
		args = append(args, created, rowid)
	}
	if req.Query != "" {
		where += ` AND (nine_tails_contains(r.body, ?) OR nine_tails_contains(r.name, ?)
			OR EXISTS (SELECT 1 FROM metadata search WHERE search.record_id = r.id AND nine_tails_contains(search.value, ?)))`
		args = append(args, req.Query, req.Query, req.Query)
	}
	// BLOB prefixes are bounded even for very large bodies and preserve NULs
	// (SQLite text substr stops at the first NUL). Four bytes per requested
	// rune guarantee enough bytes to decode the complete preview plus one.
	rows, err := q.Query(`SELECT r.id, alias.number, substr(CAST(COALESCE(r.name, '') AS BLOB), 1, 324),
		substr(CAST(r.kind AS BLOB), 1, 324), r.created_at, substr(CAST(r.body AS BLOB), 1, 644)
		FROM records r LEFT JOIN reference_aliases alias ON alias.entity_id = r.id
		WHERE `+where+` ORDER BY r.created_at DESC, r.rowid DESC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entry RecallIndexEntry
		var number sql.NullInt64
		var rawName, rawKind, rawBody []byte
		if err := rows.Scan(&entry.ID, &number, &rawName, &rawKind, &entry.CreatedAt, &rawBody); err != nil {
			return err
		}
		if !number.Valid {
			return fmt.Errorf("%w: reference for recall record %s", ErrNotFound, entry.ID)
		}
		entry.Ref = "@" + strconv.FormatInt(number.Int64, 10)
		name, nameOK := libraryPrefix(rawName, 81)
		kind, kindOK := libraryPrefix(rawKind, 81)
		prefix, bodyOK := libraryPrefix(rawBody, 161)
		if !nameOK || !kindOK || !bodyOK {
			return fmt.Errorf("%w: recall index text for %s is invalid UTF-8; inspect %s", ErrInvalid, entry.Ref, entry.Ref)
		}
		entry.Name = libraryLabel(name)
		entry.Kind = libraryLabel(kind)
		entry.Excerpt, entry.Truncated = libraryExcerpt(prefix)
		keepGoing, err := visit(entry)
		if err != nil {
			return err
		}
		if !keepGoing {
			return nil
		}
	}
	return rows.Err()
}

// The SQL byte prefix can end inside a later rune. Decode only the requested
// complete runes: maxRunes*4 bytes always includes them for valid UTF-8.
func libraryPrefix(raw []byte, maxRunes int) (string, bool) {
	end := 0
	for n := 0; n < maxRunes && end < len(raw); n++ {
		r, size := utf8.DecodeRune(raw[end:])
		if r == utf8.RuneError && size == 1 {
			return "", false
		}
		end += size
	}
	return string(raw[:end]), true
}

func libraryLabel(prefix string) string {
	runes := []rune(prefix)
	if len(runes) > 80 {
		return strings.Join(strings.Fields(string(runes[:79])+"…"), " ")
	}
	return strings.Join(strings.Fields(prefix), " ")
}

// The bounded projection supplied the first 161 Unicode characters. Preserve whole words
// within a 160-rune preview, collapse whitespace, and mark any omitted suffix.
// Full contents are always retrieved by the record's exact inspection handle.
func libraryExcerpt(prefix string) (string, bool) {
	runes := []rune(prefix)
	if len(runes) <= 160 {
		return strings.Join(strings.Fields(prefix), " "), false
	}
	end := 158 // leave room for the omission marker and its separating space
	if !unicode.IsSpace(runes[end]) {
		for end > 0 && !unicode.IsSpace(runes[end-1]) {
			end--
		}
	}
	text := strings.TrimSpace(string(runes[:end]))
	if text == "" {
		return "…", true
	}
	return strings.Join(strings.Fields(text), " ") + " …", true
}
