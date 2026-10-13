/** Saved theme authority and transient editor state shared above every application route. */
import { t } from "../i18n";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Events } from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import * as models from "../../bindings/yousage/internal/theme/models";
import {
  applyThemePalette,
  getThemeDefinition,
  getThemeModes,
  loadSavedCustomThemes,
  parseThemeFile,
  parseThemeHalves,
  resolveThemeAppearance,
  resolveThemeHalf,
  serializeThemeFile,
  setEnvironmentThemes,
  type ThemeDefinition,
} from "../../vendor/themePalette";

export type AppearanceSnapshot = models.LibrarySnapshot;
export type AppearancePreferences = models.AppearancePreferences;
type EditorInput = {
  editing: ThemeDefinition | null;
  seed: ThemeDefinition | null;
  name?: string;
};
type EditorSession = EditorInput & {
  identity: number;
  revision: number;
  signature: string | null;
};
type AppearanceContextValue = {
  snapshot: AppearanceSnapshot;
  appearance: "light" | "dark";
  isBusy: boolean;
  error: string;
  save: (
    value: models.LibraryValue,
    expectedRevision?: number,
  ) => Promise<AppearanceSnapshot>;
  patch: (patch: Partial<AppearancePreferences>) => Promise<void>;
  select: (identity: string, half?: "light" | "dark") => Promise<void>;
  editor: EditorSession | null;
  editorState: { isDirty: boolean; isPending: boolean };
  setEditorState: (isDirty: boolean, isPending: boolean) => void;
  openEditor: (session: EditorInput) => void;
  closeEditor: () => void;
  restore: () => void;
};
const APPEARANCE_CONTEXT = createContext<AppearanceContextValue | null>(null);

