# Full parity inventory and destination map

Every source below is inspected at t3code `51f7f77f87318a5329d3bab15d513fda5654200b`.
Paths are upstream paths, not Yousage implementation completion claims. The
roadmap IDs are preserved separately from the preflight task IDs.

| Inventory | Inspected upstream source | Future Yousage boundary | Verification destination |
|---|---|---|---|
| S1 navigation/range/environments/preferences/refresh/shortcuts | apps/web/src/components/usage/UsagePage.tsx; usagePagePreferences.ts; usageShortcuts.ts; state/usage.ts | frontend usage route/store, internal/app snapshot service | M6 range/default/selection/offline and keyboard scenarios |
| S2 totals/distinct sessions/provider shares/curves | UsageProviderChart.tsx; UsageShareBar.tsx; packages/shared/src/usageMerge.ts | retained components; Go aggregation/source merge | T1 hand oracle and reference; M3 Go fixtures; M6 resize/hover |
| S3 categories/cache/speed/model details/trends/day-hour tables | usageBreakdown.ts; UsageModelDialog.tsx; UsagePage.tsx; usagePricing.ts | usage DTO/calculator and retained details UI | T1 pricing oracle; M3 rounding/provenance; M6 detail screenshots |
| S4 exact prices/aliases/mixed targets/partial saves | UsagePriceOverrides.tsx; usagePriceTargets.ts; usagePriceForm.ts; packages/contracts/src/settings.ts | exact map-entry patches, environment-target coordinator | M3 aliases/prices, M6 zero/scientific/blank/cache/undo/retry fixtures |
| S5 quota identities/sparse pools/reset/pace | packages/shared/src/usageLimits.ts; UsageLimitsPooled.tsx; UsageLimits.tsx | internal/usage quota projection; retained pooled UI | T1 hand oracle; M4 dedup/freshness/hub tests; M6 sparse layouts |
| S6 native quota/CLiProxy hubs/manage usage/reset | provider Drivers/Layers; usage/UsageLimitSources.ts; cliproxyApi.ts; UsageLimits.tsx | native probes, hub client, explicit reset coordinator | T3 Claude controlled proof; M4 live-read opt-in; reset action-only tests |
| S7 six history providers/roots/notices/compatibility | UsageService.ts; usageTranscripts.ts; cursorUsageReader.ts; opencodeUsageReader.ts; antigravityUsageReader.ts; usageMerge.ts | internal/provider readers, readonly DB, source scan/cache | M3 six reader fixtures/WAL/disabled history; M4 remote partial/version fixtures |
| Theme T1 standard/five pairs/OS/fonts/glass/motion | themePalettes.ts; themePalette.ts; index.css; hooks/useTheme.ts; hooks/useDefaultTheme.ts; themeBoot.ts | retained theme engine + native appearance bootstrap | T1 all-role/parser/golden; T2 native OS/first-frame/font/DPI; M5 mix/preferences |
| Theme T2 collections/editor/inspector | ThemeEditorHost.tsx; ThemeEditorPanel.tsx; themeEditorStore.ts; themeInspector.ts; ThemeSettings.tsx | root-layer editor and theme library/drafts | T2 root slice; M5 navigation/save/cancel/inspect/drag-resize |
| Theme T3 import/Open VSX/color/alpha | ThemeImportDialog.tsx; vscodeThemeImport.ts; openVsxThemes.ts; themePalette.ts | bounded archive reader, import coordinator, native picker/drop/paste | M5 batch/conflict/schema/alpha/family/resource/license fixtures |
| Theme T4 57 roles/v1/publishing/headless/generation | themePalette.ts; packages/shared/src/themePalettes.ts; apps/server/src/environmentTheme.ts; apps/server/src/cli/theme.ts | internal/theme generator/library/publisher; pre-GUI CLI dispatch | T4 Go golden and isolated CLI; M5 once-per-generation/watcher/disappearance |
| N1 standalone cross-platform packages | Wails v2.15 official module/docs; root native boundary | embedded frontend, platform packaging | T2 Windows build/runtime; M8 Windows distribution; macOS/Linux native deferred |

The complete source of the Usage directory is retained with hashes, including
surfaces not rendered by the bounded viewer. This inventory is a full contract
map, not a claim that all surfaces are implemented in preflight. No chat/editor,
model inference, terminal or remote relay runtime is part of the app's core scope.
Managed T3 Connect relay/DPoP/SSH parity needs an explicit implementation decision
in the full contract; direct bearer support cannot silently stand in for them.
