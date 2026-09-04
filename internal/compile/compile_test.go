package compile

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func openTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func insert(t *testing.T, s *store.Store, nr store.NewRecord) *store.Record {
	t.Helper()
	var rec *store.Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		rec, err = store.InsertRecord(tx, nr)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func guidance(t *testing.T, s *store.Store, agent, body string, meta store.Meta, origin string) *store.Record {
	t.Helper()
	return insert(t, s, store.NewRecord{Agent: agent, Lane: "guidance", Kind: "prefer", Body: body, Meta: meta, OriginContext: origin})
}

func receipt(t *testing.T, s *store.Store, agent string, meta store.Meta, rendered ...string) *store.Context {
	t.Helper()
	var c *store.Context
	if err := s.Tx(func(tx *sql.Tx) error {
		var crs []store.ContextRecord
		for i, id := range rendered {
			crs = append(crs, store.ContextRecord{RecordID: id, Section: "base", Ordinal: i})
		}
		var err error
		c, err = store.CreateContext(tx, agent, "", "", 1000, meta, crs)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return c
}

// problems asserts err is the exit-2 "compiler output is invalid" error and
// returns its detail lines without the two-space indent.
func problems(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if cli.CodeOf(err) != cli.ExitInvalid {
		t.Fatalf("expected exit 2, got %d: %v", cli.CodeOf(err), err)
	}
	lines := strings.Split(err.Error(), "\n")
	if lines[0] != "compiler output is invalid" {
		t.Fatalf("summary line: %q", lines[0])
	}
	var out []string
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "  ") {
			t.Fatalf("problem line not indented: %q", l)
		}
		out = append(out, l[2:])
	}
	if len(out) == 0 {
		t.Fatalf("no problem lines in %q", err.Error())
	}
	return out
}

func mustContain(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("missing problem %q in:\n  %s", w, strings.Join(got, "\n  "))
		}
	}
}

func TestParseSnakeAndKebab(t *testing.T) {
	snake := `
input_entries: [rec_1, rec_2]
items:
  - key: k1
    body: |
      Body text.
    meta: {repo-id: r1, phase: [a, b], n: 1}
entries:
  - id: rec_1
    disposition: represented
    items: [k1]
    equivalent_records: [item_9]
    refinement: true
  - id: rec_2
    disposition: superseded-by
    successor: rec_1
`
	kebab := `{
	"input-entries": ["rec_1", "rec_2"],
	"items": [{"key": "k1", "body": "Body text.", "meta": {"repo-id": "r1", "phase": ["a", "b"], "n": 1}}],
	"entries": [
		{"id": "rec_1", "disposition": "represented", "items": ["k1"], "equivalent-records": ["item_9"], "refinement": true},
		{"id": "rec_2", "disposition": "superseded-by", "successor": "rec_1"}
	]
}`
	a, err := Parse([]byte(snake))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse([]byte(kebab))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("snake and kebab differ:\n%+v\n%+v", a, b)
	}
	if !a.HasInputEntries || !reflect.DeepEqual(a.InputEntries, []string{"rec_1", "rec_2"}) {
		t.Errorf("input_entries: %+v", a.InputEntries)
	}
	if len(a.Items) != 1 || a.Items[0].Key != "k1" || a.Items[0].Body != "Body text." {
		t.Errorf("items: %+v", a.Items)
	}
	wantMeta := store.Meta{"repo-id": {"r1"}, "phase": {"a", "b"}, "n": {"1"}}
	if !reflect.DeepEqual(a.Items[0].Meta, wantMeta) {
		t.Errorf("meta: %+v", a.Items[0].Meta)
	}
	if len(a.Entries) != 2 || !a.Entries[0].Refinement || a.Entries[0].Disposition != "represented" ||
		!a.Entries[0].HasItems || a.Entries[0].HasSuccessor ||
		!reflect.DeepEqual(a.Entries[0].Items, []string{"k1"}) || !reflect.DeepEqual(a.Entries[0].Equivalents, []string{"item_9"}) {
		t.Errorf("entry 0: %+v", a.Entries[0])
	}
	if a.Entries[1].Disposition != "superseded-by" || a.Entries[1].HasItems || !a.Entries[1].HasSuccessor ||
		a.Entries[1].Successor != "rec_1" || a.Entries[1].Refinement {
		t.Errorf("entry 1: %+v", a.Entries[1])
	}
}

