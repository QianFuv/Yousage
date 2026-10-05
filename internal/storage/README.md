# Persistent usage core

The app uses its own SQLite file, never a provider history database. Schema v1
stores physical source fingerprints, ordered normalized usage facts, replayable
EOF tails, completed-line checkpoints, source observations and versioned settings.
It contains neither transcript text nor account credentials. Every connection
enables foreign keys, a bounded busy timeout and WAL; write transactions acquire
an immediate lock. Future schema versions are rejected without migration writes.

Collectors parse outside the transaction, then call `CommitFileWithTail` with the
checkpoint revision they read. Rewrites replace committed facts, while append
retains them. Both paths replace the previous tail, update the reducer/byte offset
and commit together. Stale writes fail with `ErrConflict`. A read failure must
call `Observe`, not commit an empty file. Failed observations preserve the last
success time and all facts. Deduplication keys are not database unique constraints:
forked copies remain available for window-specific, global aggregation.

Prices and aliases are versioned per environment. Preferences validate before
writing; whole-value saves and patches use compare-and-swap revisions. Unchanged
environment rows survive patches. A new model alias removes that source model's
override. Zero prices and omitted cache prices have different representations.

Pure accounting follows t3code commit
`51f7f77f87318a5329d3bab15d513fda5654200b`: exact custom IDs, override/reported/public
precedence, contained reasoning, tiered rates, explicit unpriced provenance,
alias-cycle handling and minute-anchored rolling windows. IANA zone data is
embedded for independent Windows deployment. Calendar windows include today and
subtract dates, while 24h windows retain exactly 24 duration-based slots over DST.

Refresh coordination is per key. Manual requests share pending work and bypass
automatic cooldown; settings changes can request a second scan after pending
work settles. Limits cooldown starts at completion, including failure. History
staleness is one minute, without an automatic timer. Public rates have a separate
24h stale interval and a one-minute successful-fetch floor for forced refreshes.
Hidden, minimized or Settings views do not start automatic operations. Caller
cancellation stops waiting; the app-owned context cancels shared work on shutdown.

F2 owns file discovery, parser states, 64-byte guards, retention and scanner
integration. UI/service integration owns saved window/theme adoption and drafts.
This core is not evidence that all provider or desktop interactions are complete.
