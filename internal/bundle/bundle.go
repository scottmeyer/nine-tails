// Package bundle exports an agent's records as a YAML document or a tar
// bundle that also carries tool artifacts, and imports either back
// (DESIGN.md §13, spec §8.5). Contexts, generations and signal delivery state
// are never exported; every imported record is a fresh active record.
package bundle

import (
	"archive/tar"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/scottmeyer/nine-tails/internal/tool"
)

// Version is the export document format version (nine_tails_export).
const Version = 1

// Sections an export may include, in the order --include lists them.
var Sections = []string{"base", "brief", "journal", "state", "tools", "agents"}

// Document is the export document; inside a bundle it is manifest.yaml.
type Document struct {
	Version          int             `json:"nine_tails_export" yaml:"nine_tails_export"`
	Agent            string          `json:"agent" yaml:"agent"`
	Records          []*store.Record `json:"records" yaml:"records"`
	OmittedArtifacts []string        `json:"omitted_artifacts" yaml:"omitted_artifacts"`
}

// HasBriefItems reports whether the document carries brief-item records,
// which never render after import until a compile installs them.
func (d *Document) HasBriefItems() bool {
	for _, r := range d.Records {
		if r.Kind == "brief-item" {
			return true
		}
	}
	return false
}

// SectionOf maps a record to its export section. "" means never exported
// (signals: their delivery state is not portable). Anything that is not a
// base, brief item, state, tool or related agent is journal, matching inspect.
func SectionOf(r *store.Record) string {
	switch {
	case r.Lane == "signal":
		return ""
	case r.Lane == "definition" && r.Kind == "agent-base":
		return "base"
	case r.Kind == "brief-item":
		return "brief"
	case r.Lane == "state":
		return "state"
	case r.Lane == "definition" && r.Kind == "tool":
		return "tools"
	case r.Lane == "definition" && r.Kind == "related-agent":
		return "agents"
	}
	return "journal"
}

// ExportOptions selects what Export emits.
type ExportOptions struct {
	Agent         string
	Include       []string // nil or empty = every section
	All           bool     // include superseded and disabled records, status kept as-is
	WithArtifacts bool     // the caller bundles artifacts, so none are reported omitted
}

// Export builds the document for an agent: matching records oldest first.
// A nonexistent agent is store.ErrNotFound; an unknown section is
// store.ErrInvalid.
func Export(q store.Querier, o ExportOptions) (*Document, error) {
	if err := store.ValidAgentName(o.Agent); err != nil {
		return nil, err
	}
	want, err := wantSections(o.Include)
	if err != nil {
		return nil, err
	}
	ok, err := store.AgentExists(q, o.Agent)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: no records for agent %q", store.ErrNotFound, o.Agent)
	}
	status := ""
	if o.All {
		status = "*"
	}
	recs, err := store.ListRecords(q, store.Filter{Agent: o.Agent, Status: status})
	if err != nil {
		return nil, err
	}
	doc := &Document{Version: Version, Agent: o.Agent, Records: []*store.Record{}, OmittedArtifacts: []string{}}
	for _, r := range recs {
		sec := SectionOf(r)
		if sec == "" || !want[sec] {
			continue
		}
		doc.Records = append(doc.Records, r)
		if sec == "tools" && !o.WithArtifacts && len(ArtifactPaths(r.Body)) > 0 {
			doc.OmittedArtifacts = append(doc.OmittedArtifacts, r.ID)
		}
	}
	return doc, nil
}

func wantSections(include []string) (map[string]bool, error) {
	want := map[string]bool{}
	if len(include) == 0 {
		for _, s := range Sections {
			want[s] = true
		}
		return want, nil
	}
	known := map[string]bool{}
	for _, s := range Sections {
		known[s] = true
	}
	for _, s := range include {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !known[s] {
			return nil, fmt.Errorf("%w: unknown section %q in --include (%s)", store.ErrInvalid, s, strings.Join(Sections, ","))
		}
		want[s] = true
	}
	if len(want) == 0 {
		// "," or " " would otherwise export an empty document with exit 0,
		// which a scripted caller cannot tell from a real empty agent.
		return nil, fmt.Errorf("%w: --include names no section (%s)", store.ErrInvalid, strings.Join(Sections, ","))
	}
	return want, nil
}

