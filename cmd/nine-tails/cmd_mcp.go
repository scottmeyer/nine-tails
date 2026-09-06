package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/scottmeyer/nine-tails/internal/tool"
	"github.com/spf13/cobra"
)

// The adapter exposes a stable set of operations. Agent selection is an
// explicit receipt on each call, never mutable connection-wide persona state.
// It deliberately reuses the CLI for mutations so there is one learning model.
func newMCPCmd(a *app) *cobra.Command {
	return &cobra.Command{Use: "mcp", Short: "Serve agent context and tools over MCP stdio",
		Long: "Serve a stable set of MCP tools over newline-delimited JSON-RPC on stdin/stdout.\nThe client launches this process; no daemon, network listener, or model runs.\nSupports protocol 2025-11-25. Pass --home to select an explicit store.\nTools use context receipts, so several agents can share one connection.\nTool scripts run in this process's launch directory with their declared timeouts.\nRequests are processed sequentially; cancellation notifications are advisory.\nConfigure the MCP client with command nine-tails and args [mcp].",
		Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { return serveMCP(a) }}
}

type mcpProperty struct {
	Type        string       `json:"type"`
	Description string       `json:"description,omitempty"`
	Enum        []string     `json:"enum,omitempty"`
	MinLength   int          `json:"minLength,omitempty"`
	Items       *mcpProperty `json:"items,omitempty"`
	MinItems    int          `json:"minItems,omitempty"`
}
type mcpRequirement struct {
	Required []string `json:"required"`
}
type mcpSchema struct {
	Type                 string                 `json:"type"`
	Properties           map[string]mcpProperty `json:"properties"`
	Required             []string               `json:"required"`
	AdditionalProperties bool                   `json:"additionalProperties"`
	AnyOf                []mcpRequirement       `json:"anyOf,omitempty"`
}
type mcpTool struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	InputSchema mcpSchema `json:"inputSchema"`
}

