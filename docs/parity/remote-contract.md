# Pinned remote adapter boundary

T3 `51f7f77f87318a5329d3bab15d513fda5654200b` uses Effect 4.0.0-rc.115
framed JSON RPC, not JSON-RPC 2.0. Source anchors: contracts/environmentHttp.ts,
auth.ts, rpc.ts, environment.ts, server.ts; client-runtime/connection/onboarding.ts,
compatibility.ts, authorization/remote.ts, rpc/session.ts; server/ws.ts and
auth/RpcAuthorization.ts. The pinned Effect patch adds request hooks without
changing these envelopes. This document is not an interoperability test.

1. GET /.well-known/t3/environment and verify identity/protocol before spending
   a one-time pairing credential. Current protocol is 2; omitted means 1.
2. POST /oauth/token form-urlencoded with token-exchange grant, subject_token,
   subject_token_type=urn:t3:params:oauth:token-type:environment-bootstrap,
   requested_token_type=urn:ietf:params:oauth:token-type:access_token and optional
   narrowed scope. Store returned bearer access token in the native secret store.
   There is no inspected refresh-token endpoint. Re-pair expired/revoked login.
3. Authenticated POST /api/auth/websocket-ticket returns ticket and expiresAt.
   Connect /ws?wsTicket=...&orchestrationProtocol=2. Never put access token in URL.
   Protocol mismatch returns 426 orchestration_protocol_incompatible.

Request envelope: `_tag:Request, id, tag, payload, headers:[]`.
Terminal: `_tag:Exit, requestId, exit:{_tag:Success,value}` or tagged Failure cause.
Chunks use values[] and each consumed chunk requires Ack. Cancel uses Interrupt.
Accept object or array per frame, correlation, Defect and Ping/Pong. Upstream
ping interval is five seconds; an unanswered preceding ping times out next tick.

| Method | Scope / behavior |
|---|---|
| subscribeServerConfig {environmentThemes:true,usageLimitSources:true} | read; snapshot, then provider/settings/theme/hub changes; optional themes/hubs arrive separately from initial snapshot |
| server.getUsageSummary | read; inclusive sinceDay/untilDay and timezone; optional hourly sinceTime inclusive/untilTime exclusive |
| server.refreshUsageRates, server.getSettings | read |
| server.updateSettings | operate; map-entry price/alias patch, null deletes named entry; not full stale map replacement |
| server.refreshProviders | operate; {} refreshes all hubs too; targeted instance skips all-hub refresh |
| provider.consumeResetCredit | operate; native instance or source/account/credit route; explicit action only |
| server.probe | read; capability connectionProbe only; otherwise getConfig |

Scopes above abbreviate orchestration:read/operate. Missing capability means
unsupported. Flags include usagePriceOverrides, usageModelAliases,
usageLimitSources, environmentThemes, connectionProbe. Do not advertise
usageLimitsCommand (the chat command is not handled by this standalone app).
Clear stale optional projections when switching/reconnecting to an incompatible
host. No automatic replay of settings/reset writes; settle destinations separately
and retry only failed targets. Reset outcomes are reset/nothingToReset/noCredit/
alreadyRedeemed; a warning can accompany a successful redemption.

Usage summary v6 merges compatible v4-v6, independent of wire protocol 2.
Physical key=(hostId,provider,resolvedHomePath,volumeId). Complete beats partial
beats failed, then newest readAt, then environment ID. Newer partial supplements
only absent day/hour/provider/model cells. Count owned distinctSessions, not
summed bucket sessions; supplementation takes max distinctSessions.

Quota key is driver+normalized email, then credential fingerprint, then native
environment/instance or hub source/account fallback. Freshest checkedAt supplies
windows; freshest actual credit snapshot retains credits. Hub redemption wins
even when displayed native windows are fresher because it also clears routing
cooldown. Pool account percentages equally; preserve sparse columns.

The future Go adapter needs offline object/batch/ACK/interrupt/failure, 401/426,
identity-before-exchange, capability, summary-version/source/credit fixtures.
Direct bearer cannot claim managed relay, DPoP or SSH parity; those need explicit
full-contract decisions. Live host, secret store and TLS/proxy behavior remain
unverified. Returned expiry/error fields govern lifecycle, not guessed constants.
