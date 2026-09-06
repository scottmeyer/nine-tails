package capsule

import (
	"fmt"
	"strings"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// LibraryView is a navigation pointer, not retrieved evidence. Count is the
// eligible library size at load time; inspection later reads the current index.
type LibraryView struct {
	Count   int    `json:"count" yaml:"count"`
	Inspect string `json:"inspect" yaml:"inspect"`
	Check   string `json:"check" yaml:"check"`
}

func writeLibrary(q store.Querier, c *Capsule, md *strings.Builder) error {
	count, err := store.CountEligibleRecall(q, c.Agent, c.Metadata)
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	c.Library = &LibraryView{Count: count, Inspect: c.command("inspect --page --context " + c.ContextRef), Check: c.command("inspect <memory-ref> --context " + c.ContextRef)}
	noun := "memories"
	if count == 1 {
		noun = "memory"
	}
	fmt.Fprintf(md, "\nMemory library (data): %d %s; browse with %s. Check unexpected recall with %s.\n", count, noun, cli.InlineCode(c.Library.Inspect), cli.InlineCode(c.Library.Check))
	return nil
}