func TestParsePreservesConditionalFieldPresence(t *testing.T) {
	out, err := Parse([]byte(`input_entries: []
items: []
entries:
  - {id: rec_1, disposition: deferred}
  - {id: rec_2, disposition: deferred, items: []}
  - {id: rec_3, disposition: deferred, items: null, successor: ""}
  - {id: rec_4, disposition: superseded-by, successor: null}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 4 {
		t.Fatalf("entries: %+v", out.Entries)
	}
	want := [][2]bool{{false, false}, {true, false}, {true, true}, {false, true}}
	for i, e := range out.Entries {
		if e.HasItems != want[i][0] || e.HasSuccessor != want[i][1] {
			t.Errorf("entry %d presence: items=%t successor=%t, want items=%t successor=%t", i+1,
				e.HasItems, e.HasSuccessor, want[i][0], want[i][1])
		}
	}
}

func TestDefaultInstructionsDescribeMetadataKeyRule(t *testing.T) {
	if strings.Contains(DefaultInstructions, "metadata") && strings.Contains(DefaultInstructions, "keys must match ^[a-z0-9][a-z0-9.-]*$") {
		t.Fatal("default instructions incorrectly apply the brief-item name regex to metadata keys")
	}
	if !strings.Contains(DefaultInstructions, "keys are non-empty and may not contain whitespace, =, [ or ]") {
		t.Fatal("default instructions do not describe the metadata-key rule")
	}
}

func TestParseProblems(t *testing.T) {
	// A document that cannot be read at all is an error from Parse itself.
	fatal := []struct {
		doc  string
		want string
	}{
		{"", "the document is empty"},
		{"- a\n- b\n", "the document must be a mapping with input_entries, items and entries"},
		{"a: [\n", "not valid YAML or JSON:"}, // message is the parser's, only its prefix is asserted
		{"input_entries: []\nitems: []\nentries: []\n---\ninput_entries: []\nitems: []\nentries: []\n", "compiler output must contain exactly one YAML or JSON document"},
	}
	for _, c := range fatal {
		out, err := Parse([]byte(c.doc))
		if out != nil {
			t.Errorf("%q: fatal parse should return no output", c.doc)
		}
		if got := problems(t, err); len(got) != 1 || !strings.HasPrefix(got[0], c.want) {
			t.Errorf("%q: %v", c.doc, got)
		}
	}
	// Every other shape problem is collected on the output for Validate.
	cases := []struct {
		doc  string
		want []string
	}{
		{"items: {a: b}\nentries: 3\n", []string{"items must be a list", "entries must be a list"}},
		{"entries:\n  - id: rec_1\n    refinement: maybe\n    items: {a: 1}\nitems:\n  - key: k\n    meta: {x: {y: 1}}\n  - 7\n",
			[]string{"entry rec_1 refinement must be true or false", "entry rec_1 items must be a list of scalars",
				"item k metadata x must be a scalar or a list of scalars", "item #2 must be a mapping with key, body and meta"}},
		{"items:\n  - key: k\n    meta: {\"bad key\": v}\n", []string{`item k metadata key "bad key" may not be empty or contain whitespace, '=', '[' or ']'`}},
		{"items:\n  - key: k\n    meta: {\"bad\u2003key\": v}\n", []string{`item k metadata key "bad\u2003key" may not be empty or contain whitespace, '=', '[' or ']'`}},
		{"input_entries: []\ninput-entries: []\n", []string{`the document has both "input-entries" and "input_entries", which are the same key`}},
	}
	for _, c := range cases {
		out, err := Parse([]byte(c.doc))
		if err != nil {
			t.Fatalf("%q: shape problems must not be fatal: %v", c.doc, err)
		}
		mustContain(t, out.Problems, c.want...)
	}
	if out, _ := Parse([]byte("input_entries: []\nitems: []\nentries: []\n")); len(out.Problems) != 0 {
		t.Errorf("clean document has problems: %v", out.Problems)
	}
}

func TestParseRejectsDuplicateExactMappingKeys(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		key  string
	}{
		{"yaml document", "input_entries: []\ninput_entries: []\nitems: []\nentries: []\n", "input_entries"},
		{"json document", `{"input_entries": [], "input_entries": [], "items": [], "entries": []}`, "input_entries"},
		{"item", "input_entries: []\nitems:\n  - key: first\n    key: second\n    body: b\nentries: []\n", "key"},
		{"entry", "input_entries: [rec_1]\nitems: []\nentries:\n  - id: rec_1\n    disposition: deferred\n    disposition: represented\n", "disposition"},
		{"metadata", "input_entries: []\nitems:\n  - key: k\n    body: b\n    meta:\n      scope: one\n      scope: two\nentries: []\n", "scope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Parse([]byte(c.doc))
			if out != nil {
				t.Fatalf("duplicate key returned output: %+v", out)
			}
			got := problems(t, err)
			want := fmt.Sprintf("mapping key %q appears more than once", c.key)
			if len(got) != 1 || !strings.Contains(got[0], want) {
				t.Fatalf("problems=%v, want one containing %q", got, want)
			}
		})
	}
}

func TestParseRejectsNormalizedStructuralKeyCollisions(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"document", "input_entries: []\ninput-entries: []\n", `the document has both "input-entries" and "input_entries", which are the same key`},
		{"item", "items:\n  - key: k\n    body: b\n    ignored-key: one\n    ignored_key: two\n", `item #1 has both "ignored-key" and "ignored_key", which are the same key`},
		{"entry", "entries:\n  - id: rec_1\n    equivalent-records: []\n    equivalent_records: []\n", `entry #1 has both "equivalent-records" and "equivalent_records", which are the same key`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Parse([]byte(c.doc))
			if err != nil {
				t.Fatal(err)
			}
			mustContain(t, out.Problems, c.want)
		})
	}

	// Metadata keys are user data, not structural keys. They remain verbatim,
	// so hyphenated and underscored spellings are distinct rather than aliases.
	out, err := Parse([]byte("input_entries: []\nitems: [{key: k, body: b, meta: {scope-key: one, scope_key: two}}]\nentries: []\n"))
	if err != nil || len(out.Problems) != 0 || !reflect.DeepEqual(out.Items[0].Meta, store.Meta{"scope-key": {"one"}, "scope_key": {"two"}}) {
		t.Fatalf("verbatim metadata keys: out=%+v err=%v", out, err)
	}
}

