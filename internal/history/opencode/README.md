# OpenCode native and legacy history

This reader implements the schema compatibility of t3code51f7f77's
opencodeUsageReader.ts, without claiming a particular installed OpenCode version.
OPENCODE_DATA_DIR accepts comma-separated roots; canonical aliases are removed.
Default is absolute XDG_DATA_HOME/opencode or ~/.local/share/opencode.

Recognized top-level opencode*.db files open mode=ro/query_only with100ms busy
timeout. No immutable flag is used, so live WAL records stay visible. Each read
uses one deferred read transaction for metadata and both message tables: a writer
can commit concurrently without mixing old and new table contents in one result.
The app never changes provider journaling settings, checkpoints, schema or data.

Main database wins over suffix databases, message over session_message, then valid
database records win over migrated storage/message/**/*.json copies. Only records
eligible for the requested window claim IDs; invalid/old DB rows permit valid JSON
fallback. Legacy symlinks are not followed. Each root retains its own first-seen
order; global aggregation deduplicates copied message IDs across roots.

OpenCode input is uncached; reasoning is separate from output and is added once
to normalized output. Zero/negative reported cost is unknown and uses shared model
pricing. Read/schema errors retain already read facts and expose partial status;
malformed message JSON is skipped. Source/service integration must preserve its
last stored snapshot on a failed read and replace successful full DB snapshots,
because rows can update in place and main DB mtime alone cannot represent WAL.
