package main

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// seedExportAgent creates agent "a" with one record per export section
// (except brief, which needs a compile) and a tool whose script lives under
// artifacts/. Ids are deterministic (global counter), so the tool body can
// name its own artifact path; the id is asserted after the put.
func seedExportAgent(t *testing.T, h *harness) (toolID string) {
	t.Helper()
	h.ok("base", "a", "--meta", "title=Agent A", "Base one.")
	h.ok("prefer", "a", "--meta", "repo-id=my_repo", "Lead with evidence.")
	h.ok("remember", "a", "The build takes ten minutes.")
	h.ok("state", "put", "a/working", "--expect", "none", "status: waiting")
	toolID = "tool_5"
	body := "version: 1\ndescription: Say hi\nexec:\n  argv: [\"artifacts/" + toolID + "/x.sh\", \"{{ who }}\"]\ninput:\n  who: {type: string}\n"
	r := h.okIn(body, "put", "a", "--lane", "definition", "--kind", "tool", "--name", "x", "--stdin")
	if got := r.id(t); got != toolID {
		t.Fatalf("tool id: got %s want %s", got, toolID)
	}
	dir := filepath.Join(h.home, "artifacts", toolID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.sh"), []byte("#!/bin/sh\necho hi \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.ok("put", "a", "--lane", "definition", "--kind", "related-agent", "--name", "helper", "A helper agent.")
	return toolID
}

func parseExport(t *testing.T, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("export is not YAML: %v\n%s", err, out)
	}
	return doc
}

func TestExportYAMLShape(t *testing.T) {
	h := newHarness(t)
	toolID := seedExportAgent(t, h)
	r := h.ok("export", "a")
	doc := parseExport(t, r.out)
	if doc["nine_tails_export"] != 1 || doc["agent"] != "a" {
		t.Errorf("header: %v", doc)
	}
	recs, _ := doc["records"].([]any)
	if len(recs) != 6 {
		t.Fatalf("want 6 records, got %d:\n%s", len(recs), r.out)
	}
	wantIDs := []string{"base_1", "rec_2", "rec_3", "state_4", "tool_5", "rel_6"}
	for i, rec := range recs {
		m := rec.(map[string]any)
		if m["id"] != wantIDs[i] {
			t.Errorf("records[%d] = %v, want %s (oldest first)", i, m["id"], wantIDs[i])
		}
		if m["status"] != "active" || m["created_at"] == nil {
			t.Errorf("envelope %v", m)
		}
	}
	if !strings.Contains(r.out, "meta:\n      title:\n        - Agent A") && !strings.Contains(r.out, "title: [Agent A]") {
		t.Errorf("meta multimap missing:\n%s", r.out)
	}
	om, _ := doc["omitted_artifacts"].([]any)
	if len(om) != 1 || om[0] != toolID {
		t.Errorf("omitted_artifacts = %v, want [%s]", om, toolID)
	}
	if !strings.Contains(r.err, toolID) {
		t.Errorf("omitted artifact should be reported on stderr: %q", r.err)
	}
	if strings.Contains(r.out, "signal") {
		t.Errorf("signals must not be exported:\n%s", r.out)
	}

	r = h.ok("export", "a", "--include", "base,tools", "--format", "json")
	j := r.json(t)
	if recs := j["records"].([]any); len(recs) != 2 {
		t.Errorf("--include base,tools: %d records", len(recs))
	}

	r = h.run("export", "a", "--include", "signals")
	if r.code != 2 {
		t.Errorf("unknown section should be exit 2, got %d: %s", r.code, r.err)
	}
	r = h.run("export", "nobody")
	if r.code != 3 || !strings.HasPrefix(r.err, "nine-tails: ") {
		t.Errorf("missing agent should be exit 3, got %d: %s", r.code, r.err)
	}

	// --all keeps a superseded base with its status.
	h.ok("base", "a", "Base two.")
	r = h.ok("export", "a", "--include", "base", "--all")
	if recs := parseExport(t, r.out)["records"].([]any); len(recs) != 2 || recs[0].(map[string]any)["status"] != "superseded" {
		t.Errorf("--all should include the superseded base:\n%s", r.out)
	}
}

