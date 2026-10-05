# Pinned reference baseline

The reference is t3code commit `51f7f77f87318a5329d3bab15d513fda5654200b`.
`tools/parity/sources.json` records the Git blob ID, SHA-256 and byte length of
each unmodified copied file. `sync-upstream.mjs` retrieves only exact pinned raw
URLs and verifies the Git blob before writing. `verify-sources.mjs` verifies both
hashes before tests. The application monorepo is not installed or launched.

Run `pnpm --dir tools/parity install --frozen-lockfile`, then
`pnpm --dir tools/parity test` and `pnpm --dir tools/parity reference`.
The viewer is bound to localhost port 4174. It contains original chart, share bar,
theme preview, wireframe and UI primitives with synthetic fixed data. The host
layout is a reference workbench, not a screenshot of the full original Usage page.

## Oracle and plumbing boundaries

The original pricing, aggregation, transcript parsers, merge, quota math,
formatting, theme generator/parser and component bodies are unmodified.
The contracts bridge supplies constants and identity branding for already valid
fixtures; it does **not** test Effect schema validation or remote interoperability.
The availability predicate and cn rules are copied from the inspected originals.
The six provider SVGs and ProviderInstanceIcon are original. ACP is excluded and
its bridge throws if accidentally rendered. Its unused initials fallback is not
a provider identity parity claim. There is no credential, filesystem history,
remote socket, reset-credit or model-turn connection.

The original ThemeEditorPanel/inspector and nested color controls are also retained.
Its library uses this localhost origin's isolated localStorage when Save is used;
the read-only baseline interactions use Open/Navigate/Cancel only. It cannot access
the Wails application's separate origin. The workbench editor is mounted above
fixture navigation and applies a draft to actual component DOM. Morphicons 1.7.1
uses its React entry; no React Native dependency is installed.

Source scripts use `.mjs` because they must run before TypeScript dependencies
are installed. Application fixture source uses `.tsx`; Vite's config retains its
tool-standard `.ts` extension. Automatic JSX matches upstream. React/DOM 19.2.6,
Tailwind 4.3.3, Base UI 1.5.0, lucide-react 0.564.0, culori 4.0.2 and the patched
Effect 4.0.0-rc.115 match the pinned upstream lock. Vite is the upstream alias
`@voidzero-dev/vite-plus-core@1.0.0` (bundles Vite 8.3.1), not Vite version 1.
Scripts invoke its explicit CLI path: this package exposes no `vite` binary,
so a bare `vite` command accidentally found a global 7.1.12 during preparation.
That preparation build/screenshot evidence was superseded after correcting it.
Lucide 0.564.0 publishes its ESM entry below dist/esm/lucide/src despite its
package module field; the resolver points at the actual original file.
The alias produces peer-version warnings; actual tests/build determine viability.
Dependencies and licenses can be reproduced from package.json/pnpm-lock.yaml;
t3code's retained MIT license is in THIRD_PARTY_NOTICES.md and upstream/LICENSE.

## Comparison method

Use identical fixture inputs, theme, appearance, viewport, zoom/DPI and reference
time. Compare chart geometry, bands/axes/tooltip, semantic CSS colors, six-provider
icons, preview geometry, portal layering and focus behavior. Exclude workbench
heading, native titlebar, browser extensions and OS font rasterization from pixel
equality. Compare role colors and numeric outputs exactly where upstream rounds;
for color-conversion ports, require recorded numeric channel tolerance, rather
than accepting an unspecified visual resemblance. Geometry deviations must be
measured and recorded. Screenshots alone do not prove focus or native dialogs.

`fixtures/theme-golden.json` contains six complete 57-role palettes (two modes,
three seed pairs including grayscale/alpha/oklch). A deliberate update uses
`UPDATE_REFERENCE=1 pnpm --dir tools/parity test:theme`; normal tests only compare.
Hand-calculated tests additionally guard token reasoning subsets, reported vs
override prices, cache fallback, three timezones/DST, aliases, source ownership,
distinct sessions and quota identity/sparse equal-account pooling.

See [feature map](feature-map.md), [capabilities](capabilities.md),
[remote contract](remote-contract.md) and [screenshot manifest](screenshots.md).

Vendored upstream bytes disable Git newline conversion. The original Effect patch contains an empty context line whose required single-space prefix triggers trailing-whitespace detection; only patch files disable blank-at-eol checking. Source digests retain that byte and authored files retain normal whitespace checks.