func mcpCatalog() []mcpTool {
	str := func(d string) mcpProperty { return mcpProperty{Type: "string", Description: d} }
	obj := func(d string) mcpProperty { return mcpProperty{Type: "object", Description: d} }
	context := str("Receipt ID or local @N reference returned by nt_load; identifies the agent and applicability scope.")
	meta := obj("Applicability metadata: keys map to strings or arrays of strings. On lessons use only true scope, never copy ambient metadata automatically.")
	loadMeta := obj("Ambient metadata: supplied keys replace the parent's values; unspecified keys inherit. Strings or arrays of strings; repeated values collapse. Omit or use {} to inherit unchanged.")
	refs := func(d string) mcpProperty {
		return mcpProperty{Type: "array", Items: &mcpProperty{Type: "string", MinLength: 1}, Description: d}
	}
	def := func(n, d string, required []string, p map[string]mcpProperty) mcpTool {
		return mcpTool{n, d, mcpSchema{Type: "object", Properties: p, Required: required}}
	}
	sources := refs("At least two distinct active guidance records to consolidate, with the same owner and exact scope. Requires body and reason; mutually exclusive with supersedes, meta, clear_meta and forget. Omitted kind is inferred only when source kinds agree.")
	sources.MinItems = 2
	learn := def("nt_learn", "Save or correct learned guidance and experience. Use sources plus body/reason to consolidate guidance, or forget plus reason to retire one exact record. These actions preserve provenance and require the owning agent's receipt. Ordinary corrections with supersedes preserve omitted body and scope. No compile needed.", []string{"context"}, map[string]mcpProperty{
		"context":    context,
		"body":       {Type: "string", MinLength: 1, Description: "Concise reusable lesson, never raw transcripts, secrets or task-only instructions. Required for new records; omit with supersedes to keep the prior body."},
		"kind":       {Type: "string", Enum: []string{"note", "prefer", "avoid", "remember"}, Description: "Ordinary writes default to note with body; without body, omission preserves predecessor lane/kind. Consolidation infers agreeing source kinds, or accepts explicit note/prefer/avoid; remember is only for ordinary recall writes."},
		"supersedes": {Type: "string", MinLength: 1, Description: "Exact prior record id or local reference when replacing its lesson; required if body is omitted."},
		"meta":       meta,
		"clear_meta": {Type: "boolean", Description: "Explicitly remove all scope; true is mutually exclusive with meta."},
		"sources":    sources,
		"reason":     {Type: "string", MinLength: 1, Description: "Concise durable reason required for consolidation or forgetting; do not copy raw transcripts."},
		"forget":     {Type: "string", MinLength: 1, Description: "Exact record ID or local @N to retire without a successor. Requires reason; mutually exclusive with body, kind, sources, supersedes, meta and clear_meta."},
	})
	learn.InputSchema.AnyOf = []mcpRequirement{{Required: []string{"body"}}, {Required: []string{"supersedes"}}, {Required: []string{"forget"}}}
	inspect := def("nt_inspect", "Retrieve an agent, exact record or receipt. During work, use page:true with the current context to browse its scoped recall library; query is a substring filter and after continues a prior page. Read a record's exact reference for full evidence before relying on it.", []string{}, map[string]mcpProperty{
		"target":  {Type: "string", MinLength: 1, Description: "Agent name or exact record/receipt id. Optional on a library page when context supplies the agent; with both, the agent must match."},
		"page":    {Type: "boolean", Description: "Browse a compact recall index page. Requires context or a target agent; after advances the cursor. Does not load the agent or add recalled evidence to a receipt."},
		"context": {Type: "string", MinLength: 1, Description: "Current receipt ID or @N supplying the page's agent and applicability scope; page mode only."},
		"after":   {Type: "string", MinLength: 1, Description: "Exact record cursor ID or @N returned by the previous page; page mode only."},
		"query":   str("Case-insensitive substring filter; omitted or empty lists without a text filter."),
		"lane":    str("Optional guidance/recall filter for ordinary inspection; page mode accepts only recall."),
		"include": str("Optional comma-separated sections for ordinary inspection, e.g. base,brief,journal,tools; not valid in page mode."),
	})
	inspect.InputSchema.AnyOf = []mcpRequirement{{Required: []string{"target"}}, {Required: []string{"page", "context"}}}
	return []mcpTool{
		def("nt_load", "Adopt a named agent's role, current guidance, relevant experience, state and capabilities. Deliver the whole capsule: instructions guide behavior; referenced state, recall, library and signals remain labeled data. Forwarding only instructions drops retrieved context. Keep its receipt for learning and calls.", []string{"agent"}, map[string]mcpProperty{"agent": str("Named agent; use workshop or pilot for discovery."), "task": str("Concise non-sensitive purpose, durably recorded; do not copy the whole prompt."), "context": context, "query": str("Optional recall search/excerpt focus override; empty disables automatic recall."), "recall": refs("Exact recall IDs or local @N handles selected after inspection, in order, with no count cap. Replaces lexical results; [] selects none. Omission uses a soft size budget for lexical matches with recall_more and recall_next when more remain. Same agent, active recall and applicable scope required."), "meta": loadMeta}),
		learn,
		inspect,
		def("nt_tools", "Discover executable capabilities applicable to a loaded agent. Returns descriptions, declared inputs and exact nt_call arguments; does not change the MCP tool list.", []string{"context"}, map[string]mcpProperty{"context": context, "query": str("Optional name or description substring.")}),
		def("nt_call", "Run a discovered agent tool with its current definition and receipt scope. Inspect nt_tools first. Execution uses the server launch directory and the tool's declared timeout.", []string{"context", "tool"}, map[string]mcpProperty{"context": context, "tool": str("Exact tool name from nt_tools."), "input": obj("Tool input object; omitted means {}.")}),
		def("nt_state", "Read/update named state, or subscribe to state with target. State updates preserve omitted scope; links use exactly the supplied scope. Writes require expect; linking never writes the target.", []string{"name"}, map[string]mcpProperty{"name": str("Qualified agent/name, or bare state/link name with context."), "context": context, "body": str("YAML body for state update; omit to read."), "target": str("Qualified owner/state for a one-hop subscription; mutually exclusive with body."), "expect": str("Required for writes: current state/link id, or none."), "meta": meta}),
		def("nt_close", "Close a context receipt. Closure is optional bookkeeping and does not affect learning.", []string{"context"}, map[string]mcpProperty{"context": context}),
	}
}