// ArtifactPaths returns every distinct literal artifacts/... argv element in
// document order. It tolerates bodies that fail full tool validation because
// export must still report artifacts referenced by an inspectable corrupt
// record. Invalid YAML or a non-list argv yields no paths.
func ArtifactPaths(body string) []string {
	var e struct {
		Exec struct {
			Argv []string `yaml:"argv"`
		} `yaml:"exec"`
	}
	if yaml.Unmarshal([]byte(body), &e) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, arg := range e.Exec.Argv {
		if tool.IsArtifactRef(arg) && !seen[arg] {
			seen[arg] = true
			out = append(out, arg)
		}
	}
	return out
}

// ArtifactPath is the compatibility form used by existing callers that only
// need the first managed reference. New bundle code uses ArtifactPaths.
func ArtifactPath(body string) string {
	paths := ArtifactPaths(body)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// RewriteArgv0 replaces exec.argv[0] in a tool body. It edits the YAML node
// tree so every other key, comment and quoting style survives (spec §8.5:
// import preserves unknown keys).
func RewriteArgv0(body, newPath string) (string, error) {
	if err := tool.ValidateArtifactRef(newPath); err != nil {
		return "", err
	}
	if _, err := tool.Decode(body); err != nil {
		return "", err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return "", fmt.Errorf("not valid YAML: %v", err)
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return "", errors.New("empty document")
		}
		root = root.Content[0]
	}
	argv := mapChild(mapChild(root, "exec"), "argv")
	if argv == nil || argv.Kind != yaml.SequenceNode || len(argv.Content) == 0 || argv.Content[0].Kind != yaml.ScalarNode {
		return "", errors.New("exec.argv must be a non-empty list of strings")
	}
	argv.Content[0].Value = newPath
	argv.Content[0].Tag = "!!str"
	out, err := marshalYAML(&doc)
	if err != nil {
		return "", err
	}
	return store.NormalizeBody(string(out)), nil
}

// rewriteArtifactPaths replaces every occurrence of each old managed argv
// path with its new path. Editing the YAML node tree preserves unknown keys,
// comments, and the rest of the executable definition.
func rewriteArtifactPaths(body string, replacements map[string]string) (string, error) {
	if len(replacements) == 0 {
		return body, nil
	}
	def, err := tool.Parse(body)
	if err != nil {
		return "", err
	}
	argvValues := append([]string(nil), def.Exec.Argv...)
	seen := map[string]bool{}
	for i, value := range argvValues {
		if replacement, ok := replacements[value]; ok {
			argvValues[i] = replacement
			seen[value] = true
		}
	}
	var missing []string
	for old := range replacements {
		if !seen[old] {
			missing = append(missing, old)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("exec.argv does not contain artifact path %q", missing[0])
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return "", fmt.Errorf("not valid YAML: %v", err)
	}
	root := derefNode(&doc)
	execNode := mapChild(root, "exec")
	if execNode == nil {
		return "", errors.New("exec must be a mapping")
	}
	// An aliased exec mapping is shared with its anchor. Turn this occurrence
	// into a mapping that merges the anchor and owns an explicit argv, so the
	// import cannot rewrite another part of the document as a side effect.
	if execNode.Kind == yaml.AliasNode {
		alias := &yaml.Node{Kind: yaml.AliasNode, Value: execNode.Value, Alias: execNode.Alias}
		*execNode = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!merge", Value: "<<"}, alias,
		}}
	}
	if execNode.Kind != yaml.MappingNode {
		return "", errors.New("exec must be a mapping")
	}
	if existing := mapChild(execNode, "argv"); existing != nil && existing.Kind == yaml.SequenceNode {
		// The ordinary explicit form can be edited in place, preserving quoting,
		// comments, and every unrelated node byte-for-byte as far as yaml.v3
		// permits. Replace an aliased element at this occurrence rather than its
		// shared anchor.
		for _, elem := range existing.Content {
			valueNode := derefNode(elem)
			if valueNode == nil || valueNode.Kind != yaml.ScalarNode {
				continue
			}
			replacement, ok := replacements[valueNode.Value]
			if !ok {
				continue
			}
			if elem.Kind == yaml.AliasNode {
				head, line, foot := elem.HeadComment, elem.LineComment, elem.FootComment
				*elem = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: replacement,
					HeadComment: head, LineComment: line, FootComment: foot}
			} else {
				valueNode.Value = replacement
				valueNode.Tag = "!!str"
			}
		}
	} else {
		// A merged or aliased argv has no independently owned sequence to edit.
		// Add an explicit effective argv that overrides the merge without
		// mutating the shared anchor.
		argv := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, value := range argvValues {
			argv.Content = append(argv.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
		}
		if old := mapChild(execNode, "argv"); old != nil {
			argv.HeadComment, argv.LineComment, argv.FootComment = old.HeadComment, old.LineComment, old.FootComment
			*old = *argv
		} else {
			execNode.Content = append(execNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "argv"}, argv)
		}
	}
	out, err := marshalYAML(&doc)
	if err != nil {
		return "", err
	}
	return store.NormalizeBody(string(out)), nil
}

