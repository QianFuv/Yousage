# t3code usage-monitoring implementation notes

Inspected on 2026-10-06 at commit
[`51f7f77f87318a5329d3bab15d513fda5654200b`](https://github.com/pingdotgg/t3code/tree/51f7f77f87318a5329d3bab15d513fda5654200b).
These are source-reading findings and proposed directions for Yousage. The
upstream application and its tests were not executed.

## Two separate kinds of usage

| Concern | Input | Result |
| --- | --- | --- |
| Historical consumption | Native provider transcripts and databases | Tokens, sessions, model breakdown, API-equivalent costs |
| Subscription allowance | Provider status probes and runtime events | Used percentage, rolling window, reset time, availability |

The historical scanner includes work performed outside t3code. Local token
counts do not establish the percentage remaining in a subscription allowance,
and API-equivalent costs do not establish the actual subscription bill.

Source: [UsageService](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/usage/UsageService.ts#L1-L12)
and [usage contract](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/packages/contracts/src/usage.ts#L100-L107).

## Subscription allowance collection

- The native Codex probe requests `account/rateLimits/read` through app-server
  JSON-RPC. Runtime `account/rateLimits/updated` notifications use the same
  mapping. The probe limits the usage request to three seconds; a usage failure
  need not fail account and model discovery.
- Claude's native probe calls the SDK's experimental usage method, corresponding
  to `get_usage`. During a turn, `rate_limit_event` supplies partial updates.
  This SDK surface is explicitly experimental, so a Go implementation needs a
  version-aware adapter rather than assuming a stable cross-language API.
- Both map onto windows containing stable `id`, `kind`, `label`, `usedPercent`,
  optional `resetsAt`, and optional `windowDurationMins`.
- Sparse events merge by window ID. Missing reset times and durations retain
  prior values; unchanged events do not allocate a new snapshot. Codex
  model-specific limits are prevented from replacing the main account window.
- A failed probe preserves the previous successful snapshot. An authoritative
  `unsupported` result replaces it. Keep the observation time visible so old
  data can be identified.
- Additional configured CLIProxyAPI sources expose pooled accounts. They
  refresh on configuration changes and the provider health interval, serialize
  refreshes, and publish snapshots through server configuration subscriptions.
  Their failures remain visible as source errors. The default health interval
  is five minutes and background policy can suppress automatic work.

Sources: [Codex probe](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/provider/Layers/CodexProvider.ts#L440-L470),
[Claude probe](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/provider/Layers/ClaudeProvider.ts#L369-L380),
[snapshot merge](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/provider/providerUsageLimits.ts#L51-L140),
[external sources](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/usage/UsageLimitSources.ts),
and [health interval default](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/packages/contracts/src/settings.ts#L1034).

## Historical token and cost accounting

The pipeline is provider records -> normalized `UsageRecord` -> deduplication
and time buckets -> pricing. A record preserves provider, timestamp, model,
session, token categories, optional reported cost, speed, and a deduplication
key. Aggregation supports requested timezones and source/model/provider splits.

JSONL parsing caches records per path with `(size, mtime, provider)` metadata.
An unchanged file reuses parsed records; an appended file resumes at the last
complete newline. The checkpoint also retains parser state and a 64-byte
FNV-1a guard before the offset. A guard mismatch, shrink, or same-size rewrite
causes a full parse. The unfinished tail is stored separately and re-read on
the next append. A failed read does not become a successful empty cache entry.

Token accounting must preserve provider semantics:

- Claude repeats a message's usage across content blocks. Its message/request
  identity prevents counting each block as a separate request.
- Codex contributes `last_token_usage` deltas rather than summing cumulative
  totals. Consecutive duplicate events are dropped. Cached input and cache
  writes are subtracted from inclusive input to derive uncached input.
- Reasoning tokens already belong to output tokens and are not charged again.
- Copied Codex rollouts require cross-file deduplication. Fork-history
  suppression also uses a timing heuristic, which needs boundary tests if
  adopted.

Pricing prefers explicit user overrides, then provider-reported cost, then
public model rates. Unknown prices preserve token counts and an `unpriced`
status. The LiteLLM rate table has a 24-hour cache lifetime, with a 60-second
minimum interval even for forced refreshes. Failed downloads can reuse an old
table. Normalized records allow repricing without reparsing raw transcripts.

Sources: [incremental scan](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/usage/UsageService.ts#L450-L518),
[transcript reader](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/usage/usageTranscriptReader.ts),
[provider parsing](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/usage/usageTranscripts.ts#L96-L338),
and [pricing](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/usage/usagePricing.ts#L295-L339).

## Refresh coordination

Historical queries have a 60-second client stale time; this is not a
one-minute polling schedule. Concurrent equivalent server requests share a
scan, which continues if one caller disconnects. Manual refresh updates pricing,
invalidates the summary even if pricing fails, and awaits a new summary while
the environment remains connected.

Allowance refresh tracks one in-flight request per environment. Automatic
refresh skips duplicate work and observes a five-minute cooldown after
completion, including failure. Manual refresh bypasses the cooldown, but
normally awaits an existing check instead of starting another one.

Sources: [client refresh coordinator](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/packages/client-runtime/src/state/usage.ts#L50-L135),
[query stale time](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/packages/client-runtime/src/state/server.ts#L1084-L1090),
and [shared server scan](https://github.com/pingdotgg/t3code/blob/51f7f77f87318a5329d3bab15d513fda5654200b/apps/server/src/usage/UsageService.ts#L933-L974).

## Suggested direction for Yousage

Start with a Codex allowance reader and a CLI that prints windows, reset times,
and last observation time. Use separate interfaces for allowance collection
and historical records. Add Claude through an independently verified adapter,
then add transcript accounting if historical consumption is required.

For Go, the useful building blocks are `os/exec` and `encoding/json` for a CLI
protocol adapter, `context.Context` for bounded requests, and a mutex plus
per-account in-flight state for coordinated refreshes. Historical collection
can use a streaming reader with explicit byte offsets, normalized records,
and pure aggregation/pricing functions. Large JSONL lines require an explicit
size policy; a default `bufio.Scanner` token limit would be unsuitable.

Keep unavailable, stale, unsupported, and unpriced states distinct. Essential
future regression cases include sparse window updates, transient failures,
duplicate usage events, partial trailing lines, file replacement, and request
cancellation. The present scaffold implements only Go initialization.
