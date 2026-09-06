package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHistoricalSearchLeadsToLatestCorrectionWithoutRewritingHistory(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "designer", "Design games.")
	ctx := contextID(t, h.ok("load", "designer").out)
	old := h.ok("prefer", "--context", ctx, "--meta", "repo-id=game", "Check automatic runner choices.").id(t)
	oldRef := referenceFor(t, h, old)
	middle := h.ok("prefer", "--context", ctx, "--supersedes", old, "Check receiver choices.").id(t)
	latest := h.ok("prefer", "--context", ctx, "--supersedes", middle, "Offer one supported next action.").id(t)
	latestRef := referenceFor(t, h, latest)
	found := h.ok("refs", "--agent", "designer", "--query", "runner").out
	if !strings.Contains(found, oldRef) || strings.Contains(found, latestRef) {
		t.Fatal(found)
	}
	for _, format := range []string{"json", "yaml"} {
		r := h.ok("inspect", oldRef, "--format", format)
		var view recordView
		var err error
		if format == "json" {
			err = json.Unmarshal([]byte(r.out), &view)
		} else {
			err = yaml.Unmarshal([]byte(r.out), &view)
		}
		if err != nil {
			t.Fatal(err)
		}
		if view.ID != old || view.Body != "Check automatic runner choices." || view.Current == nil || view.Current.ID != latest || view.Current.Ref != latestRef || view.Current.Body != "Offer one supported next action." || view.Current.Status != "active" {
			t.Fatalf("history/current mismatch: %+v", view)
		}
	}
	before := h.ok("inspect", "designer", "--include", "journal", "--all", "--format", "json").out
	failure := h.run("prefer", "--context", ctx, "--supersedes", oldRef, "Stale update must not replace anything.")
	requireExit(t, failure, 7, "nine-tails inspect "+latestRef)
	if after := h.ok("inspect", "designer", "--include", "journal", "--all", "--format", "json").out; before != after {
		t.Fatal("stale ref silently wrote a replacement")
	}
	wire := mcpHello + fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"nt_inspect\",\"arguments\":{\"target\":%q}}}\n", oldRef)
	var mcpView recordView
	if err := json.Unmarshal([]byte(mcpText(t, mcpResponses(t, h.okIn(wire, "mcp").out)[1])), &mcpView); err != nil {
		t.Fatal(err)
	}
	if mcpView.Current == nil || mcpView.Current.Ref != latestRef {
		t.Fatal(mcpView)
	}
	h.ok("disable", latestRef)
	var retired recordView
	json.Unmarshal([]byte(h.ok("inspect", oldRef, "--format", "json").out), &retired)
	if retired.Current == nil || retired.Current.Status != "disabled" {
		t.Fatal("disabled successor claimed active")
	}
	var self recordView
	json.Unmarshal([]byte(h.ok("inspect", latestRef, "--format", "json").out), &self)
	if self.Current != nil {
		t.Fatal("self-referential current view")
	}
}
