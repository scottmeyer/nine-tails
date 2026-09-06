# Editors that learn and remove prose

The reflector and compiler now treat knowledge as material to edit into useful
future instructions. The retention test is whether a condition, action, or
exception changes a future decision. Incident narration and explanations need
not survive merely because they appeared in a source. Explicit preferences,
uncertainty, and meaningful exceptions still matter.

## Implementation

`compile-input` previously read only the `brief-compiler` base. Corrections saved
to that role never reached the compiler. It now includes `editorial_guidance`
with the active original method sources and their scopes, separate from target
guidance and accounting. The compiler's own derived brief cannot narrow or hide
these sources. A custom base replaces the editorial method; the mechanical
contract is always present. Storage errors propagate. Input assembly is read-only.

The reflector starter shrank from 867 to 319 words. It retains parent receipt
ownership and source inspection, then focuses on editorial judgment instead of
repeating the capsule's command manual. Existing agents are not silently
overwritten. The inspected installed reflector was explicitly reconciled, and
its redundant standing correction retired. Its combined base and guidance fell
from 540 to 319 words.

The installed compiler base contained accidental Go source fragments and an old
complete contract. It was replaced with a 33-word role definition. The user's
editorial direction was saved as ordinary compiler guidance. A subsequent
correction replaced that source and appeared in the next compiler input.

Whole-source removal still uses inspected correction, consolidation, or reasoned
retirement. No new compiler rejection status or suppression lifecycle was added:
deferral continues to expose the original guidance. New learning requires no compile.

## Actual source cleanup and use

- The 309-word dogfooding chronology became a 57-word scoped experience about
  explicit subscriptions, conflicting local advice, and verifying application.
- The 193-word contract summary was retired because maintained documentation
  already carried it and it contributed no distinct observation. Original bodies
  and the retirement reasons remain inspectable.
- Soccer Chess engineer guidance was compiled from five sources into eight
  independently correctable items: 336 body words became 230. This measures only
  guidance bodies, not the compiler prompt or JSON framing. Review rejected an
  earlier misleading comparison that counted the input contract as compression.
- All source meanings remain accounted for. Dry-run warnings concerned omitted
  origin harness/model facets; review confirmed the game rules are independent
  of those facets. Original project scopes were retained.
- The game agent identified bloated shared state. It was reduced through
  `nt_state` from 421 to 155 words, preserving artifact pointers, current source
  version, validation limits, next work, and publication status.

This is evidence of persistence, delivery, and reviewed editorial work. It is
not a controlled claim about model reliability or autonomous semantic judgment.

## Soccer Chess

Commit `b751a8a770354beea7b7f9c33f8f9a179ea2d825` adds an opening action cue on the
pitch, where tablet players can see it. It names Kick or Touch according to the
selected action and clears after play. Root review caught the first version's
unconditional fallback, which could invite a kick on a terminal result; the
final condition and behavioral regressions cover that error and mode switching.

The game worker loaded its role through the installed MCP stdio server and used
the advertised `game-check`. A fresh server process subsequently verified the
updated binary, capability access, active replacement inspection, and retirement
inspection. Native MCP tools were not exposed to this conversation; stdio use
must not be described as native connector delivery.

A new engineering assignment then loaded the compiled items and compact state
through MCP. Its receipt recorded all eight items and no uncompiled adjustments.
While tracing the requirement that feedback match the available action, it
found a missed run-setup path: the opening cue still invited a kick while Kick
was disabled. The engineer fixed that path in commit
`8c41d4efc56ca4923018525e721077f5c770a86d` and added a regression for entering and
canceling run setup. This is a concrete useful result after delivery, with
stronger review still required; it does not isolate the guidance's causal effect.

## Validation

- nine-tails: `make test`, `go vet ./...`, `make build`, and `git diff --check`.
- An isolated real-binary check exercised method delivery, correction,
  retirement, fixed contract retention, and target accounting separation.
- Compiler regressions cover custom method plus fixed contract, learned source
  correction/retirement, a broadly scoped method behind an incorrectly narrowed
  brief, target accounting isolation, no input-assembly writes, and database errors.
- Soccer Chess: 25 physics, 18 UI/real-physics, and one built-worker HTML check;
  production build, lint, and TypeScript pass. The opening cue was inspected at
  a 1024×768 browser viewport. No physical-tablet or child playtest was performed.

The existing published grid version was not replaced during this local iteration.
