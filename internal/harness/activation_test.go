package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func activateForTest(t *testing.T, name Name) (*Run, *Capability) {
	t.Helper()
	home := t.TempDir()
	run, err := BeginRun(home, "reviewer", name, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Close() })
	for _, entry := range run.Environment(nil) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	capability, ok := Probe(name)
	if !ok {
		t.Fatal("new run did not pass probe")
	}
	return run, capability
}

func TestRunCarriesClonedMetadataAcrossFreshEpisodes(t *testing.T) {
	metadata := Metadata{"repo-id": {"repo-one"}, "phase": {"review", "comment"}}
	run, err := BeginRun(t.TempDir(), "reviewer", Claude, metadata)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Close() })
	metadata["repo-id"][0] = "mutated"
	metadata["phase"] = append(metadata["phase"], "mutated")

	state, err := readState(run.Path())
	if err != nil {
		t.Fatal(err)
	}
	want := Metadata{"repo-id": {"repo-one"}, "phase": {"review", "comment"}}
	if !reflect.DeepEqual(state.Metadata, want) {
		t.Fatalf("stored metadata=%#v, want %#v", state.Metadata, want)
	}
	for _, entry := range run.Environment(nil) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	capability, ok := Probe(Claude)
	if !ok {
		t.Fatal("metadata run did not probe")
	}
	capability.Admit(Event{Name: "SessionStart", SessionID: "root", Source: "startup"})
	first, err := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "root", Prompt: "first"})
	if err != nil || !first.Load || !reflect.DeepEqual(first.Metadata, want) {
		t.Fatalf("first decision=%#v err=%v", first, err)
	}
	first.Metadata["repo-id"][0] = "decision-mutated"
	if committed, err := capability.CommitContext("root", first.Claim, "ctx_1", "capsule"); err != nil || !committed {
		t.Fatalf("commit=%v err=%v", committed, err)
	}
	capability.Admit(Event{Name: "SessionStart", SessionID: "root", Source: "clear"})
	second, err := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "root", Prompt: "second"})
	if err != nil || !second.Load || !reflect.DeepEqual(second.Metadata, want) {
		t.Fatalf("second decision=%#v err=%v", second, err)
	}
}

func TestRunMarkerBoundsMetadataAndEveryReplacement(t *testing.T) {
	home := t.TempDir()
	oversized := Metadata{"scope": {strings.Repeat("\x01", maxRunMetadataBytes/6)}}
	if _, err := BeginRun(home, "reviewer", Codex, oversized); err == nil || !strings.Contains(err.Error(), "activation metadata exceeds") {
		t.Fatalf("oversized metadata error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "runtime")); !os.IsNotExist(err) {
		t.Fatalf("oversized metadata touched runtime: %v", err)
	}

	// This is close to the metadata limit under encoding/json's six-byte
	// control-character escaping. A maximally sized Codex capsule must still
	// fit, remain readable, and preserve activation.
	metadata := Metadata{"scope": {strings.Repeat("\x01", 21_000)}}
	run, err := BeginRun(home, "reviewer", Codex, metadata)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Close() })
	for _, entry := range run.Environment(nil) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	capability, ok := Probe(Codex)
	if !ok {
		t.Fatal("bounded metadata run did not probe")
	}
	if d, err := capability.Admit(Event{Name: "SessionStart", SessionID: "root", Source: "startup"}); err != nil || !d.Active {
		t.Fatalf("start=%#v err=%v", d, err)
	}
	prompt, err := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "root", Prompt: "task"})
	if err != nil || !prompt.Load {
		t.Fatalf("prompt=%#v err=%v", prompt, err)
	}
	if committed, err := capability.CommitContext("root", prompt.Claim, "ctx_1", strings.Repeat("\x01", 140_000)); err != nil || !committed {
		t.Fatalf("maximal cache commit=%v err=%v", committed, err)
	}
	if info, err := os.Stat(run.Path()); err != nil || info.Size() > maxRunStateBytes {
		t.Fatalf("marker info=%v err=%v", info, err)
	}
	if _, ok := Probe(Codex); !ok {
		t.Fatal("maximal readable cache deactivated run")
	}

	// Any later harness field that would consume the reserved envelope fails
	// before replacement; the previously readable marker remains intact.
	err = capability.withState(func(s *runState) error {
		s.SessionID = strings.Repeat("\x01", maxRunEnvelopeBytes)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "run state envelope exceeds") {
		t.Fatalf("oversized replacement error=%v", err)
	}
	if _, ok := Probe(Codex); !ok {
		t.Fatal("rejected replacement corrupted prior marker")
	}
}

