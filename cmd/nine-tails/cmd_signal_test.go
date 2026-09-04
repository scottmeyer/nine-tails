package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const dueHeader = "## Due signals (external inbox data)"

// tickRows parses tick output.
func tickRows(t *testing.T, r result) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(r.out), &rows); err != nil {
		t.Fatalf("tick output is not a JSON array: %v\n%s", err, r.out)
	}
	return rows
}

func assertDeliveryContract(t *testing.T, label string, value any) map[string]any {
	t.Helper()
	d, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s delivery is %T: %#v", label, value, value)
	}
	want := map[string]bool{
		"state": true, "available_at": true, "dedupe_key": true,
		"lease_token": true, "leased_until": true, "acknowledged_at": true,
	}
	if len(d) != len(want) {
		t.Errorf("%s delivery keys = %v, want exactly %v", label, d, want)
	}
	for key := range want {
		if _, ok := d[key]; !ok {
			t.Errorf("%s delivery omitted %q: %#v", label, key, d)
		}
	}
	for key := range d {
		if !want[key] {
			t.Errorf("%s delivery has non-contract key %q: %#v", label, key, d)
		}
	}
	return d
}

// TestSignalLifecycle is spec AC17 end to end: a future signal appears after
// its availability time, is deduplicated, is leased by tick --claim, returns
// to pending after lease expiry, and disappears once acknowledged.
func TestSignalLifecycle(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "pr-review", "Base.")

	r := h.ok("signal", "pr-review", "--subject", "Recheck PR", "--body", "CI should be done.", "--at", "+1h",
		"--meta", "pr=1842", "--dedupe-key", "my_repo:pr-1842:recheck")
	sig := r.id(t)
	if !strings.HasPrefix(sig, "sig_") || strings.Count(r.out, "\n") != 1 {
		t.Fatalf("signal should print one id line, got %q", r.out)
	}
	if r.err != "" {
		t.Errorf("no diagnostics expected on a fresh signal: %q", r.err)
	}

	// Not due yet: absent from load and from tick.
	r = h.ok("load", "pr-review")
	if strings.Contains(r.out, dueHeader) || strings.Contains(r.out, sig) {
		t.Errorf("future signal must not render before --at:\n%s", r.out)
	}
	r = h.ok("tick")
	if strings.TrimSpace(r.out) != "[]" {
		t.Errorf("tick before --at should print [], got %q", r.out)
	}

	// Dedupe: same key → same id, stderr line, exit 0, deduplicated=true in json.
	r = h.ok("signal", "pr-review", "--subject", "Recheck PR", "--dedupe-key", "my_repo:pr-1842:recheck")
	if r.id(t) != sig {
		t.Errorf("dedupe should return the existing id %s, got %q", sig, r.out)
	}
	if !strings.Contains(r.err, "nine-tails: deduplicated against "+sig) {
		t.Errorf("dedupe stderr: %q", r.err)
	}
	r = h.ok("signal", "pr-review", "--subject", "Recheck PR", "--dedupe-key", "my_repo:pr-1842:recheck", "--format", "json")
	m := r.json(t)
	if m["id"] != sig || m["deduplicated"] != true {
		t.Errorf("json dedupe envelope: %s", r.out)
	}
	d := assertDeliveryContract(t, "deduplicated signal", m["delivery"])
	if d["state"] != "pending" || d["available_at"] != "2026-09-04T13:00:00Z" || d["dedupe_key"] != "my_repo:pr-1842:recheck" ||
		d["lease_token"] != "" || d["leased_until"] != "" || d["acknowledged_at"] != "" {
		t.Errorf("delivery in envelope: %s", r.out)
	}

	// Advance past --at: rendered in load and pending in tick.
	h.now = h.now.Add(time.Hour)
	r = h.ok("load", "pr-review")
	want := dueHeader + "\n\n- [signal=" + sig + " pr=1842] Recheck PR — CI should be done.\n"
	if !strings.Contains(r.out, want) {
		t.Errorf("due signal should render as %q:\n%s", want, r.out)
	}
	r = h.ok("tick")
	rows := tickRows(t, r)
	if len(rows) != 1 || rows[0]["id"] != sig || rows[0]["state"] != "pending" || rows[0]["subject"] != "Recheck PR" ||
		rows[0]["body"] != "CI should be done." || rows[0]["agent"] != "pr-review" || rows[0]["lease_token"] != "" ||
		rows[0]["available_at"] != "2026-09-04T13:00:00Z" {
		t.Errorf("tick pending row: %s", r.out)
	}
	if meta, _ := rows[0]["meta"].(map[string]any); meta == nil || meta["pr"] == nil {
		t.Errorf("tick row should carry meta: %s", r.out)
	}

	// Read-only tick must not lease: a claim afterwards still gets the signal.
	r = h.ok("tick", "--claim")
	rows = tickRows(t, r)
	if len(rows) != 1 || rows[0]["state"] != "leased" {
		t.Fatalf("tick --claim should lease the signal: %s", r.out)
	}
	token, _ := rows[0]["lease_token"].(string)
	if !strings.HasPrefix(token, "lease_") || rows[0]["leased_until"] != "2026-09-04T13:05:00Z" {
		t.Errorf("lease fields: %s", r.out)
	}

	// While leased: load shows state=leased; tick shows nothing; claim again is empty.
	r = h.ok("load", "pr-review")
	if !strings.Contains(r.out, "- [signal="+sig+" state=leased pr=1842] Recheck PR — CI should be done.\n") {
		t.Errorf("leased signal should render state=leased:\n%s", r.out)
	}
	for _, args := range [][]string{{"tick"}, {"tick", "--claim"}} {
		if r = h.ok(args...); strings.TrimSpace(r.out) != "[]" {
			t.Errorf("%v while leased should print [], got %q", args, r.out)
		}
	}

	// Wrong token → 7.
	r = h.run("signal", "ack", sig, "--lease", "lease_999")
	if r.code != 7 || !strings.HasPrefix(r.err, "nine-tails: ") {
		t.Errorf("wrong token should be exit 7, got %d: %s", r.code, r.err)
	}

	// Lease expiry: pending again with empty lease fields; the old token is dead.
	h.now = h.now.Add(5*time.Minute + time.Second)
	r = h.ok("tick")
	rows = tickRows(t, r)
	if len(rows) != 1 || rows[0]["state"] != "pending" || rows[0]["lease_token"] != "" || rows[0]["leased_until"] != "" {
		t.Errorf("expired lease should show as pending with empty lease fields: %s", r.out)
	}
	r = h.ok("load", "pr-review")
	if strings.Contains(r.out, "state=leased") {
		t.Errorf("expired lease must not render state=leased:\n%s", r.out)
	}
	assertPendingInspect := func(label string, delivery any) {
		t.Helper()
		d := assertDeliveryContract(t, label, delivery)
		if d["state"] != "pending" || d["lease_token"] != "" || d["leased_until"] != "" {
			t.Errorf("%s must expose a clean pending delivery: %#v", label, delivery)
		}
	}
	byID := h.ok("inspect", sig).json(t)
	assertPendingInspect("inspect id", byID["delivery"])
	flat := h.ok("inspect", "pr-review", "--lane", "signal").json(t)
	assertPendingInspect("flat inspect", flat["records"].([]any)[0].(map[string]any)["delivery"])

	// A dedupe response is another delivery view. It must not expose the stale
	// token and stored leased state after that lease has expired.
	r = h.ok("signal", "pr-review", "--subject", "Duplicate", "--dedupe-key", "my_repo:pr-1842:recheck", "--format", "json")
	m = r.json(t)
	d = assertDeliveryContract(t, "expired dedupe", m["delivery"])
	if m["id"] != sig || m["deduplicated"] != true || d["state"] != "pending" || d["lease_token"] != "" || d["leased_until"] != "" {
		t.Errorf("expired dedupe must expose effective pending state: %s", r.out)
	}
	r = h.run("signal", "ack", sig, "--lease", token)
	if r.code != 7 {
		t.Errorf("ack with an expired token should be exit 7, got %d: %s", r.code, r.err)
	}

	// Reclaim and acknowledge.
	r = h.ok("tick", "--claim", "--lease", "30s", "--agent", "pr-review")
	rows = tickRows(t, r)
	if len(rows) != 1 || rows[0]["state"] != "leased" || rows[0]["leased_until"] != "2026-09-04T13:05:31Z" {
		t.Fatalf("reclaim: %s", r.out)
	}
	token2, _ := rows[0]["lease_token"].(string)
	if token2 == token {
		t.Errorf("reclaim should mint a new token, got %s twice", token)
	}
	r = h.ok("signal", "ack", sig, "--lease", token2)
	if strings.TrimSpace(r.out) != sig {
		t.Errorf("ack should print the id, got %q", r.out)
	}
	r = h.ok("load", "pr-review")
	if strings.Contains(r.out, dueHeader) {
		t.Errorf("acknowledged signal must leave the capsule:\n%s", r.out)
	}
	for _, args := range [][]string{{"tick"}, {"tick", "--claim"}} {
		if r = h.ok(args...); strings.TrimSpace(r.out) != "[]" {
			t.Errorf("%v after ack should print [], got %q", args, r.out)
		}
	}
	r = h.run("signal", "ack", sig, "--lease", token2)
	if r.code != 7 {
		t.Errorf("acking twice should be exit 7, got %d", r.code)
	}
	r = h.ok("inspect", sig)
	if !strings.Contains(r.out, `"state": "acknowledged"`) {
		t.Errorf("inspect should show the acknowledged delivery: %s", r.out)
	}

	// The dedupe key is free again once the signal is terminal.
	r = h.ok("signal", "pr-review", "--subject", "Recheck PR", "--dedupe-key", "my_repo:pr-1842:recheck")
	if r.id(t) == sig || r.err != "" {
		t.Errorf("acknowledged signals do not dedupe: out=%q err=%q", r.out, r.err)
	}
}

