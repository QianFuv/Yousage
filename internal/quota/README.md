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