func derefNode(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func mapChild(n *yaml.Node, key string) *yaml.Node {
	n = derefNode(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- bundles ----

// Artifact is one file carried by a bundle, keyed by its artifacts/ path.
type Artifact struct {
	Data []byte
	Mode fs.FileMode
}

// WriteBundle writes a tar with manifest.yaml (the document) followed by
// artifacts/<id>/<file> for every tool record referencing one. An artifact
// missing on disk is added to doc.OmittedArtifacts before the manifest is
// written, so the bundle never implies it is self-contained.
func WriteBundle(w io.Writer, home string, doc *Document) error {
	type entry struct {
		name, file string
		info       fs.FileInfo
	}
	var entries []entry
	seen := map[string]bool{}
	for _, r := range doc.Records {
		if SectionOf(r) != "tools" {
			continue
		}
		for _, p := range ArtifactPaths(r.Body) {
			if seen[p] {
				continue
			}
			file, info, err := artifactFileForRead(home, p)
			if err != nil || !info.Mode().IsRegular() {
				if !contains(doc.OmittedArtifacts, r.ID) {
					doc.OmittedArtifacts = append(doc.OmittedArtifacts, r.ID)
				}
				continue
			}
			seen[p] = true
			entries = append(entries, entry{p, file, info})
		}
	}
	manifest, err := marshalYAML(doc)
	if err != nil {
		return err
	}
	now := store.Clock().UTC()
	tw := tar.NewWriter(w)
	if err := writeEntry(tw, "manifest.yaml", manifest, 0o644, now); err != nil {
		return err
	}
	for _, e := range entries {
		data, err := os.ReadFile(e.file)
		if err != nil {
			return err
		}
		if err := writeEntry(tw, e.name, data, e.info.Mode().Perm(), e.info.ModTime()); err != nil {
			return err
		}
	}
	return tw.Close()
}

// artifactFileForRead resolves an existing managed path and verifies its
// symlink-resolved target stays inside home before export reads any bytes.
func artifactFileForRead(home, ref string) (string, fs.FileInfo, error) {
	if err := tool.ValidateArtifactRef(ref); err != nil {
		return "", nil, err
	}
	homeReal, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", nil, err
	}
	homeReal, err = filepath.Abs(homeReal)
	if err != nil {
		return "", nil, err
	}
	artifactsReal, err := filepath.EvalSymlinks(filepath.Join(home, "artifacts"))
	if err != nil {
		return "", nil, err
	}
	artifactsReal, err = filepath.Abs(artifactsReal)
	if err != nil {
		return "", nil, err
	}
	if !withinPath(homeReal, artifactsReal) || artifactsReal == homeReal {
		return "", nil, errors.New("artifacts directory resolves outside the store")
	}
	file := filepath.Join(home, filepath.FromSlash(ref))
	fileReal, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", nil, err
	}
	fileReal, err = filepath.Abs(fileReal)
	if err != nil {
		return "", nil, err
	}
	if !withinPath(artifactsReal, fileReal) {
		return "", nil, fmt.Errorf("artifact path %q resolves outside the artifacts directory", ref)
	}
	info, err := os.Stat(fileReal)
	if err != nil {
		return "", nil, err
	}
	return fileReal, info, nil
}

func withinPath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writeEntry(tw *tar.Writer, name string, data []byte, mode fs.FileMode, mtime time.Time) error {
	hdr := &tar.Header{Name: name, Mode: int64(mode), Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: mtime}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// IsTar reports whether an import input is a bundle: a .tar file name, or
// data that starts with a ustar header.
func IsTar(name string, data []byte) bool {
	if strings.HasSuffix(strings.ToLower(name), ".tar") {
		return true
	}
	return len(data) >= 512 && string(data[257:262]) == "ustar"
}

// ReadBundle parses a tar bundle into its document and artifacts.
func ReadBundle(r io.Reader) (*Document, map[string]Artifact, error) {
	tr := tar.NewReader(r)
	var doc *Document
	arts := map[string]Artifact{}
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: not a tar bundle: %v", store.ErrInvalid, err)
		}
		name := path.Clean(hdr.Name)
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		switch {
		case name == "manifest.yaml":
			if seen[name] {
				return nil, nil, fmt.Errorf("%w: bundle contains duplicate member %s", store.ErrInvalid, name)
			}
			seen[name] = true
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: read manifest.yaml: %v", store.ErrInvalid, err)
			}
			doc, err = ReadDocument(data)
			if err != nil {
				return nil, nil, fmt.Errorf("manifest.yaml: %w", err)
			}
		case name == hdr.Name && tool.IsArtifactRef(name) && tool.ValidateArtifactRef(name) == nil:
			if seen[name] {
				return nil, nil, fmt.Errorf("%w: bundle contains duplicate member %s", store.ErrInvalid, name)
			}
			seen[name] = true
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: read %s: %v", store.ErrInvalid, name, err)
			}
			arts[name] = Artifact{Data: data, Mode: fs.FileMode(hdr.Mode)}
		}
	}
	if doc == nil {
		return nil, nil, fmt.Errorf("%w: bundle has no manifest.yaml", store.ErrInvalid)
	}
	return doc, arts, nil
}

