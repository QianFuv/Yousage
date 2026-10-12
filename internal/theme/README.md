# Native themes

The generator ports the frozen t3code vivid palette engine. Six committed oracle
cases compare exactly across all 61 roles, including grayscale, alpha and OKLCH
seeds. The generator follows actual canvas luminance rather than its appearance
slot, and intentionally ignores seed alpha while portable direct role overrides
retain alpha. Culori's input matrix and the palette engine's derivation matrix
remain distinct. Reused code/data is MIT; see the root notice and CULORI-LICENSE.txt.

`builtins.json` is data exported from the pinned shared theme definitions without
changing them or the reference oracle. It contains five paired themes and the
ordered 61-role vocabulary. Standard T3 Code remains the unthemed frontend choice.

Strict portable v1 import and lenient environment publishing are different
contracts. Portable imports reject unknown roles, repeated base variants and bad
colors. Published files accept future-role records, filename identity, shorthand
seeds and sparse palettes; resolution drops unknown/bad values and fills defaults.
The preflight native color parser supports hex and OKLCH. Full upstream literal
CSS color import support, directory watching, once-per-generation adoption,
local overrides and disappearance fallback remain M5 obligations.

Publishing uses a Yousage-owned directory, a cooperative cross-process OS file
lock, staged/synced files and Windows replacement operations. Settings are read
before publishing; malformed/unreadable settings fail without a theme side effect.
If the settings commit fails, the original file is restored unless a newer writer
has replaced the published inode. Unknown settings keys survive. `clear` removes
only default/generation fields, retaining files and client selections.

```powershell
.\bin\yousage.exe theme set ocean --state-dir "$env:TEMP\yousage-theme-example"
.\bin\yousage.exe theme set .\night.json --id night --state-dir "$env:TEMP\yousage-theme-example"
.\bin\yousage.exe theme show --state-dir "$env:TEMP\yousage-theme-example"
.\bin\yousage.exe theme clear --state-dir "$env:TEMP\yousage-theme-example"
```

The default directory is the OS user configuration directory's `Yousage` child.
These commands never write installed t3code state. CLI dispatch runs before Wails
application construction and does not start a Node process or GUI.

```powershell
go test ./internal/theme/... -count=1
pnpm --dir tools/parity test:theme
go run ./tools/spikes/theme --app bin/yousage.exe
```

The last command exercises the actual production GUI executable's headless path
against a disposable isolated directory. Windows evidence is separate from
unavailable macOS/Linux native qualification.
