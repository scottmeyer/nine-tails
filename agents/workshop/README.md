# Persistent workshop catalog

These are reusable role seeds, installed into the single user store. They carry no fabricated history. Their future preferences and experience are ordinary nine-tails records learned during work.

| Pool | Roles |
| --- | --- |
| Coordination | `workshop`, `architect` |
| Framework | `nine-tails.architect`, `nine-tails.builder`, `nine-tails.reviewer` |
| Game studio | `game.designer`, `game.engineer`, `game.playtester` |

Start with `nine-tails load workshop --task "A concise purpose" --meta repo-id=<project> --meta harness=<host>`, or directly load a role. Put the child's load command first in delegated work. Keep each returned receipt paired with its role. A playful companion session can conclude with no writes.

`workshop` advertises both pools. Game roles advertise their peers. Project facts belong in a named scoped state or current project artifacts; role bases describe how to work. The Soccer Chess project uses `workshop/soccer-chess` as its shared project pointer.

For a fresh store, first install the repository builder/reviewer roles as described in `../README.md`. Import each absent file in this folder individually. The seeds include the workshop and game-peer catalog entries; advertise workshop and architect in pilot with `nine-tails agent add pilot <role> --description "..."`. Do not blindly reimport over an existing personalized agent: inspect and reconcile it first. Current runtime capsules provide the common learning protocol, so base seeds remain short.

The optional MCP adapter is documented in `../../docs/mcp.md`. The CLI remains sufficient for every operation.
