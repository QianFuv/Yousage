# Persistent usage core

The app uses its own SQLite file, never a provider history database. Schema v2
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

## Schema and migration

Schema 2 keeps every normalized event and quota observation. `files` assigns an
integer identity to each `(source_id, path)` pair. `checkpoints` uses that identity
and stores revision and file modification time as columns. Sources, observations,
settings and theme settings retain their existing representations.

`records` and `quotas` use `WITHOUT ROWID` primary keys
`(file_id, is_tail, ordinal)`. Each event retains a compact record, including
quota-only events, so existing readers can reconstruct the exact original
sequence, counts and deduplication inputs. Every nonnil quota observation has its
own `quotas` row. No observations are deduplicated or compressed into runs.
`records_time(timestamp_ms, sequence)` supports bounded reads and preserves the
legacy rowid tie break for the latest quota observation. There is no duplicate
record primary-key index.

Payloads start with a version byte. Record payload v1 contains seven
interned string identities, five signed token varints, and a cost-presence marker
with an optional little-endian IEEE 754 float64. Timestamp, bucket and quota-only
status live in relational columns. Quota payloads contain an interned plan,
signed window count (`-1` means nil), and each window's interned identity, exact
float64 percentage, signed duration and reset varints. Nil and empty observations,
nil and zero cost pointers, window order and floating point bits are preserved.
Record payload v2 appends a closed error-kind enum code and cache TTL varint to
v1's fields; existing v1 rows remain readable without a relational migration.
The enum stores no diagnostic text or extra dictionary identities. Quota payloads
remain v1. Unknown versions and malformed payloads are rejected.

The `strings` dictionary uses stable, never-reused integer identities. Reference
counts change in the same transaction as records and quota observations; rewrites,
tail replacement and retention reclaim unused strings. The in-memory decoding
cache retains previously seen identities until the store closes, allowing older
WAL readers to finish. Dictionary misses are resolved on the reader's already-pinned connection, within
its WAL snapshot. This preserves concurrent read consistency without requesting
another pool connection.

Migration converts schema 1 in one transaction, including DDL, all records,
quota JSON, checkpoints and the schema version. A conversion error or process
termination before commit leaves schema 1 intact. Parser versions, checkpoint
revisions and cache keys are preserved, so migration does not request reparsing.
Retention still selects files strictly by checkpoint modification time; it never
prunes events by their timestamps.

After migration commits, `Open` runs SQLite's transactional `VACUUM`. SQLite
publishes rebuilt pages through its normal WAL transaction; there is no manual
rename or database-file swap. A killed or failed vacuum leaves a valid schema 2
database. A persistent `vacuum_pending` marker requests another attempt on next
open. The marker is cleared only after a successful vacuum and a checked
`wal_checkpoint(TRUNCATE)` result. Reclamation errors explicitly report that the
schema has migrated and reclamation will retry. Temporary disk space is needed
for migration pages, the vacuum rebuild and WAL. Once schema 2 commits, older
builds refuse it.

The existing 4096-byte page size is retained. On the measured real-data copy,
`WITHOUT ROWID` used 22,323,200 bytes versus 24,915,968 bytes for an integer
primary key plus a unique file/ordinal index. An 8192-byte page size saved only
24,576 bytes, while 16384-byte pages were larger; neither justified changing the
existing page size.

## Real-data verification

The migration was measured with Go 1.26.2 and modernc.org/sqlite on a copy of the
application database and its WAL/SHM files. The original database hash remained
unchanged. The harness lived outside the repository; provider histories were read
only, native probes used the stored `local-quotas` setting, and rate refresh used
stored rates. No bindings or provider history files were changed.

Before migration: 195,592,192 bytes. After migration, before reclamation:
216,645,632 bytes. After reclamation: 22,323,200 bytes (88.6% smaller). Migration
took 18,711.518 ms; reclamation took 2,097.942 ms. Integrity checking returned `ok`,
and foreign-key checking returned no rows.

Post-reclamation `dbstat` sizes:

| Objects | Bytes |
| --- | ---: |
| records | 9,764,864 |
| quotas | 6,270,976 |
| records_time | 4,685,824 |
| checkpoints | 598,016 |
| files and unique index | 413,696 |
| strings and unique index | 233,472 |
| settings | 331,776 |
| sources, source/settings indexes, schema, sequence, storage_meta | 24,576 |

All 214,677 records and 170,958 quota observations were retained. Canonical record
and checkpoint JSON hashes matched. Complete Usage JSON for 24h, 7d, 30d and 90d,
including cycle values, matched byte for byte at the same fixed clock
(`2026-10-07T08:00:00Z`). Checkpoint parser versions remained unchanged, including
443 Codex files still at parser version 1.

