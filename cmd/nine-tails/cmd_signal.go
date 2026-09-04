package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// signalView is the envelope printed by `signal --format json|yaml`: the
// record envelope (DESIGN §4), delivery state, and deduplication result.
type signalView struct {
	store.RecordEnvelope `yaml:",inline"`
	Delivery             deliveryView `json:"delivery" yaml:"delivery"`
	Deduplicated         *bool        `json:"deduplicated,omitempty" yaml:"deduplicated,omitempty"`
}

// deliveryView is the stable external delivery shape from DESIGN §4. The
// database-facing Delivery also carries the record id and agent for queries;
// those duplicate fields are not part of a signal envelope's nested delivery.
// Empty mechanical fields remain present so consumers see one stable shape at
// every point in the lifecycle.
type deliveryView struct {
	State          string `json:"state" yaml:"state"`
	AvailableAt    string `json:"available_at" yaml:"available_at"`
	DedupeKey      string `json:"dedupe_key" yaml:"dedupe_key"`
	LeaseToken     string `json:"lease_token" yaml:"lease_token"`
	LeasedUntil    string `json:"leased_until" yaml:"leased_until"`
	AcknowledgedAt string `json:"acknowledged_at" yaml:"acknowledged_at"`
}

func deliveryViewAsOf(d store.Delivery, now time.Time) deliveryView {
	d = store.DeliveryAsOf(d, now)
	return deliveryView{
		State:          d.State,
		AvailableAt:    d.AvailableAt,
		DedupeKey:      d.DedupeKey,
		LeaseToken:     d.LeaseToken,
		LeasedUntil:    d.LeasedUntil,
		AcknowledgedAt: d.AcknowledgedAt,
	}
}

// printSignal prints a mutation result for a signal: the id alone (default)
// or the envelope.
func (a *app) printSignal(format string, sig *store.Signal, deduplicated *bool) error {
	switch format {
	case "id", "":
		_, err := fmt.Fprintln(a.stdout, sig.Record.ID)
		return err
	case "json", "yaml":
		return cli.Write(a.stdout, format, signalView{
			RecordEnvelope: sig.Record.Envelope(),
			Delivery:       deliveryViewAsOf(sig.Delivery, a.now()),
			Deduplicated:   deduplicated,
		})
	}
	return cli.Invalid("unknown format %q (id|json|yaml)", format)
}

func newSignalCmd(a *app) *cobra.Command {
	var subject, body, at, dedupeKey, context, format string
	var meta []string
	var stdin bool
	c := &cobra.Command{
		Use:   "signal [<agent>] --subject S [--body B | --stdin] [--at RFC3339|+5m] [--dedupe-key K]",
		Short: "Address a signal (reminder, scheduled work, external event) to an agent",
		Long: `Create a signal. Without <agent> it is addressed to shared and appears in
every agent's capsule under "Due signals" once its availability time (--at,
default now) has passed; scope it with --meta (repo-id=...) like any record.
Name an agent only when a wake-up must start that agent: tick --claim leases
the signal for it. The subject is stored as meta subject=S;
the body may be empty, a --body string, or --stdin (arbitrary external data —
load shows a capped excerpt, inspect shows it all).

--dedupe-key K makes (agent, K) unique among unacknowledged signals: a repeat
prints the existing id, writes "nine-tails: deduplicated against sig_N" to
stderr and exits 0. --at accepts RFC 3339 or +<n><s|m|h|d>. Prints the id.

Acknowledge a leased signal with: signal ack <sig-id> --lease <token>`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			agent := "shared"
			switch len(args) {
			case 0:
			case 1:
				agent = args[0]
			default:
				return cli.Invalid("signal takes at most one positional (the agent); put the body in --body or --stdin")
			}
			if strings.TrimSpace(subject) == "" {
				return cli.Invalid("--subject is required")
			}
			if strings.ContainsAny(subject, "\r\n") {
				return cli.Invalid("--subject must be one line (CR and LF are not allowed)")
			}
			if cmd.Flags().Changed("body") && stdin {
				return cli.Invalid("give the body either as --body or via --stdin, not both")
			}
			switch format {
			case "id", "", "json", "yaml":
			default:
				return cli.Invalid("unknown format %q (id|json|yaml)", format)
			}
			if err := a.open(); err != nil {
				return err
			}
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			var positional []string
			if cmd.Flags().Changed("body") {
				positional = []string{body}
			}
			text, err := cli.ReadBody(positional, stdin, a.stdin, true)
			if err != nil {
				return err
			}
			when, err := cli.ParseAt(at, a.now())
			if err != nil {
				return err
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			if m.Has("subject") {
				return cli.Invalid("the subject is set with --subject, not --meta subject=...")
			}
			m.Add("subject", subject)
			if context != "" {
				// Signals are legitimately addressed to other agents, so the
				// context only has to exist; it need not belong to <agent>.
				if _, err := a.contextAgent(context); err != nil {
					return err
				}
			}
			var sig *store.Signal
			var deduplicated bool
			err = a.st.Tx(func(tx *sql.Tx) error {
				if context != "" {
					if _, err := store.GetContext(tx, context); err != nil {
						return err
					}
				}
				var err error
				sig, deduplicated, err = store.CreateSignal(tx, agent, text, m, when, dedupeKey, context)
				return err
			})
			if err != nil {
				return err
			}
			if deduplicated {
				fmt.Fprintf(a.stderr, "nine-tails: deduplicated against %s\n", sig.Record.ID)
			}
			return a.printSignal(format, sig, &deduplicated)
		},
	}
	c.AddCommand(newSignalAckCmd(a))
	c.Flags().StringVar(&subject, "subject", "", "required: one-line subject (stored as meta subject=...)")
	c.Flags().StringVar(&body, "body", "", "signal body (may be empty; default empty)")
	c.Flags().BoolVar(&stdin, "stdin", false, "read the body from stdin instead of --body")
	c.Flags().StringVar(&at, "at", "", "availability time: RFC 3339 or relative +5m, +2h, +1d (default now)")
	c.Flags().StringVar(&dedupeKey, "dedupe-key", "", "opaque key unique per agent among unacknowledged signals")
	c.Flags().StringArrayVar(&meta, "meta", nil, "applicability metadata key=value (repeatable)")
	c.Flags().StringVar(&context, "context", "", "originating context id (origin, not scope)")
	c.Flags().StringVar(&format, "format", "id", "id (one line) | json | yaml")
	return c
}