func TestSignalAckStructuredFormatsAreBareRecordEnvelopes(t *testing.T) {
	wantKeys := map[string]bool{
		"id": true, "agent": true, "lane": true, "kind": true, "name": true,
		"body": true, "created_at": true, "origin_context": true, "status": true,
		"supersedes": true, "meta": true,
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			h := newHarness(t)
			sig := h.ok("signal", "a", "--subject", "Ping", "--body", "payload").id(t)
			rows := tickRows(t, h.ok("tick", "--claim", "--agent", "a"))
			if len(rows) != 1 {
				t.Fatalf("claim rows: %#v", rows)
			}
			token, _ := rows[0]["lease_token"].(string)
			r := h.ok("signal", "ack", sig, "--lease", token, "--format", format)
			var got map[string]any
			if format == "json" {
				got = r.json(t)
			} else if err := yaml.Unmarshal([]byte(r.out), &got); err != nil {
				t.Fatalf("ack output is not YAML: %v\n%s", err, r.out)
			}
			if got["id"] != sig || got["lane"] != "signal" || got["body"] != "payload" {
				t.Errorf("ack envelope: %#v", got)
			}
			if len(got) != len(wantKeys) {
				t.Errorf("ack keys = %#v, want bare record envelope keys %#v", got, wantKeys)
			}
			for key := range wantKeys {
				if _, ok := got[key]; !ok {
					t.Errorf("ack omitted envelope key %q: %#v", key, got)
				}
			}
			if _, ok := got["delivery"]; ok {
				t.Errorf("ack must not add delivery: %#v", got)
			}
			if _, ok := got["deduplicated"]; ok {
				t.Errorf("ack must not add deduplicated: %#v", got)
			}
		})
	}
}

