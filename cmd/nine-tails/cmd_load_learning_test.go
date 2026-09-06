package main

import (
	"strings"
	"testing"
)

func TestLoadAgentAliasAndRecallQuery(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "coach", "Coach the current task.")
	memory := h.ok("remember", "coach", "Intercept passing by predicting the receiving space.").id(t)
	for _, args := range [][]string{
		{"load", "--agent", "coach", "--task", "intercept passing", "--format", "json"},
		{"load", "coach", "--agent", "coach", "--query", "intercept", "--format", "json"},
	} {
		c := h.ok(args...).json(t)
		if c["agent"] != "coach" || !strings.Contains(h.ok("inspect", c["context_id"].(string)).out, memory) {
			t.Fatalf("alias/query did not surface recall with receipt: %v", c)
		}
	}
	c := h.ok("load", "--agent", "coach", "--task", "intercept passing", "--query", "", "--format", "json").json(t)
	if recalls := c["recall"].([]any); len(recalls) != 0 {
		t.Fatalf("explicit empty query did not disable recall: %v", recalls)
	}
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"load"}, "missing agent"},
		{[]string{"load", "--agent", ""}, "--agent must not be empty"},
		{[]string{"load", "coach", "--agent", "other"}, "conflicting agent selection"},
	} {
		r := h.run(tc.args...)
		if r.code != 2 || !strings.Contains(r.err, tc.message) {
			t.Fatalf("%v: got exit %d, %q", tc.args, r.code, r.err)
		}
	}
}
