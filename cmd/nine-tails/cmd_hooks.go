package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/capsule"
	"github.com/scottmeyer/nine-tails/internal/cli"
	harnessadapter "github.com/scottmeyer/nine-tails/internal/harness"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func newHooksCmd(a *app) *cobra.Command {
	c := commandGroup(&cobra.Command{
		Use:   "hooks",
		Short: "Install and run explicitly activated harness adapters",
		Long: `Install a small lifecycle gate in a supported harness's user settings.
The harness invokes that gate globally, but it exits silently without reading
hook input or opening the nine-tails store unless the session was launched by
"nine-tails hooks run" and holds its live, session-bound capability.`,
	})
	c.AddCommand(newHooksInstallCmd(a), newHooksUninstallCmd(a), newHooksRunCmd(a), newHooksDispatchCmd(a))
	return c
}

func selectedHarness(claude, codex bool) (harnessadapter.Name, error) {
	if claude == codex {
		return "", cli.Invalid("choose exactly one of --claude or --codex")
	}
	if claude {
		return harnessadapter.Claude, nil
	}
	return harnessadapter.Codex, nil
}

func addHarnessFlags(c *cobra.Command, claude, codex *bool) {
	c.Flags().BoolVar(claude, "claude", false, "use the Claude Code adapter")
	c.Flags().BoolVar(codex, "codex", false, "use the Codex adapter")
}

func newHooksInstallCmd(a *app) *cobra.Command {
	var claude, codex bool
	c := &cobra.Command{
		Use:   "install (--claude|--codex)",
		Short: "Merge the nine-tails lifecycle gate into user settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := selectedHarness(claude, codex)
			if err != nil {
				return err
			}
			adapter, _ := harnessadapter.For(name)
			executable, err := os.Executable()
			if err != nil {
				return cli.ToolFailed("locate nine-tails executable: %v", err)
			}
			path, _, err := harnessadapter.Install(adapter, executable)
			if err != nil {
				return cli.ToolFailed("%v", err)
			}
			fmt.Fprintln(a.stdout, path)
			if name == harnessadapter.Codex {
				fmt.Fprintln(a.stderr, "nine-tails: Codex skips new or changed non-managed hooks until you review and trust them with /hooks")
			}
			return nil
		},
	}
	addHarnessFlags(c, &claude, &codex)
	return c
}

func newHooksUninstallCmd(a *app) *cobra.Command {
	var claude, codex bool
	c := &cobra.Command{
		Use:   "uninstall (--claude|--codex)",
		Short: "Remove only nine-tails-owned lifecycle handlers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := selectedHarness(claude, codex)
			if err != nil {
				return err
			}
			adapter, _ := harnessadapter.For(name)
			path, _, err := harnessadapter.Uninstall(adapter)
			if err != nil {
				return cli.ToolFailed("%v", err)
			}
			fmt.Fprintln(a.stdout, path)
			return nil
		},
	}
	addHarnessFlags(c, &claude, &codex)
	return c
}

func newHooksRunCmd(a *app) *cobra.Command {
	var claude, codex bool
	var meta []string
	c := &cobra.Command{
		Use:   "run <agent> (--claude|--codex) [--meta key=value]... [-- HARNESS_ARGS...]",
		Short: "Launch one harness session as a mechanically active agent run",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := selectedHarness(claude, codex)
			if err != nil {
				return err
			}
			dash := cmd.ArgsLenAtDash()
			if dash >= 0 && dash != 1 || dash < 0 && len(args) > 1 {
				return cli.Invalid("put harness arguments after --")
			}
			parsedMeta, err := store.ParseMeta(meta)
			if err != nil {
				return err
			}
			if err := harnessadapter.ValidateMetadata(harnessadapter.Metadata(parsedMeta)); err != nil {
				return cli.Invalid("%v", err)
			}
			agent := args[0]
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			if err := a.open(); err != nil {
				return err
			}
			if _, err := store.ActiveNamed(a.st.DB, agent, "definition", "agent-base", "base"); err != nil {
				return err
			}
			// Do not hold an idle SQLite connection for the lifetime of an
			// interactive harness. Hook subprocesses open their own short-lived
			// connection only for the first prompt load.
			a.close()
			run, err := harnessadapter.BeginRun(a.home, agent, name, harnessadapter.Metadata(parsedMeta))
			if err != nil {
				return cli.ToolFailed("start %s run: %v", name, err)
			}
			defer run.Close()
			child, err := harnessCommand(string(name), args[1:])
			if err != nil {
				return cli.ToolFailed("cannot resolve %s: %v", name, err)
			}
			child.Stdin, child.Stdout, child.Stderr = a.stdin, a.stdout, a.stderr
			child.Env = run.Environment(os.Environ())
			childErr := runHarnessChild(child)
			closeErr := run.Close()
			if childErr != nil {
				var exit *exec.ExitError
				if errors.As(childErr, &exit) {
					code, description := describeHarnessExit(exit)
					return cli.Errorf(code, "%s %s", name, description)
				}
				return cli.ToolFailed("cannot start %s: %v", name, childErr)
			}
			if closeErr != nil {
				return cli.ToolFailed("clean up %s run: %v", name, closeErr)
			}
			return nil
		},
	}
	addHarnessFlags(c, &claude, &codex)
	c.Flags().StringArrayVar(&meta, "meta", nil, "ambient metadata key=value for each fresh episode (repeatable)")
	return c
}