/** Normalize individual font names and fallback lists without treating a list as one family. */
export function fontFamilyList(input: string): string {
  return input
    .split(",")
    .map((name) => name.trim())
    .filter(Boolean)
    .map((name) =>
      /^(['"]).*\1$/.test(name) || /^[a-zA-Z][a-zA-Z0-9-]*$/.test(name)
        ? name
        : JSON.stringify(name.replaceAll('"', "")),
    )
    .join(", ");
}

/** Convert validated native definitions through the same portable schema the original UI expects. */
export function frontendTheme(definition: models.Definition): ThemeDefinition {
  return parseThemeFile({
    version: 1,
    id: definition.id,
    name: definition.label,
    appearance: definition.appearance,
    colors: definition.colors,
    ...(definition.variants ? { variants: definition.variants } : {}),
    ...(definition.collection ? { collection: definition.collection } : {}),
    managed: definition.managed,
  });
}

/** Convert through the JSON wire shape without letting generated constructors mutate cached theme objects. */
export function nativeLibrary(value: unknown): models.LibraryValue {
  return models.LibraryValue.createFrom(JSON.stringify(value));
}

/** Apply preferences with fallback fonts and independent light/dark owners before native reveal. */
export function applySavedAppearance(
  snapshot: AppearanceSnapshot,
  isSystemDark: boolean,
  shouldApplyPalette = true,
): "light" | "dark" {
  const value = snapshot.value.preferences;
  loadSavedCustomThemes(snapshot.value.customThemes.map(frontendTheme));
  setEnvironmentThemes(snapshot.published.themes.map(frontendTheme));
  const halves = parseThemeHalves(
    JSON.stringify({ light: value.lightOwner, dark: value.darkOwner }),
  );
  const appearance = resolveThemeAppearance(
    value.baseThemeId,
    isSystemDark,
    undefined,
    value.mode as "system" | "light" | "dark",
    halves,
  );
  if (shouldApplyPalette) {
    document.documentElement.classList.toggle("dark", appearance === "dark");
    applyThemePalette(
      resolveThemeHalf(value.baseThemeId, halves, appearance),
      appearance,
    );
  }
  const root = document.documentElement;
  root.style.fontSize = `${value.typography.interfaceSize}px`;
  root.style.setProperty(
    "--font-sans",
    `${fontFamilyList(value.typography.sans) || '"Segoe UI"'},-apple-system,BlinkMacSystemFont,system-ui,sans-serif`,
  );
  root.style.setProperty(
    "--font-mono",
    `${fontFamilyList(value.typography.code) || "Consolas"},"SF Mono",Menlo,"Liberation Mono",monospace`,
  );
  root.style.setProperty("--code-font-size", `${value.typography.codeSize}px`);
  root.style.setProperty("--font-size-code", `${value.typography.codeSize}px`);
  root.style.setProperty("--diffs-font-size", `${value.typography.codeSize}px`);
  root.style.setProperty(
    "--appearance-contrast-base",
    `${Math.min(value.contrast, 100)}%`,
  );
  root.style.setProperty(
    "--appearance-contrast-boost",
    `${Math.max(value.contrast - 100, 0)}%`,
  );
  root.style.setProperty(
    "--appearance-contrast-border-boost",
    `${Math.max(value.contrast - 100, 0) / 4}%`,
  );
  root.style.setProperty("--glass-opacity", `${value.glassOpacity}%`);
  root.style.setProperty(
    "--panel-animation-duration",
    `${window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : value.motionMs}ms`,
  );
  root.style.setProperty(
    "-webkit-font-smoothing",
    value.typography.smoothing ? "antialiased" : "auto",
  );
  return appearance;
}

/** Keep saved state, root-mounted drafts, native OS events and atomic asynchronous mutations together. */
export function AppearanceProvider({
  initial,
  isSystemDark: initialSystemDark,
  children,
}: {
  initial: AppearanceSnapshot;
  isSystemDark: boolean;
  children: ReactNode;
}) {
  const [snapshot, setSnapshot] = useState(initial);
  const current = useRef(snapshot);
  current.current = snapshot;
  const [isSystemDark, setIsSystemDark] = useState(initialSystemDark);
  const systemDark = useRef(isSystemDark);
  systemDark.current = isSystemDark;
  const [isBusy, setIsBusy] = useState(false);
  const busy = useRef(false);
  const [error, setError] = useState("");
  const [editorState, updateEditorState] = useState({
    isDirty: false,
    isPending: false,
  });
  const setEditorState = useCallback(
    (isDirty: boolean, isPending: boolean) =>
      updateEditorState({ isDirty, isPending }),
    [],
  );
  const [editor, setEditor] = useState<EditorSession | null>(null);
  const currentEditor = useRef(editor);
  currentEditor.current = editor;
  const editorSequence = useRef(0);
  const patchQueue = useRef(Promise.resolve());
  const [appearance, setAppearance] = useState<"light" | "dark">(() =>
    applySavedAppearance(initial, initialSystemDark),
  );
  const restore = useCallback(
    () =>
      setAppearance(applySavedAppearance(current.current, systemDark.current)),
    [],
  );
  useEffect(() => {
    loadSavedCustomThemes(snapshot.value.customThemes.map(frontendTheme));
    setEnvironmentThemes(snapshot.published.themes.map(frontendTheme));
    if (!editor) restore();
    else applySavedAppearance(snapshot, isSystemDark, false);
  }, [snapshot, restore, editor, isSystemDark]);
  useEffect(() => {
    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    const changed = () =>
      document.documentElement.style.setProperty(
        "--panel-animation-duration",
        `${media.matches ? 0 : current.current.value.preferences.motionMs}ms`,
      );
    media.addEventListener("change", changed);
    return () => media.removeEventListener("change", changed);
  }, []);
  useEffect(() => {
    const dispose = Events.On("appearance-snapshot", (event) => {
      const next = models.LibrarySnapshot.createFrom(event.data);
      if (next.revision >= current.current.revision) {
        current.current = next;
        setSnapshot(next);
      }
    });
    const disposeSystem = Events.On("system-appearance", (event) =>
      setIsSystemDark(Boolean(event.data)),
    );
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    /** Follow media appearance only when explicit preferences allow it. */
    const changed = () => setIsSystemDark(media.matches);
    media.addEventListener("change", changed);
    return () => {
      dispose();
      disposeSystem();
      media.removeEventListener("change", changed);
    };
  }, []);
  /** Persist exactly one library/selection transaction; failure restores the latest saved mirror and keeps drafts open. */
  const save = useCallback(
    async (value: models.LibraryValue, expectedRevision?: number) => {
      if (busy.current)
        throw new Error(
          t("Appearance is already saving. Try again after it finishes."),
        );
      busy.current = true;
      setIsBusy(true);
      setError("");
      try {
        const next = await nativeApi.SaveAppearance(
          expectedRevision ?? current.current.revision,
          value,
        );
        current.current = next;
        setSnapshot(next);
        return next;
      } catch (cause) {
        const message = String(cause);
        setError(message);
        try {
          const next = await nativeApi.GetAppearance();
          current.current = next;
          setSnapshot(next);
          applySavedAppearance(
            next,
            systemDark.current,
            !currentEditor.current,
          );
        } catch {
          applySavedAppearance(
            current.current,
            systemDark.current,
            !currentEditor.current,
          );
        }
        throw new Error(message);
      } finally {
        busy.current = false;
        setIsBusy(false);
      }
    },
    [],
  );
  /** Save ordinary preference changes immediately without modifying unsaved editor palettes. */
  const patch = useCallback(
    (patchValue: Partial<AppearancePreferences>) => {
      const operation = patchQueue.current
        .catch(() => {})
        .then(async () => {
          await save(
            nativeLibrary({
              ...current.current.value,
              preferences: {
                ...current.current.value.preferences,
                ...patchValue,
              },
            }),
          );
        });
      patchQueue.current = operation;
      return operation;
    },
    [save],
  );
  /** Claim one appearance without pruning the other raw owner; full selection clears the mix. */
  const select = useCallback(
    async (identity: string, half?: "light" | "dark") => {
      const definition = getThemeDefinition(identity);
      if (half) {
        if (identity === "default") {
          const value = current.current.value.preferences;
          const other = half === "light" ? "darkOwner" : "lightOwner";
          await patch({
            baseThemeId: value.mode,
            [half === "light" ? "lightOwner" : "darkOwner"]: "",
            [other]:
              value[other] ||
              (getThemeDefinition(value.baseThemeId) ? value.baseThemeId : ""),
          });
        } else
          await patch({
            [half === "light" ? "lightOwner" : "darkOwner"]: identity,
          });
        return;
      }
      const mode =
        definition && getThemeModes(definition).length === 1
          ? definition.appearance
          : current.current.value.preferences.mode;
      await patch({
        baseThemeId: identity,
        lightOwner: "",
        darkOwner: "",
        mode,
      });
    },
    [patch],
  );
  /** End only the draft session and restore the actual saved appearance. */
  const closeEditor = useCallback(() => {
    setEditor(null);
    updateEditorState({ isDirty: false, isPending: false });
    restore();
  }, [restore]);
  /** Create a distinct draft with its actual optimistic revision, even when an editor is already open. */
  const openEditor = useCallback(
    (session: EditorInput) =>
      setEditor({
        ...session,
        identity: ++editorSequence.current,
        revision: current.current.revision,
        signature: session.editing ? serializeThemeFile(session.editing) : null,
      }),
    [],
  );
  return (
    <APPEARANCE_CONTEXT.Provider
      value={{
        snapshot,
        appearance,
        isBusy,
        error,
        save,
        patch,
        select,
        editor,
        editorState,
        setEditorState,
        openEditor,
        closeEditor,
        restore,
      }}
    >
      {children}
    </APPEARANCE_CONTEXT.Provider>
  );
}

/** Require a root appearance provider so navigation never discards editor state. */
export function useAppearance(): AppearanceContextValue {
  const value = useContext(APPEARANCE_CONTEXT);
  if (!value) throw new Error("Appearance provider is missing");
  return value;
}
