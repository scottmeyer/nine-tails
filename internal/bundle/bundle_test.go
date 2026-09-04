package bundle

import (
	"archive/tar"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustTx(t *testing.T, s *store.Store, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := s.Tx(fn); err != nil {
		t.Fatal(err)
	}
}

func insert(t *testing.T, s *store.Store, nr store.NewRecord) *store.Record {
	t.Helper()
	var rec *store.Record
	mustTx(t, s, func(tx *sql.Tx) error {
		var err error
		rec, err = store.InsertRecord(tx, nr)
		return err
	})
	return rec
}

func putNamed(t *testing.T, s *store.Store, nr store.NewRecord) *store.Record {
	t.Helper()
	var rec *store.Record
	mustTx(t, s, func(tx *sql.Tx) error {
		var err error
		rec, err = store.PutNamed(tx, nr, "")
		return err
	})
	return rec
}

const toolBodyTmpl = `version: 1
description: Say hi
# a comment that must survive import
exec:
  argv: ["artifacts/%s/x.sh", "{{ who }}"]
  stdin: none
  extra-exec-key: kept
input:
  who: {type: string}
custom: preserved
`

// addTool mirrors `tool add`: allocate the id, write the body against it,
// insert, then copy the script under artifacts/<id>/.
func addTool(t *testing.T, s *store.Store, agent, name, script string) *store.Record {
	t.Helper()
	var rec *store.Record
	mustTx(t, s, func(tx *sql.Tx) error {
		id, err := store.NewID("tool")
		if err != nil {
			return err
		}
		body := strings.TrimSuffix(strings.ReplaceAll(toolBodyTmpl, "%s", id), "\n")
		rec, err = store.PutNamed(tx, store.NewRecord{ID: id, Agent: agent, Lane: "definition", Kind: "tool", Name: name, Body: body}, "")
		return err
	})
	dir := filepath.Join(s.Home, "artifacts", rec.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return rec
}

// seed populates every export section for agent "a" plus a signal and a
// shared tool that must not be exported.
func seed(t *testing.T, s *store.Store) (tool *store.Record) {
	t.Helper()
	putNamed(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base one.", Meta: store.Meta{"title": {"Agent A"}}})
	insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Lead with evidence.", OriginContext: "ctx_9", Meta: store.Meta{"repo-id": {"my_repo"}}})
	insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "The build takes ten minutes."})
	putNamed(t, s, store.NewRecord{Agent: "a", Lane: "state", Kind: "working-state", Name: "working", Body: "status: waiting"})
	tool = addTool(t, s, "a", "x", "#!/bin/sh\necho hi $1\n")
	putNamed(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "related-agent", Name: "helper", Body: "A helper agent."})
	mustTx(t, s, func(tx *sql.Tx) error {
		_, _, err := store.InstallGeneration(tx, "a", "", []store.NewItem{{Key: "concise", Body: "Be concise.", Meta: store.Meta{"phase": {"review"}}}}, nil)
		return err
	})
	mustTx(t, s, func(tx *sql.Tx) error {
		_, _, err := store.CreateSignal(tx, "a", "ping", store.Meta{"subject": {"Ping"}}, store.Clock(), "", "")
		return err
	})
	putNamed(t, s, store.NewRecord{Agent: "shared", Lane: "definition", Kind: "tool", Name: "shared-tool", Body: "description: shared\nexec:\n  argv: [/bin/true]"})
	return tool
}

func sectionCounts(doc *Document) map[string]int {
	out := map[string]int{}
	for _, r := range doc.Records {
		out[SectionOf(r)]++
	}
	return out
}

