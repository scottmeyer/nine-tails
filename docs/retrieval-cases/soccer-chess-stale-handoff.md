# A grid handoff reached the later 3D rules task

During Soccer Chess corner/offside controls work, the game engineer received a
memory describing the former grid game's controls, board identity and old test
counts. The actual task concerned the 3D physical game. The transferable Runner
display lesson was useful; the surrounding implementation status was obsolete.

[Captured inspection](soccer-chess-stale-handoff.json) retains the exact memory,
original receipt and its current replacement. The task was “Connect corner and
offside practice controls.” The check uses that stored task as a proxy; it does
not assert that there was no query override or explicit selection.

- Original receipt: `ctx_01M1W2P4NAGA8RVA9YAV6A7TP5`.
- Delivered memory: `rec_01M1VR3PBP8XY41J3WXEBNC09T`.
- Current replacement: `rec_01M1W37ZJ5FWJEGPV1RC6D9MN4`.

`recorded_in_context: true` confirms exact-ID delivery. The current reason is
`superseded`: the earlier cleanup replaced stale status with the reusable UI
transition and verification lessons. The new diagnostic preserved that
distinction without reloading an agent or writing an evaluation memory.

This is a content-maintenance case. It does not show that different wording
hid useful experience, and it does not show that the stale entry caused later
coding errors. The historical excerpt bytes were not retained; inspecting the
full source today does not prove that its complete body was delivered then.
The earlier audit is described in
[the shared-guidance iteration](../iteration-2026-09-06-shared-guidance.md).