// ---- documents ----

// ReadDocument parses an export document (YAML or JSON). Envelope keys are
// accepted in snake_case or kebab-case; metadata keys are taken verbatim.
func ReadDocument(data []byte) (*Document, error) {
	var raw any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: not a YAML document: %v", store.ErrInvalid, err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("%w: expected exactly one YAML or JSON document", store.ErrInvalid)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing YAML is invalid: %v", store.ErrInvalid, err)
	}
	top, ok, err := asMap(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: the document must be a mapping with nine_tails_export, agent and records", store.ErrInvalid)
	}
	v, ok := top["nine_tails_export"]
	if !ok {
		return nil, fmt.Errorf("%w: not a nine-tails export (missing nine_tails_export)", store.ErrInvalid)
	}
	doc := &Document{Records: []*store.Record{}, OmittedArtifacts: []string{}}
	n, ok := versionOf(v)
	if !ok || n != Version {
		return nil, fmt.Errorf("%w: nine_tails_export must be the integer %d (got %s)", store.ErrInvalid, Version, describe(v))
	}
	doc.Version = n
	doc.Agent, err = envelopeString(top, "agent", "agent")
	if err != nil {
		return nil, err
	}
	recordValue, hasRecords := top["records"]
	if !hasRecords {
		return nil, fmt.Errorf("%w: the document is missing records", store.ErrInvalid)
	}
	if recs, ok := recordValue.([]any); ok {
		for i, item := range recs {
			m, ok, mapErr := asMap(item)
			if mapErr != nil {
				return nil, fmt.Errorf("%w: records[%d]: %v", store.ErrInvalid, i, mapErr)
			}
			if !ok {
				return nil, fmt.Errorf("%w: records[%d] is not a mapping", store.ErrInvalid, i)
			}
			r := &store.Record{Meta: store.Meta{}}
			fields := []struct {
				key string
				dst *string
			}{
				{"id", &r.ID}, {"agent", &r.Agent}, {"lane", &r.Lane}, {"kind", &r.Kind},
				{"name", &r.Name}, {"body", &r.Body}, {"origin_context", &r.OriginContext},
				{"status", &r.Status}, {"supersedes", &r.Supersedes},
			}
			for _, field := range fields {
				value, fieldErr := envelopeString(m, field.key, fmt.Sprintf("records[%d].%s", i, field.key))
				if fieldErr != nil {
					return nil, fieldErr
				}
				*field.dst = value
			}
			if value, exists := m["created_at"]; exists && value != nil {
				switch t := value.(type) {
				case string:
					r.CreatedAt = t
				case time.Time:
					r.CreatedAt = store.FormatTime(t)
				default:
					return nil, fmt.Errorf("%w: records[%d].created_at must be a string timestamp or null (got %s)", store.ErrInvalid, i, describe(value))
				}
			}
			if mm, ok := m["meta"]; ok && mm != nil {
				metaMap, ok := asMapRaw(mm)
				if !ok {
					return nil, fmt.Errorf("%w: records[%d].meta must be a mapping", store.ErrInvalid, i)
				}
				for _, k := range sortedKeys(metaMap) {
					switch vs := metaMap[k].(type) {
					case []any:
						for j, x := range vs {
							value, valueErr := metadataScalar(x)
							if valueErr != nil {
								return nil, fmt.Errorf("%w: records[%d].meta.%s[%d] %v", store.ErrInvalid, i, k, j, valueErr)
							}
							r.Meta.Add(k, value)
						}
					case nil:
					default:
						value, valueErr := metadataScalar(vs)
						if valueErr != nil {
							return nil, fmt.Errorf("%w: records[%d].meta.%s %v", store.ErrInvalid, i, k, valueErr)
						}
						r.Meta.Add(k, value)
					}
				}
			}
			doc.Records = append(doc.Records, r)
		}
	} else {
		return nil, fmt.Errorf("%w: records must be a list", store.ErrInvalid)
	}
	if om, ok := top["omitted_artifacts"].([]any); ok {
		for i, x := range om {
			value, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("%w: omitted_artifacts[%d] must be a string (got %s)", store.ErrInvalid, i, describe(x))
			}
			doc.OmittedArtifacts = append(doc.OmittedArtifacts, value)
		}
	} else if top["omitted_artifacts"] != nil {
		return nil, fmt.Errorf("%w: omitted_artifacts must be a list", store.ErrInvalid)
	}
	return doc, nil
}

