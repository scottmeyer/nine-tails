package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/capsule"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// The temporary nine-tails command invokes the real CLI entry point in this
// compiled test binary. Emitted recipes go through a shell, rather than a test
// parser that could accidentally hide quoting, startup, or store-selection bugs.
func TestStoreRecipeCLIHelper(t *testing.T) {
	if os.Getenv("NINE_TAILS_RECIPE_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"nine-tails"}, os.Args[i+1:]...)
			main()
		}
	}
	t.Fatal("missing helper argument separator")
}

type storeRecipeCLI struct {
	t                       *testing.T
	bin, user, launch, next string
	env                     []string
}

func newStoreRecipeCLI(t *testing.T) *storeRecipeCLI {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("generated POSIX-shell recipes require a POSIX shell")
	}
	root := t.TempDir()
	f := &storeRecipeCLI{t: t, bin: filepath.Join(root, "bin", "nine-tails"), user: filepath.Join(root, "user"), launch: filepath.Join(root, "launch"), next: filepath.Join(root, "different-checkout")}
	for _, dir := range []string{filepath.Dir(f.bin), f.user, f.launch, f.next} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\nexec \"$NINE_TAILS_RECIPE_TEST_BINARY\" -test.run='^TestStoreRecipeCLIHelper$' -- \"$@\"\n"
	if err := os.WriteFile(f.bin, []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	f.env = hookHelperEnvironment(os.Environ(), map[string]string{
		"HOME": f.user, "USERPROFILE": f.user, "NINE_TAILS_HOME": "",
		"NINE_TAILS_NOW":                "2026-09-04T12:00:00Z",
		"NINE_TAILS_RECIPE_TEST_HELPER": "1", "NINE_TAILS_RECIPE_TEST_BINARY": executable,
		"RECIPE_DOLLAR": "expanded-variable-must-not-select-a-store",
		"PATH":          filepath.Dir(f.bin) + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	return f
}

func (f *storeRecipeCLI) run(cwd, home, input string, shell bool, args ...string) result {
	f.t.Helper()
	var cmd *exec.Cmd
	if shell {
		cmd = exec.Command("sh", "-c", args[0])
	} else {
		cmd = exec.Command(f.bin, args...)
	}
	cmd.Dir = cwd
	cmd.Env = hookHelperEnvironment(f.env, map[string]string{"NINE_TAILS_HOME": home})
	cmd.Stdin = strings.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		f.t.Fatalf("recipe CLI %q: %v\nstdout: %s\nstderr: %s", args, err, out.String(), errb.String())
	}
	return result{out: out.String(), err: errb.String()}
}

func (f *storeRecipeCLI) replay(home, command string) result {
	f.t.Helper()
	return f.run(f.next, home, "", true, command)
}

func storeRecipeQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func TestStoreRecipesHookRecoveryAndClosureKeepHomeAndLiteralMetadata(t *testing.T) {
	f := newStoreRecipeCLI(t)
	selected := storeRecipeHarness(t, filepath.Join(f.launch, "private's `literal`"))
	decoy := storeRecipeHarness(t, filepath.Join(f.user, ".nine-tails"))
	selected.ok("base", "a", "Selected role.")
	decoy.ok("base", "a", "Other role.")
	other := decoy.ok("load", "a", "--format", "json").id(t)
	value := "space 'quote' $RECIPE_DOLLAR `touch HOOK_EXPANDED`"
	pointer := tooLargePointer("a", store.Meta{"case": {value}}, selected.home, &capsule.TooLargeError{Bytes: 500, Max: 100})
	command := storeRecipeAfter(t, pointer, "Load it in the session: ") + " --format json"
	c := storeRecipeCapsule(t, f.replay(decoy.home, command).out)
	if c.Metadata.First("case") != value {
		t.Fatalf("hook recovery changed metadata: %+v", c.Metadata)
	}
	if _, err := os.Stat(filepath.Join(f.next, "HOOK_EXPANDED")); !os.IsNotExist(err) {
		t.Fatalf("hook metadata executed shell syntax: %v", err)
	}
	nudge := closeNudge(f.bin, selected.home, c.ContextID)
	f.replay(decoy.home, storeRecipeAfter(t, nudge, "Optional bookkeeping: "))
	if closed := selected.ok("inspect", c.ContextID).json(t)["closed_at"]; closed == nil || closed == "" {
		t.Fatal("hook closure did not reach the selected store")
	}
	if closed := decoy.ok("inspect", other).json(t)["closed_at"]; closed != nil && closed != "" {
		t.Fatal("hook closure touched the other store")
	}
}

func storeRecipeAfter(t *testing.T, text, marker string) string {
	t.Helper()
	_, rest, ok := strings.Cut(text, marker)
	if !ok {
		t.Fatalf("missing recipe after %q in %s", marker, text)
	}
	width := len(rest) - len(strings.TrimLeft(rest, "`"))
	if width == 0 {
		t.Fatalf("recipe after %q is not a Markdown code span: %s", marker, rest)
	}
	command, _, ok := strings.Cut(rest[width:], strings.Repeat("`", width))
	if !ok {
		t.Fatalf("unterminated recipe after %q", marker)
	}
	return strings.TrimSpace(command)
}

func storeRecipeCapsule(t *testing.T, body string) capsule.Capsule {
	t.Helper()
	var c capsule.Capsule
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("capsule JSON: %v\n%s", err, body)
	}
	return c
}