func TestSignalExcerptAndBodies(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	long := strings.Repeat("word ", 100) // 499 runes after trimming, > 300
	r := h.ok("signal", "a", "--subject", "Big", "--body", long)
	big := r.id(t)
	r = h.ok("load", "a")
	excerpt := strings.TrimSpace(long)[:300]
	want := "- [signal=" + big + "] Big — " + excerpt + "… (truncated; inspect with `nine-tails inspect " + big + "`)\n"
	if !strings.Contains(r.out, want) {
		t.Errorf("long body should be excerpted:\nwant %q\nin:\n%s", want, r.out)
	}
	r = h.ok("inspect", big)
	if !strings.Contains(r.out, strings.TrimSpace(long)) {
		t.Errorf("inspect should hold the complete body: %s", r.out)
	}

	// Empty body is allowed: rendered as the bare subject.
	r = h.ok("signal", "a", "--subject", "Ping")
	ping := r.id(t)
	r = h.ok("load", "a")
	if !strings.Contains(r.out, "- [signal="+ping+"] Ping\n") {
		t.Errorf("empty-body signal should render as its subject:\n%s", r.out)
	}

	// --stdin body: multi-line, one trailing newline removed, whitespace collapsed in the excerpt.
	r = h.okIn("{\"run\": 938,\n \"ok\": true}\n", "signal", "a", "--subject", "CI completed", "--stdin", "--format", "json")
	m := r.json(t)
	if m["body"] != "{\"run\": 938,\n \"ok\": true}" || m["deduplicated"] != false || m["lane"] != "signal" || m["kind"] != "signal" {
		t.Errorf("stdin envelope: %s", r.out)
	}
	if meta, _ := m["meta"].(map[string]any); meta == nil || meta["subject"] == nil {
		t.Errorf("subject should be stored in meta: %s", r.out)
	}
	r = h.ok("load", "a")
	if !strings.Contains(r.out, "CI completed — {\"run\": 938, \"ok\": true}\n") {
		t.Errorf("excerpt should collapse whitespace:\n%s", r.out)
	}

	// Signals rank and filter by metadata like everything else.
	r = h.ok("signal", "a", "--subject", "Other repo", "--meta", "repo-id=other")
	other := r.id(t)
	r = h.ok("load", "a", "--meta", "repo-id=mine")
	if strings.Contains(r.out, other) {
		t.Errorf("a signal with a disjoint repo-id must be excluded:\n%s", r.out)
	}
	if !strings.Contains(r.out, ping) {
		t.Errorf("unscoped signals stay visible:\n%s", r.out)
	}
}

