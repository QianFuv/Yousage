# Integrated acceptance record

Reference: `pingdotgg/t3code` at
`51f7f77f87318a5329d3bab15d513fda5654200b`. Runtime: Wails
`3.0.0-beta.28`, Go `1.26.2`. This record distinguishes automated functional
evidence from native Windows interaction and live-account evidence.

## Feature traceability

| Requirement | Implementation | Executed verification |
| --- | --- | --- |
| S1 ranges, environment selection, visibility, refresh | `frontend/src/usage/UsagePage.tsx`, `controller.tsx`; `internal/service`, `internal/refresh` | UTC/calendar/DST and anchored-window tests; All versus None; foreground and Settings gating; browser navigation and keyboard |
| S2 totals, shares, sessions and provider curves | Original mutable Usage renderers; `internal/accounting`, `history`, `storage`; original shared merge | Hand-calculated billing tests and pinned TypeScript oracle; copied-source/fact/session dedup; browser Cost/Tokens and six-provider chart |
| S3 cost categories, cache, speed and model/day details | `UsageStatsPanel.tsx`, original chart/model/breakdown components | Reasoning contained in output; zero versus unpriced; cache and speed fixtures; details and model/day browser controls |
| S4 exact pricing, aliases and independent write targets | Original mutable price editor; `PricingPanel.tsx`; preferences and remote transactions | Zero/scientific/blank/cache/mapping validation; Mixed, undo, discard and failed-target-only retry; per-environment atomicity |
| S5 quotas, sparse pools, reset and pace | Original mutable Limits renderers; `internal/quota`; shared pool functions | Percent/pace boundaries and sparse/account dedup oracle; native/hub merge, freshness and unknown reset state fixtures; browser popup and exact observation copy |
| S6 native accounts and management | `internal/quota`, `managedprocess`, `provider/claudeprobe`; native action host | Correlated JSONL/HTTP adapters, supported-empty/unsupported/failed states, subprocess deadlines/tree cleanup; synthetic reset only |
| S7 six historical providers | `internal/history/**`; service scanner and source index | Codex/Claude/Grok incremental file tests; OpenCode SQLite/WAL/legacy; Antigravity protobuf/DB/WAL; Cursor paged Dashboard/auth/account dedup tests |
| Theme T1 palettes, mode, fonts and effects | `internal/theme`; appearance provider; original palette/CSS | All 57 roles and six palettes against 342-role goldens; full literal CSS differential cases; fonts/fallbacks; independent mode/owners; no-motion default |
| Theme T2 collections/editor/inspector | Root-mounted original mutable editor; `ThemePanel.tsx` | Surviving variant after deletion and raw half-owner tests; browser draft retention, preview, inspector and narrow editor; ordinary settings apply without erasing preview; OS-event fixture preserves draft |
| Theme T3 imports and Open VSX | Mutable original import/conversion code; bounded ZIP/client and native pickers | JSONC and atomic bounded batches, update/conflict/copy and color/alpha tests; checksum/license/includes/cycles/expansion fixtures; paste preview browser proof |
| Theme T4 publishing/generations/headless | `internal/theme` library/store/watcher and pre-GUI CLI | Local override and once-per-generation adoption, remote primary, missing publication, watcher/save failure tests; isolated executable CLI proof at packaging |
| Separate Settings and QoL | Five sections; native desktop hooks; authoritative SQLite preferences | Eight size/density pairs, 320 reflow and 200% equivalent editor; vertical scroll and reading position; separate source/account Apply; draft close guard; rejected slider rollback/retry; diagnostics whitelist |
| N1 distribution | Embedded frontend and Wails native host | Final Windows executable/startup/CLI/package checks recorded in release record; native interactive checklist remains required; macOS/Linux native qualification deferred |

The older `docs/parity/feature-map.md` is the inspected preflight inventory,
not the final runtime-version or completion record. The current architecture
uses Wails v3. No t3code server, Node process or browser credential storage is
shipped. Direct authenticated environments are supported; relay, DPoP and SSH
transports remain explicitly outside the accepted compatible boundary.

## Integrated source and regression evidence