func storeRecipeHarness(t *testing.T, home string) *harness {
	t.Helper()
	h := newHarness(t)
	h.home = home
	return h
}

func TestStoreRecipesFollowSelectedHomeAfterWrapperAndDirectoryChange(t *testing.T) {
	for _, selection := range []string{"explicit-flag-over-environment", "wrapper-environment", "physical-default-over-environment"} {
		t.Run(selection, func(t *testing.T) {
			f := newStoreRecipeCLI(t)
			relative := "owner's $RECIPE_DOLLAR `touch RECIPE_BACKTICK` $(touch RECIPE_SUBSTITUTION)"
			selected := storeRecipeHarness(t, filepath.Join(f.launch, relative))
			decoy := storeRecipeHarness(t, filepath.Join(f.user, ".nine-tails"))
			if selection == "physical-default-over-environment" {
				selected, decoy = decoy, selected
				relative, _ = filepath.Rel(f.launch, selected.home)
			}
			base := "Keep this authored command verbatim: `nine-tails inspect @1`.\nDo not rewrite $HOME or `literal` examples."
			memory := "Needle evidence contains nine-tails inspect @1 and literal $HOME."
			selected.ok("base", "chosen", base)
			selectedMemory := selected.ok("remember", "chosen", memory).id(t)
			decoy.ok("base", "other", "The unrelated store.")
			decoy.ok("remember", "other", "Unrelated needle memory.")
			otherContext := decoy.ok("load", "other", "--format", "json").json(t)
			args := []string{"load", "chosen", "--task", "needle", "--format", "json"}
			envHome := relative
			if selection != "wrapper-environment" {
				envHome = decoy.home
				args = append([]string{"--home", relative}, args...)
			}
			c := storeRecipeCapsule(t, f.run(f.launch, envHome, "", false, args...).out)
			if c.ContextRef != otherContext["context_ref"] {
				t.Fatalf("test needs a real colliding receipt reference: %s versus %v", c.ContextRef, otherContext["context_ref"])
			}
			if !strings.Contains(c.Instructions, base) || len(c.Recall) != 1 {
				t.Fatalf("authored body changed or recall missing: %+v", c)
			}
			inspected := f.replay(decoy.home, c.Recall[0].Inspect).json(t)
			if inspected["id"] != selectedMemory || inspected["body"] != memory {
				t.Fatalf("copied inspect switched stores or rewrote body: %+v", inspected)
			}
			command := storeRecipeAfter(t, c.Instructions, "useful experience with ")
			if !strings.HasSuffix(command, `"..."`) {
				t.Fatalf("missing fillable learning body: %s", command)
			}
			lesson := "Selected-store lesson: 'quotes', $RECIPE_DOLLAR, and `literal` remain data."
			command = strings.TrimSuffix(command, `"..."`) + storeRecipeQuote(lesson)
			learned := f.replay(decoy.home, command).id(t)
			record := selected.ok("inspect", learned).json(t)
			if record["agent"] != "chosen" || record["origin_context"] != c.ContextID || record["body"] != lesson {
				t.Fatalf("copied learning recipe lost store, receipt, owner, or body: %+v", record)
			}
			if strings.Contains(decoy.ok("inspect", "other", "--lane", "recall").out, lesson) {
				t.Fatal("colliding reference silently wrote learning into the other store")
			}
			for _, name := range []string{"RECIPE_BACKTICK", "RECIPE_SUBSTITUTION"} {
				if _, err := os.Stat(filepath.Join(f.next, name)); !os.IsNotExist(err) {
					t.Fatalf("store path executed shell substitution %s: %v", name, err)
				}
			}
		})
	}
}

