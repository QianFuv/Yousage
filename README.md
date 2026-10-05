# Yousage

An independent Go/Wails desktop app reproducing usage statistics and themes from
t3code, frozen at `51f7f77f87318a5329d3bab15d513fda5654200b`.

The current component preflight uses synthetic data. Live history collectors,
quota transports and complete theme persistence are subsequent implementation
milestones; the fixture screen is not a live usage report.

## Development on Windows

Pinned tools: Go 1.26.2, Node 24.15.0, pnpm 10.32.0, Wails 3.0.0-beta.28.
Node and pnpm are build tools; the production executable embeds its frontend.

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28
wails3 doctor
pnpm --dir tools/parity install --frozen-lockfile
pnpm --dir frontend install --frozen-lockfile
wails3 task windows:build ARCH=amd64 -f
.\bin\yousage.exe
```

Use `wails3 task dev` for the development host. Production requires the Microsoft
Edge WebView2 Evergreen runtime on Windows. It does not bundle that system
runtime or start a Node server. Missing-runtime bootstrap/installer qualification
belongs to the packaging milestone. This pinned beta's dialog implementation has
limited COM failure reporting; explicit cancellation is normalized separately.

Windows 11 is the current native verification target. Windows caption colors use
documented DWM attributes available from Windows 11. macOS and Linux remain
delivery targets; native qualification is pending access to those environments.

## Verification

```powershell
pnpm --dir tools/parity test
pnpm --dir frontend typecheck
pnpm --dir frontend lint
pnpm --dir frontend format:check
pnpm --dir frontend test
go test ./...
go vet ./...
```

Usage and Settings are separate destinations. The comfortable layout preserves
reference spacing; compact density changes spacing independently of text size.
Native interaction, OS appearance, DPI and final shutdown require actual Windows
observations and are recorded separately from browser/component evidence.

## Reference and licensing

See [implementation research](docs/t3code-usage.md),
[feature traceability](docs/parity/feature-map.md) and
[third-party notices](THIRD_PARTY_NOTICES.md). Reused upstream files retain their
source provenance and MIT notices. No t3code server or account credentials are
bundled with the app.