`internal/service/parity_test.go` reads synthetic Codex and Claude JSONL, Grok
saved updates and OpenCode legacy messages through the real scanner, SQLite,
desktop environment projection and four periods. Independent expected token,
cost and session values are asserted. A zero-price patch must reprice retained
facts without rescanning or charging reasoning twice. Cursor and Antigravity
use their actual protocol/database reader fixtures, not fabricated live data.

Regression tests cover corrupt optional price cache, a valid download whose
cache cannot be written, active Cursor memory/API/ephemeral login hiding an
old saved account without deleting its retained facts, absent login versus
actual macOS Keychain opt-in, nonnegative summary duration, and an online
remote rates failure followed by a successful latest summary read.

An isolated Go overlay of the preceding committed service files demonstrated
the relevant regressions before the corrections. No reset, checkout or discard
was used. The current tests pass. Overlay copies are non-source `.go.txt`
evidence, excluded from the application package graph.

The approved import policy is an atomic, bounded batch. Mixed invalid imports
are rejected before mutation; this deliberate application contract is tested.

## Checks

- `go test -p 1 ./...`: all application and fixture packages pass; unchanged
  packages may reuse Go's input-keyed cache.
- `go vet ./...`, `gofmt -l app.go desktop.go main.go internal tools/spikes`
  and `git diff --check`: pass, with no unformatted authored Go files.
- `pnpm --dir tools/parity test`: 56 immutable source hashes verified and
  14 original TypeScript oracle tests pass.
- Frontend typecheck, scoped ESLint, Prettier, 19 tests in five files and
  production build pass. Production inputs exclude the development transport
  fixture.
- Controlled browser observations validate route separation, root-mounted
  drafts, actual `main.scrollTop` restoration, eight viewport/density pairs,
  compact spacing, long settings scrolling and small-editor actions.

Evidence remains in the ignored task artifacts. Browser sizing is CSS
verification; it does not certify Windows WebView2 DPI, native dialogs,
caption colors, OS focus, display removal or process shutdown.

## Sidebar, discovery and performance revision

The sidebar separates quota monitoring (额度监控) and history statistics
(用量统计), with Settings at the bottom. Actual production root CSS supplies
viewport height and independent content scrolling. Browser checks cover all
five settings sections, 320px and 420px widths, retained settings drafts,
and model detail comparisons including per-environment cached tokens.
Controls share 14px text and 36px/32px heights by density. Root font changes
scale titles and controls; theme editor controls use the same tokens.
Secondary actions retain keyboard access through compact disclosures; Escape
returns focus, and menus near the bottom open upward within the viewport.

Provider discovery checks native executable payloads and metadata only. Tests
cover Windows PATH quoting/casing, native npm/pnpm payloads and Volta fallback,
automatic roots changing between scans, empty overrides, Unix variable casing,
and preservation of explicit legacy roots. Detection does not enable accounts.

History synchronization no longer decodes every retained fact solely to return
scan metadata. Eligible-file SQL skips expired files before decoding while
preserving complete-file deduplication and order. Regression tests demonstrate
excluded corrupt records do not affect current windows and retained facts
remain available after metadata-only synchronization.

The isolated benchmark uses 90 files containing 9,000 synthetic Claude facts.
Three-iteration samples compare cached 1/7/30/90-day queries. The 1-day query
changes from 48.28ms and 14.46MB allocated to 1.76ms and 0.84MB allocated;
7-day changes from 46.75ms/15.05MB to 5.16ms/2.36MB. The 90-day query remains
roughly unchanged (54.79ms/27.29MB versus 60.87ms/27.16MB). These allocations
are query costs, not whole-application resident memory measurements.

Settings, statistics, the theme editor and OpenVSX ZIP handling load lazily.
Visited settings remain mounted to retain drafts. Market requests begin only
when browsing or searching the catalogue. Final startup artifact/process
measurements are recorded in the Windows release record.

## Remaining qualification boundaries

No real provider, hub or remote account was enabled for implementation tests.
Live-account comparisons require user-enabled sources and remain unavailable,
separate from the implemented fixture-tested protocols. No live reset credit
was consumed. Windows final-package native observations are required by V3
and recorded separately; macOS/Linux native environments are deferred by the
user's Windows-first instruction. Do not describe these as passed browser tests.
