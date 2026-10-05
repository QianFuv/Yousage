# Native subscription limits

These adapters follow t3code commit
`51f7f77f87318a5329d3bab15d513fda5654200b`, independently of historical spend.
Codex uses initialize/account/read/account/rateLimits/read without model turns;
Claude uses the controlled initialize/get_usage protocol. Cursor, Grok and
OpenCode Go use the pinned authenticated HTTP endpoints and auth precedence.
Antigravity has no native quota adapter in this reference.

Percentages are clamped, main Codex buckets exclude Spark, reset timestamps retain
their provider units, Claude scoped windows retain stable IDs, and successful empty
Grok limits can recover later. Failed probes preserve last-good bars; authoritative
unsupported results replace them. Sparse unchanged updates preserve checked time.
Malformed explicit nulls cannot silently become unsupported accounts.

Native children are owned by managedprocess (Windows atomic Job Object assignment;
Unix process groups and owned pipes). All paths stop descendants and drain/close
pipes. Grok production credential reads run in an owned, ten-second helper before
the independently bounded ten-second billing request. Cursor Keychain access is
opt-in and restricted to the default endpoint. Native keyring subprocesses use a
separate owned helper, with credentials on inherited stdin rather than arguments.

Fixtures send no model turns or reset requests. They verify fragmented/correlated
replies, deadlines, redaction, account identity, schema failures and Windows parent,
child and grandchild termination. The actual GUI-subsystem executable's headless
dispatch is exercised. macOS/Linux native qualification remains deferred; compiling
or reading their implementation does not establish a native runtime pass.

## Hubs, pooling and resets

CLIProxyAPI uses origin-relative management auth-files, api-call and reset-quota.
The provider body/data are JSON strings, and the hub substitutes `$TOKEN$`.
Account requests are limited to four. Credits outages preserve quota windows;
list failures and account failures stay distinct. Native secrets hold management
keys; the application integration supplies the key callback rather than serializing
keys into frontend configuration.

Accounts merge by driver plus normalized email, then credential fingerprint, then
source identity. Parsed instants select fresh windows/credits independently. Hub
reset routes win because redemption must clear routing cooldown. Pool windows use
equal account weights, fixed sparse columns, timed-member pace and reset shares.
Duration minutes obey the pinned server contract's NonNegativeInt (a non-integral
hub seconds/60 result is rejected rather than publishing an invalid wire value).

Reset operations use deterministic hub UUIDv5/account-credit identity or a caller
persisted native idempotency key. The gate serializes refresh/reset and reads current
configuration after waiting. Confirmed outcomes cannot replay; failed/unknown
outcomes require explicit retry. Native consumption rechecks the selected email.
Successful spend plus failed refresh/cooldown is a warning, not a failed spend.
Remote operations stay at their owning environment and require operate scope.
F11 supplies persisted recovery state and frontend confirmation; these library
fixtures never consume real credits.
