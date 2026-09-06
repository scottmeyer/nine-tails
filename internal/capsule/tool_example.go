package capsule

import (
	"encoding/json"
	"strings"

	"github.com/scottmeyer/nine-tails/internal/tool"
)

// toolInputExample makes a syntactically usable JSON calling template. Include
// required inputs and argv placeholders; optional inputs remain discoverable
// in the adjacent input list and the complete immutable definition.
func toolInputExample(def *tool.Definition) string {
	in := map[string]any{}
	for name, input := range def.Input {
		if !input.Required {
			continue
		}
		switch input.Type {
		case "integer", "number":
			in[name] = 0
		case "boolean":
			in[name] = false
		case "array":
			in[name] = []any{}
		case "object":
			in[name] = map[string]any{}
		default:
			in[name] = "VALUE"
		}
	}
	for _, arg := range def.Exec.Argv {
		if name := tool.Placeholder(arg); name != "" {
			if _, exists := in[name]; !exists {
				in[name] = "VALUE"
			}
		}
	}
	b, _ := json.Marshal(in)
	// JSON keys can contain apostrophes. Use actual POSIX shell quoting,
	// not JSON quoting, around the complete input argument.
	return "'" + strings.ReplaceAll(string(b), "'", "'\"'\"'") + "'"
}