// versionOf reads nine_tails_export liberally (DESIGN §0: model-authored
// documents): an integer, an integral float, or a string holding an integer
// such as the quoted "1" a model may write.
func versionOf(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		if t == float64(int(t)) {
			return int(t), true
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// describe renders a decoded scalar for an error message so that a string
// "1" is distinguishable from the integer 1.
func describe(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("the string %q", t)
	case bool, int, int64, float64:
		return fmt.Sprintf("%v", t)
	case []any:
		return "a list"
	case map[string]any, map[any]any:
		return "a mapping"
	}
	return fmt.Sprintf("a %T", v)
}

// asMap converts a decoded mapping to map[string]any with envelope keys
// normalized from kebab-case to snake_case.
func asMap(v any) (map[string]any, bool, error) {
	raw, ok := asMapRaw(v)
	if !ok {
		return nil, false, nil
	}
	out := make(map[string]any, len(raw))
	original := make(map[string]string, len(raw))
	for _, k := range sortedKeys(raw) {
		normalized := strings.ReplaceAll(k, "-", "_")
		if first, exists := original[normalized]; exists && first != k {
			return nil, true, fmt.Errorf("envelope keys %q and %q both normalize to %q", first, k, normalized)
		}
		original[normalized] = k
		out[normalized] = raw[k]
	}
	return out, true, nil
}

func envelopeString(m map[string]any, key, label string) (string, error) {
	v, exists := m[key]
	if !exists || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be a string or null (got %s)", store.ErrInvalid, label, describe(v))
	}
	return s, nil
}

// asMapRaw converts a decoded mapping to map[string]any, keys verbatim.
func asMapRaw(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, x := range m {
			out[fmt.Sprint(k)] = x
		}
		return out, true
	}
	return nil, false
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case time.Time:
		return store.FormatTime(t)
	}
	return fmt.Sprint(v)
}