func find(t *testing.T, s *store.Store, f store.Filter) []*store.Record {
	t.Helper()
	recs, err := store.ListRecords(s.DB, f)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestExportSectionsAndOmitted(t *testing.T) {
	s := openStore(t)
	tl := seed(t, s)
	doc, err := Export(s.DB, ExportOptions{Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || doc.Agent != "a" {
		t.Errorf("header: %+v", doc)
	}
	want := map[string]int{"base": 1, "journal": 2, "state": 1, "tools": 1, "agents": 1, "brief": 1}
	got := sectionCounts(doc)
	for k, n := range want {
		if got[k] != n {
			t.Errorf("section %s: got %d want %d (%v)", k, got[k], n, got)
		}
	}
	if len(doc.Records) != 7 {
		t.Errorf("want 7 records, got %d", len(doc.Records))
	}
	for i := 1; i < len(doc.Records); i++ {
		if doc.Records[i].CreatedAt < doc.Records[i-1].CreatedAt {
			t.Errorf("records not oldest first")
		}
	}
	for _, r := range doc.Records {
		if r.Lane == "signal" || r.Agent != "a" {
			t.Errorf("exported %s (%s/%s)", r.ID, r.Agent, r.Lane)
		}
	}
	if len(doc.OmittedArtifacts) != 1 || doc.OmittedArtifacts[0] != tl.ID {
		t.Errorf("omitted_artifacts = %v, want [%s]", doc.OmittedArtifacts, tl.ID)
	}
	if !doc.HasBriefItems() {
		t.Error("brief items should be reported")
	}

	doc, err = Export(s.DB, ExportOptions{Agent: "a", Include: []string{"base", "tools"}, WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Records) != 2 || len(doc.OmittedArtifacts) != 0 {
		t.Errorf("include base,tools with artifacts: %d records, omitted %v", len(doc.Records), doc.OmittedArtifacts)
	}

	// --all keeps superseded records with their status.
	putNamed(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base two."})
	doc, err = Export(s.DB, ExportOptions{Agent: "a", Include: []string{"base"}})
	if err != nil || len(doc.Records) != 1 || doc.Records[0].Body != "Base two." {
		t.Errorf("active-only base: %v %+v", err, doc.Records)
	}
	doc, err = Export(s.DB, ExportOptions{Agent: "a", Include: []string{"base"}, All: true})
	if err != nil || len(doc.Records) != 2 || doc.Records[0].Status != "superseded" || doc.Records[1].Status != "active" {
		t.Errorf("--all base: %v %+v", err, doc.Records)
	}

	if _, err := Export(s.DB, ExportOptions{Agent: "nobody"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing agent: %v", err)
	}
	if _, err := Export(s.DB, ExportOptions{Agent: "a", Include: []string{"signals"}}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("unknown section: %v", err)
	}
}

func TestRoundTripBundle(t *testing.T) {
	src := openStore(t)
	oldTool := seed(t, src)
	doc, err := Export(src.DB, ExportOptions{Agent: "a", WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteBundle(&buf, src.Home, doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.OmittedArtifacts) != 0 {
		t.Errorf("bundle should carry the artifact, omitted %v", doc.OmittedArtifacts)
	}
	if !IsTar("anything", buf.Bytes()) {
		t.Error("bundle not detected as tar")
	}
	rdoc, arts, err := ReadBundle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rdoc.Records) != len(doc.Records) {
		t.Fatalf("manifest has %d records, want %d", len(rdoc.Records), len(doc.Records))
	}
	oldPath := "artifacts/" + oldTool.ID + "/x.sh"
	if string(arts[oldPath].Data) != "#!/bin/sh\necho hi $1\n" {
		t.Fatalf("artifact missing from bundle: %v", arts)
	}

	dst := openStore(t)
	var warnings []string
	res, err := Import(dst, rdoc, arts, ImportOptions{Warn: func(f string, a ...any) { warnings = append(warnings, sprintf(f, a...)) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(res) != len(doc.Records) {
		t.Fatalf("got %d results, want %d", len(res), len(doc.Records))
	}
	byOld := map[string]string{}
	for _, r := range res {
		byOld[r.Old] = r.New
	}
	all := find(t, dst, store.Filter{Agent: "a"})
	if len(all) != len(doc.Records) {
		t.Fatalf("dst has %d active records, want %d", len(all), len(doc.Records))
	}
	for _, r := range all {
		if r.Status != "active" || r.Supersedes != "" || r.OriginContext != "" {
			t.Errorf("%s: status=%s supersedes=%q origin=%q", r.ID, r.Status, r.Supersedes, r.OriginContext)
		}
		if len(r.Meta["imported-from"]) != 1 || byOld[r.Meta.First("imported-from")] != r.ID {
			t.Errorf("%s: imported-from meta %v", r.ID, r.Meta)
		}
	}
	base, err := store.ActiveNamed(dst.DB, "a", "definition", "agent-base", "base")
	if err != nil || base.Body != "Base one." || base.Meta.First("title") != "Agent A" {
		t.Errorf("base: %v %+v", err, base)
	}
	if _, err := store.ActiveNamed(dst.DB, "a", "state", "working-state", "working"); err != nil {
		t.Errorf("state: %v", err)
	}
	if _, err := store.ActiveNamed(dst.DB, "a", "definition", "related-agent", "helper"); err != nil {
		t.Errorf("related agent: %v", err)
	}
	items := find(t, dst, store.Filter{Agent: "a", Kind: "brief-item"})
	if len(items) != 1 || items[0].Lane != "guidance" || items[0].Name != "concise" || items[0].Meta.First("phase") != "review" {
		t.Errorf("brief item: %+v", items)
	}
	if !strings.HasPrefix(items[0].ID, "item_") {
		t.Errorf("brief item keeps the item prefix: %s", items[0].ID)
	}
	journal := find(t, dst, store.Filter{Agent: "a", Lane: "guidance", Kind: "prefer"})
	if len(journal) != 1 || journal[0].Meta.First("repo-id") != "my_repo" {
		t.Errorf("guidance meta lost: %+v", journal)
	}

	newTool, err := store.ActiveNamed(dst.DB, "a", "definition", "tool", "x")
	if err != nil {
		t.Fatal(err)
	}
	if newTool.ID != byOld[oldTool.ID] || !strings.HasPrefix(newTool.ID, "tool_") {
		t.Errorf("tool id mapping: %s vs %v", newTool.ID, byOld)
	}
	wantPath := "artifacts/" + newTool.ID + "/x.sh"
	if ArtifactPath(newTool.Body) != wantPath {
		t.Errorf("argv[0] not rewritten: %q", newTool.Body)
	}
	for _, keep := range []string{"# a comment that must survive import", "extra-exec-key: kept", "custom: preserved", `"{{ who }}"`} {
		if !strings.Contains(newTool.Body, keep) {
			t.Errorf("tool body lost %q:\n%s", keep, newTool.Body)
		}
	}
	data, err := os.ReadFile(filepath.Join(dst.Home, "artifacts", newTool.ID, "x.sh"))
	if err != nil || string(data) != "#!/bin/sh\necho hi $1\n" {
		t.Errorf("artifact not copied: %v %q", err, data)
	}
	if info, _ := os.Stat(filepath.Join(dst.Home, "artifacts", newTool.ID, "x.sh")); info != nil && info.Mode().Perm()&0o100 == 0 {
		t.Error("artifact should be executable")
	}
	if len(find(t, dst, store.Filter{Agent: "a", Lane: "signal", Status: "*"})) != 0 {
		t.Error("signals must not travel")
	}
}

func TestRoundTripBundleFindsEveryArtifactArgvElement(t *testing.T) {
	src := openStore(t)
	insert(t, src, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "consume the first id"})
	var oldTool *store.Record
	mustTx(t, src, func(tx *sql.Tx) error {
		id, err := store.NewID("tool")
		if err != nil {
			return err
		}
		body := fmt.Sprintf(`version: 1
description: interpreter plus multiple files
exec:
  argv: [/bin/sh, artifacts/%[1]s/run.sh, artifacts/%[1]s/data.json, artifacts/%[1]s/run.sh]
  adapter: preserved
input:
  unused: {type: string, choices: [one, two]}
`, id)
		oldTool, err = store.PutNamed(tx, store.NewRecord{ID: id, Agent: "a", Lane: "definition", Kind: "tool", Name: "multi", Body: strings.TrimSuffix(body, "\n")}, "")
		return err
	})
	oldDir := filepath.Join(src.Home, "artifacts", oldTool.ID)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "data.json"), []byte("{\"ok\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plain, err := Export(src.DB, ExportOptions{Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.OmittedArtifacts) != 1 || plain.OmittedArtifacts[0] != oldTool.ID {
		t.Fatalf("plain export omitted_artifacts = %v", plain.OmittedArtifacts)
	}
	doc, err := Export(src.DB, ExportOptions{Agent: "a", WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteBundle(&buf, src.Home, doc); err != nil {
		t.Fatal(err)
	}
	rdoc, arts, err := ReadBundle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	wantOldPaths := []string{
		"artifacts/" + oldTool.ID + "/run.sh",
		"artifacts/" + oldTool.ID + "/data.json",
	}
	if len(arts) != len(wantOldPaths) {
		t.Fatalf("bundle artifacts = %v", arts)
	}
	for _, p := range wantOldPaths {
		if _, ok := arts[p]; !ok {
			t.Errorf("bundle omitted %s", p)
		}
	}

	dst := openStore(t)
	res, err := Import(dst, rdoc, arts, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("import results = %+v", res)
	}
	newTool, err := store.ActiveNamed(dst.DB, "a", "definition", "tool", "multi")
	if err != nil {
		t.Fatal(err)
	}
	wantNewPaths := []string{
		"artifacts/" + newTool.ID + "/run.sh",
		"artifacts/" + newTool.ID + "/data.json",
	}
	if got := ArtifactPaths(newTool.Body); strings.Join(got, "\x00") != strings.Join(wantNewPaths, "\x00") {
		t.Fatalf("rewritten artifact paths = %v, want %v\n%s", got, wantNewPaths, newTool.Body)
	}
	for _, keep := range []string{"adapter: preserved", "choices:"} {
		if !strings.Contains(newTool.Body, keep) {
			t.Errorf("rewritten body lost %q:\n%s", keep, newTool.Body)
		}
	}
	for name, want := range map[string]string{"run.sh": "#!/bin/sh\n", "data.json": "{\"ok\":true}\n"} {
		data, err := os.ReadFile(filepath.Join(dst.Home, "artifacts", newTool.ID, name))
		if err != nil || string(data) != want {
			t.Errorf("copied %s: %v %q", name, err, data)
		}
	}
}

func TestRoundTripBundleRewritesMergedArtifactArgv(t *testing.T) {
	src := openStore(t)
	var oldTool *store.Record
	mustTx(t, src, func(tx *sql.Tx) error {
		id, err := store.NewID("tool")
		if err != nil {
			return err
		}
		body := fmt.Sprintf(`description: merged argv
common: &common
  argv: [artifacts/%[1]s/run.sh]
  stdin: json
exec:
  <<: *common
`, id)
		oldTool, err = store.PutNamed(tx, store.NewRecord{ID: id, Agent: "a", Lane: "definition", Kind: "tool", Name: "merged", Body: strings.TrimSuffix(body, "\n")}, "")
		return err
	})
	dir := filepath.Join(src.Home, "artifacts", oldTool.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc, err := Export(src.DB, ExportOptions{Agent: "a", WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteBundle(&buf, src.Home, doc); err != nil {
		t.Fatal(err)
	}
	rdoc, arts, err := ReadBundle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	dst := openStore(t)
	if _, err := Import(dst, rdoc, arts, ImportOptions{}); err != nil {
		t.Fatalf("merged argv bundle did not import: %v", err)
	}
	got, err := store.ActiveNamed(dst.DB, "a", "definition", "tool", "merged")
	if err != nil {
		t.Fatal(err)
	}
	wantRef := "artifacts/" + got.ID + "/run.sh"
	if refs := ArtifactPaths(got.Body); len(refs) != 1 || refs[0] != wantRef {
		t.Fatalf("merged argv was not rewritten: refs=%v body=\n%s", refs, got.Body)
	}
	if !strings.Contains(got.Body, "common: &common") || !strings.Contains(got.Body, "<<: *common") {
		t.Errorf("merge and unknown key were not preserved:\n%s", got.Body)
	}
	if data, err := os.ReadFile(filepath.Join(dst.Home, filepath.FromSlash(wantRef))); err != nil || string(data) != "#!/bin/sh\nexit 0\n" {
		t.Errorf("merged artifact was not copied: %v %q", err, data)
	}
}

func TestArtifactReplacementDisambiguatesSameBasename(t *testing.T) {
	refs := []string{"artifacts/tool_1/left/config.json", "artifacts/tool_2/right/config.json"}
	got, err := artifactReplacements(refs, "tool_9")
	if err != nil {
		t.Fatal(err)
	}
	if got[refs[0]] == got[refs[1]] || got[refs[0]] != "artifacts/tool_9/tool_1/left/config.json" || got[refs[1]] != "artifacts/tool_9/tool_2/right/config.json" {
		t.Errorf("replacements = %v", got)
	}
}

func TestWriteBundleDoesNotReadEscapingArtifactPath(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(home, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := []byte("unique secret outside the store")
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{{
		ID: "tool_1", Agent: "a", Lane: "definition", Kind: "tool", Name: "escape",
		Body: "description: escape\nexec:\n  argv: [artifacts/x/../../../secret.txt]",
	}}}
	var buf bytes.Buffer
	if err := WriteBundle(&buf, home, doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.OmittedArtifacts) != 1 || doc.OmittedArtifacts[0] != "tool_1" {
		t.Errorf("unsafe reference was not reported omitted: %v", doc.OmittedArtifacts)
	}
	if bytes.Contains(buf.Bytes(), secret) {
		t.Fatal("bundle copied bytes from outside the store")
	}
	_, arts, err := ReadBundle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 0 {
		t.Errorf("unsafe bundle contains artifacts: %v", arts)
	}
}

func TestWriteBundleDoesNotReadStoreFilesThroughArtifactSymlink(t *testing.T) {
	s := openStore(t)
	leakDir := filepath.Join(s.Home, "artifacts", "leak")
	if err := os.Mkdir(leakDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(leakDir, "database")
	if err := os.Symlink(filepath.Join("..", "..", "nine-tails.db"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	database, err := os.ReadFile(filepath.Join(s.Home, "nine-tails.db"))
	if err != nil {
		t.Fatal(err)
	}
	doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{{
		ID: "tool_1", Agent: "a", Lane: "definition", Kind: "tool", Name: "leak",
		Body: "description: leak\nexec:\n  argv: [artifacts/leak/database]",
	}}}
	var buf bytes.Buffer
	if err := WriteBundle(&buf, s.Home, doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.OmittedArtifacts) != 1 || doc.OmittedArtifacts[0] != "tool_1" {
		t.Fatalf("store-file reference was not omitted: %v", doc.OmittedArtifacts)
	}
	if len(database) > 0 && bytes.Contains(buf.Bytes(), database) {
		t.Fatal("bundle included the store database through an artifact symlink")
	}
	_, arts, err := ReadBundle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 0 {
		t.Fatalf("bundle contains escaped artifacts: %v", arts)
	}
}

func TestFailedImportCleansNewArtifactDirectories(t *testing.T) {
	dst := openStore(t)
	preexisting := filepath.Join(dst.Home, "artifacts", "tool_KEEP")
	if err := os.MkdirAll(preexisting, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(preexisting, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A document that fails validation after a tool with an artifact: nothing
	// may be written, and no artifact directory may appear.
	doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{
		{ID: "tool_10", Lane: "definition", Kind: "tool", Name: "first", Body: "description: first\nexec:\n  argv: [artifacts/tool_10/first.sh]"},
		{ID: "rec_11", Lane: "recall", Kind: "memory", Body: ""},
	}}
	arts := map[string]Artifact{"artifacts/tool_10/first.sh": {Data: []byte("first"), Mode: 0o755}}
	if _, err := Import(dst, doc, arts, ImportOptions{}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("Import should fail validation: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dst.Home, "artifacts"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "tool_KEEP" {
		t.Errorf("artifact directories after failed import: %v (%v)", entries, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Errorf("pre-existing artifact directory was changed: %v %q", err, data)
	}
	if recs := find(t, dst, store.Filter{Status: "*"}); len(recs) != 0 {
		t.Errorf("records survived failed import: %+v", recs)
	}

	// The artifact writer itself refuses a destination that already exists
	// and removes only the directories it created.
	var roots []string
	files := []pendingFile{
		{rel: "artifacts/tool_NEW/x.sh", art: Artifact{Data: []byte("x"), Mode: 0o755}},
		{rel: "artifacts/tool_KEEP/keep.txt", art: Artifact{Data: []byte("clobber"), Mode: 0o644}},
	}
	err = writePendingArtifacts(dst.Home, files, &roots)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("pre-existing destination: %v", err)
	}
	if err := cleanupArtifactRoots(roots); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst.Home, "artifacts", "tool_NEW")); !os.IsNotExist(err) {
		t.Errorf("new artifact directory survived cleanup: %v", err)
	}
	if data, _ := os.ReadFile(marker); string(data) != "keep" {
		t.Errorf("pre-existing file was clobbered: %q", data)
	}
}

func TestImportSupersedesSameNamed(t *testing.T) {
	src := openStore(t)
	seed(t, src)
	doc, err := Export(src.DB, ExportOptions{Agent: "a", Include: []string{"base", "state", "tools", "agents"}})
	if err != nil {
		t.Fatal(err)
	}
	dst := openStore(t)
	for i := 0; i < 2; i++ {
		if _, err := Import(dst, doc, nil, ImportOptions{}); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	bases := find(t, dst, store.Filter{Agent: "a", Lane: "definition", Kind: "agent-base", Status: "*"})
	if len(bases) != 2 || bases[0].Status != "superseded" || bases[1].Status != "active" || bases[1].Supersedes != bases[0].ID {
		t.Errorf("bases after double import: %+v %+v", bases[0], bases[1])
	}
	for _, f := range []store.Filter{{Lane: "state", Name: "working"}, {Lane: "definition", Kind: "related-agent", Name: "helper"}} {
		f.Agent = "a"
		if n := len(find(t, dst, f)); n != 1 {
			t.Errorf("%+v: %d active, want 1", f, n)
		}
	}
	// The plain document carries no artifact, so the tool is skipped, not
	// installed as a definition that cannot run.
	if n := len(find(t, dst, store.Filter{Agent: "a", Lane: "definition", Kind: "tool", Name: "x", Status: "*"})); n != 0 {
		t.Errorf("artifact-less tool was imported %d times, want 0", n)
	}
}

func TestReadDocumentKebabAndDefaults(t *testing.T) {
	doc, err := ReadDocument([]byte(`nine-tails-export: 1
agent: a
records:
  - id: rec_7
    body: Remember this.
    created-at: 2026-09-04T16:30:00Z
    origin-context: ctx_1
    meta:
      repo-id: my_repo
      phase: [review, comment]
  - id: base_1
    lane: definition
    kind: agent-base
    name: base
    body: Base.
omitted-artifacts: [tool_3]
`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || doc.Agent != "a" || len(doc.Records) != 2 || len(doc.OmittedArtifacts) != 1 {
		t.Fatalf("doc: %+v", doc)
	}
	r := doc.Records[0]
	if r.ID != "rec_7" || r.CreatedAt != "2026-09-04T16:30:00Z" || r.OriginContext != "ctx_1" {
		t.Errorf("kebab keys: %+v", r)
	}
	if r.Meta.First("repo-id") != "my_repo" || len(r.Meta["phase"]) != 2 {
		t.Errorf("meta: %v", r.Meta)
	}

	dst := openStore(t)
	res, err := Import(dst, doc, nil, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRecord(dst.DB, res[0].New)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != "recall" || got.Kind != "memory" || got.Agent != "a" || got.OriginContext != "" {
		t.Errorf("missing lane should default to recall/memory: %+v", got)
	}
	if got.Meta.First("imported-from") != "rec_7" || got.Meta.First("repo-id") != "my_repo" {
		t.Errorf("meta: %v", got.Meta)
	}

	for _, bad := range []string{"agent: a\n", "nine_tails_export: 2\n", "- not a map\n", "nine_tails_export: 1\nrecords: [1]\n"} {
		if _, err := ReadDocument([]byte(bad)); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%q: want ErrInvalid, got %v", bad, err)
		}
	}
}

func TestReadDocumentRejectsEnvelopeAliasCollisionsAndNonStringBodies(t *testing.T) {
	for _, raw := range []string{
		"nine_tails_export: 1\nnine-tails-export: 2\nagent: a\nrecords: []\n",
		"nine_tails_export: 1\nagent: a\nrecords:\n  - origin_context: ctx_1\n    origin-context: ctx_2\n    body: x\n",
	} {
		if _, err := ReadDocument([]byte(raw)); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "both normalize") {
			t.Errorf("alias collision should be invalid, got %v", err)
		}
	}
	for _, body := range []string{"[alpha, beta]", "001", "{text: alpha}"} {
		raw := "nine_tails_export: 1\nagent: a\nrecords:\n  - body: " + body + "\n"
		if _, err := ReadDocument([]byte(raw)); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "records[0].body must be a string") {
			t.Errorf("body %s should be rejected rather than coerced, got %v", body, err)
		}
	}
	for _, meta := range []string{"{nested: {x: y}}", "{nested: [[x]]}"} {
		raw := "nine_tails_export: 1\nagent: a\nrecords:\n  - body: x\n    meta: " + meta + "\n"
		if _, err := ReadDocument([]byte(raw)); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "must be a scalar value") {
			t.Errorf("structured metadata %s should be rejected rather than coerced, got %v", meta, err)
		}
	}
	for _, raw := range []string{
		"nine_tails_export: 1\nagent: a\nrecords: []\n---\nrecords: []\n",
		"nine_tails_export: 1\nagent: a\nrecords: []\n---\n",
	} {
		if _, err := ReadDocument([]byte(raw)); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "exactly one") {
			t.Errorf("multiple YAML documents should be invalid, got %v", err)
		}
	}
	if _, err := ReadDocument([]byte("nine_tails_export: 1\nagent: a\n")); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "missing records") {
		t.Errorf("missing records should be invalid, got %v", err)
	}
	invalidUTF8 := append([]byte("nine_tails_export: 1\nagent: a\nrecords:\n  - body: \""), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte("\"\n")...)
	if _, err := ReadDocument(invalidUTF8); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("invalid UTF-8 document should be invalid, got %v", err)
	}
}

func TestImportRejectsInvalidUTF8DocumentValues(t *testing.T) {
	dst := openStore(t)
	invalid := string([]byte{0xff})
	doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{
		{ID: "rec_1", Lane: "recall", Kind: "memory", Body: "valid"},
		{ID: "rec_2", Lane: "recall", Kind: "memory", Body: invalid},
	}}
	if _, err := Import(dst, doc, nil, ImportOptions{}); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("Import error = %v, want ErrInvalid mentioning UTF-8", err)
	}
	if got := find(t, dst, store.Filter{Status: "*"}); len(got) != 0 {
		t.Fatalf("invalid UTF-8 import was not atomic: %+v", got)
	}
}

func TestImportInvalidToolAbortsAtomically(t *testing.T) {
	dst := openStore(t)
	insert(t, dst, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "pre-existing"})
	before := find(t, dst, store.Filter{Status: "*"})
	doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{
		{ID: "rec_1", Lane: "guidance", Kind: "note", Body: "fine"},
		{ID: "base_2", Lane: "definition", Kind: "agent-base", Name: "base", Body: "fine too"},
		{ID: "tool_3", Lane: "definition", Kind: "tool", Name: "bad", Body: "description: x\nexec:\n  argv: [\"artifacts/tool_3/x.sh\", \"{{ undeclared }}\"]"},
	}}
	arts := map[string]Artifact{"artifacts/tool_3/x.sh": {Data: []byte("#!/bin/sh\n"), Mode: 0o755}}
	_, err := Import(dst, doc, arts, ImportOptions{})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "tool_3") {
		t.Errorf("error should name the record: %v", err)
	}
	after := find(t, dst, store.Filter{Status: "*"})
	if len(after) != len(before) {
		t.Errorf("records changed: %d -> %d", len(before), len(after))
	}
	entries, _ := os.ReadDir(filepath.Join(dst.Home, "artifacts"))
	if len(entries) != 0 {
		t.Errorf("artifacts written despite failure: %v", entries)
	}
	// Other failures are ErrInvalid too and write nothing.
	for _, d := range []*Document{
		{Version: 1, Records: []*store.Record{{Body: "no agent anywhere"}}},
		{Version: 1, Agent: "Bad Name", Records: []*store.Record{{Body: "x"}}},
		{Version: 1, Agent: "a", Records: []*store.Record{{Lane: "state", Kind: "working-state", Body: "x: 1"}}},
		{Version: 1, Agent: "a", Records: []*store.Record{{Lane: "definition", Kind: "tool", Name: "t", Body: "no argv"}}},
		{Version: 1, Agent: "a", Records: []*store.Record{{Lane: "recall", Kind: "memory", Body: ""}}},
		{Version: 1, Agent: "a", Records: []*store.Record{{Lane: "weird", Kind: "x", Body: "y"}}},
		{Version: 0, Agent: "a"},
	} {
		if _, err := Import(dst, d, nil, ImportOptions{}); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", d, err)
		}
	}
	if after := find(t, dst, store.Filter{Status: "*"}); len(after) != len(before) {
		t.Errorf("records changed: %d -> %d", len(before), len(after))
	}
	if _, err := Import(dst, nil, nil, ImportOptions{}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("nil document: want ErrInvalid, got %v", err)
	}
}

func TestImportSkipsSignalsAndToolsWithoutArtifacts(t *testing.T) {
	dst := openStore(t)
	doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{
		{ID: "sig_1", Lane: "signal", Kind: "signal", Body: "ping", Meta: store.Meta{"subject": {"Ping"}}},
		{ID: "tool_2", Lane: "definition", Kind: "tool", Name: "x", Body: "description: x\nexec:\n  argv: [artifacts/tool_2/x.sh]"},
		{ID: "rec_3", Lane: "recall", Kind: "memory", Body: "kept"},
	}}
	var warnings []string
	res, err := Import(dst, doc, nil, ImportOptions{Warn: func(f string, a ...any) { warnings = append(warnings, strings.TrimSpace(sprintf(f, a...))) }})
	if err != nil {
		t.Fatal(err)
	}
	// Only the recall record lands; the skipped tool consumes no id.
	if len(res) != 1 || res[0].Old != "rec_3" || !strings.HasPrefix(res[0].New, "rec_") {
		t.Errorf("results: %+v", res)
	}
	wantTool := `skipped tool_2: tool "x" references artifacts/tool_2/x.sh, which this document does not carry (export it with --bundle); the active definition is kept`
	if len(warnings) != 2 || !strings.Contains(warnings[0], "skipped sig_1") || warnings[1] != wantTool {
		t.Errorf("warnings: %q", warnings)
	}
	if n := len(find(t, dst, store.Filter{Agent: "a", Lane: "definition", Kind: "tool", Status: "*"})); n != 0 {
		t.Errorf("artifact-less tool was written %d times, want 0", n)
	}
}

func TestRewriteArgv0(t *testing.T) {
	body := "description: d\nexec:\n  argv: ['artifacts/tool_1/a.sh', x]\n  stdin: text\n"
	out, err := RewriteArgv0(body, "artifacts/tool_9/a.sh")
	if err != nil {
		t.Fatal(err)
	}
	if ArtifactPath(out) != "artifacts/tool_9/a.sh" || !strings.Contains(out, "stdin: text") || strings.HasSuffix(out, "\n") {
		t.Errorf("rewrite: %q", out)
	}
	for _, bad := range []string{"description: d", "exec: {argv: []}", "exec: {argv: [[nested]]}", "not: [valid"} {
		if _, err := RewriteArgv0(bad, "artifacts/tool_9/a.sh"); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
	if ArtifactPath("exec:\n  argv: [/bin/sh]") != "" || ArtifactPath("garbage: [") != "" {
		t.Error("ArtifactPath should be empty for non-artifact or broken bodies")
	}
}

func TestReadBundleRejectsNonBundle(t *testing.T) {
	if _, _, err := ReadBundle(strings.NewReader("nine_tails_export: 1\n")); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("want ErrInvalid, got %v", err)
	}
	if IsTar("x.yaml", []byte("nine_tails_export: 1\n")) || !IsTar("x.tar", nil) {
		t.Error("IsTar")
	}
}

func TestReadBundleRejectsDuplicateMembers(t *testing.T) {
	manifest := []byte("nine_tails_export: 1\nagent: a\nrecords: []\n")
	for _, duplicate := range []string{"manifest.yaml", "artifacts/tool_1/run.sh"} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := writeEntry(tw, "manifest.yaml", manifest, 0o644, store.Clock()); err != nil {
			t.Fatal(err)
		}
		if duplicate != "manifest.yaml" {
			if err := writeEntry(tw, duplicate, []byte("first"), 0o755, store.Clock()); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeEntry(tw, duplicate, []byte("second"), 0o755, store.Clock()); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadBundle(bytes.NewReader(buf.Bytes())); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "duplicate member") {
			t.Errorf("duplicate %s should be invalid, got %v", duplicate, err)
		}
	}
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// A re-import supersedes the orphan brief item of the earlier import instead
// of hitting the unique name index; an item still installed by the live
// generation is kept and the imported one skipped with a warning.
func TestImportBriefReimportSupersedesOrphanKeepsLive(t *testing.T) {
	src := openStore(t)
	seed(t, src) // installs a generation whose item is "concise"
	doc, err := Export(src.DB, ExportOptions{Agent: "a", Include: []string{"base", "brief"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Records) != 2 {
		t.Fatalf("want base + item, got %+v", doc.Records)
	}

	dst := openStore(t)
	for i := 0; i < 2; i++ {
		res, err := Import(dst, doc, nil, ImportOptions{})
		if err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
		if len(res) != 2 {
			t.Fatalf("import %d: %d results, want 2", i, len(res))
		}
	}
	items := find(t, dst, store.Filter{Agent: "a", Kind: "brief-item", Status: "*"})
	if len(items) != 2 || items[0].Status != "superseded" || items[1].Status != "active" || items[1].Supersedes != items[0].ID {
		t.Fatalf("items after double import: %+v", items)
	}
	if items[1].Name != "concise" || items[1].Lane != "guidance" || !strings.HasPrefix(items[1].ID, "item_") {
		t.Errorf("re-imported item: %+v", items[1])
	}
	if n := len(find(t, dst, store.Filter{Agent: "a", Kind: "agent-base"})); n != 1 {
		t.Errorf("one active base, got %d", n)
	}

	live := openStore(t)
	var liveItem *store.Record
	mustTx(t, live, func(tx *sql.Tx) error {
		_, recs, err := store.InstallGeneration(tx, "a", "", []store.NewItem{{Key: "concise", Body: "Live."}}, nil)
		if err == nil {
			liveItem = recs[0]
		}
		return err
	})
	var warnings []string
	res, err := Import(live, doc, nil, ImportOptions{Warn: func(f string, a ...any) { warnings = append(warnings, sprintf(f, a...)) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !strings.HasPrefix(res[0].New, "base_") {
		t.Errorf("only the base should import: %+v", res)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped item_") || !strings.Contains(warnings[0], liveItem.ID) {
		t.Errorf("warnings: %q", warnings)
	}
	got, err := store.GetRecord(live.DB, liveItem.ID)
	if err != nil || got.Status != "active" || got.Body != "Live." {
		t.Errorf("live item must be untouched: %v %+v", err, got)
	}
	if n := len(find(t, live, store.Filter{Agent: "a", Kind: "brief-item", Status: "*"})); n != 1 {
		t.Errorf("no orphan should be written next to a live item, got %d items", n)
	}
}

// The document carries an already-stored body, so import must not strip a
// trailing newline again and make repeated export/import lossy.
func TestImportKeepsBodyBytes(t *testing.T) {
	src := openStore(t)
	bodies := []string{"x\n", "line1\n\nline3\n", "two\n\n", "no newline", "  padded  \n"}
	for _, b := range bodies {
		insert(t, src, store.NewRecord{Agent: "g", Lane: "guidance", Kind: "note", Body: b})
	}
	doc, err := Export(src.DB, ExportOptions{Agent: "g"})
	if err != nil {
		t.Fatal(err)
	}
	// Through the on-disk form: marshal, parse back, import.
	data, err := marshalYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	rdoc, err := ReadDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	dst := openStore(t)
	res, err := Import(dst, rdoc, nil, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range bodies {
		got, err := store.GetRecord(dst.DB, res[i].New)
		if err != nil {
			t.Fatal(err)
		}
		if got.Body != b {
			t.Errorf("body %q became %q", b, got.Body)
		}
	}
}

func TestImportRejectsBadMetaKeys(t *testing.T) {
	dst := openStore(t)
	before := len(find(t, dst, store.Filter{Status: "*"}))
	for _, k := range []string{"bad key", "bad\u2003key", "k=v", "br[x]", "b]r", "tab\tkey", " ", ""} {
		doc := &Document{Version: 1, Agent: "m", Records: []*store.Record{
			{ID: "rec_1", Lane: "guidance", Kind: "prefer", Body: "pref", Meta: store.Meta{k: {"v"}}},
		}}
		_, err := Import(dst, doc, nil, ImportOptions{})
		if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "rec_1: ") || !strings.Contains(err.Error(), "metadata key") {
			t.Errorf("key %q: want ErrInvalid naming the key, got %v", k, err)
		}
	}
	// The same rule through a parsed document, whose keys arrive verbatim.
	rdoc, err := ReadDocument([]byte("nine_tails_export: 1\nagent: m\nrecords:\n  - lane: guidance\n    kind: prefer\n    body: pref\n    meta:\n      \"bad key\": v1\n      \"k=v\": v2\n      \"br[x]\": v3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dst, rdoc, nil, ImportOptions{}); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), `metadata key "bad key"`) {
		t.Errorf("parsed document: %v", err)
	}
	if n := len(find(t, dst, store.Filter{Status: "*"})); n != before {
		t.Errorf("records changed: %d -> %d", before, n)
	}
	ok := &Document{Version: 1, Agent: "m", Records: []*store.Record{
		{Lane: "guidance", Kind: "prefer", Body: "pref", Meta: store.Meta{"repo-id": {"x"}, "phase.2": {"y"}, "a:b/c": {"z"}}},
	}}
	if _, err := Import(dst, ok, nil, ImportOptions{}); err != nil {
		t.Errorf("valid keys rejected: %v", err)
	}
}

func TestImportRejectsInvalidReservedTuples(t *testing.T) {
	for _, rec := range []*store.Record{
		{Lane: "state", Kind: "agent-base", Name: "base", Body: "status: invalid"},
		{Lane: "definition", Kind: "agent-base", Name: "other", Body: "invalid base"},
		{Lane: "recall", Kind: "brief-item", Name: "item", Body: "invalid item lane"},
	} {
		dst := openStore(t)
		doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{rec}}
		if _, err := Import(dst, doc, nil, ImportOptions{}); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("record %+v: want ErrInvalid, got %v", rec, err)
		}
		if got := find(t, dst, store.Filter{Status: "*"}); len(got) != 0 {
			t.Errorf("invalid tuple wrote records: %+v", got)
		}
	}
}

func TestImportRejectsDuplicateSourceIDs(t *testing.T) {
	dst := openStore(t)
	doc := &Document{Version: 1, Agent: "a", Records: []*store.Record{
		{ID: "rec_7", Lane: "recall", Kind: "memory", Body: "first"},
		{ID: "rec_7", Lane: "recall", Kind: "memory", Body: "second"},
	}}
	if _, err := Import(dst, doc, nil, ImportOptions{}); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "source id rec_7 is duplicated") {
		t.Fatalf("duplicate source ids: %v", err)
	}
	if got := find(t, dst, store.Filter{Status: "*"}); len(got) != 0 {
		t.Fatalf("duplicate source ids wrote records: %+v", got)
	}
}

func TestImportValidatesState(t *testing.T) {
	dst := openStore(t)
	bad := &Document{Version: 1, Agent: "s", Records: []*store.Record{
		{Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."},
		{ID: "state_2", Lane: "state", Kind: "working-state", Name: "working", Body: "a: [unclosed"},
	}}
	_, err := Import(dst, bad, nil, ImportOptions{})
	if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "state_2") || !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("invalid state: %v", err)
	}
	if n := len(find(t, dst, store.Filter{Status: "*"})); n != 0 {
		t.Errorf("nothing should be written, got %d records", n)
	}
	big := &Document{Version: 1, Agent: "s", Records: []*store.Record{
		{Lane: "state", Kind: "working-state", Name: "working", Body: "status: " + strings.Repeat("x", 100)},
	}}
	if _, err := Import(dst, big, nil, ImportOptions{StateMaxBytes: 64}); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "cap is 64") {
		t.Errorf("oversize state: %v", err)
	}
	if n := len(find(t, dst, store.Filter{Status: "*"})); n != 0 {
		t.Errorf("nothing should be written, got %d records", n)
	}
	if _, err := Import(dst, big, nil, ImportOptions{}); err != nil {
		t.Errorf("StateMaxBytes 0 means no cap: %v", err)
	}
	good := &Document{Version: 1, Agent: "s", Records: []*store.Record{
		{Lane: "state", Kind: "working-state", Name: "working", Body: "status: waiting\nnext: recheck\n"},
	}}
	if _, err := Import(dst, good, nil, ImportOptions{StateMaxBytes: 8192}); err != nil {
		t.Errorf("valid state: %v", err)
	}
}

func TestExportIncludeMustNameASection(t *testing.T) {
	s := openStore(t)
	seed(t, s)
	for _, inc := range [][]string{{""}, {"", ""}, {" "}, {" ", ""}} {
		if _, err := Export(s.DB, ExportOptions{Agent: "a", Include: inc}); !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "names no section") {
			t.Errorf("Include %q: want ErrInvalid, got %v", inc, err)
		}
	}
	// nil or empty still means every section; blanks next to a name are ignored.
	if doc, err := Export(s.DB, ExportOptions{Agent: "a", Include: []string{}}); err != nil || len(doc.Records) != 7 {
		t.Errorf("empty include: %v %d", err, len(doc.Records))
	}
	if doc, err := Export(s.DB, ExportOptions{Agent: "a", Include: []string{"", " base "}}); err != nil || len(doc.Records) != 1 {
		t.Errorf("include with blanks: %v %d", err, len(doc.Records))
	}
}

func TestReadDocumentVersionScalar(t *testing.T) {
	for _, v := range []string{`1`, `"1"`, `'1'`, `1.0`, `" 1 "`} {
		doc, err := ReadDocument([]byte("nine_tails_export: " + v + "\nagent: v\nrecords: []\n"))
		if err != nil || doc.Version != 1 {
			t.Errorf("nine_tails_export: %s: %v %+v", v, err, doc)
		}
	}
	for _, c := range []struct{ v, want string }{
		{`"2"`, `got the string "2"`}, {`2`, `got 2`}, {`true`, `got true`}, {`"one"`, `got the string "one"`}, {`[1]`, `got a list`}, {`null`, `got null`},
	} {
		_, err := ReadDocument([]byte("nine_tails_export: " + c.v + "\nagent: v\nrecords: []\n"))
		if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "must be the integer 1") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("nine_tails_export: %s: want %q, got %v", c.v, c.want, err)
		}
	}
}
