package main

import (
	"os"
	"strings"
	"testing"
)

func helpText(t *testing.T, h *harness, args ...string) string {
	t.Helper()
	r := h.run(args...)
	if r.code != 0 || r.out == "" || r.err != "" {
		t.Fatalf("nine-tails %s: code=%d stdout=%q stderr=%q", strings.Join(args, " "), r.code, r.out, r.err)
	}
	return r.out
}

func requireHelpText(t *testing.T, got string, wants ...string) {
	t.Helper()
	compact := strings.Join(strings.Fields(got), " ")
	for _, want := range wants {
		if !strings.Contains(compact, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("help is missing %q:\n%s", want, got)
		}
	}
}

func TestRootHelpDocumentsMachineContractAndGroupsCommands(t *testing.T) {
	h := newHarness(t)
	out := helpText(t, h, "--help")

	requireHelpText(t, out,
		"nine-tails itself never prompts;\nhooks run explicitly launches a harness, which may be interactive",
		"Mutation\noutput is command-specific; see that command's help.",
		"Exit codes are 0 success, 2 invalid input, 3 not found, 4 store failure",
		"When --format json is present, errors also go to stdout as an error/code object",
		"call is the exception because its stdout belongs exclusively to the tool",
		"A ctx_... id is a context receipt created by load and passed to --context",
		"Immutable record ids such as base_..., rec_..., state_..., and tool_...",
		"Everyday Commands:",
		"Advanced Commands:",
	)
	if strings.Contains(out, "Additional Commands:") {
		t.Errorf("all root commands should be in a model-facing group:\n%s", out)
	}
	for _, stale := range []string{"Never interactive.", "Mutations print the new id on one line"} {
		if strings.Contains(out, stale) {
			t.Errorf("root help retains overbroad claim %q:\n%s", stale, out)
		}
	}

	everydayAt := strings.Index(out, "Everyday Commands:")
	advancedAt := strings.Index(out, "Advanced Commands:")
	if everydayAt < 0 || advancedAt <= everydayAt {
		t.Fatalf("command groups are missing or out of order:\n%s", out)
	}
	everyday := out[everydayAt:advancedAt]
	advanced := out[advancedAt:]
	for _, command := range []string{"  load ", "  note ", "  remember ", "  state ", "  call "} {
		if !strings.Contains(everyday, command) {
			t.Errorf("everyday group is missing %q:\n%s", command, everyday)
		}
	}
	for _, command := range []string{"  append ", "  base ", "  put ", "  disable ", "  completion ", "  help "} {
		if !strings.Contains(advanced, command) {
			t.Errorf("advanced group is missing %q:\n%s", command, advanced)
		}
	}
}

func TestModelFacingCommandHelp(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name  string
		args  []string
		wants []string
	}{
		{
			name: "load",
			args: []string{"load", "--help"},
			wants: []string{
				"opaque ctx_... id",
				"record ids and state CAS ids are not contexts",
				"The --task value is stored on that receipt",
				"Use a concise, non-sensitive purpose",
				"Examples:\n  nine-tails load pilot --task \"Review this change\"",
			},
		},
		{
			name: "base",
			args: []string{"base", "--help"},
			wants: []string{
				"Use --expect none for safe creation",
				"Omitting --expect is unconditional",
				"current base_... record id",
				"Examples:\n  nine-tails base pr-review --expect none",
			},
		},
		{
			name: "note",
			args: []string{"note", "--help"},
			wants: []string{
				"eligible for compilation into the brief",
				"ctx_... value passed to --context identifies the originating load receipt",
				"Use --supersedes rec_... to replace an active record",
				"--meta becomes the exact new applicability scope",
				"Examples:\n  nine-tails note pr-review",
			},
		},
		{
			name: "avoid",
			args: []string{"avoid", "--help"},
			wants: []string{
				"guidance describing behavior the agent should avoid",
				"--supersedes rec_...",
				"Examples:\n  nine-tails avoid pr-review",
			},
		},
		{
			name: "prefer",
			args: []string{"prefer", "--help"},
			wants: []string{
				"guidance describing behavior the agent should prefer",
				"--supersedes rec_...",
				"Examples:\n  nine-tails prefer pr-review",
			},
		},
		{
			name: "remember",
			args: []string{"remember", "--help"},
			wants: []string{
				"Recall records are not\nloaded into context capsules and are never compiled into the brief",
				"--supersedes rec_...",
				"nine-tails inspect pr-review --lane recall --query \"patch bodies\" --format json",
			},
		},
		{
			name: "disable",
			args: []string{"disable", "--help"},
			wants: []string{
				"Pass an immutable record id such as rec_..., base_..., or tool_...",
				"not a ctx_... context receipt",
				"Use --supersedes on a writing command when replacing a record",
				"represented in the active brief invalidates that compiled cache",
				"JSON and YAML print its record envelope",
				"nine-tails disable rec_01JABC...",
			},
		},
		{
			name: "state get",
			args: []string{"state", "get", "--help"},
			wants: []string{
				"YAML state body verbatim to stdout",
				"state_... record id to stderr as the compare-and-swap hint",
				"--format json writes the full record envelope to stdout",
				"nine-tails state get pr-review/working --format id",
			},
		},
		{
			name: "state put",
			args: []string{"state", "put", "--help"},
			wants: []string{
				"Use --expect none\nto create safely",
				"A context id is not a\nstate record id and cannot be used for --expect",
				"nine-tails state put pr-review/working --expect none",
			},
		},
		{
			name: "call",
			args: []string{"call", "--help"},
			wants: []string{
				"nine-tails inspect <agent> --include tools --format yaml",
				"current working directory (cwd)",
				"ctx_... value is a\ncontext receipt id used with --context, not a tool record id",
				"nine-tails call --context ctx_72 complete-pr-diff --input",
			},
		},
		{
			name: "tool add",
			args: []string{"tool", "add", "--help"},
			wants: []string{
				"owning agent's ctx_... receipt with --context",
				"records provenance and must belong to <agent>",
				"Inspect and review a script before registering it",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireHelpText(t, helpText(t, h, tt.args...), tt.wants...)
		})
	}

	entries, err := os.ReadDir(h.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("help-only invocations touched the store: %v", entries)
	}
}