func TestExportBundleAndImportTar(t *testing.T) {
	h := newHarness(t)
	toolID := seedExportAgent(t, h)
	tarPath := filepath.Join(t.TempDir(), "a.tar")
	r := h.ok("export", "a", "--bundle", tarPath)
	if strings.TrimSpace(r.out) != tarPath {
		t.Errorf("--bundle should print the bundle path, got %q", r.out)
	}
	f, err := os.Open(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	names := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		names[hdr.Name] = string(data)
	}
	if _, ok := names["manifest.yaml"]; !ok {
		t.Errorf("bundle lacks manifest.yaml: %v", names)
	}
	if names["artifacts/"+toolID+"/x.sh"] != "#!/bin/sh\necho hi \"$1\"\n" {
		t.Errorf("bundle lacks the artifact: %v", names)
	}
	if om := parseExport(t, names["manifest.yaml"])["omitted_artifacts"].([]any); len(om) != 0 {
		t.Errorf("bundle manifest should omit nothing: %v", om)
	}

	h2 := newHarness(t)
	r = h2.ok("import", tarPath)
	ids := strings.Fields(r.out)
	if len(ids) != 6 {
		t.Fatalf("import should print 6 ids, got %q (stderr %q)", r.out, r.err)
	}
	if strings.Contains(r.err, "warning") {
		t.Errorf("no warnings expected: %q", r.err)
	}
	var newTool string
	for _, id := range ids {
		if strings.HasPrefix(id, "tool_") {
			newTool = id
		}
	}
	if newTool == "" {
		t.Fatalf("no tool id in %v", ids)
	}
	script := filepath.Join(h2.home, "artifacts", newTool, "x.sh")
	info, err := os.Stat(script)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("artifact under new id: %v", err)
	}
	r = h2.ok("inspect", newTool)
	j := r.json(t)
	meta := j["meta"].(map[string]any)
	if from, _ := meta["imported-from"].([]any); len(from) != 1 || from[0] != toolID {
		t.Errorf("imported-from meta: %v", meta)
	}
	if body := j["body"].(string); !strings.Contains(body, "artifacts/"+newTool+"/x.sh") || !strings.Contains(body, "{{ who }}") {
		t.Errorf("argv not rewritten: %q", body)
	}
	if j["supersedes"] != nil || j["origin_context"] != nil || j["status"] != "active" {
		t.Errorf("mechanical fields should be reset: %v", j)
	}
	r = h2.ok("inspect", "a", "--format", "json")
	if !strings.Contains(r.out, `"title": [`) || !strings.Contains(r.out, "The build takes ten minutes.") || !strings.Contains(r.out, `"name": "helper"`) || !strings.Contains(r.out, `"name": "working"`) {
		t.Errorf("every section should arrive:\n%s", r.out)
	}
	r = h2.ok("load", "a")
	if !strings.HasPrefix(r.out, "# Agent A\n") || !strings.Contains(r.out, "- `x`: Say hi") {
		t.Errorf("imported agent should load:\n%s", r.out)
	}
}

func TestImportStdinAndTwiceSupersedesBase(t *testing.T) {
	h := newHarness(t)
	seedExportAgent(t, h)
	doc := h.ok("export", "a").out

	h2 := newHarness(t)
	r := h2.okIn(doc, "import", "--stdin")
	if n := len(strings.Fields(r.out)); n != 6 {
		t.Fatalf("want 6 ids, got %q", r.out)
	}
	if !strings.Contains(r.err, "nine-tails: warning: tool_") || !strings.Contains(r.err, "references a missing artifact") {
		t.Errorf("plain YAML import should warn about the missing artifact: %q", r.err)
	}
	r = h2.okIn(doc, "import", "--stdin", "--format", "json")
	ids := r.json(t)["ids"].(map[string]any)
	if len(ids) != 6 || ids["base_1"] == nil || !strings.HasPrefix(ids["base_1"].(string), "base_") {
		t.Errorf("json ids map: %v", ids)
	}
	r = h2.ok("inspect", "a", "--all", "--lane", "definition", "--kind", "agent-base", "--format", "json")
	recs := r.json(t)["records"].([]any)
	if len(recs) != 2 {
		t.Fatalf("want two bases after importing twice, got %d:\n%s", len(recs), r.out)
	}
	if recs[0].(map[string]any)["status"] != "superseded" || recs[1].(map[string]any)["status"] != "active" {
		t.Errorf("first base should be superseded by the second:\n%s", r.out)
	}
	if recs[1].(map[string]any)["supersedes"] != recs[0].(map[string]any)["id"] {
		t.Errorf("supersedes should link the two bases:\n%s", r.out)
	}
	// Guidance is appended, not superseded.
	r = h2.ok("inspect", "a", "--lane", "guidance", "--format", "json")
	if n := len(r.json(t)["records"].([]any)); n != 2 {
		t.Errorf("guidance should be inserted twice, got %d", n)
	}
}

func TestImportRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	r := h.runIn("hello: world\n", "import", "--stdin")
	if r.code != 2 {
		t.Errorf("non-export document should be exit 2, got %d: %s", r.code, r.err)
	}
	r = h.run("import", filepath.Join(t.TempDir(), "missing.yaml"))
	if r.code != 2 {
		t.Errorf("unreadable file should be exit 2, got %d", r.code)
	}
	r = h.run("import")
	if r.code != 2 {
		t.Errorf("neither FILE nor --stdin should be exit 2, got %d", r.code)
	}
	bad := "nine_tails_export: 1\nagent: a\nrecords:\n  - id: rec_1\n    body: fine\n  - id: tool_2\n    lane: definition\n    kind: tool\n    name: t\n    body: |\n      description: x\n      exec: {argv: [\"artifacts/tool_2/x.sh\", \"{{ undeclared }}\"]}\n"
	r = h.runIn(bad, "import", "--stdin", "--format", "json")
	if r.code != 2 || !strings.Contains(r.err, "tool_2") || !strings.Contains(r.out, `"code": 2`) {
		t.Errorf("invalid tool should abort with exit 2: code=%d err=%q out=%q", r.code, r.err, r.out)
	}
	if r := h.run("inspect", "a"); r.code != 3 {
		t.Errorf("nothing should have been written, inspect a: %d %s", r.code, r.out)
	}
	sig := "nine_tails_export: 1\nagent: a\nrecords:\n  - id: sig_1\n    lane: signal\n    kind: signal\n    body: ping\n  - body: kept\n"
	r = h.okIn(sig, "import", "--stdin")
	if strings.TrimSpace(r.out) != "rec_1" || !strings.Contains(r.err, "warning: skipped sig_1") {
		t.Errorf("signal should be skipped with a warning: out=%q err=%q", r.out, r.err)
	}
}

// compileAgent creates agent "a" with a base, one guidance entry and a
// compiled brief whose single item is keyed concise-evidence (via brief put).
func compileAgent(t *testing.T, h *harness) {
	t.Helper()
	base := h.ok("base", "a", "--meta", "title=Agent A", "Base one.").id(t)
	entry := h.ok("prefer", "a", "Lead with evidence.").id(t)
	doc := "input_entries: [" + entry + "]\nitems:\n  - key: concise-evidence\n    body: Lead review comments with concrete evidence.\nentries:\n  - id: " + entry + "\n    disposition: represented\n    items: [concise-evidence]\n"
	h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin")
}

func TestImportBriefTwiceSupersedesOrphan(t *testing.T) {
	h := newHarness(t)
	compileAgent(t, h)
	r := h.ok("export", "a", "--include", "base,brief")
	if !strings.Contains(r.err, "compile installs them") {
		t.Errorf("export should report that brief items travel inert: %q", r.err)
	}
	doc := r.out

	h2 := newHarness(t)
	for i := 0; i < 2; i++ {
		r := h2.okIn(doc, "import", "--stdin")
		if n := len(strings.Fields(r.out)); n != 2 || strings.Contains(r.err, "warning") {
			t.Fatalf("import %d: want base + item ids and no warning, got %q (stderr %q)", i, r.out, r.err)
		}
	}
	r = h2.ok("inspect", "a", "--all", "--lane", "guidance", "--kind", "brief-item", "--format", "json")
	recs := r.json(t)["records"].([]any)
	if len(recs) != 2 {
		t.Fatalf("want two items after importing twice, got %d:\n%s", len(recs), r.out)
	}
	old, cur := recs[0].(map[string]any), recs[1].(map[string]any)
	if old["status"] != "superseded" || cur["status"] != "active" || cur["supersedes"] != old["id"] || cur["name"] != "concise-evidence" {
		t.Errorf("first item should be superseded by the second:\n%s", r.out)
	}
	if r := h2.ok("load", "a"); strings.Contains(r.out, "## Working brief") {
		t.Errorf("orphan brief items must not render (DESIGN §13):\n%s", r.out)
	}

	// Against an agent whose live generation installs the same key: the
	// imported item is skipped with a warning and the live brief is intact.
	h3 := newHarness(t)
	compileAgent(t, h3)
	r = h3.okIn(doc, "import", "--stdin")
	if ids := strings.Fields(r.out); len(ids) != 1 || !strings.HasPrefix(ids[0], "base_") {
		t.Errorf("only the base should import over a live brief, got %q", r.out)
	}
	if !strings.Contains(r.err, "nine-tails: warning: skipped item_") || !strings.Contains(r.err, "active generation") {
		t.Errorf("skip warning expected: %q", r.err)
	}
	r = h3.ok("load", "a")
	if !strings.Contains(r.out, "## Working brief") || !strings.Contains(r.out, "Lead review comments with concrete evidence.") {
		t.Errorf("live brief should still render:\n%s", r.out)
	}
}

