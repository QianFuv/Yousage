# Reference screenshot manifest

Source: t3code 51f7f77f87318a5329d3bab15d513fda5654200b; local synthetic fixture
viewer, not the whole upstream application. Captured 2026-10-06 Asia/Shanghai
using Chrome browser CUA. Viewport 1480x820 CSS pixels, devicePixelRatio 2,
reference time 2026-10-06T12:00Z, chart timezone UTC, six days 2026-10-01..06.
Default font: -apple-system, BlinkMacSystemFont, Segoe UI, system-ui, sans-serif.
No webfont download. Browser extension injection is excluded from the app;
darkreader-lock prevents Dark Reader from rewriting colors, confirmed via DOM.
Full-page screenshot timed out; viewport captures are used and not represented
as full-page or native Windows screenshots.

| File | Input/action | Visible comparison surface |
|---|---|---|
| screenshots/light.png | appearance=light, default theme | original cost curves/share bar, first row of paired previews |
| screenshots/tokens.png | same URL, click Metric button | original token axes/curves |
| screenshots/ocean-dark.png | appearance=dark&theme=ocean | dark semantic theme mapping and preview geometry |
| screenshots/theme-cards.png | default light state, scroll down | all six standard/paired theme cards |
| screenshots/tooltip.png | token metric, pointer over Oct 5 | six actual provider icons and individual values/total |
| screenshots/menu.png | Ocean dark, open Fixture menu | original menu portal |
| screenshots/dialog.png | Ocean dark, open Fixture dialog | original popup/backdrop |
| screenshots/editor.png | Ocean dark, open Floating editor | original guided editor/color fields |
| screenshots/editor-navigation.png | editor open, Navigate fixture | editor/name retained across fixture navigation |
| screenshots/narrow.png | Ocean dark, 560x800 CSS pixels | responsive original chart/share/cards |
| screenshots/narrow-dialog.png | same narrow input, Fixture dialog | original mobile sheet geometry/focus |

Exact screenshot byte digests are recorded in screenshots/manifest.json. Fixture
and source identities live in tools/parity/sources.json, src/fixtures.tsx and lock.
UI overlays have additional states recorded after their actual interaction.
V3 Windows picker/drop/focus/DPI/shutdown still requires native evidence.
