package harness

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	EnvRunFile          = "NINE_TAILS_RUN_FILE"
	EnvRunToken         = "NINE_TAILS_RUN_TOKEN"
	EnvHome             = "NINE_TAILS_HOME"
	runVersion          = 1
	maxRunStateBytes    = 1 << 20
	maxRunEnvelopeBytes = 192 << 10
	maxRunMetadataBytes = 128 << 10
)

// Run owns one explicit harness process and its ephemeral capability. Closing
// it revokes the capability even if the harness never sends SessionEnd.
type Run struct {
	path  string
	lock  string
	token string
	home  string
}

type runState struct {
	Version    int      `json:"version"`
	Capability string   `json:"capability"`
	Harness    Name     `json:"harness"`
	Agent      string   `json:"agent"`
	Metadata   Metadata `json:"metadata,omitempty"`
	Home       string   `json:"home"`
	OwnerPID   int      `json:"owner_pid"`
	CreatedAt  string   `json:"created_at"`
	ExpiresAt  string   `json:"expires_at"`
	SessionID  string   `json:"session_id,omitempty"`
	NextSource string   `json:"next_source,omitempty"`
	Revoked    bool     `json:"revoked,omitempty"`
	Loaded     bool     `json:"episode_loaded,omitempty"`
	LoadClaim  string   `json:"load_claim,omitempty"`
	ClaimedAt  string   `json:"claimed_at,omitempty"`
	ContextID  string   `json:"context_id,omitempty"`
	Capsule    string   `json:"capsule,omitempty"`
}

// Metadata is ambient activation scope carried by the harness-neutral run
// contract. CLI parsing and validation remain outside this adapter package.
type Metadata map[string][]string

func cloneMetadata(metadata Metadata) Metadata {
	out := make(Metadata, len(metadata))
	for key, values := range metadata {
		out[key] = append([]string(nil), values...)
	}
	return out
}

// ValidateMetadata bounds the JSON-encoded ambient metadata kept in a run
// marker. The remaining envelope budget leaves room for harness session state
// and a worst-case JSON-escaped 40,000-token Codex capsule while keeping the
// complete marker below maxRunStateBytes.
func ValidateMetadata(metadata Metadata) error {
	b, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode activation metadata: %w", err)
	}
	if len(b) > maxRunMetadataBytes {
		return fmt.Errorf("activation metadata exceeds %d encoded bytes", maxRunMetadataBytes)
	}
	return nil
}

// BeginRun creates an unbound, owner-liveness-checked capability. A real
// SessionStart must claim it before prompt events are admitted.
func BeginRun(home, agent string, name Name, metadata Metadata) (*Run, error) {
	if _, err := For(name); err != nil {
		return nil, err
	}
	if err := ValidateMetadata(metadata); err != nil {
		return nil, err
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("resolve nine-tails home: %w", err)
	}
	runDir := filepath.Join(home, "runtime")
	info, err := os.Lstat(runDir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(runDir, 0o700); err != nil && !os.IsExist(err) {
			return nil, fmt.Errorf("create runtime directory: %w", err)
		}
		info, err = os.Lstat(runDir)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect runtime directory: %w", err)
	}
	if !privateRuntimeDirectory(info) {
		return nil, fmt.Errorf("runtime path %s must be a private, non-symlink directory", runDir)
	}
	token, err := randomString(32)
	if err != nil {
		return nil, fmt.Errorf("create run capability: %w", err)
	}
	id, err := randomString(18)
	if err != nil {
		return nil, fmt.Errorf("create run id: %w", err)
	}
	path := filepath.Join(runDir, "run-"+id+".json")
	r := &Run{path: path, lock: path + ".lock", token: token, home: home}
	now := time.Now().UTC()
	state := runState{Version: runVersion, Capability: token, Harness: name, Agent: agent, Metadata: cloneMetadata(metadata), Home: home,
		OwnerPID: os.Getpid(), CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano)}
	if err := createState(path, state); err != nil {
		return nil, err
	}
	return r, nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Environment returns base with any inherited activation removed and this
// run's capability installed. The explicit home makes path validation
// independent of ambient defaults in hook subprocesses.
func (r *Run) Environment(base []string) []string {
	out := make([]string, 0, len(base)+3)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if key == EnvRunFile || key == EnvRunToken || key == EnvHome {
			continue
		}
		out = append(out, entry)
	}
	return append(out, EnvRunFile+"="+r.path, EnvRunToken+"="+r.token, EnvHome+"="+r.home)
}