func TestImportKeepsBodyBytes(t *testing.T) {
	h := newHarness(t)
	id1 := h.okIn("x\n\n", "note", "g", "--stdin").id(t)
	h.okIn("line1\n\nline3\n\n", "note", "g", "--stdin")
	if b := h.ok("inspect", id1).json(t)["body"]; b != "x\n" {
		t.Fatalf("source body %q, want one trailing newline kept", b)
	}
	doc := h.ok("export", "g").out
	h2 := newHarness(t)
	ids := strings.Fields(h2.okIn(doc, "import", "--stdin").out)
	if len(ids) != 2 {
		t.Fatalf("ids: %v", ids)
	}
	for i, want := range []string{"x\n", "line1\n\nline3\n"} {
		if got := h2.ok("inspect", ids[i]).json(t)["body"]; got != want {
			t.Errorf("%s: body %q, want %q", ids[i], got, want)
		}
	}
}

func TestImportRejectsBadMetaKeys(t *testing.T) {
	h := newHarness(t)
	doc := "nine_tails_export: 1\nagent: m\nrecords:\n  - id: rec_1\n    lane: guidance\n    kind: prefer\n    body: pref\n    meta:\n      \"bad key\": v1\n      \"k=v\": v2\n      \"br[x]\": v3\n"
	r := h.runIn(doc, "import", "--stdin")
	if r.code != 2 || !strings.Contains(r.err, `nine-tails: rec_1: invalid: metadata key "bad key" may not contain`) {
		t.Errorf("bad meta key should be exit 2: code=%d err=%q", r.code, r.err)
	}
	if r := h.run("inspect", "m"); r.code != 3 {
		t.Errorf("nothing should have been written, inspect m: %d", r.code)
	}
}

func TestImportValidatesState(t *testing.T) {
	h := newHarness(t)
	doc := "nine_tails_export: 1\nagent: s\nrecords:\n  - lane: definition\n    kind: agent-base\n    name: base\n    body: Base.\n  - lane: state\n    kind: working-state\n    name: working\n    body: \"a: [unclosed\"\n"
	r := h.runIn(doc, "import", "--stdin")
	if r.code != 2 || !strings.Contains(r.err, "records[1]") || !strings.Contains(r.err, "not valid YAML") {
		t.Errorf("invalid state should be exit 2: code=%d err=%q", r.code, r.err)
	}
	if r := h.run("inspect", "s"); r.code != 3 {
		t.Errorf("nothing (not even the base) should have been written, inspect s: %d", r.code)
	}
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("state_max_bytes: 16\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := "nine_tails_export: 1\nagent: s\nrecords:\n  - lane: state\n    kind: working-state\n    name: working\n    body: \"status: waiting for a much longer value\"\n"
	r = h.runIn(big, "import", "--stdin")
	if r.code != 2 || !strings.Contains(r.err, "cap is 16") {
		t.Errorf("oversize state should be exit 2 with the configured cap: code=%d err=%q", r.code, r.err)
	}
	good := "nine_tails_export: 1\nagent: s\nrecords:\n  - lane: state\n    kind: working-state\n    name: working\n    body: \"status: ok\"\n"
	h.okIn(good, "import", "--stdin")
	if r := h.ok("state", "get", "s/working"); r.out != "status: ok\n" {
		t.Errorf("state get: %q", r.out)
	}
}

func TestExportIncludeMustNameASection(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	for _, inc := range []string{",", " ", " , "} {
		r := h.run("export", "a", "--include", inc)
		if r.code != 2 || r.out != "" || !strings.Contains(r.err, "names no section") {
			t.Errorf("--include %q: want exit 2 and no document, got code=%d out=%q err=%q", inc, r.code, r.out, r.err)
		}
	}
	r := h.ok("export", "a", "--include", " base ")
	if recs := parseExport(t, r.out)["records"].([]any); len(recs) != 1 {
		t.Errorf("blanks around a section name are fine: %d records", len(recs))
	}
}

func TestImportAcceptsQuotedVersion(t *testing.T) {
	h := newHarness(t)
	r := h.okIn("nine_tails_export: \"1\"\nagent: v\nrecords:\n  - body: kept\n", "import", "--stdin")
	if strings.TrimSpace(r.out) != "rec_1" {
		t.Errorf("quoted version should import: %q", r.out)
	}
	r = h.runIn("nine_tails_export: \"2\"\nagent: v\nrecords: []\n", "import", "--stdin")
	if r.code != 2 || !strings.Contains(r.err, `must be the integer 1 (got the string "2")`) {
		t.Errorf("wrong version: code=%d err=%q", r.code, r.err)
	}
}
