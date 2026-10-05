# Grok Build saved turns

Statistics read only `GROK_HOME/sessions/**/updates.jsonl` (default `~/.grok`).
Automatic durable production is supported by the pinned official Grok Build
snapshot2bdd1d6a and the source evidence in docs/parity/capabilities.md. Existing
installed releases still need compatible completed-turn usage records; text or
missing files cannot reconstruct previously billed usage.

Only `params.update.sessionUpdate=turn_completed` emits records. The agent's
millisecond clock wins over the outer seconds/milliseconds timestamp. Inclusive
input subtracts cache categories; reasoning remains contained in output. One USD
equals10^10 cost ticks. Per-model ticked costs remain authoritative; unticked rows
share the clamped remaining aggregate cost by their own token counts. Zero-token
rows neither emit nor consume aggregate cost. Unknown cost stays null.

Saved prompt/session/model identity deduplicates repeated turn updates. The shared
reader/collector owns streaming, tail replay, guards, persistence and retention.
No Grok process/account or network endpoint is used for historical statistics.
Native account billing/limits is a separate F8 adapter.