func serveMCP(a *app) error {
	scan := bufio.NewScanner(a.stdin)
	scan.Buffer(make([]byte, 4096), 8<<20)
	enc := json.NewEncoder(a.stdout)
	initialized, ready := false, false
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		respond := func(id json.RawMessage, result any, code int, msg string) error {
			if len(id) == 0 {
				id = json.RawMessage("null")
			}
			r := map[string]any{"jsonrpc": "2.0", "id": id}
			if code != 0 {
				r["error"] = map[string]any{"code": code, "message": msg}
			} else {
				r["result"] = result
			}
			return enc.Encode(r)
		}
		if !utf8.Valid(line) || !json.Valid(line) {
			if err := respond(nil, nil, -32700, "Invalid JSON"); err != nil {
				return err
			}
			continue
		}
		if err := json.Unmarshal(line, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" || !validRPCID(req.ID) {
			if err := respond(nil, nil, -32600, "Invalid JSON-RPC request"); err != nil {
				return err
			}
			continue
		}
		// Notifications have no response, including unknown future notifications.
		if len(req.ID) == 0 {
			if req.Method == "notifications/initialized" && initialized {
				ready = true
			}
			continue
		}
		var result any
		code := 0
		msg := ""
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string         `json:"protocolVersion"`
				Capabilities    map[string]any `json:"capabilities"`
				ClientInfo      struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"clientInfo"`
			}
			if initialized {
				code, msg = -32600, "Already initialized"
			} else if json.Unmarshal(req.Params, &p) != nil || p.ProtocolVersion == "" || p.Capabilities == nil || p.ClientInfo.Name == "" || p.ClientInfo.Version == "" {
				code, msg = -32602, "initialize requires protocolVersion, capabilities and clientInfo"
			} else {
				initialized = true
				result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "nine-tails", "version": version}, "instructions": "Use nt_load to adopt an agent. Save useful corrections with nt_learn; use exact context receipts on all calls. nt_tools discovers agent capabilities through a stable catalog, without changing the active agent for other calls."}
			}
		case "ping":
			result = map[string]any{}
		default:
			if !ready {
				code, msg = -32002, "Initialize and send notifications/initialized first"
				break
			}
			switch req.Method {
			case "tools/list":
				result = map[string]any{"tools": mcpCatalog()}
			case "tools/call":
				var p struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if json.Unmarshal(req.Params, &p) != nil || p.Name == "" {
					code, msg = -32602, "tools/call requires a name and an arguments object"
					break
				}
				args, err := validateMCPArguments(p.Name, p.Arguments)
				if err != nil {
					code, msg = -32602, err.Error()
					break
				}
				body, failed := a.callMCPTool(p.Name, args)
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": body}}, "isError": failed}
			default:
				code, msg = -32601, "Unknown method"
			}
		}
		if err := respond(req.ID, result, code, msg); err != nil {
			return err
		}
	}
	if err := scan.Err(); err != nil {
		return cli.Invalid("MCP input: %v", err)
	}
	return nil
}

func validRPCID(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return false
	}
	switch v.(type) {
	case string, json.Number:
		return true
	}
	return false
}

func validateMCPArguments(name string, raw json.RawMessage) (map[string]any, error) {
	var spec *mcpTool
	for _, s := range mcpCatalog() {
		if s.Name == name {
			copy := s
			spec = &copy
			break
		}
	}
	if spec == nil {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	args := map[string]any{}
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&args); err != nil || args == nil {
			return nil, fmt.Errorf("arguments must be an object")
		}
	}
	for _, key := range spec.InputSchema.Required {
		if v, ok := args[key]; !ok || v == "" {
			return nil, fmt.Errorf("%s is required", key)
		}
	}
	for k, v := range args {
		p, ok := spec.InputSchema.Properties[k]
		if !ok {
			return nil, fmt.Errorf("unknown argument %q", k)
		}
		switch p.Type {
		case "string":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string", k)
			}
			if utf8.RuneCountInString(s) < p.MinLength {
				return nil, fmt.Errorf("%s must contain at least %d character(s)", k, p.MinLength)
			}
			if len(p.Enum) > 0 {
				found := false
				for _, e := range p.Enum {
					found = found || s == e
				}
				if !found {
					return nil, fmt.Errorf("invalid %s", k)
				}
			}
		case "object":
			if _, ok := v.(map[string]any); !ok {
				return nil, fmt.Errorf("%s must be an object", k)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", k)
			}
		case "array":
			items, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array", k)
			}
			if len(items) < p.MinItems {
				return nil, fmt.Errorf("%s needs at least %d entries", k, p.MinItems)
			}
			for _, item := range items {
				if p.Items != nil && p.Items.Type == "string" {
					s, ok := item.(string)
					if !ok || utf8.RuneCountInString(s) < p.Items.MinLength {
						return nil, fmt.Errorf("%s entries must be nonempty strings", k)
					}
				}
			}
		}
	}
	if len(spec.InputSchema.AnyOf) > 0 {
		matched := false
		var alternatives []string
		for _, alternative := range spec.InputSchema.AnyOf {
			alternatives = append(alternatives, strings.Join(alternative.Required, " and "))
			present := true
			for _, key := range alternative.Required {
				_, found := args[key]
				present = present && found
			}
			matched = matched || present
		}
		if !matched {
			return nil, fmt.Errorf("required: %s", strings.Join(alternatives, " or "))
		}
	}
	if name == "nt_inspect" {
		if args["page"] == true {
			if _, supplied := args["include"]; supplied {
				return nil, fmt.Errorf("page and include are mutually exclusive")
			}
			if lane, supplied := args["lane"]; supplied && lane != "recall" {
				return nil, fmt.Errorf("page mode only supports the recall lane")
			}
		} else {
			if _, supplied := args["target"]; !supplied {
				return nil, fmt.Errorf("target is required for ordinary inspection")
			}
			for _, key := range []string{"context", "after"} {
				if _, supplied := args[key]; supplied {
					return nil, fmt.Errorf("%s requires page mode", key)
				}
			}
		}
	}
	if name == "nt_learn" {
		_, forgetting := args["forget"]
		_, consolidating := args["sources"]
		if forgetting || consolidating {
			reason, _ := args["reason"].(string)
			if strings.TrimSpace(reason) == "" {
				return nil, fmt.Errorf("reason is required for consolidation or forgetting")
			}
			if consolidating && args["kind"] == "remember" {
				return nil, fmt.Errorf("consolidation accepts guidance kinds; remember is for recall writes")
			}
			incompatible := []string{"supersedes", "meta", "clear_meta"}
			action := "sources"
			if forgetting {
				action = "forget"
				incompatible = append(incompatible, "body", "kind", "sources")
			} else if body, _ := args["body"].(string); body == "" {
				return nil, fmt.Errorf("body is required for consolidation")
			}
			for _, key := range incompatible {
				if _, supplied := args[key]; supplied {
					return nil, fmt.Errorf("%s and %s are mutually exclusive", action, key)
				}
			}
		} else {
			if _, supplied := args["reason"]; supplied {
				return nil, fmt.Errorf("reason applies only to consolidation or forgetting")
			}
			if args["clear_meta"] == true {
				if _, supplied := args["meta"]; supplied {
					return nil, fmt.Errorf("clear_meta and meta are mutually exclusive")
				}
			}
		}
	}
	return args, nil
}

func (a *app) callMCPTool(name string, v map[string]any) (string, bool) {
	get := func(k string) string { s, _ := v[k].(string); return s }
	if name == "nt_inspect" && v["page"] == true {
		var args []string
		if target := get("target"); target != "" {
			args = []string{target}
		}
		// This adapter also resolves handles before CLI dispatch. Reuse the
		// page preflight before either resolver can open a store.
		if err := validateLibraryArguments(args, get("context"), get("query"), get("after"), "json"); err != nil {
			return err.Error(), true
		}
	}
	for _, key := range []string{"context", "supersedes", "expect", "target"} {
		if key == "target" && name != "nt_inspect" {
			continue
		}
		value, _ := v[key].(string)
		if strings.HasPrefix(value, "@") {
			resolved, err := a.resolveReference(value)
			if err != nil {
				return err.Error(), true
			}
			v[key] = resolved
		}
	}
	for _, key := range []string{"agent", "target", "tool", "name", "forget"} {
		if strings.HasPrefix(get(key), "-") {
			return key + " must be a name or identifier, not a flag.", true
		}
	}
	if ctx := get("context"); ctx != "" && (!strings.HasPrefix(ctx, "ctx_") || !cli.IsID(ctx)) {
		return "context must identify a context receipt (ctx_ ID or its @N reference).", true
	}
	var argv []string
	body := ""
	switch name {
	case "nt_load":
		argv = []string{"load", get("agent"), "--task", get("task"), "--format", "json"}
		if _, ok := v["query"]; ok {
			argv = append(argv, "--query", get("query"))
		}
		if selected, supplied := v["recall"].([]any); supplied {
			if len(selected) == 0 {
				argv = append(argv, "--query", "")
			}
			for _, ref := range selected {
				argv = append(argv, "--recall", ref.(string))
			}
		}
	case "nt_learn":
		if get("forget") != "" {
			argv = []string{"disable", get("forget"), "--reason", get("reason"), "--format", "json"}
			break
		}
		if selected, supplied := v["sources"].([]any); supplied {
			argv = []string{"consolidate", "--reason", get("reason"), "--stdin", "--format", "json"}
			for _, source := range selected {
				argv = append(argv, "--source", source.(string))
			}
			if get("kind") != "" {
				argv = append(argv, "--kind", get("kind"))
			}
			body = get("body")
			break
		}
		kind := get("kind")
		_, bodySupplied := v["body"]
		if kind == "" && !bodySupplied {
			if err := a.open(); err != nil {
				return err.Error(), true
			}
			prior, err := store.GetRecord(a.st.DB, get("supersedes"))
			if err != nil {
				return err.Error(), true
			}
			// Lane and kind are immutable; the CLI transaction still checks
			// that this exact predecessor is active and owned by the caller.
			argv = []string{"append", "--lane", prior.Lane, "--kind", prior.Kind, "--format", "json"}
		} else {
			if kind == "" {
				kind = "note"
			}
			argv = []string{kind, "--format", "json"}
		}
		if bodySupplied {
			argv = append(argv, "--stdin")
			body = get("body")
		}
		if get("supersedes") != "" {
			argv = append(argv, "--supersedes", get("supersedes"))
		}
		if v["clear_meta"] == true {
			argv = append(argv, "--clear-meta")
		}
	case "nt_inspect":
		argv = []string{"inspect", "--format", "json"}
		if get("target") != "" {
			argv = append(argv, get("target"))
		}
		if v["page"] == true {
			argv = append(argv, "--page")
		}
		for _, k := range []string{"query", "lane", "include", "after"} {
			if _, ok := v[k]; ok {
				argv = append(argv, "--"+k, get(k))
			}
		}
	case "nt_tools":
		return a.mcpAgentTools(get("context"), get("query"))
	case "nt_call":
		input := v["input"]
		if input == nil {
			input = map[string]any{}
		}
		raw, _ := json.Marshal(input)
		argv = []string{"call", get("tool"), "--stdin"}
		body = string(raw)
	case "nt_state":
		if _, link := v["target"]; link {
			if get("target") == "" || get("expect") == "" {
				return "State links require a qualified target and expect (current link id, or none).", true
			}
			if _, write := v["body"]; write {
				return "State link target and state body are mutually exclusive.", true
			}
			argv = []string{"state", "link", get("name"), get("target"), "--expect", get("expect"), "--format", "json"}
		} else if _, write := v["body"]; write {
			if get("expect") == "" {
				return "State updates require expect (current state_ id, or none).", true
			}
			argv = []string{"state", "put", get("name"), "--stdin", "--expect", get("expect"), "--format", "json"}
			body = get("body")
		} else {
			if _, ok := v["meta"]; ok {
				return "Metadata applies only to state updates.", true
			}
			if _, ok := v["expect"]; ok {
				return "expect applies only to state updates.", true
			}
			argv = []string{"state", "get", get("name"), "--format", "json"}
		}
	case "nt_close":
		argv = []string{"close", get("context")}
	}
	if get("context") != "" && name != "nt_close" {
		argv = append(argv, "--context", get("context"))
	}
	if raw, ok := v["meta"].(map[string]any); ok {
		if ((name == "nt_state" && get("target") == "") || name == "nt_learn") && len(raw) == 0 {
			argv = append(argv, "--clear-meta")
		}
		keys := make([]string, 0, len(raw))
		for k := range raw {
			if err := store.ValidateMeta(store.Meta{k: []string{}}); err != nil {
				return err.Error(), true
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch x := raw[k].(type) {
			case string:
				argv = append(argv, "--meta", k+"="+x)
			case []any:
				if len(x) == 0 {
					return "Metadata values must not be empty arrays.", true
				}
				for _, e := range x {
					s, ok := e.(string)
					if !ok {
						return "Metadata values must be strings or arrays of strings.", true
					}
					argv = append(argv, "--meta", k+"="+s)
				}
			default:
				return "Metadata values must be strings or arrays of strings.", true
			}
		}
	}
	var stdout, stderr bytes.Buffer
	home := a.home
	if a.homeFlag != "" {
		home = a.homeFlag
	}
	child := &app{stdout: &stdout, stderr: &stderr, stdin: strings.NewReader(body), home: home, now: a.now}
	status := run(child, argv)
	if status == 0 {
		// Successful commands own stdout: JSON stays parseable, and tools
		// retain exact bytes including empty output. Diagnostics belong to
		// the server's stderr, never after the command's result payload.
		if stderr.Len() > 0 {
			fmt.Fprint(a.stderr, stderr.String())
		}
		return stdout.String(), false
	}
	text := strings.TrimSpace(stdout.String())
	if stderr.Len() > 0 {
		text += "\n" + strings.TrimSpace(stderr.String())
	}
	if text == "" {
		text = "Completed."
	}
	return text, status != 0
}

func (a *app) mcpAgentTools(contextID, query string) (string, bool) {
	if err := a.open(); err != nil {
		return err.Error(), true
	}
	ctx, err := store.GetContext(a.st.DB, contextID)
	if err != nil {
		return err.Error(), true
	}
	own, err := store.ListRecords(a.st.DB, store.Filter{Agent: ctx.Agent, Lane: "definition", Kind: "tool"})
	if err != nil {
		return err.Error(), true
	}
	shared, err := store.ListRecords(a.st.DB, store.Filter{Agent: "shared", Lane: "definition", Kind: "tool"})
	if err != nil {
		return err.Error(), true
	}
	seen := map[string]bool{}
	items := []any{}
	for _, r := range append(own, shared...) {
		if seen[r.Name] {
			continue
		}
		if r.Agent == "shared" && ctx.Agent != "shared" && r.Meta.Has("available-to") && !r.Meta.Contains("available-to", ctx.Agent) {
			continue
		}
		seen[r.Name] = true
		if store.Conflicts(r.Meta, ctx.Meta) {
			continue
		}
		d, err := tool.Parse(r.Body)
		if err != nil {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(r.Name+" "+d.Description), strings.ToLower(query)) {
			continue
		}
		items = append(items, map[string]any{"name": r.Name, "record_id": r.ID, "description": d.Description, "input": d.Input, "output": d.Output, "call": map[string]any{"name": "nt_call", "arguments": map[string]any{"context": contextID, "tool": r.Name, "input": map[string]any{}}}})
	}
	raw, err := json.Marshal(map[string]any{"context": contextID, "agent": ctx.Agent, "tools": items})
	if err != nil {
		return err.Error(), true
	}
	return string(raw), false
}