func TestStoreRecipesMCPRecallToolsSignalsAndPagedFollowups(t *testing.T) {
	f := newStoreRecipeCLI(t)
	selected := storeRecipeHarness(t, filepath.Join(f.launch, "selected ' $RECIPE_DOLLAR `literal`"))
	decoy := storeRecipeHarness(t, filepath.Join(f.user, ".nine-tails"))
	query := "needle's $RECIPE_DOLLAR `touch RECIPE_QUERY`"
	for _, item := range []struct {
		h     *harness
		label string
	}{{selected, "selected"}, {decoy, "decoy"}} {
		item.h.ok("base", "a", "Base keeps `nine-tails inspect @1` unchanged.")
		for i := 0; i < 24; i++ {
			item.h.ok("remember", "a", "--meta", "repo-id=recipes", fmt.Sprintf("%s %02d %s %s", item.label, i, query, strings.Repeat("evidence remains available. ", 20)))
		}
		item.h.ok("signal", "a", "--subject", "Check store identity", "--body", item.label+" "+strings.Repeat("external evidence ", 30))
		body := "description: Check selected store\nexec:\n  argv: [printf, '" + item.label + " tool']\n"
		item.h.okIn(body, "put", "a", "--lane", "definition", "--kind", "tool", "--name", "store-check", "--stdin")
		item.h.ok("state", "put", "a/current", "--expect", "none", "source: "+item.label+"\nexample: nine-tails inspect @1\n")
	}
	otherContext := decoy.ok("load", "a", "--format", "json").json(t)
	packet := mcpHello + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":"a","task":"needle","meta":{"repo-id":"recipes"}}}}` + "\n"
	loaded := f.run(f.launch, decoy.home, packet, false, "--home", selected.home, "mcp")
	c := storeRecipeCapsule(t, mcpText(t, mcpResponses(t, loaded.out)[1]))
	if c.ContextRef != otherContext["context_ref"] || c.Library == nil || c.RecallNext == nil || len(c.Signals) != 1 || len(c.Tools) != 1 {
		t.Fatalf("fixture must expose colliding receipt and every discovery path: %+v", c)
	}
	for _, command := range []string{c.Recall[0].Inspect, c.RecallNext.Inspect, c.Signals[0].Inspect} {
		body := f.replay(decoy.home, command).json(t)["body"].(string)
		if !strings.HasPrefix(body, "selected ") {
			t.Fatalf("MCP recipe inspected another store: %s => %s", command, body)
		}
	}
	toolInspect := storeRecipeAfter(t, c.Instructions, "Inspect: ")
	if !strings.Contains(f.replay(decoy.home, toolInspect).json(t)["body"].(string), "selected tool") {
		t.Fatal("tool definition inspector selected the other store")
	}
	toolCall := storeRecipeAfter(t, c.Instructions, "Call (fill input values): ")
	if got := f.replay(decoy.home, toolCall).out; got != "selected tool" {
		t.Fatalf("copied tool call selected the wrong store: %q", got)
	}
	stateGet := strings.Replace(storeRecipeAfter(t, c.Instructions, "State: "), "<owner>/<name>", "a/current", 1)
	if got := f.replay(decoy.home, stateGet).out; strings.TrimSuffix(got, "\n") != c.State[0].Body {
		t.Fatalf("state recipe or authored body changed: %q versus %q", got, c.State[0].Body)
	}
	first := decodeLibrary(t, f.replay(decoy.home, c.Library.Inspect))
	if first.Context != c.ContextID || first.Next == nil {
		t.Fatalf("MCP library hint lost selected receipt: %+v", first)
	}
	inspectArgs, err := json.Marshal(map[string]any{"page": true, "context": c.ContextRef, "query": query})
	if err != nil {
		t.Fatal(err)
	}
	packet = mcpHello + fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_inspect","arguments":%s}}`+"\n", inspectArgs)
	paged := f.run(f.launch, selected.home, packet, false, "mcp")
	page := decodeLibrary(t, result{out: mcpText(t, mcpResponses(t, paged.out)[1])})
	if page.Next == nil || len(page.Entries) == 0 {
		t.Fatalf("fixture did not produce a page continuation: %+v", page)
	}
	next := decodeLibrary(t, f.replay(decoy.home, page.Next.Inspect))
	if next.Context != c.ContextID || next.Query != query || len(next.Entries) == 0 {
		t.Fatalf("copied page continuation lost store, query, or receipt: %+v", next)
	}
	seen := map[string]bool{}
	for _, entry := range page.Entries {
		seen[entry.ID] = true
	}
	for _, entry := range next.Entries {
		if seen[entry.ID] || !strings.HasPrefix(entry.Excerpt, "selected ") {
			t.Fatalf("continuation repeated an entry or switched stores: %+v", entry)
		}
	}
	full := f.replay(decoy.home, next.Entries[0].Inspect).json(t)
	if full["id"] != next.Entries[0].ID || !strings.Contains(full["body"].(string), query) {
		t.Fatal("page entry inspection lost its literal complete body")
	}
	if _, err := os.Stat(filepath.Join(f.next, "RECIPE_QUERY")); !os.IsNotExist(err) {
		t.Fatalf("page query performed shell substitution: %v", err)
	}
}