func TestCapabilityBindsFirstSessionAndUsesEpisodeCache(t *testing.T) {
	run, capability := activateForTest(t, Claude)
	if info, err := os.Stat(filepath.Dir(run.Path())); err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime mode: info=%v err=%v", info, err)
	}

	d, err := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "s1", Prompt: "too early"})
	if err != nil || d.Active {
		t.Fatalf("prompt bound an unstarted run: %#v %v", d, err)
	}
	d, err = capability.Admit(Event{Name: "SessionStart", SessionID: "s1", Source: "startup"})
	if err != nil || !d.Active || d.Load || d.Capsule != "" {
		t.Fatalf("start decision=%#v err=%v", d, err)
	}
	d, _ = capability.Admit(Event{Name: "SessionStart", SessionID: "nested", Source: "startup"})
	if d.Active {
		t.Fatal("inherited nested session displaced first binding")
	}

	d, err = capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "s1", Prompt: "real task"})
	if err != nil || !d.Active || !d.Load || d.Task != "real task" || d.Parent != "" || d.Claim == "" {
		t.Fatalf("first prompt decision=%#v err=%v", d, err)
	}
	if ok, err := capability.CommitContext("s1", d.Claim, "ctx_1", "CAPSULE ONE"); err != nil || !ok {
		t.Fatal(err)
	}
	d, _ = capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "s1", Prompt: "follow up"})
	if !d.Active || d.Load || d.Capsule != "" {
		t.Fatalf("same-episode followup was not silent: %#v", d)
	}
	d, _ = capability.Admit(Event{Name: "SessionStart", SessionID: "s1", Source: "compact"})
	if !d.Active || d.Load || d.Capsule != "CAPSULE ONE" {
		t.Fatalf("compact did not use cache: %#v", d)
	}

	d, _ = capability.Admit(Event{Name: "SessionStart", SessionID: "s1", Source: "clear"})
	if !d.Active || d.Load || d.Capsule != "" {
		t.Fatalf("clear emitted context: %#v", d)
	}
	d, _ = capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "s1", Prompt: "new episode"})
	if !d.Load || d.Task != "new episode" || d.Parent != "ctx_1" {
		t.Fatalf("new episode=%#v", d)
	}
	if ok, err := capability.CommitContext("s1", d.Claim, "ctx_2", "CAPSULE TWO"); err != nil || !ok {
		t.Fatal(err)
	}

	d, _ = capability.Admit(Event{Name: "SessionEnd", SessionID: "s1", Reason: "resume"})
	if !d.Active {
		t.Fatal("same-run resume transition not admitted")
	}
	d, _ = capability.Admit(Event{Name: "SessionStart", SessionID: "s2", Source: "resume"})
	if !d.Active || d.Capsule != "CAPSULE TWO" {
		t.Fatalf("same-run resume did not rehydrate: %#v", d)
	}
	d, _ = capability.Admit(Event{Name: "SessionEnd", SessionID: "s2", Reason: "other"})
	if !d.Active {
		t.Fatal("final end not admitted")
	}
	if _, ok := Probe(Claude); ok {
		t.Fatal("final SessionEnd did not revoke capability")
	}
}

func TestFirstSessionBindingIsAtomic(t *testing.T) {
	_, capability := activateForTest(t, Codex)
	const n = 16
	var wg sync.WaitGroup
	results := make(chan Decision, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, _ := capability.Admit(Event{Name: "SessionStart", SessionID: fmt.Sprintf("s%d", i), Source: "startup"})
			results <- d
		}(i)
	}
	wg.Wait()
	close(results)
	winners := 0
	for d := range results {
		if d.Active {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("first-session winners=%d", winners)
	}
}

func TestFirstPromptLoadClaimIsAtomicAndAbortable(t *testing.T) {
	_, capability := activateForTest(t, Codex)
	if d, err := capability.Admit(Event{Name: "SessionStart", SessionID: "root", Source: "startup"}); err != nil || !d.Active {
		t.Fatalf("start=%#v err=%v", d, err)
	}
	const n = 16
	var wg sync.WaitGroup
	results := make(chan Decision, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "root", Prompt: fmt.Sprintf("task %d", i)})
			results <- d
		}(i)
	}
	wg.Wait()
	close(results)
	loads := 0
	var winner Decision
	for d := range results {
		if d.Load {
			loads++
			winner = d
		}
	}
	if loads != 1 || winner.Claim == "" {
		t.Fatalf("fresh-load winners=%d, winner=%#v", loads, winner)
	}
	if err := capability.AbortLoad("root", winner.Claim); err != nil {
		t.Fatal(err)
	}
	retry, err := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "root", Prompt: "retry"})
	if err != nil || !retry.Load || retry.Claim == "" || retry.Claim == winner.Claim {
		t.Fatalf("retry=%#v err=%v", retry, err)
	}
}