func newSignalAckCmd(a *app) *cobra.Command {
	var lease, format string
	c := &cobra.Command{
		Use:   "ack <sig-id> --lease <token>",
		Short: "Acknowledge a leased signal using the token from tick --claim",
		Long: `Move a leased signal to acknowledged. The signal must currently be leased
with an unexpired lease and the token must match (exit 7 otherwise; unknown
id exits 3). Acknowledged signals leave the capsule and tick for good.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if lease == "" {
				return cli.Invalid("--lease is required (the lease_token returned by `tick --claim`)")
			}
			if !store.IsID(id) || !strings.HasPrefix(id, "sig_") {
				return cli.Invalid("expected a signal id like sig_9, got %q", id)
			}
			switch format {
			case "id", "", "json", "yaml":
			default:
				return cli.Invalid("unknown format %q (id|json|yaml)", format)
			}
			if err := a.open(); err != nil {
				return err
			}
			var rec *store.Record
			err := a.st.Tx(func(tx *sql.Tx) error {
				if err := store.AckSignal(tx, id, lease, a.now()); err != nil {
					return err
				}
				var err error
				rec, err = store.GetRecord(tx, id)
				return err
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&lease, "lease", "", "required: the lease token (lease_N)")
	c.Flags().StringVar(&format, "format", "id", "id (one line) | json | yaml")
	return c
}

// tickRow is one due signal as printed by tick (DESIGN §11).
type tickRow struct {
	ID          string     `json:"id" yaml:"id"`
	Agent       string     `json:"agent" yaml:"agent"`
	Subject     string     `json:"subject" yaml:"subject"`
	Body        string     `json:"body" yaml:"body"`
	Meta        store.Meta `json:"meta" yaml:"meta"`
	AvailableAt string     `json:"available_at" yaml:"available_at"`
	State       string     `json:"state" yaml:"state"`
	LeaseToken  string     `json:"lease_token" yaml:"lease_token"`
	LeasedUntil string     `json:"leased_until" yaml:"leased_until"`
}

func newTickCmd(a *app) *cobra.Command {
	var agent, lease, format string
	var claim bool
	c := &cobra.Command{
		Use:   "tick [--claim] [--lease 5m] [--agent A]",
		Short: "List due signals; with --claim, lease them for wake-up",
		Long: `Print a JSON array of signals that are due: available now and pending (or
whose lease has expired), oldest-available first, across all agents unless
--agent is given. Without --claim this is read-only. With --claim, every
listed signal is atomically leased for --lease (default 5m) and returned with
state "leased", a lease_token and leased_until; a signal leased elsewhere is
not returned until its lease expires. Start the addressed agents, then run
  signal ack <id> --lease <token>
Prints [] when nothing is due. An external timer (cron, launchd) calls this.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "", "json", "yaml":
			default:
				return cli.Invalid("unknown format %q (json|yaml)", format)
			}
			leaseFor, err := cli.ParseDuration(lease)
			if err != nil {
				return err
			}
			if leaseFor <= 0 {
				return cli.Invalid("--lease must be positive, got %q", lease)
			}
			if agent != "" {
				if err := store.ValidAgentName(agent); err != nil {
					return err
				}
			}
			if err := a.open(); err != nil {
				return err
			}
			now := a.now()
			var sigs []*store.Signal
			if claim {
				err = a.st.Tx(func(tx *sql.Tx) error {
					var err error
					sigs, err = store.ClaimDue(tx, agent, now, leaseFor)
					return err
				})
			} else {
				sigs, err = dueForTick(a.st.DB, agent, now)
			}
			if err != nil {
				return err
			}
			rows := make([]tickRow, 0, len(sigs))
			for _, s := range sigs {
				rows = append(rows, tickRow{
					ID: s.Record.ID, Agent: s.Record.Agent, Subject: s.Record.Meta.First("subject"), Body: s.Record.Body,
					Meta: s.Record.Meta, AvailableAt: s.Delivery.AvailableAt, State: s.Delivery.State,
					LeaseToken: s.Delivery.LeaseToken, LeasedUntil: s.Delivery.LeasedUntil,
				})
			}
			return cli.Write(a.stdout, format, rows)
		},
	}
	c.Flags().BoolVar(&claim, "claim", false, "lease every due signal (the only write tick ever makes)")
	c.Flags().StringVar(&lease, "lease", "5m", "lease duration with --claim (30s, 5m, 2h, 1d)")
	c.Flags().StringVar(&agent, "agent", "", "only this agent's signals (default all agents)")
	c.Flags().StringVar(&format, "format", "json", "json|yaml")
	return c
}

// dueForTick is the read-only tick set (DESIGN §11): due signals that are
// pending or whose lease has expired. Signals under a live lease are left to
// whoever holds them.
func dueForTick(q store.Querier, agent string, now time.Time) ([]*store.Signal, error) {
	due, err := store.DueSignals(q, agent, now)
	if err != nil {
		return nil, err
	}
	out := due[:0]
	for _, s := range due {
		if s.Delivery.State == "pending" {
			out = append(out, s)
		}
	}
	return out, nil
}