func newHooksDispatchCmd(a *app) *cobra.Command {
	var claude, codex bool
	var owner string
	c := &cobra.Command{
		Use:    "dispatch",
		Short:  "internal lifecycle gate",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The ownership argument and capability probe intentionally precede
			// hook JSON decoding and every config/store operation. Any stale,
			// forged, or ordinary global invocation is a byte-silent success.
			if owner != harnessadapter.OwnerTag() {
				return nil
			}
			name, err := selectedHarness(claude, codex)
			if err != nil {
				return nil
			}
			capability, active := harnessadapter.Probe(name)
			if !active {
				return nil
			}
			adapter, _ := harnessadapter.For(name)
			event, err := adapter.DecodeEvent(a.stdin)
			if err != nil {
				return cli.ToolFailed("hook dispatch: %v", err)
			}
			decision, err := capability.Admit(event)
			if err != nil {
				// The wrapper can remove the marker between Probe and Admit.
				// That close race is inactive; other state/locking failures are
				// admitted adapter failures and must remain non-blocking exit 5.
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return cli.ToolFailed("hook dispatch: admit lifecycle event: %v", err)
			}
			if !decision.Active {
				return nil
			}
			if decision.Capsule != "" {
				if err := adapter.EncodeContext(a.stdout, event.Name, decision.Capsule); err != nil {
					return cli.ToolFailed("hook dispatch: %v", err)
				}
				return nil
			}
			if !decision.Load {
				return nil
			}
			if s := os.Getenv("NINE_TAILS_NOW"); s != "" {
				t, err := time.Parse(time.RFC3339, s)
				if err != nil {
					_ = capability.AbortLoad(event.SessionID, decision.Claim)
					return cli.ToolFailed("hook dispatch: NINE_TAILS_NOW must be RFC 3339: %v", err)
				}
				a.now = func() time.Time { return t }
			}
			committed := false
			defer func() {
				if !committed {
					_ = capability.AbortLoad(event.SessionID, decision.Claim)
				}
			}()
			a.home, a.homeFlag = decision.Home, ""
			if err := a.open(); err != nil {
				return cli.ToolFailed("hook dispatch: %v", err)
			}
			cp, err := capsule.Load(a.st, capsule.Request{Agent: decision.Agent, Task: decision.Task, Parent: decision.Parent,
				Meta: store.Meta(decision.Metadata), SignalExcerptChars: a.cfg.SignalExcerptChars, MaxBytes: adapter.CapsuleMaxBytes(), Now: a.now()})
			if err != nil {
				var tooLarge *capsule.TooLargeError
				if errors.As(err, &tooLarge) {
					// The harness cannot deliver this capsule whole and nothing was
					// recorded. Hand the session a pointer to an in-session load,
					// whose output is not bound by the hook ceiling.
					if err := adapter.EncodeContext(a.stdout, event.Name, tooLargePointer(decision.Agent, store.Meta(decision.Metadata), tooLarge)); err != nil {
						return cli.ToolFailed("hook dispatch: %v", err)
					}
					return nil
				}
				return cli.ToolFailed("hook dispatch: %v", err)
			}
			ok, err := capability.CommitContext(event.SessionID, decision.Claim, cp.ContextID, cp.Markdown)
			if err != nil {
				return cli.ToolFailed("hook dispatch: advance active run context: %v", err)
			}
			if !ok {
				return nil
			}
			committed = true
			if err := adapter.EncodeContext(a.stdout, event.Name, cp.Markdown); err != nil {
				return cli.ToolFailed("hook dispatch: %v", err)
			}
			return nil
		},
	}
	addHarnessFlags(c, &claude, &codex)
	c.Flags().StringVar(&owner, "owner", "", "installed-entry ownership marker")
	return c
}

// tooLargePointer replaces a capsule the harness could not deliver whole. No
// receipt exists for it; the in-session load the pointer names makes one.
func tooLargePointer(agent string, meta store.Meta, e *capsule.TooLargeError) string {
	var flags strings.Builder
	for _, k := range store.SortedKeys(meta) {
		for _, v := range meta[k] {
			flags.WriteString(" --meta " + k + "=" + v)
		}
	}
	return fmt.Sprintf("nine-tails: the %s capsule is %d bytes, over this harness's %d-byte hook limit, so it was not injected and no receipt was recorded. Load it in the session: nine-tails load %s --task \"<task>\"%s. If its \"Recent adjustments\" section is long, compile first: nine-tails compile %s.", agent, e.Bytes, e.Max, agent, flags.String(), agent)
}
