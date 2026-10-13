/** Complete desktop theme library, bounded imports and root-mounted original editor. */
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  MonitorIcon,
  MoonIcon,
  PlusIcon,
  RotateCcwIcon,
  SearchIcon,
  SunIcon,
  UploadIcon,
} from "lucide-react";
import { ActionMenu, ActionMenuItem } from "../ActionMenu";
import { msg, t } from "../i18n";
import {
  Notice,
  OptionSelect,
  PageBody,
  PageHeader,
  SectionHeading,
  SettingsRow,
  SettingsSection,
} from "../ui";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { Input } from "../../../tools/parity/upstream/apps/web/src/components/ui/input";
import { Switch } from "../../../tools/parity/upstream/apps/web/src/components/ui/switch";
import {
  Toggle,
  ToggleGroup,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group";
import { parse as parseJsonc } from "jsonc-parser";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import type { ThemeFileInput } from "../../bindings/yousage/internal/desktop/models";
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
  type OpenVsxThemeExtension,
  type OpenVsxThemeSort,
} from "../../vendor/openVsxThemes";
import { CATPPUCCIN_MOCHA_THEME } from "./catppuccinMocha";
import { ThemeWireframe } from "../../vendor/ThemeWireframe";
import {
  getThemeCardDefinition,
  STANDARD_THEME_CARDS,
  ThemePreviewCircles,
} from "../../vendor/ThemePreviewCircles";
import {
  fontFamilyList,
  frontendTheme,
  nativeLibrary,
  useAppearance,
  type AppearancePreferences,
} from "./appearance";
import {
  Dialog,
  DialogFooter,
  DialogHeader,
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

/** Keep a collection visible when its previously selected variant has been removed. */
export function survivingCollectionVariant(
  members: ThemeDefinition[],
  selected?: string,
): ThemeDefinition {
  return members.find((theme) => theme.id === selected) ?? members[0];
}

/** Assign supported halves while preserving unrelated raw ownership, mode and base selection. */
export function collectionOwners(
  members: ThemeDefinition[],
): Partial<AppearancePreferences> | null {
  if (
    members.length === 0 ||
    (members.length === 1 && getThemeModes(members[0]).length === 2)
  )
    return null;
  const light = members.find((theme) => getThemeModes(theme).includes("light"));
  const dark = members.find((theme) => getThemeModes(theme).includes("dark"));
  return {
    ...(light ? { lightOwner: light.id } : {}),
    ...(dark ? { darkOwner: dark.id } : {}),
  };
}

/** Preview the latest range value locally and save only settled pointer/keyboard changes. */
function EffectSlider({
  label,
  min,
  max,
  value,
  unit,
  onSave,
  defaultValue,
}: {
  label: string;
  min: number;
  max: number;
  value: number;
  unit: string;
  onSave: (value: number) => Promise<void>;
  defaultValue: number;
}) {
  const [draft, setDraft] = useState(value);
  const last = useRef(value);
  const [isPending, setIsPending] = useState(false);
  const pending = useRef(false);
  useEffect(() => {
    setDraft(value);
    last.current = value;
  }, [value]);
  /** Avoid duplicate blur/up writes while acknowledging the final selected value. */
  async function commit(next = draft) {
    if (pending.current || last.current === next) return;
    pending.current = true;
    setIsPending(true);
    try {
      await onSave(next);
      last.current = next;
      setDraft(next);
    } catch {
      setDraft(last.current);
    } finally {
      pending.current = false;
      setIsPending(false);
    }
  }
  return (
    <SettingsRow
      title={label}
      control={
        <>
          <input
            type="range"
            aria-label={label}
            className="w-40 accent-primary"
            min={min}
            max={max}
            value={draft}
            disabled={isPending}
            onChange={(event) => setDraft(Number(event.target.value))}
            onPointerUp={() => void commit()}
            onKeyUp={() => void commit()}
            onBlur={() => void commit()}
          />
          <span className="w-14 text-right text-xs text-muted-foreground tabular-nums">
            {draft}
            {unit}
          </span>
          <ResetButton
            label={t("Reset {label}", { label })}
            disabled={isPending}
            onClick={() => void commit(defaultValue)}
          />
        </>
      }
    />
  );
}

/** Parse a complete selected batch before presenting any install mutation. */
export function parseThemeBatch(
  files: { name: string; text: string; size: number; error?: string }[],
): ThemeDefinition[] {
  const parsed = files.map((file) => {
    if (file.error) throw new Error(`${file.name}: ${t(file.error)}`);
    if (
      file.size > 256 * 1024 ||
      new TextEncoder().encode(file.text).length > 256 * 1024
    )
      throw new Error(
        `${file.name}: ${t("theme files must be no larger than 256 KiB.")}`,
      );
    const errors: import("jsonc-parser").ParseError[] = [];
    const value: unknown = parseJsonc(file.text, errors, {
      allowTrailingComma: true,
    });
    if (errors.length)
      throw new Error(`${file.name}: ${t("invalid theme JSON.")}`);
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
        t(
          "This collection conflicts with a theme outside the collection. No themes were changed.",
        ),
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
      onDraftStateChange={context.setEditorState}
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
              t(
                "This theme changed while the editor was open. Reopen the saved theme before replacing it.",
              ),
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
export function ThemePanel({
  onDirtyChange,
  isActive = true,
  droppedFiles = [],
}: {
  onDirtyChange?: (isDirty: boolean) => void;
  isActive?: boolean;
  droppedFiles?: ThemeFileInput[];
}) {
  const context = useAppearance();
  const preferences = context.snapshot.value.preferences;
  const [status, setStatus] = useState("");
  const [paste, setPaste] = useState("");
  const [draft, setDraft] = useState<ImportDraft | null>(null);
  const [shouldReplace, setShouldReplace] = useState(true);
  const [query, setQuery] = useState("");
  const [isMarketOpen, setIsMarketOpen] = useState(false);
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
  useLayoutEffect(() => {
    onDirtyChange?.(
      Boolean(draft || paste) ||
        JSON.stringify(fontDraft) !== JSON.stringify(preferences.typography),
    );
  }, [draft, paste, fontDraft, preferences.typography, onDirtyChange]);
  useEffect(
    () => setFontDraft(preferences.typography),
    [JSON.stringify(preferences.typography)],
  );
  /** Settle one explicitly initiated action into a status message without hiding failed drafts. */
  async function action(execute: () => Promise<void>) {
    try {
      await execute();
    } catch (cause) {
      setStatus(cause instanceof Error ? cause.message : String(cause));
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
      setStatus(cause instanceof Error ? cause.message : String(cause));
    }
  }
  useEffect(() => {
    if (droppedFiles.length) prepare(droppedFiles);
  }, [droppedFiles]);
  useEffect(() => {
    if (!isActive || !isMarketOpen) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setIsSearching(true);
      void import("../../vendor/openVsxThemes")
        .then((module) =>
          module.searchOpenVsxThemes(query, {
            signal: controller.signal,
            sortBy: sort,
          }),
        )
        .then((value) => {
          if (!controller.signal.aborted) setExtensions([...value]);
        })
        .catch((cause) => {
          if (!controller.signal.aborted)
            setStatus(cause instanceof Error ? cause.message : String(cause));
        })
        .finally(() => {
          if (!controller.signal.aborted) setIsSearching(false);
        });
    }, 350);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [query, sort, isActive, isMarketOpen]);
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
      const { importOpenVsxThemeExtension } =
        await import("../../vendor/openVsxThemes");
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
    setStatus(t("Themes imported."));
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
    setStatus(t("Selected themes removed."));
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
        t(
          "Local font discovery is unavailable here. Enter an installed font family manually.",
        ),
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
  const definitions = [CATPPUCCIN_MOCHA_THEME, ...customs, ...published];
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
      card.id ===
      survivingCollectionVariant(
        members,
        collectionVariants[theme.collection.id],
      ).id
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
  /** Persist the full typography draft after a field settles. */
  function saveTypography() {
    void action(() =>
      context.patch({
        typography: models.Typography.createFrom(fontDraft),
      }),
    );
  }
  /** Restore one typography field to its default. */
  function resetTypography(patch: Partial<typeof preferences.typography>) {
    void action(() =>
      context.patch({
        typography: models.Typography.createFrom({
          ...preferences.typography,
          ...patch,
        }),
      }),
    );
  }
  const message = context.error || context.snapshot.error || status;
  return (
    <section aria-label={t("Appearance")}>
      <PageHeader parent={t("Settings")} title={t("Appearance")}>
        <ToggleGroup
          aria-label={t("Appearance")}
          variant="segmented"
          disabled={context.isBusy}
          value={[preferences.mode]}
          onValueChange={(next) => {
            const mode = next[0];
            if (mode) void action(() => context.patch({ mode }));
          }}
        >
          <Toggle value="system" aria-label={t("System")}>
            <MonitorIcon aria-hidden />
            {t("System")}
          </Toggle>
          <Toggle value="light" aria-label={t("Light")}>
            <SunIcon aria-hidden />
            {t("Light")}
          </Toggle>
          <Toggle value="dark" aria-label={t("Dark")}>
            <MoonIcon aria-hidden />
            {t("Dark")}
          </Toggle>
        </ToggleGroup>
        <Button
          size="sm"
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
          <PlusIcon aria-hidden />
          {t("Create theme")}
        </Button>
      </PageHeader>
      <PageBody>
        {message && (
          <Notice
            tone={context.error || context.snapshot.error ? "error" : "default"}
          >
            {message}
          </Notice>
        )}
        <section className="flex flex-col gap-2.5">
          <SectionHeading
            title={t("Themes")}
            action={
              <Button
                variant="ghost"
                size="xs"
                disabled={context.isBusy}
                onClick={() =>
                  void action(() =>
                    context.patch({ lightOwner: "", darkOwner: "" }),
                  )
                }
              >
                {t("Clear mix")}
              </Button>
            }
          />
          <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,15rem),1fr))] gap-4">
            {visibleCards.map((card) => {
              const definition = definitions.find(
                (theme) => theme.id === card.id,
              );
              const isCustom = customs.some((theme) => theme.id === card.id);
              const activeModes = picked(card.id);
              /** Apply the whole theme, or every variant of its collection. */
              const applyTheme = () =>
                void action(async () => {
                  const members = definition?.collection
                    ? definitions.filter(
                        (theme) =>
                          theme.collection?.id === definition.collection!.id,
                      )
                    : definition
                      ? [definition]
                      : [];
                  const owners = collectionOwners(members);
                  if (owners) await context.patch(owners);
                  else
                    await context.select(
                      card.id === "default" ? preferences.mode : card.id,
                    );
                  if (definition?.collection)
                    setCollectionVariants((current) => ({
                      ...current,
                      [definition.collection!.id]: members[0].id,
                    }));
                });
              return (
                <article
                  key={card.id}
                  role="button"
                  tabIndex={0}
                  aria-label={t("Use theme {name}", {
                    name: definition?.collection?.label ?? card.label,
                  })}
                  aria-disabled={context.isBusy || undefined}
                  className={cn(
                    "flex min-w-0 cursor-pointer flex-col overflow-hidden rounded-xl border bg-card transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring aria-disabled:cursor-wait",
                    activeModes.length > 0
                      ? "border-primary/50 ring-1 ring-primary/20"
                      : "border-border hover:border-foreground/25 hover:bg-accent/30",
                  )}
                  onClick={(event) => {
                    const target = event.target as Element;
                    if (
                      context.isBusy ||
                      !event.currentTarget.contains(target) ||
                      target.closest(
                        "button, input, select, [role=combobox], [role=menuitem]",
                      )
                    )
                      return;
                    applyTheme();
                  }}
                  onKeyDown={(event) => {
                    if (
                      event.target === event.currentTarget &&
                      !context.isBusy &&
                      (event.key === "Enter" || event.key === " ")
                    ) {
                      event.preventDefault();
                      applyTheme();
                    }
                  }}
                >
                  <div className="border-b border-border p-2">
                    <ThemeWireframe
                      className="h-24 overflow-hidden rounded-md"
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
                  </div>
                  <div className="flex flex-1 flex-col gap-3 p-3">
                    <div className="flex min-w-0 items-center justify-between gap-2">
                      <h3 className="truncate text-sm font-medium">
                        {definition?.collection?.label ?? card.label}
                      </h3>
                      <div className="flex shrink-0 items-center gap-1">
                        {activeModes.map((mode) => (
                          <span
                            key={mode}
                            className="rounded-full bg-primary/10 px-1.5 py-px text-2xs font-medium text-primary"
                          >
                            {mode === "light" ? t("Light") : t("Dark")}
                          </span>
                        ))}
                        <ActionMenu
                          label={t("More actions for {name}", {
                            name: card.label,
                          })}
                        >
                          <ActionMenuItem
                            onClick={() =>
                              context.openEditor({
                                editing: null,
                                seed: definition ?? null,
                                name: t("{name} copy", {
                                  name: card.label.slice(0, 40),
                                }),
                              })
                            }
                          >
                            {t("Duplicate")}
                          </ActionMenuItem>
                          {isCustom && (
                            <ActionMenuItem
                              onClick={() =>
                                context.openEditor({
                                  editing: definition!,
                                  seed: null,
                                })
                              }
                            >
                              {t("Edit")}
                            </ActionMenuItem>
                          )}
                          {definition && (
                            <ActionMenuItem
                              onClick={() =>
                                void action(async () => {
                                  const wasSaved = await nativeApi.ExportTheme(
                                    models.Definition.createFrom(
                                      JSON.stringify(definition),
                                    ),
                                  );
                                  setStatus(
                                    wasSaved
                                      ? t("Theme exported.")
                                      : t("Export cancelled."),
                                  );
                                })
                              }
                            >
                              {t("Export")}
                            </ActionMenuItem>
                          )}
                          {definition && (
                            <ActionMenuItem
                              onClick={() =>
                                void action(async () => {
                                  await navigator.clipboard.writeText(
                                    serializeThemeFile(definition),
                                  );
                                  setStatus(t("Theme JSON copied."));
                                })
                              }
                            >
                              {t("Copy JSON")}
                            </ActionMenuItem>
                          )}
                          {isCustom && (
                            <ActionMenuItem
                              variant="destructive"
                              onClick={() => requestRemoval([definition!])}
                            >
                              {t("Remove")}
                            </ActionMenuItem>
                          )}
                        </ActionMenu>
                      </div>
                    </div>
                    {definition?.collection && (
                      <OptionSelect
                        label={t("{name} variant", {
                          name: definition.collection.label,
                        })}
                        className="w-full"
                        value={card.id}
                        options={definitions
                          .filter(
                            (theme) =>
                              theme.collection?.id ===
                              definition.collection!.id,
                          )
                          .map((theme) => ({
                            value: theme.id,
                            label: theme.label,
                          }))}
                        onChange={(identity) =>
                          setCollectionVariants({
                            ...collectionVariants,
                            [definition.collection!.id]: identity,
                          })
                        }
                      />
                    )}
                    <ThemePreviewCircles
                      label={card.label}
                      previews={card.previews}
                      activeModes={activeModes}
                      onSelectMode={(mode) => {
                        if (mode !== "system")
                          void action(() => context.select(card.id, mode));
                      }}
                    />
                  </div>
                </article>
              );
            })}
          </div>
        </section>
        {collections.length > 0 && (
          <SettingsSection title={t("Collections")}>
            {collections.map((collection) => (
              <SettingsRow
                key={collection.id}
                title={collection.label}
                description={t("{count} variants", {
                  count: customs.filter(
                    (theme) => theme.collection?.id === collection.id,
                  ).length,
                })}
                control={
                  <Button
                    variant="destructive-outline"
                    size="sm"
                    onClick={() =>
                      requestRemoval(
                        customs.filter(
                          (theme) => theme.collection?.id === collection.id,
                        ),
                      )
                    }
                  >
                    {t("Remove variants…")}
                  </Button>
                }
              />
            ))}
          </SettingsSection>
        )}
        <Dialog
          open={removal !== null}
          onOpenChange={(isOpen) => {
            if (!isOpen) setRemoval(null);
          }}
        >
          <DialogPopup className="max-w-md">
            <DialogHeader>
              <DialogTitle>{t("Remove custom themes")}</DialogTitle>
              <DialogDescription>
                {t(
                  "Choose the variants to remove. Selected owners will fall back to the standard palette.",
                )}
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-1 px-6 pb-4">
              {removal?.map((theme) => (
                <label
                  key={theme.id}
                  className="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                >
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={removedIds.includes(theme.id)}
                    onChange={(event) =>
                      setRemovedIds(
                        event.target.checked
                          ? [...removedIds, theme.id]
                          : removedIds.filter(
                              (identity) => identity !== theme.id,
                            ),
                      )
                    }
                  />
                  {theme.label}
                </label>
              ))}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setRemoval(null)}>
                {t("Cancel")}
              </Button>
              <Button
                variant="destructive"
                disabled={context.isBusy || removedIds.length === 0}
                onClick={() =>
                  void action(async () => {
                    await removeThemes(removedIds);
                    setRemoval(null);
                  })
                }
              >
                {t("Remove selected themes")}
              </Button>
            </DialogFooter>
          </DialogPopup>
        </Dialog>
        <SettingsSection title={t("Typography")}>
          <SettingsRow
            title={t("Interface font")}
            control={
              <>
                <Input
                  size="sm"
                  aria-label={t("Interface font")}
                  className="w-52"
                  list="local-font-families"
                  value={fontDraft.sans}
                  placeholder={t("System default")}
                  onChange={(event) =>
                    setFontDraft({ ...fontDraft, sans: event.target.value })
                  }
                  onBlur={saveTypography}
                />
                <ResetButton
                  label={t("Reset interface font")}
                  disabled={context.isBusy}
                  onClick={() => resetTypography({ sans: "" })}
                />
              </>
            }
          />
          <SettingsRow
            title={t("Monospace font")}
            control={
              <>
                <Input
                  size="sm"
                  aria-label={t("Monospace font")}
                  className="w-52"
                  list="local-font-families"
                  value={fontDraft.code}
                  placeholder={t("System default")}
                  onChange={(event) =>
                    setFontDraft({ ...fontDraft, code: event.target.value })
                  }
                  onBlur={saveTypography}
                />
                <ResetButton
                  label={t("Reset monospace font")}
                  disabled={context.isBusy}
                  onClick={() => resetTypography({ code: "" })}
                />
              </>
            }
          />
          <SettingsRow
            title={t("Interface size")}
            description="12–20 px"
            control={
              <>
                <Input
                  size="sm"
                  type="number"
                  aria-label={t("Interface size")}
                  className="w-20"
                  min={12}
                  max={20}
                  value={fontDraft.interfaceSize}
                  onChange={(event) =>
                    setFontDraft({
                      ...fontDraft,
                      interfaceSize: Number(event.target.value),
                    })
                  }
                  onBlur={saveTypography}
                />
                <ResetButton
                  label={t("Reset interface size")}
                  disabled={context.isBusy}
                  onClick={() => resetTypography({ interfaceSize: 16 })}
                />
              </>
            }
          />
          <SettingsRow
            title={t("Monospace size")}
            description="10–18 px"
            control={
              <>
                <Input
                  size="sm"
                  type="number"
                  aria-label={t("Monospace size")}
                  className="w-20"
                  min={10}
                  max={18}
                  value={fontDraft.codeSize}
                  onChange={(event) =>
                    setFontDraft({
                      ...fontDraft,
                      codeSize: Number(event.target.value),
                    })
                  }
                  onBlur={saveTypography}
                />
                <ResetButton
                  label={t("Reset monospace size")}
                  disabled={context.isBusy}
                  onClick={() => resetTypography({ codeSize: 14 })}
                />
              </>
            }
          />
          <SettingsRow
            title={t("Smooth fonts")}
            control={
              <Switch
                aria-label={t("Smooth fonts")}
                checked={fontDraft.smoothing}
                onCheckedChange={(isChecked) => {
                  const typography = models.Typography.createFrom({
                    ...fontDraft,
                    smoothing: isChecked,
                  });
                  setFontDraft(typography);
                  void action(() => context.patch({ typography }));
                }}
              />
            }
          />
          <SettingsRow
            title={t("Preview")}
            control={
              <Button
                variant="ghost"
                size="xs"
                onClick={() => void action(discoverFonts)}
              >
                {t("Find installed fonts")}
              </Button>
            }
          >
            <div className="flex flex-col gap-1.5 rounded-lg bg-muted/40 px-3 py-2.5">
              <p
                style={{
                  fontFamily: fontFamilyList(fontDraft.sans || "Segoe UI"),
                  fontSize: fontDraft.interfaceSize,
                }}
              >
                The quick brown fox · 0123456789
              </p>
              <code
                className="text-muted-foreground"
                style={{
                  fontFamily: fontFamilyList(fontDraft.code || "Consolas"),
                  fontSize: fontDraft.codeSize,
                }}
              >
                input: 123,456 · cost: $1.2345
              </code>
            </div>
            <datalist id="local-font-families">
              {fontNames.map((font) => (
                <option key={font} value={font} />
              ))}
            </datalist>
          </SettingsRow>
        </SettingsSection>
        <SettingsSection title={t("Effects")}>
          {(
            [
              { key: "contrast", label: msg("Contrast"), min: 50, max: 200 },
              {
                key: "glassOpacity",
                label: msg("Glass opacity"),
                min: 40,
                max: 100,
              },
              {
                key: "motionMs",
                label: msg("Panel animation duration"),
                min: 0,
                max: 400,
              },
            ] as const
          ).map((setting) => (
            <EffectSlider
              key={setting.key}
              label={t(setting.label)}
              min={setting.min}
              max={setting.max}
              value={preferences[setting.key]}
              unit={setting.key === "motionMs" ? " ms" : "%"}
              defaultValue={
                setting.key === "contrast"
                  ? 100
                  : setting.key === "glassOpacity"
                    ? 80
                    : 0
              }
              onSave={(value) => context.patch({ [setting.key]: value })}
            />
          ))}
        </SettingsSection>
        <SettingsSection title={t("Import themes")}>
          <SettingsRow
            title={t("Theme files")}
            description={t(
              "T3 Code and VS Code JSON. Files and pasted data are limited to 256 KiB. You can also drop files into the window.",
            )}
            control={
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  void action(async () =>
                    prepare(await nativeApi.PickThemeFiles()),
                  )
                }
              >
                <UploadIcon aria-hidden />
                {t("Choose files")}
              </Button>
            }
          />
          <SettingsRow title={t("Paste JSON")}>
            <textarea
              aria-label={t("Paste theme JSON")}
              value={paste}
              onChange={(event) => setPaste(event.target.value)}
              placeholder='{ "name": "My theme", … }'
              className="h-32 w-full resize-y rounded-lg border border-input bg-background px-3 py-2 font-mono text-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/24 dark:bg-input/32"
            />
            <div className="flex justify-end">
              <Button
                variant="outline"
                size="sm"
                disabled={!paste.trim()}
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
                {t("Preview pasted theme")}
              </Button>
            </div>
          </SettingsRow>
          {draft && (
            <div
              role="region"
              aria-label={t("Theme import preview")}
              className="settings-row flex flex-col gap-3 bg-primary/[0.04]"
            >
              <div className="flex flex-col gap-0.5">
                <h3 className="text-sm font-medium">
                  {t("Ready to import {count} themes", {
                    count: draft.themes.length,
                  })}
                </h3>
                <p className="text-xs text-muted-foreground">
                  {draft.themes.map((theme) => theme.label).join(", ")}
                </p>
                {draft.attribution && (
                  <p className="text-xs text-muted-foreground">
                    {draft.attribution.publisher} · {draft.attribution.version}{" "}
                    · {draft.attribution.license}
                  </p>
                )}
              </div>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <label className="flex items-center gap-2 text-sm">
                  <Switch
                    size="sm"
                    checked={shouldReplace}
                    onCheckedChange={setShouldReplace}
                  />
                  {t("Update existing themes / replace matching collection")}
                </label>
                <div className="flex gap-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setDraft(null)}
                  >
                    {t("Discard import")}
                  </Button>
                  <Button
                    size="sm"
                    disabled={context.isBusy}
                    onClick={() => void action(commitImport)}
                  >
                    {t("Import themes")}
                  </Button>
                </div>
              </div>
              <p className="text-xs text-muted-foreground">
                {t("Turn off to keep both when IDs collide.")}
              </p>
            </div>
          )}
        </SettingsSection>
        <SettingsSection
          title="Open VSX"
          action={
            isSearching ? (
              <span role="status" className="text-xs text-muted-foreground">
                {t("Searching…")}
              </span>
            ) : undefined
          }
        >
          <SettingsRow>
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative min-w-48 flex-1">
                <SearchIcon
                  aria-hidden
                  className="pointer-events-none absolute top-1/2 left-2.5 z-10 size-3.5 -translate-y-1/2 text-muted-foreground"
                />
                <Input
                  size="sm"
                  className="[&_input]:ps-8"
                  aria-label={t("Search Open VSX themes")}
                  value={query}
                  onChange={(event) => {
                    setIsMarketOpen(true);
                    setQuery(event.target.value);
                  }}
                  placeholder="Dracula, Catppuccin, Nord, Tokyo Night"
                />
              </div>
              <OptionSelect
                label={t("Theme search sort")}
                value={sort}
                options={SORT_OPTIONS.map((option) => ({
                  ...option,
                  label: t(option.label),
                }))}
                onChange={(next) => {
                  setIsMarketOpen(true);
                  setSort(next);
                }}
              />
            </div>
            <div className="flex flex-wrap gap-1.5">
              {["Dracula", "Catppuccin", "Nord", "Tokyo Night"].map(
                (suggestion) => (
                  <Button
                    key={suggestion}
                    variant="secondary"
                    size="xs"
                    className="rounded-full"
                    onClick={() => {
                      setIsMarketOpen(true);
                      setQuery(suggestion);
                    }}
                  >
                    {suggestion}
                  </Button>
                ),
              )}
              {!isMarketOpen && (
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => setIsMarketOpen(true)}
                >
                  {t("Browse theme catalogue")}
                </Button>
              )}
            </div>
          </SettingsRow>
          {extensions.map((extension) => (
            <SettingsRow
              key={extension.id}
              title={<span className="font-medium">{extension.name}</span>}
              description={
                <>
                  {extension.description && (
                    <span className="line-clamp-2">
                      {extension.description}
                    </span>
                  )}
                  <span className="text-2xs">
                    {extension.publisher} · {extension.version} ·{" "}
                    {extension.license} ·{" "}
                    {t("{count} downloads", {
                      count: extension.downloadCount.toLocaleString(),
                    })}
                  </span>
                </>
              }
              control={
                <Button
                  variant="outline"
                  size="sm"
                  disabled={isDownloading}
                  onClick={() =>
                    void action(() => downloadExtension(extension))
                  }
                >
                  {collections.some(
                    (collection) => collection.id === extension.collectionId,
                  )
                    ? t("Preview update")
                    : t("Preview import")}
                </Button>
              }
            />
          ))}
          {isDownloading && (
            <SettingsRow
              title={t("Downloading…")}
              control={
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => download.current?.abort()}
                >
                  {t("Cancel download")}
                </Button>
              }
            />
          )}
        </SettingsSection>
        {(context.snapshot.value.attributions ?? []).length > 0 && (
          <SettingsSection title={t("Imported theme licenses")}>
            {context.snapshot.value.attributions.map((value) => (
              <SettingsRow
                key={value.collectionId}
                title={value.name}
                description={`${value.publisher} · ${value.version} · ${value.license}`}
              />
            ))}
          </SettingsSection>
        )}
      </PageBody>
    </section>
  );
}

const SORT_OPTIONS: readonly { value: OpenVsxThemeSort; label: string }[] = [
  { value: "downloadCount", label: msg("Most downloaded") },
  { value: "rating", label: msg("Best rated") },
  { value: "timestamp", label: msg("Newest") },
  { value: "relevance", label: msg("Most relevant") },
];

/**
 * Icon-only reset-to-default action.
 *
 * @param props - Accessible label, disabled state and click handler.
 * @returns The reset button.
 */
function ResetButton({
  label,
  disabled,
  onClick,
}: {
  label: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      <RotateCcwIcon aria-hidden />
    </Button>
  );
}