Fixture tests cover every record field, nil versus zero costs, exact float bits,
nil versus empty windows, multiple source/path identities, tails, quota-only
events, timestamp ties, parser versions, cache keys, accounting and complete
Usage/cycle views. An injected conversion failure and a killed process after
conversion but before commit both preserved the complete logical v1 database.
Concurrent dictionary reads are also tested across another store's rewrite.

Final API timing run (milliseconds), with 24h/7d/30d/90d read in order and each
window read twice:

| Measurement | Fresh v1 copy | Schema 2 | Supplied round 7 |
| --- | ---: | ---: | ---: |
| Usage 24h cold | 6874.853 | 2311.316 | 2017 |
| Usage 24h warm | 423.807 | 223.861 | 39 |
| Usage 7d first read | 1207.771 | 797.805 | |
| Usage 7d warm | 557.440 | 218.920 | |
| Usage 30d first read | 2094.361 | 1563.344 | |
| Usage 30d warm | 605.077 | 210.331 | |
| Usage 90d first read | 9589.120 | 6533.352 | |
| Usage 90d warm | 609.005 | 202.247 | |
| Usage 24h after append | 2664.578 | 1286.794 | 167 |
| No-change background follow-up | 2738.508 | 1928.999 | 1200 |

The append adds one normalized usage fact through CommitFile on the copy; its
timing covers Usage after invalidation, excluding the commit. Background timing
uses read-only live provider histories after settling their initial scan. The
fresh v1 copy's initial follow-up took 83,050.726 ms and included outstanding
parser-version updates. Migration itself does not change parser versions.

All measured APIs improved over the fresh v1 copy in this session. The historical
round-7 latencies were not reproduced by the fresh v1 baseline, and the final run
did not meet those historical figures. No functional or observation reduction
was used to improve these measurements.

Final checks: `go test -p 1 ./...` reported 270 passes in 20 packages;
`go vet ./...` and `go build ./...` exited successfully without diagnostics;
`gofmt -l .` reported no files. Bindings were unchanged by this storage round.

SQLite's [VACUUM documentation](https://www.sqlite.org/lang_vacuum.html) describes
rebuilding into a temporary database and publishing the rebuilt contents using a
normal rollback-journal or WAL transaction.

## Latency follow-up (2026-10-08)

The same disposable Go test harness measured the round-8 implementation and this
follow-up using Go 1.26.2. It copied the application database, WAL and SHM before
opening any database. Original database/WAL SHA-256 hashes remained unchanged.
The live Codex and Claude homes were read-only; native probes returned the saved
`local-quotas` snapshot, and public prices remained local and fresh.

Preparation settled outstanding parser upgrades outside the timing samples
(82,596.026 ms). The no-change measurement settled each live-history copy again
immediately before timing `RefreshLimits` and its accepted background worker.
For Usage, the newest Codex session was copied into a throwaway `CODEX_HOME`;
only the copied database's source/file identities and configuration were remapped,
preserving the other cached files. Each of five runs restored that database and
session, opened a new service, read cold 24h Usage, repeated warm 24h Usage five
times, appended one synthetic `token_count` event with `rate_limits`, waited for
the real background history/rates follow-up, then read Usage. The 7d/30d/90d
windows were each primed and read warm in every run. No direct `CommitFile`
append was substituted for transcript parsing. The clock and inputs were shared
between implementations. Preparation, migration and `Open` are excluded from
the cold Usage metric, matching the earlier round-7 API measurement.

All values below are milliseconds, with five-run medians. The 24h warm result is
the median of each run's five warm reads, then the median across the five runs.

| Measurement | Round 8, same harness | Latency follow-up | Target |
| --- | ---: | ---: | ---: |
| Usage 24h cold | 2811.640 | 1894.259 | <= 2000 |
| Usage 24h warm | 101.090 | 33.349 | <= 50 |
| Usage after transcript append | 291.487 | 218.843 | <= 300 |
| Append background follow-up | 355.133 | 227.429 | |
| No-change background follow-up | 2499.445 | 304.460 | <= 1200 |
| Usage 7d warm | 102.015 | 27.229 | |
| Usage 30d warm | 99.072 | 30.324 | |
| Usage 90d warm | 96.958 | 30.447 | |
| RefreshLimits return | 1.124 | 0.553 | approximately immediate |

All requested targets passed. Final cold samples ranged from 833.429 to
1992.275 ms; after-append samples ranged from 85.612 to 242.684 ms. These are
API/service cold reads, not cold operating-system caches. Native process and
network latency is excluded by the requested stored-snapshot stubs.