func TestTickOrderingAndAgentFilter(t *testing.T) {
	h := newHarness(t)
	h.ok("signal", "b", "--subject", "later", "--at", "2026-09-04T11:30:00Z")
	h.ok("signal", "a", "--subject", "earlier", "--at", "2026-09-04T11:00:00Z")
	h.ok("signal", "a", "--subject", "future", "--at", "2026-09-05T00:00:00Z")
	r := h.ok("tick")
	rows := tickRows(t, r)
	if len(rows) != 2 || rows[0]["subject"] != "earlier" || rows[1]["subject"] != "later" {
		t.Errorf("tick should list due signals by available_at across agents: %s", r.out)
	}
	r = h.ok("tick", "--agent", "b")
	rows = tickRows(t, r)
	if len(rows) != 1 || rows[0]["agent"] != "b" {
		t.Errorf("tick --agent should filter: %s", r.out)
	}
	r = h.ok("tick", "--claim", "--agent", "a")
	rows = tickRows(t, r)
	if len(rows) != 1 || rows[0]["subject"] != "earlier" || rows[0]["state"] != "leased" {
		t.Errorf("claim with --agent should lease only that agent's due signals: %s", r.out)
	}
	r = h.ok("tick")
	rows = tickRows(t, r)
	if len(rows) != 1 || rows[0]["agent"] != "b" {
		t.Errorf("b's signal stays pending after a's claim: %s", r.out)
	}
	r = h.ok("tick", "--format", "yaml")
	if !strings.Contains(r.out, "subject: later") {
		t.Errorf("tick --format yaml: %s", r.out)
	}
	// An explicit --context is origin only; it need not belong to the addressee.
	h.ok("base", "a", "Base.")
	r = h.ok("load", "a")
	ctx := contextID(t, r.out)
	r = h.ok("signal", "b", "--subject", "from a", "--context", ctx, "--format", "json")
	if r.json(t)["origin_context"] != ctx {
		t.Errorf("origin context should be recorded: %s", r.out)
	}
}

