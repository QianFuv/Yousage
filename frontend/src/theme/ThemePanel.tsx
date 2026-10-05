/** Complete desktop theme library, bounded imports and root-mounted original editor. */
import { useEffect, useRef, useState } from "react";
import { parse as parseJsonc } from "jsonc-parser";
import { Events } from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/app";
import type { ThemeFileInput } from "../../bindings/yousage/models";
import * as models from "../../bindings/yousage/internal/theme/models";
import { ThemeEditorPanel } from "../../vendor/ThemeEditorPanel";
import {
  getCustomThemes,
  getThemeDefinition,
  getThemeModes,
  parseThemeFile,
  serializeThemeFile,
  type ThemeDefinition,
} from "../../vendor/themePalette";
import {
  isVsCodeThemeFile,
  parseVsCodeThemeFile,
  pairVsCodeThemes,
  resolveThemeLabelCollisions,
} from "../../vendor/vscodeThemeImport";
import {
  importOpenVsxThemeExtension,
  searchOpenVsxThemes,
  type OpenVsxThemeExtension,
  type OpenVsxThemeSort,
} from "../../vendor/openVsxThemes";
import { BUILT_IN_THEMES } from "../../../tools/parity/upstream/packages/shared/src/themePalettes";
import { ThemeWireframe } from "../../vendor/ThemeWireframe";
import {
  getThemeCardDefinition,
  STANDARD_THEME_CARDS,
  ThemePreviewCircles,
} from "../../vendor/ThemePreviewCircles";
import { frontendTheme, nativeLibrary, useAppearance } from "./appearance";
import {
  Dialog,
  DialogPopup,
  DialogTitle,
  DialogDescription,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/dialog";
import "./theme.css";

type ImportDraft = {
  themes: ThemeDefinition[];
  revision: number;
  attribution?: OpenVsxThemeExtension;
};

/** Preview the latest range value locally and save only settled pointer/keyboard changes. */
function EffectSlider({
  label,
  min,
  max,
  value,
  unit,
  onSave,
}: {
  label: string;
  min: number;
  max: number;
  value: number;
  unit: string;
  onSave: (value: number) => Promise<void>;
}) {
  const [draft, setDraft] = useState(value);
  const last = useRef(value);
  useEffect(() => {
    setDraft(value);
    last.current = value;
  }, [value]);
  /** Avoid duplicate blur/up writes while acknowledging the final selected value. */
  function commit() {
    if (last.current !== draft) {
      last.current = draft;
      void onSave(draft);
    }
  }
  return (
    <label className="preference-row">
      {label}
      <input
        type="range"
        min={min}
        max={max}
        value={draft}
        onChange={(event) => setDraft(Number(event.target.value))}
        onPointerUp={commit}
        onKeyUp={commit}
        onBlur={commit}
      />
      <span>
        {draft}
        {unit}
      </span>
    </label>
  );
}

/** Parse a complete selected batch before presenting any install mutation. */
export function parseThemeBatch(
  files: { name: string; text: string; size: number; error?: string }[],
): ThemeDefinition[] {
  const parsed = files.map((file) => {
    if (file.error) throw new Error(`${file.name}: ${file.error}`);
    if (
      file.size > 256 * 1024 ||
      new TextEncoder().encode(file.text).length > 256 * 1024
    )
      throw new Error(
        `${file.name}: theme files must be no larger than 256 KiB.`,
      );
    const errors: import("jsonc-parser").ParseError[] = [];
    const value: unknown = parseJsonc(file.text, errors, {
      allowTrailingComma: true,
    });
    if (errors.length) throw new Error(`${file.name}: invalid theme JSON.`);
    return {
      theme: isVsCodeThemeFile(value)
        ? parseVsCodeThemeFile(value)
        : parseThemeFile(value),
      sourceName: file.name,
    };
  });
  return [...pairVsCodeThemes(resolveThemeLabelCollisions(parsed))];
}

/** Apply import collision decisions as one intended library value, leaving saved state untouched until commit. */
export function mergedImport(
  existing: readonly ThemeDefinition[],
  incoming: readonly ThemeDefinition[],
  shouldReplace: boolean,
  collectionId?: string,
): ThemeDefinition[] {
  const result = existing.filter(
    (theme) => !collectionId || theme.collection?.id !== collectionId,
  );
  for (const theme of incoming) {
    const position = result.findIndex((candidate) => candidate.id === theme.id);
    if (position >= 0 && collectionId)
      throw new Error(
        "This collection conflicts with a theme outside the collection. No themes were changed.",
      );
    if (position >= 0 && shouldReplace) {
      result[position] = {
        ...theme,
        collection: theme.collection ?? result[position].collection,
      };
      continue;
    }
    let identity = theme.id;
    let suffix = 2;
    while (result.some((candidate) => candidate.id === identity)) {
      const tail = `-${suffix++}`;
      identity = theme.id.slice(0, 48 - tail.length) + tail;
    }
    result.push({
      ...theme,
      id: identity,
      label:
        identity === theme.id
          ? theme.label
          : `${theme.label.slice(0, 40)} (${suffix - 1})`,
    });
  }
  return result;
}

/** Keep the movable, resizable inspector/editor alive while the page route changes. */
export function RootThemeEditor() {
  const context = useAppearance();
  const session = context.editor;
  if (!session) return null;
  const editing = session.editing
    ? (getCustomThemes().find((theme) => theme.id === session.editing!.id) ??
      null)
    : null;
  return (
    <ThemeEditorPanel
      key={session.identity}
      open
      editingTheme={editing}
      seedTheme={session.seed ?? session.editing}
      seedName={session.name}
      initialAppearance={context.appearance}
      restoreTheme={context.restore}
      onOpenChange={(isOpen) => {
        if (!isOpen) context.closeEditor();
      }}
      onSaved={async (theme, { created, mergedAppearance }) => {
        if (session.editing) {
          const saved = context.snapshot.value.customThemes.find(
            (value) => value.id === session.editing!.id,
          );
          if (
            !saved ||
            serializeThemeFile(frontendTheme(saved)) !== session.signature
          ) {
            context.restore();
            throw new Error(
              "This theme changed while the editor was open. Reopen the saved theme before replacing it.",
            );
          }
        }
        const preferences = { ...context.snapshot.value.preferences };
        if (created || mergedAppearance) {
          preferences.baseThemeId = theme.id;
          preferences.lightOwner = "";
          preferences.darkOwner = "";
          if (getThemeModes(theme).length === 1)
            preferences.mode = theme.appearance;
        }
        await context.save(
          nativeLibrary({
            ...context.snapshot.value,
            preferences,
            customThemes: getCustomThemes(),
          }),
          context.snapshot.revision,
        );
        return true;
      }}
    />
  );
}

/** Show independent mode/half ownership, original palette previews, imports, collections, fonts and effects. */
export function ThemePanel() {
  const context = useAppearance();
  const preferences = context.snapshot.value.preferences;
  const [status, setStatus] = useState("");
  const [paste, setPaste] = useState("");
  const [draft, setDraft] = useState<ImportDraft | null>(null);
  const [shouldReplace, setShouldReplace] = useState(true);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<OpenVsxThemeSort>("downloadCount");
  const [extensions, setExtensions] = useState<OpenVsxThemeExtension[]>([]);
  const [isSearching, setIsSearching] = useState(false);
  const [isDownloading, setIsDownloading] = useState(false);
  const download = useRef<AbortController | null>(null);
  const [fontNames, setFontNames] = useState<string[]>([]);
  const [fontDraft, setFontDraft] = useState(preferences.typography);
  const [collectionVariants, setCollectionVariants] = useState<
    Record<string, string>
  >({});
  const [removal, setRemoval] = useState<ThemeDefinition[] | null>(null);
  const [removedIds, setRemovedIds] = useState<string[]>([]);
  useEffect(
    () => setFontDraft(preferences.typography),
    [preferences.typography],
  );
  /** Settle one explicitly initiated action into a status message without hiding failed drafts. */
  async function action(execute: () => Promise<void>) {
    try {
      await execute();
    } catch (cause) {
      setStatus(String(cause));
    }
  }
  /** Stage a selected or dropped batch with the revision used to resolve its collisions. */
  function prepare(
    files: { name: string; text: string; size: number; error?: string }[],
  ) {
    try {
      const themes = parseThemeBatch(files);
      if (themes.length) {
        setDraft({ themes, revision: context.snapshot.revision });
        setStatus("");
      }
    } catch (cause) {
      setStatus(String(cause));
    }
  }
  useEffect(() => {
    const stop = Events.On("theme-files-dropped", (event) =>
      prepare(event.data as ThemeFileInput[]),
    );
    return stop;
  }, [context.snapshot.revision]);
  useEffect(() => {
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setIsSearching(true);
      void searchOpenVsxThemes(query, {
        signal: controller.signal,
        sortBy: sort,
      })
        .then((value) => {
          if (!controller.signal.aborted) setExtensions([...value]);
        })
        .catch((cause) => {
          if (!controller.signal.aborted) setStatus(String(cause));
        })
        .finally(() => {
          if (!controller.signal.aborted) setIsSearching(false);
        });
    }, 350);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [query, sort]);
  useEffect(() => () => download.current?.abort(), []);
  /** Verify an extension and hold the complete collection as a draft before replacing anything. */
  async function downloadExtension(extension: OpenVsxThemeExtension) {
    download.current?.abort();
    const controller = new AbortController();
    download.current = controller;
    setIsDownloading(true);
    const revision = context.snapshot.revision;
    const timer = setTimeout(() => controller.abort(), 60_000);
    try {
      const themes = await importOpenVsxThemeExtension(
        extension,
        controller.signal,
      );
      setDraft({ themes: [...themes], revision, attribution: extension });
      setStatus("");
    } finally {
      clearTimeout(timer);
      setIsDownloading(false);
    }
  }
  /** Persist batch updates, selection and license provenance atomically under the captured revision. */
  async function commitImport() {
    if (!draft) return;
    const collectionId = draft.attribution?.collectionId;
    const themes = mergedImport(
      context.snapshot.value.customThemes.map(frontendTheme),
      draft.themes,
      shouldReplace,
      shouldReplace ? collectionId : undefined,
    );
    const attributions = [...(context.snapshot.value.attributions ?? [])];
    if (draft.attribution) {
      const extension = draft.attribution;
      const index = attributions.findIndex(
        (value) => value.collectionId === extension.collectionId,
      );
      const attribution = models.Attribution.createFrom({
        collectionId: extension.collectionId,
        name: extension.name,
        publisher: extension.publisher,
        version: extension.version,
        license: extension.license,
        url: extension.sourceUrl ?? extension.manifestUrl,
      });
      if (index >= 0) attributions[index] = attribution;
      else attributions.push(attribution);
    }
    await context.save(
      nativeLibrary({
        ...context.snapshot.value,
        customThemes: themes,
        attributions,
      }),
      draft.revision,
    );
    setDraft(null);
    setPaste("");
    setStatus("Themes imported.");
  }
  /** Delete only selected custom members, retaining missing raw owners for published themes that may return. */
  async function removeThemes(ids: string[]) {
    await context.save(
      nativeLibrary({
        ...context.snapshot.value,
        preferences: {
          ...preferences,
          baseThemeId: ids.includes(preferences.baseThemeId)
            ? preferences.mode
            : preferences.baseThemeId,
          lightOwner: ids.includes(preferences.lightOwner ?? "")
            ? ""
            : preferences.lightOwner,
          darkOwner: ids.includes(preferences.darkOwner ?? "")
            ? ""
            : preferences.darkOwner,
        },
        customThemes: context.snapshot.value.customThemes.filter(
          (theme) => !ids.includes(theme.id),
        ),
      }),
      context.snapshot.revision,
    );
    setStatus("Selected themes removed.");
  }
  /** Request local-font access when supported; manual family input always remains available. */
  async function discoverFonts() {
    const queryFonts = (
      window as Window & {
        queryLocalFonts?: () => Promise<{ family: string }[]>;
      }
    ).queryLocalFonts;
    if (!queryFonts) {
      setStatus(
        "Local font discovery is unavailable here. Enter an installed font family manually.",
      );
      return;
    }
    const fonts = await queryFonts();
    setFontNames([...new Set(fonts.map((font) => font.family))].sort());
  }
  const customs = context.snapshot.value.customThemes.map(frontendTheme);
  const published = context.snapshot.published.themes
    .map(frontendTheme)
    .filter((theme) => !customs.some((custom) => custom.id === theme.id));
  const definitions = [...BUILT_IN_THEMES, ...customs, ...published];
  const cards = [
    ...STANDARD_THEME_CARDS,
    ...definitions.map(getThemeCardDefinition),
  ];
  const visibleCards = cards.filter((card) => {
    const theme = definitions.find((value) => value.id === card.id);
    if (!theme?.collection) return true;
    const members = definitions.filter(
      (value) => value.collection?.id === theme.collection!.id,
    );
    return (
      card.id === (collectionVariants[theme.collection.id] ?? members[0].id)
    );
  });
  /** Review individual or whole-collection deletion before changing persisted ownership. */
  function requestRemoval(themes: ThemeDefinition[]) {
    setRemoval(themes);
    setRemovedIds(themes.map((theme) => theme.id));
  }
  /** Resolve visual active indicators without overwriting saved half-owner IDs. */
  function picked(identity: string): ("light" | "dark")[] {
    return (["light", "dark"] as const).filter((half) => {
      const raw =
        half === "light" ? preferences.lightOwner : preferences.darkOwner;
      const owner = raw && getThemeDefinition(raw);
      const base = getThemeDefinition(preferences.baseThemeId);
      return (
        (owner && getThemeModes(owner).includes(half)
          ? owner.id
          : base && getThemeModes(base).includes(half)
            ? base.id
            : "default") === identity
      );
    });
  }
  const collections = [
    ...new Map(
      customs
        .filter((theme) => theme.collection)
        .map((theme) => [theme.collection!.id, theme.collection!]),
    ).values(),
  ];
  return (
    <section className="appearance-panel" aria-label="Appearance and themes">
      <header className="section-heading">
        <div>
          <h1>Appearance</h1>
          <p>Choose a palette for each mode, or tune your own.</p>
        </div>
        <button
          onClick={() =>
            context.openEditor({
              editing: null,
              seed: getThemeDefinition(
                (context.appearance === "light"
                  ? preferences.lightOwner
                  : preferences.darkOwner) || preferences.baseThemeId,
              ),
            })
          }
        >
          Create theme
        </button>
      </header>
      <div className="preference-row">
        <label>
          Appearance{" "}
          <select
            disabled={context.isBusy}
            value={preferences.mode}
            onChange={(event) =>
              void action(() => context.patch({ mode: event.target.value }))
            }
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </label>
        <button
          disabled={context.isBusy}
          onClick={() =>
            void action(() => context.patch({ lightOwner: "", darkOwner: "" }))
          }
        >
          Clear mix
        </button>
        <span>Light and dark can use different themes.</span>
      </div>
      {(context.error || context.snapshot.error || status) && (
        <p role="status" className="notice">
          {context.error || context.snapshot.error || status}
        </p>
      )}
      <div className="theme-library-grid">
        {visibleCards.map((card) => {
          const definition = definitions.find((theme) => theme.id === card.id);
          const isCustom = customs.some((theme) => theme.id === card.id);
          return (
            <article className="theme-library-card" key={card.id}>
              <h2>{definition?.collection?.label ?? card.label}</h2>
              {definition?.collection && (
                <label>
                  Variant{" "}
                  <select
                    aria-label={`${definition.collection.label} variant`}
                    value={card.id}
                    onChange={(event) =>
                      setCollectionVariants({
                        ...collectionVariants,
                        [definition.collection!.id]: event.target.value,
                      })
                    }
                  >
                    {definitions
                      .filter(
                        (theme) =>
                          theme.collection?.id === definition.collection!.id,
                      )
                      .map((theme) => (
                        <option key={theme.id} value={theme.id}>
                          {theme.label}
                        </option>
                      ))}
                  </select>
                </label>
              )}
              <ThemeWireframe
                className="h-28"
                panes={card.previews.map((preview) => ({
                  colors: preview.colors,
                  clip:
                    card.previews.length === 2
                      ? preview.mode === "light"
                        ? "left"
                        : "right"
                      : undefined,
                }))}
              />
              <ThemePreviewCircles
                label={card.label}
                previews={card.previews}
                activeModes={picked(card.id)}
                onSelectMode={(mode) => {
                  if (mode !== "system")
                    void action(() => context.select(card.id, mode));
                }}
              />
              <div className="card-actions">
                <button
                  disabled={context.isBusy}
                  onClick={() =>
                    void action(() =>
                      context.select(
                        card.id === "default" ? preferences.mode : card.id,
                      ),
                    )
                  }
                >
                  Use theme
                </button>
                <button
                  onClick={() =>
                    context.openEditor({
                      editing: null,
                      seed: definition ?? null,
                      name: `${card.label.slice(0, 40)} copy`,
                    })
                  }
                >
                  Duplicate
                </button>
                {isCustom && (
                  <button
                    onClick={() =>
                      context.openEditor({ editing: definition!, seed: null })
                    }
                  >
                    Edit
                  </button>
                )}
                {definition && (
                  <button
                    onClick={() =>
                      void action(async () => {
                        const wasSaved = await nativeApi.ExportTheme(
                          models.Definition.createFrom(
                            JSON.stringify(definition),
                          ),
                        );
                        setStatus(
                          wasSaved ? "Theme exported." : "Export cancelled.",
                        );
                      })
                    }
                  >
                    Export
                  </button>
                )}
                {definition && (
                  <button
                    onClick={() =>
                      void action(async () => {
                        await navigator.clipboard.writeText(
                          serializeThemeFile(definition),
                        );
                        setStatus("Theme JSON copied.");
                      })
                    }
                  >
                    Copy JSON
                  </button>
                )}
                {isCustom && (
                  <button onClick={() => requestRemoval([definition!])}>
                    Remove
                  </button>
                )}
              </div>
            </article>
          );
        })}
      </div>
      {collections.length > 0 && (
        <section className="settings-block">
          <h2>Collections</h2>
          {collections.map((collection) => (
            <div className="preference-row" key={collection.id}>
              <span>
                {collection.label} ·{" "}
                {
                  customs.filter(
                    (theme) => theme.collection?.id === collection.id,
                  ).length
                }{" "}
                variants
              </span>
              <button
                onClick={() =>
                  requestRemoval(
                    customs.filter(
                      (theme) => theme.collection?.id === collection.id,
                    ),
                  )
                }
              >
                Remove variants…
              </button>
            </div>
          ))}
        </section>
      )}
      <Dialog
        open={removal !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen) setRemoval(null);
        }}
      >
        <DialogPopup>
          <DialogTitle>Remove custom themes</DialogTitle>
          <DialogDescription>
            Choose the variants to remove. Selected owners will fall back to the
            standard palette.
          </DialogDescription>
          {removal?.map((theme) => (
            <label key={theme.id} className="preference-row">
              <input
                type="checkbox"
                checked={removedIds.includes(theme.id)}
                onChange={(event) =>
                  setRemovedIds(
                    event.target.checked
                      ? [...removedIds, theme.id]
                      : removedIds.filter((identity) => identity !== theme.id),
                  )
                }
              />
              {theme.label}
            </label>
          ))}
          <button onClick={() => setRemoval(null)}>Cancel</button>
          <button
            disabled={context.isBusy || removedIds.length === 0}
            onClick={() =>
              void action(async () => {
                await removeThemes(removedIds);
                setRemoval(null);
              })
            }
          >
            Remove selected themes
          </button>
        </DialogPopup>
      </Dialog>
      <section className="settings-block">
        <h2>Import themes</h2>
        <p>
          T3 Code and VS Code JSON. Files and pasted data are limited to 256
          KiB.
        </p>
        <button
          onClick={() =>
            void action(async () => prepare(await nativeApi.PickThemeFiles()))
          }
        >
          Choose files
        </button>
        <textarea
          aria-label="Paste theme JSON"
          value={paste}
          onChange={(event) => setPaste(event.target.value)}
          placeholder="Paste theme JSON or drop files into the window"
        />
        <button
          onClick={() =>
            prepare([
              {
                name: "Pasted theme",
                text: paste,
                size: new TextEncoder().encode(paste).length,
              },
            ])
          }
        >
          Preview pasted theme
        </button>
        {draft && (
          <div
            role="region"
            aria-label="Theme import preview"
            className="import-preview"
          >
            <h3>Ready to import {draft.themes.length} themes</h3>
            <p>{draft.themes.map((theme) => theme.label).join(", ")}</p>
            {draft.attribution && (
              <p>
                {draft.attribution.publisher} · {draft.attribution.version} ·{" "}
                {draft.attribution.license}
              </p>
            )}
            <label>
              <input
                type="checkbox"
                checked={shouldReplace}
                onChange={(event) => setShouldReplace(event.target.checked)}
              />{" "}
              Update existing themes / replace matching collection
            </label>
            <p>Uncheck to keep both when IDs collide.</p>
            <button
              disabled={context.isBusy}
              onClick={() => void action(commitImport)}
            >
              Import themes
            </button>
            <button onClick={() => setDraft(null)}>Discard import</button>
          </div>
        )}
      </section>
      <section className="settings-block">
        <h2>Open VSX</h2>
        <p>
          Only verified theme resources with supported licenses are imported.
        </p>
        <div className="preference-row">
          <input
            aria-label="Search Open VSX themes"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Dracula, Catppuccin, Nord, Tokyo Night"
          />
          <select
            aria-label="Theme search sort"
            value={sort}
            onChange={(event) =>
              setSort(event.target.value as OpenVsxThemeSort)
            }
          >
            <option value="downloadCount">Most downloaded</option>
            <option value="rating">Best rated</option>
            <option value="timestamp">Newest</option>
            <option value="relevance">Most relevant</option>
          </select>
          {isSearching && <span role="status">Searching…</span>}
        </div>
        <div className="card-actions">
          {["Dracula", "Catppuccin", "Nord", "Tokyo Night"].map(
            (suggestion) => (
              <button key={suggestion} onClick={() => setQuery(suggestion)}>
                {suggestion}
              </button>
            ),
          )}
        </div>
        <div className="extension-list">
          {extensions.map((extension) => (
            <div className="extension-row" key={extension.id}>
              <div>
                <strong>{extension.name}</strong>
                <p>{extension.description}</p>
                <small>
                  {extension.publisher} · {extension.version} ·{" "}
                  {extension.license} ·{" "}
                  {extension.downloadCount.toLocaleString()} downloads
                </small>
              </div>
              <button
                disabled={isDownloading}
                onClick={() => void action(() => downloadExtension(extension))}
              >
                {collections.some(
                  (collection) => collection.id === extension.collectionId,
                )
                  ? "Preview update"
                  : "Preview import"}
              </button>
            </div>
          ))}
        </div>
        {isDownloading && (
          <button onClick={() => download.current?.abort()}>
            Cancel download
          </button>
        )}
      </section>
      <section className="settings-block">
        <h2>Typography</h2>
        <p>Font size, display scale and spacing density are independent.</p>
        <div className="preference-row">
          <label>
            Interface font{" "}
            <input
              list="local-font-families"
              value={fontDraft.sans}
              placeholder="System default"
              onChange={(event) =>
                setFontDraft({ ...fontDraft, sans: event.target.value })
              }
              onBlur={() =>
                void action(() =>
                  context.patch({
                    typography: models.Typography.createFrom(fontDraft),
                  }),
                )
              }
            />
          </label>
          <label>
            Monospace font{" "}
            <input
              list="local-font-families"
              value={fontDraft.code}
              placeholder="System default"
              onChange={(event) =>
                setFontDraft({ ...fontDraft, code: event.target.value })
              }
              onBlur={() =>
                void action(() =>
                  context.patch({
                    typography: models.Typography.createFrom(fontDraft),
                  }),
                )
              }
            />
          </label>
          <button onClick={() => void action(discoverFonts)}>
            Find installed fonts
          </button>
          <datalist id="local-font-families">
            {fontNames.map((font) => (
              <option key={font} value={font} />
            ))}
          </datalist>
        </div>
        <div className="preference-row">
          <label>
            Interface size{" "}
            <input
              type="number"
              min={12}
              max={20}
              value={fontDraft.interfaceSize}
              onChange={(event) =>
                setFontDraft({
                  ...fontDraft,
                  interfaceSize: Number(event.target.value),
                })
              }
              onBlur={() =>
                void action(() =>
                  context.patch({
                    typography: models.Typography.createFrom(fontDraft),
                  }),
                )
              }
            />
          </label>
          <label>
            Monospace size{" "}
            <input
              type="number"
              min={10}
              max={18}
              value={fontDraft.codeSize}
              onChange={(event) =>
                setFontDraft({
                  ...fontDraft,
                  codeSize: Number(event.target.value),
                })
              }
              onBlur={() =>
                void action(() =>
                  context.patch({
                    typography: models.Typography.createFrom(fontDraft),
                  }),
                )
              }
            />
          </label>
          <label>
            <input
              type="checkbox"
              checked={fontDraft.smoothing}
              onChange={(event) => {
                const typography = models.Typography.createFrom({
                  ...fontDraft,
                  smoothing: event.target.checked,
                });
                setFontDraft(typography);
                void action(() => context.patch({ typography }));
              }}
            />{" "}
            Smooth fonts
          </label>
        </div>
        <p
          className="font-preview"
          style={{
            fontFamily: JSON.stringify(fontDraft.sans || "Segoe UI"),
            fontSize: fontDraft.interfaceSize,
          }}
        >
          The quick brown fox · 0123456789
        </p>
        <code
          style={{
            fontFamily: JSON.stringify(fontDraft.code || "Consolas"),
            fontSize: fontDraft.codeSize,
          }}
        >
          input: 123,456 · cost: $1.2345
        </code>
      </section>
      <section className="settings-block">
        <h2>Effects</h2>
        {(
          [
            { key: "contrast", label: "Contrast", min: 50, max: 200 },
            { key: "glassOpacity", label: "Glass opacity", min: 40, max: 100 },
            {
              key: "motionMs",
              label: "Panel animation duration",
              min: 0,
              max: 400,
            },
          ] as const
        ).map((setting) => (
          <EffectSlider
            key={setting.key}
            label={setting.label}
            min={setting.min}
            max={setting.max}
            value={preferences[setting.key]}
            unit={setting.key === "motionMs" ? " ms" : "%"}
            onSave={(value) =>
              action(() => context.patch({ [setting.key]: value }))
            }
          />
        ))}
        <p>Reduced-motion preferences take precedence over animations.</p>
      </section>
      {(context.snapshot.value.attributions ?? []).length > 0 && (
        <details className="settings-block">
          <summary>Imported theme licenses</summary>
          {context.snapshot.value.attributions.map((value) => (
            <p key={value.collectionId}>
              {value.name} · {value.publisher} · {value.version} ·{" "}
              {value.license}
            </p>
          ))}
        </details>
      )}
    </section>
  );
}