// Shape problems from Parse and semantic problems from Validate come out in
// one report, so the compiler never has to fix a document in two rounds.
func TestValidateReportsParseProblemsToo(t *testing.T) {
	s := openTest(t)
	e2 := guidance(t, s, "a", "two", nil, "")
	e3 := guidance(t, s, "a", "three", nil, "")
	doc := "input_entries: [" + e2.ID + ", " + e3.ID + ", rec_88]\n" +
		"items: [{key: Bad_Key, body: x, meta: {\"a=b\": 1}}]\n" +
		"entries:\n" +
		"  - {id: " + e2.ID + ", disposition: represented}\n" +
		"  - {id: " + e3.ID + ", disposition: superseded-by, successor: " + e3.ID + ", refinement: maybe}\n"
	out, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Validate(s.DB, "a", out)
	got := problems(t, err)
	want := []string{
		`item Bad_Key metadata key "a=b" may not be empty or contain whitespace, '=', '[' or ']'`,
		"entry " + e3.ID + " refinement must be true or false",
		`brief item name "Bad_Key" must match ^[a-z0-9][a-z0-9.-]*$ (lowercase, no _ or /)`,
		"entry rec_88 is missing from entries",
		"entry " + e2.ID + " is represented but lists no items",
		"entry " + e3.ID + " cannot supersede itself",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems:\n  got  %q\n  want %q", got, want)
	}
}

// Metadata keys are visited in sorted order, so the same document always
// yields the same problem lines in the same order.
func TestParseMetaProblemsAreDeterministic(t *testing.T) {
	doc := "input_entries: []\nitems: [{key: k, body: b, meta: {\"a=b\": 1, \"sp ace\": 2, \"c[\": 3, ok: 4}}]\nentries: []\n"
	want := []string{
		`item k metadata key "a=b" may not be empty or contain whitespace, '=', '[' or ']'`,
		`item k metadata key "c[" may not be empty or contain whitespace, '=', '[' or ']'`,
		`item k metadata key "sp ace" may not be empty or contain whitespace, '=', '[' or ']'`,
	}
	for i := 0; i < 25; i++ {
		out, err := Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out.Problems, want) {
			t.Fatalf("run %d: %q", i, out.Problems)
		}
		if !reflect.DeepEqual(out.Items[0].Meta, store.Meta{"ok": {"4"}}) {
			t.Fatalf("run %d: meta %v", i, out.Items[0].Meta)
		}
	}
}

// input_entries must be the input's list verbatim: a duplicated id is a
// problem for brief put (self-consistency) and any deviation from the compile
// input's list is a problem for compile (CheckEcho).
func TestInputEntriesVerbatim(t *testing.T) {
	s := openTest(t)
	e2 := guidance(t, s, "a", "two", nil, "")
	e3 := guidance(t, s, "a", "three", nil, "")
	doc := "input_entries: [" + e2.ID + ", " + e2.ID + ", " + e3.ID + "]\nitems: []\nentries:\n  - {id: " + e2.ID + ", disposition: deferred}\n  - {id: " + e3.ID + ", disposition: deferred}\n"
	out, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Validate(s.DB, "a", out)
	if got := problems(t, err); !reflect.DeepEqual(got, []string{"input_entries lists " + e2.ID + " more than once"}) {
		t.Errorf("duplicate id: %q", got)
	}

	cases := []struct {
		name string
		want []string
		ok   bool
	}{
		{"same", []string{e2.ID, e2.ID, e3.ID}, true},
		{"set-equal but different order", []string{e3.ID, e2.ID, e2.ID}, false},
		{"set-equal but deduplicated", []string{e2.ID, e3.ID}, false},
		{"missing", []string{e2.ID, e2.ID, e3.ID, "rec_9"}, false},
	}
	for _, c := range cases {
		out, _ := Parse([]byte(doc))
		before := len(out.Problems)
		CheckEcho(out, c.want)
		added := out.Problems[before:]
		if c.ok != (len(added) == 0) {
			t.Errorf("%s: problems %q", c.name, added)
			continue
		}
		if !c.ok && !strings.HasPrefix(added[0], "input_entries must echo the compile input's input_entries ["+strings.Join(c.want, ", ")+"] unchanged, got [") {
			t.Errorf("%s: %q", c.name, added[0])
		}
	}
	out, _ = Parse([]byte("input_entries: []\nitems: []\nentries: []\n"))
	if CheckEcho(out, []string{}); len(out.Problems) != 0 {
		t.Errorf("empty echo of empty input: %q", out.Problems)
	}
}

