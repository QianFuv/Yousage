# Antigravity native history

This reader follows t3code commit `51f7f77f87318a5329d3bab15d513fda5654200b`,
`apps/server/src/usage/antigravityUsageReader.ts` (Git blob
`34a88114f42eab0595aa74782ded89482855c51c`). It reads only usage metadata tables
inside a read-only SQLite transaction, including uncheckpointed WAL records.
Malformed metadata invalidates that database batch while other stores remain useful.
The application integration retains its last successful snapshot on failure.

Alias identities merge transitively across every configured store before window
filtering. Duplicate observations use category maxima, the first record owner and
the highest quality timestamp, choosing the earliest time on equal quality.
Reasoning is contained within normalized output. Reported monetary cost is unknown.

Discovery accepts explicit roots and already resolved T3 instance profile paths;
it prefers an existing `conversations` child and never creates provider directories.
T3 profile resolution belongs to connection configuration. Antigravity has no native
quota reader registered in the pinned T3 provider path; history support does not
invent one. Live source stores are never modified by this module.
