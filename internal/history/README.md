# Native transcript history

Codex reads the shared home `sessions` tree; Claude reads `projects`. Explicit
roots and CODEX_HOME/CLAUDE_CONFIG_DIR defaults are supported. Shadow auth homes
and archived_sessions are not substituted for Codex's statistics root. Disabled
account history remains eligible. Directory symlinks resolve before physical
identity dedup; history files are opened for reading only.

The streaming JSON projection validates syntax but discards unselected tool text
as it passes, without an import-size budget. Provider reducer state commits only
through completed LF bytes, including CRLF. Valid unterminated EOF usage is stored
as a separate tail and replayed later. Growth can resume after a matching final
64-byte FNV-1a32 guard and parser version; shrink, rewrite and guard mismatch reread.
The guard is not a whole-file integrity proof. Read errors preserve stored facts.

Codex counts last-token deltas, carries model/tier/session context, excludes
consecutive duplicate usage and suppresses the copied leading fork burst. Across
files, moved sessions use occurrence-aware keys, retaining repeated equal events
inside one rollout. Claude de-duplicates assistant content blocks by message and
request identity. Returned facts carry their canonical source root.

Retention is90days by source-file mtime. Removed files and directories retain
cached usage and their old physical identity while cache facts remain. Repricing
and window filtering happen later. The collector scans the entire retained
interval once rather than tying persisted facts to one current viewer window;
query filtering can apply t3code's36h mtime slack separately. Empty/missing/partial/
failed roots remain distinct. Pinned `malformedRecords` stays0; the additional
host diagnostic `malformedLines` reports syntax failures without changing totals.

Common ISO timestamps (fractional seconds, offsets, minute precision and date
only), unzoned ISO using the host zone, and standard RFC/ANSIC forms are accepted.
Numeric timestamp values are rejected. Locale-specific implementation-dependent
JavaScript Date.parse strings are not a provider format contract or a compatibility
claim. Raw sources/oracles remain unchanged at t3code51f7f77.

Parser version 4 replays all retained older checkpoints for generation timing,
redacted error events
and explicit Claude cache TTL observations. Claude system `api_error` and
`isApiErrorMessage` markers, Codex `turn_aborted`, transport errors and failed
`task_complete` records retain only timestamp, provider, session, known model and
a closed error classification. Interruptions remain separate from failures.
Usage and quota-cycle pricing exclude these error facts. Arbitrary diagnostics,
prompts and response text are never persisted. Error facts use the same atomic
checkpoint/tail replacement and file-mtime retention as usage.

Parser version 6 also retains the transcript basename and an epoch for explicit
Claude `compact_boundary` and Codex `compacted` markers as a context identity.
Loss analysis compares usage and links retries only within that context, so a new
agent or rollout that inherits a session id cannot borrow the parent's cache.
Appending or resuming the same transcript preserves its context identity; moved
copies with the same basename retain the existing cross-file deduplication.

Codex generation timing starts at a timestamped task/user boundary or preceding
tool output and ends at the latest model response item before its unique usage
event. Turn context can tighten the start before output. Tool outputs arriving
before usage establish the next request and do not extend the preceding response.
Overlapping ambiguous responses, missing boundaries, failures, zero-output
requests, intervals above ten minutes and rates above 10,000 output tokens/second
remain unmeasured. Timing includes time to first token and local logging/scheduling
overhead; observed tool execution and human idle gaps are excluded.

OpenCode uses paired native assistant `time.created` and `time.completed` values,
including time to first token, under the same duration/rate bounds. SQL fallback
timestamps never establish timing. Its full snapshots are replaced on every
successful scan.

Claude Code writes each assistant message as one line per content block. Blocks
streamed before the message ends carry a null `stop_reason` and partial usage, and
tool results may be interleaved before the final block. The Claude reducer keeps
only entry ids, parent ids and timestamps: it emits each message from its first
block with a stop reason, so usage is final, and times it from the user prompt or
tool result reached through the first block's `parentUuid` chain (skipping
attachments) to that block. A message that never receives a final block is emitted
untimed with its partial usage when the next message begins. Requests with an API
error inside the interval, a parent outside the retained chain, or the shared
duration/rate bounds remain unmeasured. Grok, Cursor and the supported
Antigravity schema expose no reliable paired per-request times and stay unmeasured.