// Path exposes the capability location for diagnostics and tests. Its token
// is intentionally never exposed through this API.
func (r *Run) Path() string { return r.path }

// Close revokes the run and removes its lock file. It is idempotent.
func (r *Run) Close() error {
	lock, err := lockFile(r.lock)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	if err := os.Remove(r.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Capability is returned only after the cheap environment/file/liveness gate
// succeeds. Probe deliberately turns every stale or malformed marker into an
// inactive result: globally installed hooks must be silent outside a run.
type Capability struct {
	path    string
	lock    string
	token   string
	harness Name
	home    string
}

// Probe performs the inactive fast path using only environment variables and
// the small capability file. It never opens nine-tails config or SQLite.
func Probe(name Name) (*Capability, bool) {
	path, token, home := os.Getenv(EnvRunFile), os.Getenv(EnvRunToken), os.Getenv(EnvHome)
	if path == "" || token == "" || home == "" || !filepath.IsAbs(path) || !filepath.IsAbs(home) {
		return nil, false
	}
	if len(token) != base64.RawURLEncoding.EncodedLen(32) {
		return nil, false
	}
	expectedDir := filepath.Clean(filepath.Join(home, "runtime"))
	if filepath.Clean(filepath.Dir(path)) != expectedDir || !strings.HasPrefix(filepath.Base(path), "run-") || filepath.Ext(path) != ".json" {
		return nil, false
	}
	c := &Capability{path: path, lock: path + ".lock", token: token, harness: name, home: home}
	var valid bool
	if err := c.withState(func(s *runState) error {
		valid = c.valid(s, home)
		return nil
	}); err != nil || !valid {
		return nil, false
	}
	return c, true
}

func (c *Capability) valid(s *runState, home string) bool {
	created, createdErr := time.Parse(time.RFC3339Nano, s.CreatedAt)
	expires, expiresErr := time.Parse(time.RFC3339Nano, s.ExpiresAt)
	now := time.Now().UTC()
	return s.Version == runVersion && !s.Revoked && s.Harness == c.harness && s.Home == home && s.Agent != "" &&
		s.OwnerPID > 0 && processAlive(s.OwnerPID) &&
		createdErr == nil && expiresErr == nil && !created.After(now.Add(5*time.Minute)) && now.Before(expires) &&
		len(s.Capability) == len(c.token) && subtle.ConstantTimeCompare([]byte(s.Capability), []byte(c.token)) == 1
}

// Decision describes the only work an admitted event may request.
type Decision struct {
	Active   bool
	Load     bool
	Agent    string
	Home     string
	Metadata Metadata
	Task     string
	Parent   string
	Capsule  string // cached context for compaction/resume; creates no receipt
	Claim    string // single-winner claim for a fresh episode load
}

// Admit atomically binds the first root SessionStart and rejects all other
// session ids. SessionEnd permits only the documented in-process session
// transitions to rebind; a final end revokes the marker.
func (c *Capability) Admit(event Event) (Decision, error) {
	switch event.Name {
	case "SessionStart":
		if !validStartSource(c.harness, event.Source) {
			return Decision{}, nil
		}
	case "UserPromptSubmit":
	case "SessionEnd":
		if !validEndReason(c.harness, event.Reason) {
			return Decision{}, nil
		}
	default:
		return Decision{}, nil
	}
	var decision Decision
	var claim string
	var err error
	if event.Name == "UserPromptSubmit" {
		claim, err = randomString(18)
		if err != nil {
			return Decision{}, fmt.Errorf("create episode load claim: %w", err)
		}
	}
	err = c.withState(func(s *runState) error {
		if !c.valid(s, c.home) {
			return nil
		}
		decision.Agent, decision.Home, decision.Metadata = s.Agent, s.Home, cloneMetadata(s.Metadata)
		switch event.Name {
		case "SessionStart":
			if s.SessionID == "" {
				if s.NextSource != "" {
					if event.Source != s.NextSource {
						return nil
					}
				} else if !initialSource(c.harness, event.Source) {
					return nil
				}
				s.SessionID = event.SessionID
				s.NextSource = ""
			} else if s.SessionID != event.SessionID {
				// Codex currently reports only SessionEnd(reason=other), so clear
				// and resume replacements must identify themselves here. Ordinary
				// nested startup still cannot displace the bound root.
				if c.harness != Codex || event.Source != "clear" && event.Source != "resume" {
					return nil
				}
				s.SessionID = event.SessionID
			}
			decision.Active = true
			if event.Source == "clear" {
				// /clear begins a new episode. Retain the last receipt only as
				// the parent for the next real-prompt load.
				s.Loaded = false
				s.Capsule = ""
				s.LoadClaim = ""
				s.ClaimedAt = ""
			} else if (event.Source == "compact" || event.Source == "resume") && s.Loaded {
				decision.Capsule = s.Capsule
			}
		case "UserPromptSubmit":
			if s.SessionID == "" || s.SessionID != event.SessionID {
				return nil
			}
			decision.Active = true
			if !s.Loaded {
				now := time.Now().UTC()
				claimedAt, claimedErr := time.Parse(time.RFC3339Nano, s.ClaimedAt)
				if s.LoadClaim != "" && claimedErr == nil && now.Before(claimedAt.Add(2*time.Minute)) {
					return nil
				}
				s.LoadClaim = claim
				s.ClaimedAt = now.Format(time.RFC3339Nano)
				decision.Load = true
				decision.Task, decision.Parent, decision.Claim = event.Prompt, s.ContextID, claim
			}
		case "SessionEnd":
			if s.SessionID == "" || s.SessionID != event.SessionID {
				return nil
			}
			decision.Active = true
			if c.harness == Claude && (event.Reason == "clear" || event.Reason == "resume") {
				s.SessionID = ""
				s.NextSource = event.Reason
			} else {
				s.Revoked = true
			}
		}
		if decision.Active {
			s.ExpiresAt = time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
		}
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func initialSource(name Name, source string) bool {
	return source == "startup" || source == "resume" || name == Claude && source == "fork"
}

// CommitContext advances the ephemeral parent chain after a successful load.
// The prompt itself lives in the normal receipt created by load; the run file
// caches only rendered context needed for same-run lifecycle rehydration.
func (c *Capability) CommitContext(sessionID, claim, contextID, capsule string) (bool, error) {
	committed := false
	err := c.withState(func(s *runState) error {
		if !c.valid(s, c.home) || s.SessionID != sessionID || claim == "" || s.LoadClaim != claim {
			return nil
		}
		s.ContextID = contextID
		s.Capsule = capsule
		s.Loaded = true
		s.LoadClaim = ""
		s.ClaimedAt = ""
		committed = true
		return nil
	})
	return committed, err
}

// AbortLoad releases a failed load claim so the next real prompt can retry.
func (c *Capability) AbortLoad(sessionID, claim string) error {
	return c.withState(func(s *runState) error {
		if !c.valid(s, c.home) || s.SessionID != sessionID || s.LoadClaim != claim {
			return nil
		}
		s.LoadClaim = ""
		s.ClaimedAt = ""
		return nil
	})
}

func (c *Capability) withState(fn func(*runState) error) error {
	lock, err := lockFile(c.lock)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	s, err := readState(c.path)
	if err != nil {
		return err
	}
	before, _ := json.Marshal(s)
	if err := fn(&s); err != nil {
		return err
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		return replaceState(c.path, s)
	}
	return nil
}

func createState(path string, state runState) error {
	data, err := encodeState(state)
	if err != nil {
		return fmt.Errorf("write run state: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create run state: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return fmt.Errorf("write run state: %w", err)
	}
	return nil
}

func readState(path string) (runState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return runState{}, err
	}
	if !privateRunFile(info) || info.Size() > maxRunStateBytes {
		return runState{}, fmt.Errorf("invalid run state file")
	}
	f, err := os.Open(path)
	if err != nil {
		return runState{}, err
	}
	defer f.Close()
	var state runState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return runState{}, err
	}
	return state, nil
}

func replaceState(path string, state runState) error {
	data, err := encodeState(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".run-state-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(tmp, path)
}

// encodeState is the single write-side size invariant for run markers. The
// envelope check prevents metadata or lifecycle identifiers from consuming the
// space reserved for a maximally escaped adapter capsule; the total check means
// no successful write can create a marker that readState will reject.
func encodeState(state runState) ([]byte, error) {
	envelope := state
	envelope.Capsule = ""
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if len(envelopeBytes)+1 > maxRunEnvelopeBytes {
		return nil, fmt.Errorf("run state envelope exceeds %d encoded bytes", maxRunEnvelopeBytes)
	}
	b, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if len(b) > maxRunStateBytes {
		return nil, fmt.Errorf("run state exceeds %d encoded bytes", maxRunStateBytes)
	}
	return b, nil
}
