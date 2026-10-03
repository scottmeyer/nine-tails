package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// selectorInput is what a configured selector reads on stdin: the load's
// identity and scope, never record bodies. The selector answers with the
// recall records it judges relevant; nine-tails still applies every ownership,
// status and scope rule before delivering them.
type selectorInput struct {
	Agent string     `json:"agent"`
	Task  string     `json:"task"`
	Query string     `json:"query"`
	Meta  store.Meta `json:"meta"`
	Home  string     `json:"home"`
}

type selectorOutput struct {
	Recall []string `json:"recall"`
}

// selectRecall consults the configured selector (DESIGN.md §7.1) and returns
// the usable record ids, or nil with a one-line diagnostic when lexical
// retrieval should proceed instead. It never fails the load: the selector is
// an external relevance judgment, not a gate.
func (a *app) selectRecall(agent, task string, query *string, meta store.Meta) ([]string, string) {
	argv := a.cfg.Selector.Argv
	if len(argv) == 0 {
		return nil, ""
	}
	if query != nil && *query == "" {
		return nil, ""
	}
	q := task
	if query != nil {
		q = *query
	}
	timeout, err := cli.ParseDuration(a.cfg.Selector.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 5 * time.Second
	}
	input, err := json.Marshal(selectorInput{Agent: agent, Task: task, Query: q, Meta: meta, Home: a.home})
	if err != nil {
		return nil, "selector: cannot encode input; using lexical recall"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	configureCompilerProcess(cmd)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "NINE_TAILS_HOME="+a.home, "NINE_TAILS_AGENT="+agent)
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	_ = terminateCompilerProcess(cmd)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Sprintf("selector %s timed out after %s; using lexical recall", argv[0], timeout)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			detail = ": " + firstLine(detail)
		}
		return nil, fmt.Sprintf("selector %s failed%s; using lexical recall", argv[0], detail)
	}
	var out selectorOutput
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &out); err != nil {
		return nil, fmt.Sprintf("selector %s returned no JSON {\"recall\": [...]}; using lexical recall", argv[0])
	}
	var ids []string
	seen := map[string]bool{}
	dropped := 0
	for _, id := range out.Recall {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if !store.IsID(id) {
			dropped++
			continue
		}
		r, err := store.GetRecord(a.st.DB, id)
		if err != nil || r.Agent != agent || r.Lane != "recall" || r.Status != "active" || store.Conflicts(r.Meta, meta) {
			dropped++
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		if dropped > 0 {
			return nil, fmt.Sprintf("selector %s named %d record(s) that are not this agent's active, in-scope recall; using lexical recall", argv[0], dropped)
		}
		return nil, fmt.Sprintf("selector %s selected nothing; using lexical recall", argv[0])
	}
	note := ""
	if dropped > 0 {
		note = fmt.Sprintf(" (%d unusable id(s) ignored)", dropped)
	}
	return ids, fmt.Sprintf("recall selected by %s: %d record(s)%s", argv[0], len(ids), note)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
