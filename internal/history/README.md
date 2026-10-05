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
