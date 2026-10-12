# Yousage

A standalone Go/Wails v3 desktop application reproducing Usage statistics and
themes from [t3code](https://github.com/pingdotgg/t3code/tree/51f7f77f87318a5329d3bab15d513fda5654200b),
frozen at `51f7f77f87318a5329d3bab15d513fda5654200b`.

## Running on Windows

Open `bin/yousage.exe` or extract `bin/yousage-0.1.0-windows-amd64.zip` and open
the included executable. The frontend is embedded; Node, pnpm, a t3code server
and a development server are unnecessary at runtime. The package is unsigned.
Windows 11 amd64 is the native qualification target.

The [Microsoft Edge WebView2 Evergreen Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)
must be installed. This portable package does not install or download it.

## Usage and Settings

The sidebar separates **用量统计** (tokens, estimated cost and history; the
default page) and **额度监控** (quota/account/reset observations). Only local
Codex and Claude Code are monitored; both are configured by default and can be
switched off in Settings. Settings stays at the bottom;
inside Settings the sidebar lists its sections with a back action, following
the t3code settings layout. Each page has a sticky header holding its title
and controls: segmented metric and 24h/7d/30d/90d toggles, an icon refresh action and an
overflow menu. Statistics provides provider
charts, clickable model rows with environment comparisons, and time breakdowns.
The content area scrolls independently. The desktop window keeps one layout:
it is at least 1024×680 at 100% scale, and dragging its edges keeps it between
3:4 and 21:9; maximizing and snapping are unrestricted. Viewports narrower than
768 CSS pixels, such as phone browsers, use a single-column mobile layout with
a bottom tab bar.
Controls use the original t3code button, toggle, select, switch, menu and
dialog primitives. Settings are grouped into cards of title/description/control
rows; staged source and connection drafts show a floating Apply/Discard bar.
Compact density reduces page and row spacing; typography and app scale adjust
text and controls together.
Totals distinguish unpriced observations from genuine zero cost. Manual and
foreground automatic refresh use separate bounded schedules. Price overrides
reprice retained facts without rereading provider logs.

Settings is a separate destination with five sections:

- **Appearance:** built-in/custom themes, collections, separate light/dark
  choices, color editor/inspector, JSON/JSONC/VS Code/OpenVSX imports, fonts with
  fallbacks, contrast, transparency and motion.
- **Display & behavior:** comfortable/compact density, 100–200% scale (lowered
  temporarily when the screen cannot hold the scaled minimum window), regional
  or explicit 12/24-hour timestamps, refresh and window restoration. Recenter
  recovers the window after display changes.
- **Sources & connections:** automatic provider discovery, quota hubs and
  directly authenticated remote environments. Staged changes use Apply/Discard.
- **Prices & mappings:** per-environment pricing, aliases, mixed values, undo,
  discard and retry of failed destinations.
- **About & diagnostics:** versions, licenses, and explicit preview/copy/export
  of a redacted report. No diagnostics are automatically uploaded.

Drafts survive navigation. Closing with unapplied changes requires explicit
discard; closing while account/settings writes are running waits for the result.
`Ctrl+Alt+A` opens Appearance; `Ctrl+Alt+Shift+A` cycles System/Light/Dark.
On macOS the shortcuts use Command instead of Control.

## Data sources and storage

Read-only collectors support saved Codex, Claude, Grok and OpenCode records,
Antigravity local databases, and authenticated Cursor Dashboard history.
Sources & connections discovers registered providers through PATH, supported
native npm payloads and standard environment locations. History roots resolve
on each scan; no path entry is required. Re-detection checks metadata without
querying an account. Existing custom configurations remain usable until you
explicitly switch them to automatic detection. History scanning and quota
account enablement remain independent. Cursor requires a supported saved login;
macOS Keychain access is explicitly enabled. An unavailable login does not
substitute a previously saved account's history.

Native quota adapters cover Codex, Claude, Grok, Cursor and OpenCode, configured
independently of history collectors. Antigravity quotas are unsupported.
CLIProxy hub observations/reset routes retain independent failure states.
Reset-credit consumption requires a deliberate user action; unknown results
are retained for recovery instead of automatically replayed.
Remote environments use direct Bearer-authenticated t3code Effect WebSocket
connections. Relay DPoP and SSH tunnels are unsupported.
Rates come from the [models.dev](https://models.dev) catalog, so models used
through gateways or other providers are priced too. A model's first-party
provider list price wins over reseller listings, and gateway-prefixed IDs
(`gateway/model`) price as the bare model. Fast and Ultrafast usage uses the
official OpenAI and Anthropic per-model multipliers built into the app (no
online lookup); models without an official faster tier stay at Standard rates.
Requests are bounded and cached;
offline views retain available cached rates and expose unpriced data.
Native account actions require the corresponding installed executable and a
supported login. No model turn is issued merely to obtain quotas.

Settings, retained 90-day facts, checkpoints and themes are stored under
`Yousage` in the OS user configuration directory, normally `%APPDATA%\Yousage`
on Windows. Published themes use `themes/`, `theme-default` and
`theme-generation`. The application's WebView profile uses its `webview/`
subdirectory. OS-native credential storage contains owned connection secrets;
these are excluded from diagnostics.

## Headless themes

Theme commands run before creating a GUI window:

```powershell
.\bin\yousage.exe theme set ocean
.\bin\yousage.exe theme set C:\Themes\my-theme.json
.\bin\yousage.exe theme show
.\bin\yousage.exe theme clear
```

`--state-dir <directory>` selects an isolated publication directory; `--id`
sets a file theme identity. Manual GUI choices override publication until a
new publication generation arrives. Clearing preserves published theme files.

## Building

Pinned: Go 1.26.2, Node 24.15.0, pnpm 10.32.0, Wails 3.0.0-beta.28.
Builds use frozen npm lockfiles and retain the Go dependency lock.

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28
wails3 doctor
wails3 task windows:build ARCH=amd64 -f
powershell -NoProfile -File build/package-windows.ps1
```

Use `wails3 task dev` on Windows for development. Build-time Node tooling is
not packaged. `build/dependencies.json` and `THIRD_PARTY_NOTICES.md` regenerate
from installed locked packages, including nested native runtime notices.

Native macOS builds require Xcode command-line tools and macOS 12 or later:
`wails3 task darwin:package ARCH=arm64 -f` or `ARCH=amd64`. The bundle receives
an ad-hoc signature; distribution signing/notarization is separate.
Native Linux amd64 builds require a C compiler, `pkg-config`, GTK 4,
WebKitGTK 6.0 and X11 development headers:
`wails3 task linux:build ARCH=amd64 -f`.
These requirements follow pinned Wails source. macOS/Linux native builds and
runtime qualification are deferred by the Windows-first delivery decision.

## Verification and provenance

```powershell
go test -p 1 ./...
go vet ./...
pnpm --dir tools/parity test
pnpm --dir frontend typecheck
pnpm --dir frontend lint
pnpm --dir frontend format:check
pnpm --dir frontend test
go run ./tools/spikes/theme --app bin/yousage.exe
```

See [integrated acceptance](docs/validation/acceptance.md),
[Windows qualification](docs/releases/0.1.0-windows.md),
[research](docs/t3code-usage.md), [feature map](docs/parity/feature-map.md) and
[third-party notices](THIRD_PARTY_NOTICES.md).
Original source hashes and tests are immutable oracles. Protocol fixtures do
not certify live accounts; browser sizing does not certify native DPI/dialogs.
Yousage is independent and does not imply T3 endorsement.