func TestValidateRules(t *testing.T) {
	s := openTest(t)
	e1 := guidance(t, s, "a", "one", nil, "")
	e2 := guidance(t, s, "a", "two", nil, "")
	other := guidance(t, s, "b", "other agent", nil, "")
	recall := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "fact"})
	old := guidance(t, s, "a", "old", nil, "")
	if err := s.Tx(func(tx *sql.Tx) error { return store.SetStatus(tx, old.ID, "superseded") }); err != nil {
		t.Fatal(err)
	}
	sub := strings.NewReplacer("E1", e1.ID, "E2", e2.ID, "OTHER", other.ID, "RECALL", recall.ID, "OLD", old.ID)

	cases := []struct {
		name string
		doc  string
		want []string // nil = valid
	}{
		{"missing entry", "input_entries: [E1, E2]\nentries: [{id: E1, disposition: deferred}]\n", []string{"entry E2 is missing from entries"}},
		{"extra entry", "input_entries: [E1]\nentries: [{id: E1, disposition: deferred}, {id: E2, disposition: deferred}]\n", []string{"entry E2 is not in input_entries"}},
		{"duplicate entry", "input_entries: [E1]\nentries: [{id: E1, disposition: deferred}, {id: E1, disposition: deferred}]\n", []string{"entry E1 appears more than once"}},
		{"no input_entries", "items: []\nentries: []\n", []string{"input_entries is missing (echo the input's input_entries unchanged)"}},
		{"recall entry", "input_entries: [RECALL]\nentries: [{id: RECALL, disposition: deferred}]\n", []string{"entry RECALL is not an active guidance entry of a"}},
		{"other agent", "input_entries: [OTHER]\nentries: [{id: OTHER, disposition: deferred}]\n", []string{"entry OTHER is not an active guidance entry of a"}},
		{"superseded entry", "input_entries: [OLD]\nentries: [{id: OLD, disposition: deferred}]\n", []string{"entry OLD is not an active guidance entry of a"}},
		{"nonexistent entry", "input_entries: [rec_999]\nentries: [{id: rec_999, disposition: deferred}]\n", []string{"entry rec_999 is not an active guidance entry of a"}},
		{"unknown key", "input_entries: [E1]\nentries: [{id: E1, disposition: represented, items: [nope]}]\n", []string{"entry E1 references unknown item key nope"}},
		{"represented without items", "input_entries: [E1]\nentries: [{id: E1, disposition: represented}]\n", []string{"entry E1 is represented but lists no items"}},
		{"represented with successor", "input_entries: [E1, E2]\nitems: [{key: k, body: b}]\nentries: [{id: E1, disposition: represented, items: [k], successor: E2}, {id: E2, disposition: deferred}]\n", []string{"entry E1 is represented but names a successor"}},
		{"represented with empty successor", "input_entries: [E1]\nitems: [{key: k, body: b}]\nentries: [{id: E1, disposition: represented, items: [k], successor: \"\"}]\n", []string{"entry E1 is represented but names a successor"}},
		{"represented with null successor", "input_entries: [E1]\nitems: [{key: k, body: b}]\nentries: [{id: E1, disposition: represented, items: [k], successor: null}]\n", []string{"entry E1 is represented but names a successor"}},
		{"deferred with items", "input_entries: [E1]\nitems: [{key: k, body: b}]\nentries: [{id: E1, disposition: deferred, items: [k]}]\n", []string{"entry E1 is deferred but lists items"}},
		{"deferred with empty items", "input_entries: [E1]\nitems: []\nentries: [{id: E1, disposition: deferred, items: []}]\n", []string{"entry E1 is deferred but lists items"}},
		{"deferred with null items", "input_entries: [E1]\nitems: []\nentries: [{id: E1, disposition: deferred, items: null}]\n", []string{"entry E1 is deferred but lists items"}},
		{"deferred with successor", "input_entries: [E1, E2]\nentries: [{id: E1, disposition: deferred, successor: E2}, {id: E2, disposition: deferred}]\n", []string{"entry E1 is deferred but names a successor"}},
		{"deferred with empty successor", "input_entries: [E1]\nentries: [{id: E1, disposition: deferred, successor: \"\"}]\n", []string{"entry E1 is deferred but names a successor"}},
		{"deferred with null successor", "input_entries: [E1]\nentries: [{id: E1, disposition: deferred, successor: null}]\n", []string{"entry E1 is deferred but names a successor"}},
		{"superseded-by without successor", "input_entries: [E1]\nentries: [{id: E1, disposition: superseded-by}]\n", []string{"entry E1 is superseded-by but names no successor"}},
		{"superseded-by with items", "input_entries: [E1, E2]\nitems: [{key: k, body: b}]\nentries: [{id: E1, disposition: superseded-by, successor: E2, items: [k]}, {id: E2, disposition: deferred}]\n", []string{"entry E1 is superseded-by but lists items"}},
		{"superseded-by with empty items", "input_entries: [E1, E2]\nitems: []\nentries: [{id: E1, disposition: superseded-by, successor: E2, items: []}, {id: E2, disposition: deferred}]\n", []string{"entry E1 is superseded-by but lists items"}},
		{"superseded-by with null items", "input_entries: [E1, E2]\nitems: []\nentries: [{id: E1, disposition: superseded-by, successor: E2, items: null}, {id: E2, disposition: deferred}]\n", []string{"entry E1 is superseded-by but lists items"}},
		{"successor not guidance", "input_entries: [E1]\nentries: [{id: E1, disposition: superseded-by, successor: RECALL}]\n", []string{"entry E1 successor RECALL is not an active guidance entry of a"}},
		{"successor missing", "input_entries: [E1]\nentries: [{id: E1, disposition: superseded-by, successor: rec_999}]\n", []string{"entry E1 successor rec_999 is not an active guidance entry of a"}},
		{"successor self", "input_entries: [E1]\nentries: [{id: E1, disposition: superseded-by, successor: E1}]\n", []string{"entry E1 cannot supersede itself"}},
		{"unknown disposition", "input_entries: [E1]\nentries: [{id: E1, disposition: maybe}]\n", []string{`entry E1 has unknown disposition "maybe" (represented|deferred|superseded-by)`}},
		{"no disposition", "input_entries: [E1]\nentries: [{id: E1}]\n", []string{"entry E1 has no disposition (represented|deferred|superseded-by)"}},
		{"equivalent missing", "input_entries: [E1]\nentries: [{id: E1, disposition: deferred, equivalent_records: [rec_999]}]\n", []string{"entry E1 equivalent record rec_999 does not exist"}},
		{"bad key", "input_entries: []\nitems: [{key: Bad_Key, body: b}]\nentries: []\n", []string{`brief item name "Bad_Key" must match ^[a-z0-9][a-z0-9.-]*$ (lowercase, no _ or /)`}},
		{"id-like key", "input_entries: []\nitems: [{key: item_9, body: b}]\nentries: []\n", []string{`brief item name "item_9" must match ^[a-z0-9][a-z0-9.-]*$ (lowercase, no _ or /)`}},
		{"reserved key", "input_entries: []\nitems: [{key: none, body: b}]\nentries: []\n", []string{`"none" is a reserved name`}},
		{"duplicate key", "input_entries: []\nitems: [{key: k, body: b}, {key: k, body: c}]\nentries: []\n", []string{"item key k is duplicated"}},
		{"empty body", "input_entries: []\nitems: [{key: k, body: \"\"}]\nentries: []\n", []string{"item k has an empty body"}},
		{"no key", "input_entries: []\nitems: [{body: b}]\nentries: []\n", []string{"item #1 has no key"}},
		{"several at once", "input_entries: [E1, E2]\nitems: [{key: k, body: \"\"}]\nentries: [{id: E1, disposition: represented}]\n",
			[]string{"item k has an empty body", "entry E2 is missing from entries", "entry E1 is represented but lists no items"}},
		{"empty items list is fine", "input_entries: [E1]\nitems: []\nentries: [{id: E1, disposition: deferred}]\n", nil},
		{"valid", "input_entries: [E1, E2]\nitems: [{key: k, body: b, meta: {x: y}}]\nentries: [{id: E1, disposition: represented, items: [k], equivalent_records: [OLD]}, {id: E2, disposition: superseded-by, successor: E1}]\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Parse([]byte(sub.Replace(c.doc)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			plan, err := Validate(s.DB, "a", out)
			if c.want == nil {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				if plan == nil || len(plan.Inputs) != len(out.Entries) {
					t.Fatalf("plan: %+v", plan)
				}
				return
			}
			got := problems(t, err)
			var want []string
			for _, w := range c.want {
				want = append(want, sub.Replace(w))
			}
			mustContain(t, got, want...)
		})
	}
}