func TestSignalErrors(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		args []string
		code int
	}{
		{[]string{"signal"}, 2},
		{[]string{"signal", "--subject", "s"}, 2},
		{[]string{"signal", "a"}, 2},                  // --subject required
		{[]string{"signal", "a", "--subject", ""}, 2}, // blank subject
		{[]string{"signal", "a", "--subject", "line one\nline two"}, 2},
		{[]string{"signal", "a", "--subject", "line one\rline two"}, 2},
		{[]string{"signal", "a", "b", "--subject", "s"}, 2}, // two positionals
		{[]string{"signal", "Bad_Name", "--subject", "s"}, 2},
		{[]string{"signal", "none", "--subject", "s"}, 2}, // reserved
		{[]string{"signal", "a", "--subject", "s", "--at", "yesterday"}, 2},
		{[]string{"signal", "a", "--subject", "s", "--at", "+5x"}, 2},
		{[]string{"signal", "a", "--subject", "s", "--meta", "novalue"}, 2},
		{[]string{"signal", "a", "--subject", "s", "--meta", "subject=dup"}, 2},
		{[]string{"signal", "a", "--subject", "s", "--body", "b", "--stdin"}, 2},
		{[]string{"signal", "a", "--subject", "s", "--format", "xml"}, 2},
		{[]string{"signal", "a", "--subject", "s", "--context", "ctx_999"}, 3},
		{[]string{"signal", "a", "--subject", "s", "--bogus"}, 2},
		{[]string{"signal", "ack"}, 2},
		{[]string{"signal", "ack", "sig_1"}, 2}, // --lease required
		{[]string{"signal", "ack", "notanid", "--lease", "lease_1"}, 2},
		{[]string{"signal", "ack", "base_1", "--lease", "lease_1"}, 2},
		{[]string{"signal", "ack", "sig_999", "--lease", "lease_1"}, 3},
		{[]string{"tick", "extra"}, 2},
		{[]string{"tick", "--lease", "soon"}, 2},
		{[]string{"tick", "--lease", "0s"}, 2},
		{[]string{"tick", "--agent", "ack"}, 2},
		{[]string{"tick", "--format", "csv"}, 2},
	}
	for _, c := range cases {
		r := h.run(c.args...)
		if r.code != c.code {
			t.Errorf("nine-tails %s: want exit %d, got %d (stdout %q, stderr %q)", strings.Join(c.args, " "), c.code, r.code, r.out, r.err)
		}
		if r.code != 0 && !strings.HasPrefix(r.err, "nine-tails: ") {
			t.Errorf("nine-tails %s: first stderr line must start with 'nine-tails: ', got %q", strings.Join(c.args, " "), r.err)
		}
	}
	// Nothing above may have written a signal.
	if r := h.ok("tick"); strings.TrimSpace(r.out) != "[]" {
		t.Errorf("error paths must not create signals: %s", r.out)
	}

	// Errors carry the JSON envelope with --format json.
	r := h.run("signal", "ack", "sig_999", "--lease", "lease_1", "--format", "json")
	if r.code != 3 || !strings.Contains(r.out, `"code": 3`) {
		t.Errorf("json error envelope: code=%d out=%q", r.code, r.out)
	}

	// ack on a pending (never leased) signal is a conflict, not a not-found.
	sig := h.ok("signal", "a", "--subject", "s").id(t)
	r = h.run("signal", "ack", sig, "--lease", "lease_1")
	if r.code != 7 {
		t.Errorf("ack of a pending signal should be exit 7, got %d: %s", r.code, r.err)
	}
	// The agent now exists (created implicitly), but load still needs a base.
	if r = h.ok("agents"); !strings.Contains(r.out, "a\n") {
		t.Errorf("signal should create the agent implicitly: %q", r.out)
	}
}
