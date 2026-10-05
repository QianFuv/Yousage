# Native Claude control probe

This package sends only `initialize` and `get_usage` control requests. It does
not submit a prompt or bundle the Claude SDK, CLI or Node. Windows requires an
explicit native `.exe`; shell shims are rejected. Source evidence is t3code
`51f7f77f87318a5329d3bab15d513fda5654200b` and SDK 0.3.276, not a universal CLI
compatibility guarantee. Actual installed CLI/account qualification remains M4/M7.

Initialization has a 25-second budget including spawn; usage receives a separate
four-second budget. Only a correlated unknown `skip_behaviors` field triggers one
fallback without that field, within the same usage deadline. Unknown `get_usage`,
authentication errors and timeouts do not trigger a retry. Usage failure preserves
the successful initialization result; unavailable quota remains unsupported.
Utilization is already a percentage from zero through 100.

The argv/environment mirror the inspected SDK's no-prompt transport, strict empty
MCP configuration, disabled hooks and IDE auto-connect, and no session persistence.
Managed CLI policy can still override settings; these flags do not certify that
all possible managed hooks are disabled. Account homes use `CLAUDE_CONFIG_DIR`.
No private account is queried by the fixture tests.

Windows creates each process atomically inside its own kill-on-close Job Object
using `PROC_THREAD_ATTRIBUTE_JOB_LIST`, plus a restricted inherited stdio-handle
list. There is no assign-after-start race and no fallback to an unmanaged process.
Cleanup terminates the job, reaps the root and closes/drains pipes on success,
error, cancellation and deadline. It covers ordinary CreateProcess descendants;
it does not claim control over existing external brokers/services. POSIX uses an
isolated process group; native platform qualification remains pending.

JSONL frames are bounded to one MiB and total stdout to 16 MiB. Invalid UTF-8 or
oversized output fails the probe. Non-JSON banners, unrelated control IDs and
keep-alives cannot satisfy a request. Stderr is drained without retaining it.

```powershell
go test ./internal/provider/claudeprobe/... -count=1 -timeout=45s
go vet ./internal/provider/claudeprobe/...
```

The controlled Go fixture is `tools/spikes/claude`. Tests cover fragmented/noisy
output, stderr backpressure, correlation, capability/error states, fallback,
independent deadlines, cancellation, account isolation, early exit, oversized
frames and immediate children/grandchildren. They prove transport and lifecycle
behavior; they do not prove a live subscription response.