func TestValidatePlanSources(t *testing.T) {
	s := openTest(t)
	e1 := guidance(t, s, "a", "one", nil, "")
	e2 := guidance(t, s, "a", "two", nil, "")
	out, err := Parse([]byte("input_entries: [" + e1.ID + ", " + e2.ID + "]\nitems: [{key: k1, body: b1}, {key: k2, body: b2}]\nentries: [{id: " + e1.ID + ", disposition: represented, items: [k1, k2]}, {id: " + e2.ID + ", disposition: represented, items: [k2]}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Validate(s.DB, "a", out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Items[0].Sources, []string{e1.ID}) || !reflect.DeepEqual(plan.Items[1].Sources, []string{e1.ID, e2.ID}) {
		t.Errorf("sources: %+v / %+v", plan.Items[0].Sources, plan.Items[1].Sources)
	}
}

func TestCoverage(t *testing.T) {
	s := openTest(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	unrendered := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "existing"})
	ctx := receipt(t, s, "a", store.Meta{"repo-id": {"r1"}}, base.ID)
	withOrigin := guidance(t, s, "a", "x", nil, ctx.ID)
	noOrigin := guidance(t, s, "a", "y", nil, "")

	cases := []struct {
		name        string
		entry       *store.Record
		refinement  bool
		equivalents []string
		want        string
	}{
		{"refinement wins over equivalents", withOrigin, true, []string{base.ID}, "refinement"},
		{"refinement without equivalents", noOrigin, true, nil, "refinement"},
		{"equivalent rendered in origin", withOrigin, false, []string{unrendered.ID, base.ID}, "covered-rendered"},
		{"equivalent not rendered in origin", withOrigin, false, []string{unrendered.ID}, "covered-unrendered"},
		{"equivalents but no origin", noOrigin, false, []string{base.ID}, "unknown"},
		{"no equivalents, no origin", noOrigin, false, nil, "unknown"},
		{"no equivalents with origin", withOrigin, false, nil, "novel"},
	}
	for _, c := range cases {
		got, err := Coverage(s.DB, c.entry, c.refinement, c.equivalents)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func install(t *testing.T, s *store.Store, agent, expectGen, expectBase, doc string) (*Result, error) {
	t.Helper()
	out, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var res *Result
	err = s.Tx(func(tx *sql.Tx) error {
		var err error
		res, err = Install(tx, agent, expectGen, expectBase, out)
		return err
	})
	return res, err
}

func TestLintAfterInstall(t *testing.T) {
	s := openTest(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	e1 := guidance(t, s, "a", "one", store.Meta{"repo-id": {"r1"}}, "")
	e2 := guidance(t, s, "a", "two", store.Meta{"repo-id": {"r1", "r2"}}, "")
	e3 := guidance(t, s, "a", "three", store.Meta{"repo-id": {"r3"}}, "")
	e4 := guidance(t, s, "a", "four", nil, "")
	doc := "input_entries: [" + strings.Join([]string{e1.ID, e2.ID, e3.ID, e4.ID}, ", ") + "]\n" +
		"items:\n" +
		"  - {key: common, body: both carry r1}\n" + // sources e1, e2: repo-id ∩ = r1, item lacks it → strong
		"  - {key: disjoint, body: r1 vs r3}\n" + // sources e1, e3: disjoint values → nothing
		"  - {key: orphan, body: no sources}\n" + // no sources → nothing
		"entries:\n" +
		"  - {id: " + e1.ID + ", disposition: represented, items: [common, disjoint]}\n" +
		"  - {id: " + e2.ID + ", disposition: represented, items: [common]}\n" +
		"  - {id: " + e3.ID + ", disposition: represented, items: [disjoint]}\n" +
		"  - {id: " + e4.ID + ", disposition: deferred}\n"
	res, err := install(t, s, "a", "none", base.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items: %v", res.Items)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings: %+v", res.Warnings)
	}
	w := res.Warnings[0]
	if w.Item != res.Items[0] || w.Key != "repo-id" || w.Strength != "strong" || !reflect.DeepEqual(w.Values, []string{"r1"}) ||
		!reflect.DeepEqual(w.Sources, []string{e1.ID, e2.ID}) || !strings.Contains(w.Message, "repo-id=r1") {
		t.Errorf("warning: %+v", w)
	}
	again, err := LintGeneration(s.DB, res.Generation)
	if err != nil || !reflect.DeepEqual(again, res.Warnings) {
		t.Errorf("LintGeneration: %+v %v", again, err)
	}
	active, err := LintConditionLoss(s.DB, "a")
	if err != nil || !reflect.DeepEqual(active, res.Warnings) {
		t.Errorf("LintConditionLoss: %+v %v", active, err)
	}
	// inputs echo item record ids and coverage
	if len(res.Inputs) != 4 || res.Inputs[0].Coverage != "unknown" || !reflect.DeepEqual(res.Inputs[0].Items, []string{res.Items[0], res.Items[1]}) {
		t.Errorf("inputs: %+v", res.Inputs)
	}
}

func TestLintWeakFromOriginContexts(t *testing.T) {
	s := openTest(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	c1 := receipt(t, s, "a", store.Meta{"repo-id": {"r1"}}, base.ID)
	c2 := receipt(t, s, "a", store.Meta{"repo-id": {"r1"}, "pr": {"7"}}, base.ID)
	e1 := guidance(t, s, "a", "one", store.Meta{"phase": {"review"}}, c1.ID)
	e2 := guidance(t, s, "a", "two", store.Meta{"phase": {"review"}}, c2.ID)
	doc := "input_entries: [" + e1.ID + ", " + e2.ID + "]\nitems: [{key: k, body: b, meta: {phase: review}}]\n" +
		"entries: [{id: " + e1.ID + ", disposition: represented, items: [k]}, {id: " + e2.ID + ", disposition: represented, items: [k]}]\n"
	res, err := install(t, s, "a", "none", base.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Strength != "weak" || res.Warnings[0].Key != "repo-id" {
		t.Errorf("warnings: %+v", res.Warnings)
	}
}

func TestInstallCASAndLineage(t *testing.T) {
	s := openTest(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	e1 := guidance(t, s, "a", "one", nil, "")
	e2 := guidance(t, s, "a", "two", nil, "")
	doc1 := "input_entries: [" + e1.ID + ", " + e2.ID + "]\nitems: [{key: k, body: b}]\n" +
		"entries: [{id: " + e1.ID + ", disposition: represented, items: [k]}, {id: " + e2.ID + ", disposition: deferred}]\n"

	if _, err := install(t, s, "a", "", base.ID, doc1); cli.CodeOf(err) != cli.ExitInvalid {
		t.Errorf("missing --expect-generation: %v", err)
	}
	if _, err := install(t, s, "a", "none", "", doc1); cli.CodeOf(err) != cli.ExitInvalid {
		t.Errorf("missing --expect-base: %v", err)
	}
	if _, err := install(t, s, "a", "gen_99", base.ID, doc1); cli.CodeOf(err) != cli.ExitConflict || err.Error() != "expected gen_99 but no generation is active" {
		t.Errorf("stale generation: %v", err)
	}
	if _, err := install(t, s, "a", "none", "base_99", doc1); cli.CodeOf(err) != cli.ExitConflict || err.Error() != "expected base_99 but "+base.ID+" is active" {
		t.Errorf("stale base: %v", err)
	}
	res1, err := install(t, s, "a", "none", base.ID, doc1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := install(t, s, "a", "none", base.ID, doc1); cli.CodeOf(err) != cli.ExitConflict || err.Error() != "expected none but "+res1.Generation+" is active" {
		t.Errorf("expect none with active generation: %v", err)
	}
	// source entries stay active; only the deferred one is still recent
	for _, id := range []string{e1.ID, e2.ID} {
		r, _ := store.GetRecord(s.DB, id)
		if r.Status != "active" {
			t.Errorf("%s should stay active, is %s", id, r.Status)
		}
	}
	recent, _ := store.RecentGuidance(s.DB, "a")
	if len(recent) != 1 || recent[0].ID != e2.ID {
		t.Errorf("recent after install: %+v", recent)
	}
	// second generation re-using the key links lineage and supersedes the old item
	doc2 := "input_entries: [" + e2.ID + "]\nitems: [{key: k, body: b2}]\nentries: [{id: " + e2.ID + ", disposition: represented, items: [k]}]\n"
	res2, err := install(t, s, "a", res1.Generation, base.ID, doc2)
	if err != nil {
		t.Fatal(err)
	}
	newItem, _ := store.GetRecord(s.DB, res2.Items[0])
	oldItem, _ := store.GetRecord(s.DB, res1.Items[0])
	if newItem.Supersedes != oldItem.ID || oldItem.Status != "superseded" || newItem.Status != "active" {
		t.Errorf("lineage: new %+v old %+v", newItem, oldItem)
	}
	g1, _ := store.GetGeneration(s.DB, res1.Generation)
	g2, _ := store.ActiveGeneration(s.DB, "a")
	if g1.Status != "superseded" || g2.ID != res2.Generation || g2.Parent != res1.Generation {
		t.Errorf("generations: %+v %+v", g1, g2)
	}
	// Reusing the key carries the surviving item's prior source accounting, so
	// both e1 and e2 remain represented by the active generation.
	if recent, _ := store.RecentGuidance(s.DB, "a"); len(recent) != 0 {
		t.Errorf("nothing should be recent: %+v", recent)
	}
}

func TestBuildInput(t *testing.T) {
	s := openTest(t)
	if _, err := BuildInput(s.DB, "nobody"); cli.CodeOf(err) != cli.ExitNotFound {
		t.Errorf("unknown agent: %v", err)
	}
	guidance(t, s, "baseless", "x", nil, "")
	if _, err := BuildInput(s.DB, "baseless"); cli.CodeOf(err) != cli.ExitNotFound {
		t.Errorf("agent without base: %v", err)
	}
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	ctx := receipt(t, s, "a", store.Meta{"repo-id": {"r1"}}, base.ID)
	e1 := guidance(t, s, "a", "one", nil, ctx.ID)
	e2 := guidance(t, s, "a", "two", nil, "")
	insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "never compiled"})

	in, err := BuildInput(s.DB, "a")
	if err != nil {
		t.Fatal(err)
	}
	if in.Agent != "a" || in.Instructions != DefaultInstructions || in.ExpectGeneration != "none" ||
		in.ExpectBase != base.ID || in.Base.ID != base.ID || in.Base.Body != "Base." || in.ActiveGeneration != nil {
		t.Errorf("input: %+v", in)
	}
	if !reflect.DeepEqual(in.InputEntries, []string{e1.ID, e2.ID}) || len(in.Entries) != 2 {
		t.Fatalf("entries: %v %+v", in.InputEntries, in.Entries)
	}
	if in.Entries[0].OriginContext != ctx.ID || in.Entries[0].OriginContextRendered == nil || !reflect.DeepEqual(*in.Entries[0].OriginContextRendered, []string{base.ID}) ||
		in.Entries[0].OriginContextMetadata == nil || !reflect.DeepEqual(*in.Entries[0].OriginContextMetadata, store.Meta{"repo-id": {"r1"}}) {
		t.Errorf("origin: %+v", in.Entries[0])
	}
	if in.Entries[1].OriginContext != "" || in.Entries[1].OriginContextRendered != nil || in.Entries[1].OriginContextMetadata != nil {
		t.Errorf("no origin expected: %+v", in.Entries[1])
	}

	// a brief-compiler agent's base replaces the built-in instructions
	insert(t, s, store.NewRecord{Agent: "brief-compiler", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Custom instructions."})
	in, _ = BuildInput(s.DB, "a")
	if in.Instructions != "Custom instructions." {
		t.Errorf("instructions: %q", in.Instructions)
	}

	// after an install the active generation and lineage show up
	res, err := install(t, s, "a", "none", base.ID, "input_entries: ["+e1.ID+", "+e2.ID+"]\nitems: [{key: k, body: b, meta: {x: y}}]\n"+
		"entries: [{id: "+e1.ID+", disposition: represented, items: [k]}, {id: "+e2.ID+", disposition: deferred}]\n")
	if err != nil {
		t.Fatal(err)
	}
	in, _ = BuildInput(s.DB, "a")
	if in.ExpectGeneration != res.Generation || in.ActiveGeneration == nil || in.ActiveGeneration.ID != res.Generation ||
		len(in.ActiveGeneration.Items) != 1 || in.ActiveGeneration.Items[0].Key != "k" || in.ActiveGeneration.Items[0].ID != res.Items[0] ||
		!reflect.DeepEqual(in.InputEntries, []string{e2.ID}) {
		t.Errorf("after install: %+v", in)
	}
	// each active item lists the entries it represents, with their own
	// metadata, so a compiler can re-derive scope instead of inheriting it
	if srcs := in.ActiveGeneration.Items[0].Sources; len(srcs) != 1 || srcs[0].ID != e1.ID || !reflect.DeepEqual(srcs[0].Meta, e1.Meta) {
		t.Errorf("item sources: %+v", srcs)
	}
}

// An origin context that carried no metadata (or rendered nothing) still
// yields both origin_context_* keys, as {} and [], so a compiler can tell
// "origin knew nothing" from "no origin" without cross-checking origin_context.
func TestBuildInputOriginWithoutMetadata(t *testing.T) {
	s := openTest(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	bare := receipt(t, s, "a", nil) // no metadata, nothing rendered
	withOrigin := guidance(t, s, "a", "one", nil, bare.ID)
	noOrigin := guidance(t, s, "a", "two", nil, "")
	in, err := BuildInput(s.DB, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Entries) != 2 || in.Entries[0].ID != withOrigin.ID || in.Entries[1].ID != noOrigin.ID {
		t.Fatalf("entries: %+v", in.Entries)
	}
	e := in.Entries[0]
	if e.OriginContextMetadata == nil || len(*e.OriginContextMetadata) != 0 || e.OriginContextRendered == nil || len(*e.OriginContextRendered) != 0 {
		t.Errorf("origin without metadata: %+v", e)
	}
	var js bytes.Buffer
	if err := cli.WriteJSON(&js, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"origin_context_metadata": {}`) || !strings.Contains(js.String(), `"origin_context_rendered": []`) {
		t.Errorf("json should carry empty origin fields:\n%s", js.String())
	}
	if n := strings.Count(js.String(), `"origin_context_metadata"`); n != 1 {
		t.Errorf("origin_context_metadata should appear once (entry with origin only), got %d:\n%s", n, js.String())
	}
	var ym bytes.Buffer
	if err := cli.WriteYAML(&ym, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ym.String(), "origin_context_metadata: {}") || !strings.Contains(ym.String(), "origin_context_rendered: []") {
		t.Errorf("yaml should carry empty origin fields:\n%s", ym.String())
	}
	_ = base
}

func TestCheckExpectTrimsAndRejectsMalformedIDs(t *testing.T) {
	s := openTest(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	e := guidance(t, s, "a", "one", nil, "")
	doc := "input_entries: [" + e.ID + "]\nitems: []\nentries: [{id: " + e.ID + ", disposition: deferred}]\n"

	// surrounding whitespace is tolerated on both flags, symmetrically
	if _, err := CheckExpect(s.DB, "a", " none ", " "+base.ID+" "); err != nil {
		t.Errorf("padded ids: %v", err)
	}
	// malformed ids are exit 2, never a self-contradictory exit-7 conflict
	bad := []struct{ gen, base, msg string }{
		{"gen_1x", base.ID, "--expect-generation must be 'none' or the active generation id like gen_11 (compile-input shows it as expect_generation), got \"gen_1x\""},
		{"None", base.ID, "--expect-generation must be"},
		{"none", "base1", "--expect-base must be the active base id like base_4 (compile-input shows it as expect_base), got \"base1\""},
		{"none", "gen_1", "--expect-base must be"},
		{"none", "none", "--expect-base must be"},
		{"", base.ID, "--expect-generation is required"},
		{"none", "", "--expect-base is required"},
	}
	for _, c := range bad {
		_, err := CheckExpect(s.DB, "a", c.gen, c.base)
		if cli.CodeOf(err) != cli.ExitInvalid || !strings.HasPrefix(err.Error(), c.msg) {
			t.Errorf("gen=%q base=%q: code %d %v", c.gen, c.base, cli.CodeOf(err), err)
		}
	}
	// well-formed but wrong ids stay exit 7 with the active id named
	if _, err := CheckExpect(s.DB, "a", "none", " base_99 "); cli.CodeOf(err) != cli.ExitConflict || err.Error() != "expected base_99 but "+base.ID+" is active" {
		t.Errorf("wrong base: %v", err)
	}
	res, err := install(t, s, "a", " none", base.ID+" ", doc)
	if err != nil {
		t.Fatalf("install with padded ids: %v", err)
	}
	if _, err := CheckExpect(s.DB, "a", " gen_99 ", base.ID); cli.CodeOf(err) != cli.ExitConflict || err.Error() != "expected gen_99 but "+res.Generation+" is active" {
		t.Errorf("wrong generation: %v", err)
	}
}

// CoverageRows carries the items and equivalent records behind each
// classification (DESIGN §12), with snake_case keys and never-null lists.
func TestCoverageRows(t *testing.T) {
	s := openTest(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	ctx := receipt(t, s, "a", store.Meta{"team": {"core"}}, base.ID)
	e1 := guidance(t, s, "a", "one", nil, ctx.ID)
	e2 := guidance(t, s, "a", "two", nil, "")
	e3 := guidance(t, s, "a", "three", nil, ctx.ID)
	doc := "input_entries: [" + e1.ID + ", " + e2.ID + ", " + e3.ID + "]\nitems: [{key: k, body: b}]\nentries:\n" +
		"  - {id: " + e1.ID + ", disposition: represented, items: [k], equivalent_records: [" + base.ID + "]}\n" +
		"  - {id: " + e2.ID + ", disposition: deferred}\n" +
		"  - {id: " + e3.ID + ", disposition: represented, items: [k]}\n"
	res, err := install(t, s, "a", "none", base.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := CoverageRows(s.DB, "a", "covered-rendered")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Entry.ID != e1.ID || rows[0].Disposition != "represented" || rows[0].Coverage != "covered-rendered" ||
		!reflect.DeepEqual(rows[0].Items, res.Items) || !reflect.DeepEqual(rows[0].Equivalents, []string{base.ID}) {
		t.Errorf("covered-rendered rows: %+v", rows)
	}
	var js bytes.Buffer
	if err := cli.WriteJSON(&js, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"equivalent_records": [`) || strings.Contains(js.String(), "equivalent-records") {
		t.Errorf("snake_case key expected:\n%s", js.String())
	}
	// a deferred entry has neither items nor equivalents: [] rather than absent or null
	rows, err = CoverageRows(s.DB, "a", "unknown")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Entry.ID != e2.ID || rows[0].Items == nil || len(rows[0].Items) != 0 || rows[0].Equivalents == nil || len(rows[0].Equivalents) != 0 {
		t.Errorf("unknown rows: %+v", rows)
	}
	js.Reset()
	cli.WriteJSON(&js, rows)
	if !strings.Contains(js.String(), `"items": []`) || !strings.Contains(js.String(), `"equivalent_records": []`) {
		t.Errorf("empty lists should be []:\n%s", js.String())
	}
	if rows, _ = CoverageRows(s.DB, "a", "novel"); len(rows) != 1 || rows[0].Entry.ID != e3.ID {
		t.Errorf("novel rows: %+v", rows)
	}
	// nothing matching → an empty, non-nil list, so callers print [] not nothing
	if rows, err = CoverageRows(s.DB, "a", "refinement"); err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("no rows: %+v %v", rows, err)
	}
	if rows, err = CoverageRows(s.DB, "nobody", "novel"); err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("unknown agent: %+v %v", rows, err)
	}
	if _, err = CoverageRows(s.DB, "a", "bogus"); cli.CodeOf(err) != cli.ExitInvalid {
		t.Errorf("bad coverage name: %v", err)
	}
	// the latest generation's row wins and rows come back ordered by id
	doc2 := "input_entries: [" + e2.ID + "]\nitems: [{key: k, body: b2}]\nentries: [{id: " + e2.ID + ", disposition: represented, items: [k], refinement: true}]\n"
	if _, err := install(t, s, "a", res.Generation, base.ID, doc2); err != nil {
		t.Fatal(err)
	}
	if rows, _ = CoverageRows(s.DB, "a", "unknown"); len(rows) != 0 {
		t.Errorf("superseded row should not linger: %+v", rows)
	}
	if rows, _ = CoverageRows(s.DB, "a", "refinement"); len(rows) != 1 || rows[0].Entry.ID != e2.ID {
		t.Errorf("latest row should win: %+v", rows)
	}
}
