# Six-provider capability evidence

All T3 paths refer to commit `51f7f77f87318a5329d3bab15d513fda5654200b`.
This is source-level feasibility evidence. No installed account/history or live
provider was read. Missing/unsupported/failed/partial are distinct states.

| Provider | Historical reader | Native quota and unsupported conditions |
|---|---|---|
| Codex | UsageService + usageTranscripts parseCodexRecord, incremental token deltas | CodexProvider account/rateLimits/read; API-key accounts unsupported; managed-auth path omits native limits |
| Claude Code | UsageService + usageTranscripts parseClaudeRecord, occurrence-aware cross-file copies | ClaudeProvider SDK experimental get_usage; unavailable rate_limits means unsupported; Go protocol is T3 preflight proof |
| Grok Build | UsageService scans saved updates.jsonl; parseGrokRecord completed-turn usage | grokUsageLimits GET cli-chat-proxy.grok.com/v1/billing?format=credits; default OAuth only; API-key/custom-auth/custom endpoint unsupported; no percent can be supported-empty |
| Cursor | cursorUsage account-wide historical read, separate from dashboard quota | cursorUsageLimits POST aiserver.v1.DashboardService/GetCurrentPeriodUsage; OAuth login; API-key-only/in-memory/no-token unsupported; bonus percentages retained; macOS Keychain opt-in only |
| OpenCode | openCodeUsage reads native DB/JSON histories | openCodeUsageLimits reads opencode.ai/zen/go/v1/usage for Go subscription only; external server/missing key/403 unsupported |
| Antigravity | antigravityUsageReader opens native .db readonly; protobuf gen/trajectory/step metadata | No native quota registration in pinned T3; omit Limits row. Hub also supports only Codex/Claude |

The registration traces are Drivers/GrokDriver.ts:149-165,
Drivers/OpenCodeDriver.ts:370-386, Layers/CodexProvider.ts:451-462,670-706,
Layers/ClaudeProvider.ts:370-396,593-625, Layers/cursorUsageLimits.ts:62-151,
Layers/grokUsageLimits.ts:43-174, Layers/openCodeUsageLimits.ts:32-119.

AntigravityDriver:384-393 calls makeAntigravityProvider. Its initial draft,
checkAntigravityProvider, makeManagedServerProvider and onSessionStarted in
Layers/AntigravityProvider.ts:137-290 never assign usageLimits. The now-empty
ProviderUsageLimitsIngestion layer adds nothing. shared/usageLimits.ts:60-74
requires a defined usageLimits field; cliproxyApi.ts:306 excludes this driver.
History roots in UsageService:620-655 include native/custom roots and existing
T3 profiles. Yousage must not create T3 profiles to monitor history. Missing tables,
changing protobuf schemas and readonly/WAL failures remain visible diagnostics.

## Grok record production outside T3

Official Grok Build snapshot `2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8`
provides the missing producer evidence. Its
[session documentation](https://github.com/xai-org/grok-build/blob/2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8/crates/codegen/xai-grok-pager/docs/user-guide/17-sessions.md)
describes automatic persistence in TUI/headless/agent stdio under
GROK_HOME/sessions/<encoded-cwd>/<session-id>/updates.jsonl (default ~/.grok).
[emit_turn_completed](https://github.com/xai-org/grok-build/blob/2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8/crates/codegen/xai-grok-shell/src/session/acp_session_impl/turn_end.rs)
requests durable persistence, updates.rs queues AppendUpdateDurablyAndAck, and
[the native JSONL writer](https://github.com/xai-org/grok-build/blob/2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8/crates/codegen/xai-grok-shell/src/session/storage/jsonl/mod.rs)
serializes/appends a timestamped envelope. T3 reads params.update.sessionUpdate
and usage/model metadata, not one exact method name. Its separate
ProviderEventLoggers observability file is not this history source.

This confirms independent production at these source versions; it does not prove
every installed Grok release emits compatible usage. Fixture/version checks are
still required. No absent records are reconstructed as real usage.