CPU and allocation profiles were collected separately for 30 warm Usage calls,
one post-append Usage call, and three no-change follow-ups. Allocation figures
are sampled `alloc_space` deltas after GC, including a small profiler overhead:

| Profile workload | Before allocated | After allocated | Main costs |
| --- | ---: | ---: | --- |
| 30 warm Usage calls | 259.13 MiB | 51.17 MiB | Before: repeated price JSON decoding, path/revision keys and provider-home resolution. After: cache-key JSON compaction and metadata reads. |
| One post-append Usage | 51.44 MiB | 21.91 MiB | Bounded record reads, deduplication, repricing and active-cycle reconstruction. The final short CPU sample is coarse. |
| Three follow-ups | 27.33 MiB | 13.40 MiB | Before: Windows file stats and individual checkpoint reads. After: directory enumeration and batched checkpoint decoding. |

Before, warm `Rates` occupied 27.72% of sampled CPU cumulatively and repeated
physical-home resolution occupied 23.10%. No-change `os.Stat` occupied 74.27%.
After, warm JSON compaction remained the largest sampled leaf; follow-up
directory enumeration occupied 50% cumulatively. Cold profiling also identified
an avoidable SQL ordering sort, repeated negative native-package probes, and
growing record/observation slices. Query-plan checks showed the old Codex range
reader used a temporary ORDER BY B-tree; the file-first reader uses existing
file and record primary-key order without that temporary sort.

The changes retain schema 2 and binary payload version 1:

- `HistoryCacheKey` preserves its exact existing string format and reuses keys
  under `PRAGMA data_version` on one dedicated observer connection. Local and
  external commits invalidate that observer; four other pooled connections
  remain available.
- Saved prices are decoded once per setting revision and their canonical JSON
  is reused in cache fingerprints. Provider source selection resolves each home
  once per Usage call without repeated volume-identity discovery.
- Latest Codex quota reads cache the decoded observation until history changes
  or the next future quota timestamp becomes eligible. Returned windows are
  copied, so callers cannot mutate the cached observation.
- Collectors use directory-entry metadata, preserve symlink following, batch
  checkpoints, count records by file primary key, and skip empty retention
  transactions. Codex range reads filter eligible files before replaying rows.
- Deduplication, source projections, priced histories and cycle groups avoid
  repeated slice growth. Cycle groups share disjoint slices of their sorted
  observations. Windows discovery skips absent native-package directories.

Regression tests cover price-cache reuse and revision invalidation, quota-cache
reads without record decoding, future/backwards-clock eligibility, independent
returned windows, and another Store's append/retention invalidation. Safe source
overlays restored the old functions without changing the working tree: both new
reuse regressions failed before their fixes, with `warm quota read decoded
record strings` and `unchanged prices were decoded again`; the full suite passes
with the fixes. Existing cycle tests continue to pin completed-fit reuse after
an append. Post-append projections still reread their bounded active ranges;
this round does not add a general per-record cache or claim that only the new
event is decoded.

The real v1 copy was also read with the legacy JSON storage implementation and
equivalent bounded readers. Its complete Usage JSON, including cycleValues,
matched the migrated implementation byte for byte for 24h, 7d, 30d and 90d at
`2026-10-07T08:00:00Z`. The exact shared SHA-256 values were:

```text
24h e0bf5046af3ee6b76d125e8e39b5d1f861eb615fe7416b6f8770011cc761ae8c
7d  f082c90d19e2c361506c868b99815bd1cf38ba9472c051e4c18248745df152ce
30d d38459e89578ca115e764a15447d293e16ba83609122eeb90c4bdf30052b4667
90d f2a5baa6560c9fb9a69278f6477c8e5ef58a536303d80d8b1ca227ef02404b0b
```

The migrated copy retained 214,677 records and 170,958 quota observations;
`integrity_check` returned `ok`, and `foreign_key_check` returned zero rows.
The disposable harness, copied histories/databases, binaries and profiles were
removed after their results were recorded here. No protected frontend/tools
files or Git/index state were changed by this follow-up.

Actual final checks:

```text
go test -p 1 ./...  -> Go test: 273 passed in 20 packages
go vet ./...       -> exit 0, no diagnostics
gofmt -l .         -> exit 0, no files
go build ./...     -> exit 0, no diagnostics
git diff --check   -> exit 0, no diagnostics
```

Files changed in this follow-up: `internal/storage/{store.go,history.go,
cache_test.go,README.md}`, `internal/history/{collector.go,transcripts.go}`,
`internal/service/{service.go,rates.go,rates_cache_test.go,history.go,
cycle_values.go,discovery.go}`, and `internal/quota/cycle_prior.go`. Earlier
uncommitted work was preserved.