func TestStoreRecipesDefaultHomeStaysCompactWithoutRewritingAuthoredBodies(t *testing.T) {
	f := newStoreRecipeCLI(t)
	h := storeRecipeHarness(t, filepath.Join(f.user, ".nine-tails"))
	body := "Keep authored `nine-tails inspect @1` and nine-tails --home './example' intact."
	h.ok("base", "a", body)
	h.ok("remember", "a", "needle "+body)
	c := storeRecipeCapsule(t, f.run(f.launch, "", "", false, "load", "a", "--task", "needle", "--format", "json").out)
	if !strings.Contains(c.Instructions, body) {
		t.Fatal("authored commands were rewritten while binding generated recipes")
	}
	command := storeRecipeAfter(t, c.Instructions, "useful experience with ")
	if command != "nine-tails remember --context "+c.ContextRef+` "..."` || c.Library == nil || c.Library.Inspect != "nine-tails inspect --page --context "+c.ContextRef {
		t.Fatalf("ordinary default-home recipes became noisy: %s, %+v", command, c.Library)
	}
	if len(c.Recall) != 1 || c.Recall[0].Inspect != "nine-tails inspect "+c.Recall[0].Ref {
		t.Fatalf("default recall recipe became qualified: %+v", c.Recall)
	}
	if got := f.replay("", c.Recall[0].Inspect).json(t)["body"]; got != "needle "+body {
		t.Fatalf("default recipe did not retain original data across cwd changes: %q", got)
	}
}