func TestNestedSessionCannotDisplaceOrEndClaudeRoot(t *testing.T) {
	_, capability := activateForTest(t, Claude)
	if d, _ := capability.Admit(Event{Name: "SessionStart", SessionID: "root", Source: "startup"}); !d.Active {
		t.Fatal("root did not bind")
	}
	for _, event := range []Event{
		{Name: "SessionStart", SessionID: "nested", Source: "startup"},
		{Name: "UserPromptSubmit", SessionID: "nested", Prompt: "nested task"},
		{Name: "SessionEnd", SessionID: "nested", Reason: "other"},
	} {
		if d, _ := capability.Admit(event); d.Active {
			t.Fatalf("nested event admitted: %#v", event)
		}
	}
	rootPrompt, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "root", Prompt: "root task"})
	if !rootPrompt.Load {
		t.Fatalf("nested events disrupted root: %#v", rootPrompt)
	}
	if ok, err := capability.CommitContext("root", rootPrompt.Claim, "ctx_root", "ROOT CAPSULE"); err != nil || !ok {
		t.Fatalf("commit ok=%v err=%v", ok, err)
	}
	if d, _ := capability.Admit(Event{Name: "SessionEnd", SessionID: "root", Reason: "clear"}); !d.Active {
		t.Fatal("clear transition not admitted")
	}
	if d, _ := capability.Admit(Event{Name: "SessionStart", SessionID: "wrong", Source: "resume"}); d.Active {
		t.Fatal("wrong transition source consumed rebind")
	}
	clear, _ := capability.Admit(Event{Name: "SessionStart", SessionID: "cleared", Source: "clear"})
	if !clear.Active || clear.Capsule != "" {
		t.Fatalf("clear rebind=%#v", clear)
	}
	newPrompt, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "cleared", Prompt: "new task"})
	if !newPrompt.Load || newPrompt.Parent != "ctx_root" {
		t.Fatalf("post-clear prompt=%#v", newPrompt)
	}
}

func TestCodexClearRebindsWithoutSessionEnd(t *testing.T) {
	_, capability := activateForTest(t, Codex)
	capability.Admit(Event{Name: "SessionStart", SessionID: "old", Source: "startup"})
	first, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "old", Prompt: "first"})
	if ok, err := capability.CommitContext("old", first.Claim, "ctx_old", "OLD CAPSULE"); err != nil || !ok {
		t.Fatalf("commit ok=%v err=%v", ok, err)
	}
	if d, _ := capability.Admit(Event{Name: "SessionStart", SessionID: "nested", Source: "startup"}); d.Active {
		t.Fatal("nested startup displaced Codex root")
	}
	clear, _ := capability.Admit(Event{Name: "SessionStart", SessionID: "new", Source: "clear"})
	if !clear.Active || clear.Capsule != "" {
		t.Fatalf("Codex clear=%#v", clear)
	}
	if d, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "old", Prompt: "stale"}); d.Active {
		t.Fatal("old session remained bound after clear")
	}
	next, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "new", Prompt: "after clear"})
	if !next.Load || next.Parent != "ctx_old" {
		t.Fatalf("post-clear decision=%#v", next)
	}
}

func TestCodexResumeRebindsAndRehydratesWithoutSessionEnd(t *testing.T) {
	_, capability := activateForTest(t, Codex)
	capability.Admit(Event{Name: "SessionStart", SessionID: "old", Source: "startup"})
	first, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "old", Prompt: "first"})
	if ok, err := capability.CommitContext("old", first.Claim, "ctx_old", "OLD CAPSULE"); err != nil || !ok {
		t.Fatalf("commit ok=%v err=%v", ok, err)
	}
	resume, _ := capability.Admit(Event{Name: "SessionStart", SessionID: "resumed", Source: "resume"})
	if !resume.Active || resume.Capsule != "OLD CAPSULE" || resume.Load {
		t.Fatalf("Codex resume=%#v", resume)
	}
	if d, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "old", Prompt: "stale"}); d.Active {
		t.Fatal("old session remained bound after resume")
	}
	if d, _ := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "resumed", Prompt: "followup"}); !d.Active || d.Load {
		t.Fatalf("resumed cached episode prompt=%#v", d)
	}
}

