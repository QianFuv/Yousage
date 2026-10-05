# Yousage architecture

Reference: t3code `51f7f77f87318a5329d3bab15d513fda5654200b`.
Execution authority: the user's explicit approval of Wails v3 and subsequent
in-task changes through completion. Windows qualification is required now;
macOS/Linux native qualification waits for those environments.

## Runtime

One Go application embeds React/Vite assets in Wails `3.0.0-beta.28`, with matching
`@wailsio/runtime`. Node/pnpm are development tools, not shipped services. Provider
CLIs and platform WebViews are external dependencies and have explicit capability
states. No local t3code server is required. Optional existing remote t3code
environments use the bounded compatible protocol documented in
[the remote contract](parity/remote-contract.md).

The preflight proved Windows production startup/theme synchronization/shutdown,
managed native Claude JSONL control transport and headless palette publishing.
It does not prove live usage, full UI interactions, DPI or non-Windows packages.
Windows frame colors use documented DWM attributes without rebuilding the window.
The initially hidden WebView must not wait for animation frames before becoming
visible: hidden WebViews can suspend them. Commit the DOM theme, synchronize its
native background, then reveal the window. Keep startup failures reviewable.

## Domain and storage

Keep Environment, ProviderInstance, AccountIdentity, SourceIdentity and QuotaRoute
distinct. One local environment can contain multiple accounts and roots.
Normalized history records preserve source/session identity, provider, time,
model, input/output/cache-read/cache-write/reasoning tokens, speed and cost
provenance. Reasoning is contained in output; no double charge. Alias and price
overrides reprice historical local records; remote aggregates remain authoritative.

SQLite stores records, incremental checkpoints, settings, prices, aliases and
observations. Select pure-Go `modernc.org/sqlite v1.60.1`, verified from official Go
module metadata (Go minimum 1.26.0) and primary driver documentation. Its selected
graph upgrades x/sys to 0.48.0; rerun the existing Windows subprocess/theme tests
and native smoke after that change. Use per-connection DSN pragmas for foreign
keys, bounded busy waits and WAL. Migration and source-record/checkpoint updates
are transactions. Query dates use injected now/IANA zone inputs with calendar
and DST semantics; stale time does not imply a periodic scanner.

JSONL checkpoints retain completed-newline offsets, metadata, reducer state and
a 64-byte FNV guard. Separate incomplete tails; invalidate on shrink/rewrite.
Read failures retain successful cached observations. Remote partial snapshots
retain nonoverlapping successful data with explicit source identities/freshness.

Go/SQLite is the saved-preference authority. Frontend state contains unsaved
drafts and transient context. The separately published theme default/generation
file is an external input, not a second display-preference database. Apply each
generation once to saved theme owners; clear leaves current owners unchanged.

## Sources and limits

Implement the six history providers using the inspected capability table:
Codex/Claude JSONL, Grok completed-turn records, authenticated Cursor Dashboard
history, WAL-consistent OpenCode SQLite/legacy JSON, and Antigravity conversation
databases/profiles. Preserve custom roots/homes and disabled-account history.
Source absence or missing authentication is a notice, never fabricated totals.

History and subscription limits are independent. Limits adapters include native
Codex/Claude/Grok/OpenCode/Cursor routes and supported hub routes. Antigravity
absence of a registered limit path is evidence-backed. API-key/custom endpoints
that do not expose subscription limits remain unsupported.

Claude sends initialize/get_usage only, with 25-second initialization and 4-second
usage budgets. Unknown skip_behaviors can fall back once within the same deadline;
method/auth/timeouts cannot. Windows processes are created atomically inside
kill-on-close Jobs with restricted inherited handles. Fixtures prove ordinary
process-tree cleanup, not arbitrary external broker control or live CLI support.

Limits share in-flight reads, automatically cool down five minutes from
completion including failure, and preserve successful observations after errors.
Manual refresh bypasses cooldown. Deduplicate by account/source and pool equal
account percentages; retain sparse windows and independent reset credits/routes.
Reset uses explicit confirmation, operation state and unknown-result handling.
No implementation test consumes real reset credits or silently retries a timeout.

## User interface and settings

Usage and Settings are separate destinations. Usage contains Cost/Tokens/Limits,
environment filters, totals, shares, charts, token/model details, breakdowns,
quota rows and account-scoped Manage/reset. First visit uses Limits and 30 days;
Limits disables the time selector. There is no invented provider/date filter.

Settings contains five sections: Appearance; Display and behavior; Sources and
connections; Prices and aliases; Advanced and diagnostics. Price drafts have
fixed transaction targets independent of current view filters, Mixed cells,
atomic per-environment patches and retries for failed targets only. Display
preferences apply immediately; theme preview has Save/Cancel; connections and
prices have explicit Apply.

Keep the floating theme editor and DOM inspector above routes. Preserve drafts,
selection and reading context across navigation. Theme modes and light/dark
owners are separate fields. Preserve all 57 roles, six standard/built-in choices,
paired variants, imports, collections, advanced families, preview/cancel, fonts,
contrast, glass, motion, Open VSX and publishing behavior. The preflight hex/OKLCH
parser is a bounded proof; complete literal CSS import compatibility remains work.

Comfortable retains original spacing; Compact changes only spacing. Density,
text/UI scale and system DPI are independent. Test both densities at CSS
480x600,720x800,1024x768, 1440x900, plus 200% text/UI scaling and separate native
DPI 100/125/150/200%. Wrap filters, constrain editors, limit form width and scroll
wide tables locally. Keep values, stable quota columns and keyboard focus usable.

Restore normal bounds/maximized state safely within current monitor work areas;
never restore minimized. Provide recenter, exact-value copy, keyboard help,
Ctrl/Cmd+, for Settings and redacted diagnostics. Closing exits with draft and
external-operation guards. Hidden/minimized/Settings routes start no new automatic
refresh; existing work settles. No implicit tray, autostart, notifications or upload.

## Verification and limits

Keep accounting, functional, visual and native conclusions separate. Compare
against immutable TypeScript fixtures plus independent calculations. Current
native observations are Windows startup/lifecycle and headless execution only.
Original component captures are browser reference evidence. Final integration
requires feature interactions, responsive states and fresh Windows observations
against the delivered executable; browser tests cannot substitute for native DPI,
dialog/drop, OS appearance and process-exit evidence.

No remote publication, paid signing, store submission, auto-update or unrelated
Git operations are included. Exact implementation task/commit boundaries and
execution evidence live in the ignored task contracts.

Primary driver documentation: [modernc SQLite](https://pkg.go.dev/modernc.org/sqlite).