func metadataScalar(v any) (string, error) {
	switch v.(type) {
	case string, bool, int, int64, uint, uint64, float64, time.Time:
		return asString(v), nil
	default:
		return "", fmt.Errorf("must be a scalar value (got %s)", describe(v))
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---- import ----

// Result maps one imported record's old id to its new id.
type Result struct {
	Old string
	New string
}

// ImportOptions tunes Import.
type ImportOptions struct {
	// StateMaxBytes is the state body cap (config state_max_bytes); 0 = no cap.
	StateMaxBytes int
	// Warn receives diagnostics without the "nine-tails:" prefix; nil discards.
	Warn func(format string, args ...any)
}

type pendingFile struct {
	rel string
	art Artifact
}

// artifactReplacements gives every old reference a path below the new tool
// record's directory. The common one-file shape keeps its basename for
// compatibility. When two source paths have the same basename, their path
// below artifacts/ is retained to avoid overwriting either file.
func artifactReplacements(refs []string, newID string) (map[string]string, error) {
	baseCount := map[string]int{}
	for _, ref := range refs {
		if err := tool.ValidateArtifactRef(ref); err != nil {
			return nil, err
		}
		baseCount[path.Base(ref)]++
	}
	out := make(map[string]string, len(refs))
	for _, ref := range refs {
		tail := path.Base(ref)
		if baseCount[tail] > 1 {
			tail = strings.TrimPrefix(ref, "artifacts/")
		}
		newPath := path.Join("artifacts", newID, tail)
		if err := tool.ValidateArtifactRef(newPath); err != nil {
			return nil, err
		}
		out[ref] = newPath
	}
	return out, nil
}

// Import writes every record of doc into s in one transaction. Each record
// gets a new id, status active, no supersedes or origin, and
// imported-from=<old id> in its metadata. Bodies are stored exactly as the
// document carries them: the document's body is already the stored body
// (DESIGN §4), so the §3 trailing-newline strip is not applied again.
//
// Named records supersede a same-named active record of the target agent:
// definitions and state always; a brief item only when the active one is an
// orphan (outside the active generation, e.g. from an earlier import). A
// brief item whose name is installed by the live generation is skipped with a
// warning so the rendered brief is not silently degraded. Guidance and recall
// records are plain inserts.
//
// An import document describes exactly one agent: a record naming another
// agent is rejected. A tool whose argv references artifacts/ paths gets them
// copied under the new id from arts and its body rewritten, then validated
// with tool.Parse; when the document does not carry an artifact the tool is
// skipped with a warning so a re-import never replaces a working definition
// with a broken one. State bodies get the state put validation
// (valid YAML, byte cap). Metadata keys follow store.ParseMeta's rule. Signal
// records are skipped with a warning. Any validation failure is
// store.ErrInvalid and nothing is written.
func Import(s *store.Store, doc *Document, arts map[string]Artifact, o ImportOptions) ([]Result, error) {
	warn := o.Warn
	if warn == nil {
		warn = func(string, ...any) {}
	}
	if doc == nil {
		return nil, fmt.Errorf("%w: import document is null", store.ErrInvalid)
	}
	if doc.Version != Version {
		return nil, fmt.Errorf("%w: nine_tails_export must be the integer %d (got %d)", store.ErrInvalid, Version, doc.Version)
	}
	seenSourceIDs := map[string]int{}
	for i, r := range doc.Records {
		if r == nil {
			return nil, fmt.Errorf("%w: records[%d] is null", store.ErrInvalid, i)
		}
		if r.ID == "" {
			continue
		}
		if first, exists := seenSourceIDs[r.ID]; exists {
			return nil, fmt.Errorf("%w: source id %s is duplicated in records[%d] and records[%d]", store.ErrInvalid, r.ID, first, i)
		}
		seenSourceIDs[r.ID] = i
	}
	var results []Result
	var files []pendingFile
	var createdArtifactRoots []string
	err := s.Tx(func(tx *sql.Tx) error {
		for i, r := range doc.Records {
			label := r.ID
			if label == "" {
				label = fmt.Sprintf("records[%d]", i)
			}
			agent := r.Agent
			if agent == "" {
				agent = doc.Agent
			}
			if agent == "" {
				return fmt.Errorf("%w: %s: agent is required", store.ErrInvalid, label)
			}
			if doc.Agent != "" && agent != doc.Agent {
				return fmt.Errorf("%w: %s: agent %q does not match the document's agent %q (an import document describes one agent; edit the record's agent to move it)", store.ErrInvalid, label, agent, doc.Agent)
			}
			if err := store.ValidAgentName(agent); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			lane, kind := r.Lane, r.Kind
			if lane == "" {
				lane = "recall"
			}
			switch lane {
			case "signal":
				warn("skipped %s: signal delivery state is not portable", label)
				continue
			case "guidance", "recall", "definition", "state":
			default:
				return fmt.Errorf("%w: %s: unknown lane %q (guidance|recall|definition|state)", store.ErrInvalid, label, lane)
			}
			if kind == "" {
				switch lane {
				case "guidance":
					kind = "note"
				case "recall":
					kind = "memory"
				case "state":
					kind = "working-state"
				default:
					return fmt.Errorf("%w: %s: kind is required for the %s lane", store.ErrInvalid, label, lane)
				}
			}
			if kind == "brief-item" && lane != "guidance" {
				return fmt.Errorf("%w: %s: brief-item records must use the guidance lane", store.ErrInvalid, label)
			}
			// An import document carries the already-stored envelope body. Applying
			// NormalizeBody again would make export/import progressively delete
			// legitimate trailing blank lines.
			body := r.Body
			if body == "" {
				return fmt.Errorf("%w: %s: body is empty", store.ErrInvalid, label)
			}
			named := lane == "definition" || lane == "state" || kind == "brief-item"
			name := ""
			if named {
				if r.Name == "" {
					return fmt.Errorf("%w: %s: name is required for %s/%s", store.ErrInvalid, label, lane, kind)
				}
				if err := store.ValidNamedRecord(lane, kind, r.Name); err != nil {
					return fmt.Errorf("%s: %w", label, err)
				}
				name = r.Name
			}
			meta := r.Meta.Clone()
			if meta == nil {
				meta = store.Meta{}
			}
			for _, k := range store.SortedKeys(meta) {
				if err := validMetaKey(k); err != nil {
					return fmt.Errorf("%s: %w", label, err)
				}
			}
			if r.ID != "" {
				meta.Add("imported-from", r.ID)
			}
			nr := store.NewRecord{Agent: agent, Lane: lane, Kind: kind, Name: name, Body: body, Meta: meta}
			if lane == "definition" && kind == "tool" {
				def, err := tool.Parse(body)
				if err != nil {
					return fmt.Errorf("%w: %s: tool body: %v", store.ErrInvalid, label, err)
				}
				var refs []string
				seenRefs := map[string]bool{}
				for _, arg := range def.Exec.Argv {
					if tool.IsArtifactRef(arg) && !seenRefs[arg] {
						seenRefs[arg] = true
						refs = append(refs, arg)
					}
				}
				if len(refs) > 0 {
					var missing []string
					for _, ref := range refs {
						if _, ok := arts[ref]; !ok {
							missing = append(missing, ref)
						}
					}
					if len(missing) > 0 {
						warn("skipped %s: tool %q references %s, which this document does not carry (export it with --bundle); the active definition is kept", label, name, strings.Join(missing, ", "))
						continue
					}
					id, err := store.NewID(store.Prefix(lane, kind))
					if err != nil {
						return err
					}
					replacements, err := artifactReplacements(refs, id)
					if err != nil {
						return fmt.Errorf("%w: %s: tool body: %v", store.ErrInvalid, label, err)
					}
					body, err = rewriteArtifactPaths(body, replacements)
					if err != nil {
						return fmt.Errorf("%w: %s: tool body: %v", store.ErrInvalid, label, err)
					}
					nr.ID = id
					for _, ref := range refs {
						files = append(files, pendingFile{rel: replacements[ref], art: arts[ref]})
					}
				}
				if _, err := tool.Parse(body); err != nil {
					return fmt.Errorf("%w: %s: tool body: %v", store.ErrInvalid, label, err)
				}
				nr.Body = body
			}
			if lane == "state" {
				if err := validateState(body, o.StateMaxBytes); err != nil {
					return fmt.Errorf("%w: %s: %v", store.ErrInvalid, label, err)
				}
			}
			var rec *store.Record
			var err error
			switch {
			case lane == "definition" || lane == "state":
				rec, err = store.PutNamed(tx, nr, "")
			case kind == "brief-item":
				var live *store.Record
				if live, err = liveBriefItem(tx, agent, name); err != nil {
					return fmt.Errorf("%s: %w", label, err)
				}
				if live != nil {
					warn("skipped %s: brief item %q is installed by the active generation as %s (recompile to replace it)", label, name, live.ID)
					continue
				}
				rec, err = store.PutNamed(tx, nr, "")
			default:
				rec, err = store.InsertRecord(tx, nr)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			old := r.ID
			if old == "" {
				old = label
			}
			results = append(results, Result{Old: old, New: rec.ID})
		}
		// Artifacts are written last so a validation failure earlier in the
		// document leaves the filesystem untouched too.
		if err := writePendingArtifacts(s.Home, files, &createdArtifactRoots); err != nil {
			return fmt.Errorf("copy artifact: %w", err)
		}
		return nil
	})
	if err != nil {
		if cleanupErr := cleanupArtifactRoots(createdArtifactRoots); cleanupErr != nil {
			return nil, fmt.Errorf("%w; cleanup imported artifacts: %v", err, cleanupErr)
		}
		return nil, err
	}
	return results, nil
}

// writePendingArtifacts writes into one freshly-created artifacts/<new-id>
// directory per imported tool. It never follows a pre-existing tool directory
// and records every directory it creates so the caller can remove them if the
// surrounding database transaction later fails to commit.
func writePendingArtifacts(home string, files []pendingFile, createdRoots *[]string) error {
	if len(files) == 0 {
		return nil
	}
	homeReal, err := filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	homeReal, err = filepath.Abs(homeReal)
	if err != nil {
		return err
	}
	artifactsDir := filepath.Join(home, "artifacts")
	artifactsReal, err := filepath.EvalSymlinks(artifactsDir)
	if err != nil {
		return err
	}
	artifactsReal, err = filepath.Abs(artifactsReal)
	if err != nil {
		return err
	}
	if !withinPath(homeReal, artifactsReal) || artifactsReal == homeReal {
		return errors.New("artifacts directory resolves outside the store")
	}

	roots := map[string]string{}
	for _, f := range files {
		if err := tool.ValidateArtifactRef(f.rel); err != nil {
			return err
		}
		parts := strings.Split(f.rel, "/")
		if len(parts) < 3 || parts[0] != "artifacts" {
			return fmt.Errorf("artifact destination %q must be under artifacts/<record-id>/", f.rel)
		}
		root, ok := roots[parts[1]]
		if !ok {
			root = filepath.Join(artifactsDir, parts[1])
			if _, err := os.Lstat(root); err == nil {
				return fmt.Errorf("artifact directory %s already exists", root)
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.Mkdir(root, 0o755); err != nil {
				return err
			}
			*createdRoots = append(*createdRoots, root)
			roots[parts[1]] = root
		}
		tail := filepath.FromSlash(strings.Join(parts[2:], "/"))
		dst := filepath.Join(root, tail)
		if !withinPath(root, dst) {
			return fmt.Errorf("artifact destination %q escapes its record directory", f.rel)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("artifact destination %s already exists", dst)
		} else if !os.IsNotExist(err) {
			return err
		}
		mode := f.art.Mode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		file, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(f.art.Data)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func cleanupArtifactRoots(roots []string) error {
	var errs []error
	for i := len(roots) - 1; i >= 0; i-- {
		if err := os.RemoveAll(roots[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// validMetaKey applies store.ParseMeta's key rule (DESIGN §3) to a key that
// arrived through a document mapping rather than a k=v flag.
func validMetaKey(k string) error {
	if k == "" {
		return fmt.Errorf("%w: metadata key must not be empty", store.ErrInvalid)
	}
	if strings.IndexFunc(k, unicode.IsSpace) >= 0 || strings.ContainsAny(k, "[]=") {
		return fmt.Errorf("%w: metadata key %q may not contain whitespace, '=', '[' or ']'", store.ErrInvalid, k)
	}
	return nil
}

// validateState mirrors state put (DESIGN §8): valid YAML of any shape and
// within maxBytes (0 = no cap). A state that load would skip must not import.
func validateState(body string, maxBytes int) error {
	if maxBytes > 0 && len(body) > maxBytes {
		return fmt.Errorf("state is %d bytes; the cap is %d (state_max_bytes in config.yaml)", len(body), maxBytes)
	}
	dec := yaml.NewDecoder(strings.NewReader(body))
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("state is not valid YAML: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("state must contain exactly one YAML or JSON document")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("state is not valid YAML: %v", err)
	}
	return nil
}

// liveBriefItem returns the active brief item named name when it belongs to
// the agent's active generation, else nil. Same-named active items outside
// the generation are orphans (earlier imports) that a re-import supersedes.
func liveBriefItem(q store.Querier, agent, name string) (*store.Record, error) {
	cur, err := store.ActiveNamed(q, agent, "guidance", "brief-item", name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	gen, err := store.ActiveGeneration(q, agent)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items, err := store.GenerationItems(q, gen.ID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ID == cur.ID {
			return cur, nil
		}
	}
	return nil, nil
}