func TestFreshResumeWaitsForPromptAndClaudeForkIsHarnessSpecific(t *testing.T) {
	_, resumed := activateForTest(t, Claude)
	start, _ := resumed.Admit(Event{Name: "SessionStart", SessionID: "resume", Source: "resume"})
	if !start.Active || start.Capsule != "" || start.Load {
		t.Fatalf("fresh resume=%#v", start)
	}
	prompt, _ := resumed.Admit(Event{Name: "UserPromptSubmit", SessionID: "resume", Prompt: "resumed task"})
	if !prompt.Load {
		t.Fatalf("fresh resume prompt=%#v", prompt)
	}

	claude, _ := For(Claude)
	if _, err := claude.DecodeEvent(strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"fork","source":"fork"}`)); err != nil {
		t.Fatalf("Claude fork rejected: %v", err)
	}
	codex, _ := For(Codex)
	if _, err := codex.DecodeEvent(strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"fork","source":"fork"}`)); err == nil {
		t.Fatal("Codex fork accepted")
	}
	if _, err := codex.DecodeEvent(strings.NewReader(`{"hook_event_name":"SessionEnd","session_id":"s","reason":"clear"}`)); err == nil {
		t.Fatal("Codex SessionEnd(clear) accepted")
	}
	if _, err := claude.DecodeEvent(strings.NewReader(`{"hook_event_name":"SessionEnd","session_id":"s","reason":"bypass_permissions_disabled"}`)); err == nil {
		t.Fatal("removed Claude SessionEnd reason accepted")
	}
}

func TestProbeRejectsForgedExpiredAndWrongHarnessMarkers(t *testing.T) {
	run, _ := activateForTest(t, Claude)
	t.Setenv(EnvRunToken, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if _, ok := Probe(Claude); ok {
		t.Fatal("forged token accepted")
	}
	for _, entry := range run.Environment(nil) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	if _, ok := Probe(Codex); ok {
		t.Fatal("wrong harness accepted")
	}

	capability, ok := Probe(Claude)
	if !ok {
		t.Fatal("valid capability rejected")
	}
	if err := capability.withState(func(s *runState) error {
		s.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Probe(Claude); ok {
		t.Fatal("expired marker accepted")
	}
}

func TestBeginRunRejectsRuntimeSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires privileges on Windows")
	}
	home, target := t.TempDir(), t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, "runtime")); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginRun(home, "reviewer", Claude, nil); err == nil {
		t.Fatal("runtime symlink accepted")
	}
}

func TestConcurrentBeginRunCreatesPrivateRuntimeOnce(t *testing.T) {
	home := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	runs := make(chan *Run, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := BeginRun(home, "reviewer", Claude, nil)
			if err != nil {
				errs <- err
				return
			}
			runs <- run
		}()
	}
	wg.Wait()
	close(runs)
	close(errs)
	for err := range errs {
		t.Errorf("BeginRun: %v", err)
	}
	count := 0
	paths := make(map[string]bool)
	tokens := make(map[string]bool)
	for run := range runs {
		count++
		if paths[run.path] || tokens[run.token] {
			t.Errorf("duplicate run identity: path=%q", run.path)
		}
		paths[run.path], tokens[run.token] = true, true
		for _, entry := range run.Environment(nil) {
			key, value, _ := strings.Cut(entry, "=")
			t.Setenv(key, value)
		}
		if _, ok := Probe(Claude); !ok {
			t.Errorf("concurrent run did not probe: %s", run.path)
		}
		if err := run.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	if count != n {
		t.Fatalf("successful runs=%d, want %d", count, n)
	}
	if info, err := os.Lstat(filepath.Join(home, "runtime")); err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime info=%v err=%v", info, err)
	}
}

func TestDecodeEventRequiresOneObject(t *testing.T) {
	a, _ := For(Codex)
	event, err := a.DecodeEvent(strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"task"}`))
	if err != nil || event.Prompt != "task" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
	if _, err := a.DecodeEvent(strings.NewReader(`{} {}`)); err == nil {
		t.Fatal("multiple JSON values accepted")
	}
}
