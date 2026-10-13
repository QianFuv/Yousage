/** Desktop routes share saved appearance and a root-mounted editor across navigation. */
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useState,
  lazy,
  Suspense,
} from "react";
import { Events } from "@wailsio/runtime";
import { useRef } from "react";

import { createRoot } from "react-dom/client";
import { DataProvider, useData } from "./usage/controller";
import { UsagePage } from "./usage/UsagePage";

import * as nativeApi from "../bindings/yousage/internal/desktop/app";
import { canvasBackground, sidebarBackground } from "./native";
import { configureTimestampLocale } from "../vendor/timestampFormat";
import {
  formattingLocale,
  msg,
  resolveLanguage,
  setLanguage,
  t,
  useI18n,
} from "./i18n";
import {
  AppearanceProvider,
  applySavedAppearance,
  useAppearance,
} from "./theme/appearance";
import type { ThemeFileInput } from "../bindings/yousage/internal/desktop/models";
import {
  ArrowLeftIcon,
  ChartNoAxesCombinedIcon,
  FlameIcon,
  GaugeIcon,
  InfoIcon,
  PaletteIcon,
  PlugIcon,
  SettingsIcon,
  SlidersHorizontalIcon,
  TagsIcon,
  TimerIcon,
  type LucideIcon,
} from "lucide-react";
import { Button } from "../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  AlertDialog,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
} from "../vendor/ui/alert-dialog";

const DisplayPanel = lazy(() =>
  import("./settings/DisplayPanel").then((module) => ({
    default: module.DisplayPanel,
  })),
);
const SourcesPanel = lazy(() =>
  import("./settings/SourcesPanel").then((module) => ({
    default: module.SourcesPanel,
  })),
);
const DiagnosticsPanel = lazy(() =>
  import("./settings/DiagnosticsPanel").then((module) => ({
    default: module.DiagnosticsPanel,
  })),
);
const LossPage = lazy(() =>
  import("./loss/LossPage").then((module) => ({
    default: module.LossPage,
  })),
);
const SpeedPage = lazy(() =>
  import("./speed/SpeedPage").then((module) => ({
    default: module.SpeedPage,
  })),
);
const PricingPanel = lazy(() =>
  import("./usage/PricingPanel").then((module) => ({
    default: module.PricingPanel,
  })),
);
const ThemePanel = lazy(() =>
  import("./theme/ThemePanel").then((module) => ({
    default: module.ThemePanel,
  })),
);
const RootThemeEditor = lazy(() =>
  import("./theme/ThemePanel").then((module) => ({
    default: module.RootThemeEditor,
  })),
);
import "./style.css";

/** Keep Usage and Settings independent while synchronizing actual CSS with the native window. */
function Desktop({ systemLocale }: { systemLocale: string }) {
  useI18n();
  const appearance = useAppearance();
  const [sourcesState, setSourcesState] = useState({
    isDirty: false,
    isPending: false,
  });
  const changeSourcesState = useCallback(
    (isDirty: boolean, isPending: boolean) =>
      setSourcesState({ isDirty, isPending }),
    [],
  );
  const [isThemeDirty, setIsThemeDirty] = useState(false);
  const [isClosing, setIsClosing] = useState(false);
  const [closeError, setCloseError] = useState("");
  const data = useData();
  useLayoutEffect(() => {
    document.documentElement.dataset.density = data.display.value.density;
  }, [data.display.value.density]);
  useLayoutEffect(
    () => applyLanguage(data.display.value.language, systemLocale),
    [data.display.value.language, systemLocale],
  );
  useEffect(() => {
    void nativeApi
      .SetTrayLabels({
        usage: t("Usage"),
        quota: t("Quota"),
        loss: t("Loss"),
        speed: t("Speed"),
        settings: t("Settings"),
        quit: t("Quit"),
      })
      .catch(() => {});
  }, [data.display.value.language, systemLocale]);
  const [priceModel, setPriceModel] = useState<string>();
  const [pricingState, setPricingState] = useState({
    isDirty: false,
    isPending: false,
  });
  const changePricingState = useCallback(
    (isDirty: boolean, isPending: boolean) =>
      setPricingState({ isDirty, isPending }),
    [],
  );
  const [route, setRoute] = useState<PageRoute | "settings">("statistics");
  const [section, setSection] = useState<SettingsSection>("appearance");
  const usageRoute = useRef<PageRoute>("statistics");
  const [visitedPages, setVisitedPages] = useState<Set<PageRoute>>(
    () => new Set(),
  );
  const [visitedSections, setVisitedSections] = useState<Set<string>>(
    () => new Set(),
  );
  const [droppedThemes, setDroppedThemes] = useState<ThemeFileInput[]>([]);
  const positions = useRef<Record<string, number>>({});
  const content = useRef<HTMLElement>(null);
  /** Capture the current viewport before hiding its page, then select the destination. */
  const navigate = useCallback(
    (nextRoute: typeof route, nextSection = section) => {
      const identity = route === "settings" ? `settings:${section}` : route;
      positions.current[identity] = content.current?.scrollTop ?? 0;
      if (nextRoute !== "settings") {
        usageRoute.current = nextRoute;
        setVisitedPages((current) => new Set([...current, nextRoute]));
      }
      if (nextRoute === "settings")
        setVisitedSections((current) => new Set([...current, nextSection]));
      setSection(nextSection);
      setRoute(nextRoute);
    },
    [route, section],
  );
  useLayoutEffect(() => {
    const identity = route === "settings" ? `settings:${section}` : route;
    if (content.current)
      content.current.scrollTop = positions.current[identity] ?? 0;
  }, [route, section]);
  useEffect(
    () =>
      Events.On("theme-files-dropped", (event) => {
        setDroppedThemes(event.data as ThemeFileInput[]);
        navigate("settings", "appearance");
      }),
    [navigate],
  );
  useEffect(
    () =>
      Events.On("tray-navigate", (event) =>
        navigate(event.data as PageRoute | "settings"),
      ),
    [navigate],
  );
  const [status, setStatus] = useState("");
  const closeState = useRef({ isDirty: false, isPending: false });
  closeState.current = {
    isDirty:
      pricingState.isDirty ||
      sourcesState.isDirty ||
      isThemeDirty ||
      appearance.editorState.isDirty,
    isPending:
      data.isSavingDisplay ||
      pricingState.isPending ||
      sourcesState.isPending ||
      appearance.isBusy ||
      appearance.editorState.isPending,
  };
  useEffect(() => {
    /** Reveal a window hidden in the tray before asking about its drafts. */
    const confirmVisibly = (error: string) => {
      void nativeApi.ShowWindow().catch(() => {});
      setCloseError(error);
      setIsClosing(true);
    };
    const dispose = Events.On("close-requested", () => {
      if (closeState.current.isDirty || closeState.current.isPending)
        confirmVisibly("");
      else
        void nativeApi
          .ConfirmClose()
          .catch((cause) => confirmVisibly(String(cause)));
    });
    void nativeApi
      .SetCloseBridge(true)
      .catch((cause) => setStatus(String(cause)));
    return () => {
      dispose();
      void nativeApi.SetCloseBridge(false).catch(() => {});
    };
  }, []);

  useEffect(() => {
    /** Offer the same settings shortcut in every application route. */
    const changed = (event: KeyboardEvent) => {
      if (isClosing) return;
      if ((event.ctrlKey || event.metaKey) && event.key === ",") {
        event.preventDefault();
        navigate("settings");
      } else if ((event.ctrlKey || event.metaKey) && event.key === "1") {
        event.preventDefault();
        navigate("statistics");
      } else if ((event.ctrlKey || event.metaKey) && event.key === "2") {
        event.preventDefault();
        navigate("quota");
      } else if ((event.ctrlKey || event.metaKey) && event.key === "3") {
        event.preventDefault();
        navigate("loss");
      } else if ((event.ctrlKey || event.metaKey) && event.key === "4") {
        event.preventDefault();
        navigate("speed");
      } else if (
        (event.ctrlKey || event.metaKey) &&
        event.altKey &&
        event.key.toLowerCase() === "a"
      ) {
        event.preventDefault();
        if (event.shiftKey) {
          const modes = ["system", "light", "dark"];
          const mode = appearance.snapshot.value.preferences.mode;
          void appearance
            .patch({ mode: modes[(modes.indexOf(mode) + 1) % modes.length] })
            .catch((cause) => setStatus(String(cause)));
        } else navigate("settings", "appearance");
      } else if (
        (event.ctrlKey || event.metaKey) &&
        event.key.toLowerCase() === "r"
      ) {
        event.preventDefault();
        if (route !== "settings") void data.refresh(true, true);
      } else if (event.key === "Escape") setIsClosing(false);
    };
    window.addEventListener("keydown", changed);
    return () => window.removeEventListener("keydown", changed);
  }, [route, data.refresh, navigate, appearance, isClosing]);
  useEffect(() => {
    /** Synchronize preview colors as well as saved themes without waiting on hidden WebView frames. */
    const synchronize = () => {
      void nativeApi
        .SyncAppearance(
          document.documentElement.classList.contains("dark"),
          ...canvasBackground(),
          ...sidebarBackground(),
        )
        .catch((cause) => setStatus(String(cause)));
    };
    synchronize();
    const observer = new MutationObserver(synchronize);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "style", "data-theme-id"],
    });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    let isDisposed = false;
    /** Show only after the persisted palette and committed DOM background reach the native surface. */
    async function reveal() {
      try {
        await nativeApi.SyncAppearance(
          document.documentElement.classList.contains("dark"),
          ...canvasBackground(),
          ...sidebarBackground(),
        );
      } catch (cause) {
        setStatus(String(cause));
        setRoute("settings");
        setSection("diagnostics");
        setVisitedSections((current) => new Set([...current, "diagnostics"]));
      }
      if (!isDisposed) await nativeApi.FrontendReady();
    }
    void reveal();
    return () => {
      isDisposed = true;
    };
  }, []);
  const sectionState: Partial<Record<SettingsSection, boolean>> = {
    pricing: pricingState.isDirty,
    sources: sourcesState.isDirty,
    appearance: isThemeDirty || appearance.editorState.isDirty,
  };
  return (
    <div className="desktop-layout" data-density={data.display.value.density}>
      <nav
        inert={isClosing}
        className="app-sidebar"
        aria-label={t("Application")}
      >
        <div className="sidebar-brand">
          <strong className="sidebar-wordmark" aria-label="Yousage">
            <span aria-hidden>Y</span>
            <span className="sidebar-wordmark-tail" aria-hidden>
              ou
            </span>
            <span className="sidebar-wordmark-tail" aria-hidden>
              sage
            </span>
          </strong>
        </div>
        {route === "settings" ? (
          <>
            <NavItem
              icon={ArrowLeftIcon}
              label={t("Back")}
              title={t("Back to usage")}
              onClick={() => navigate(usageRoute.current)}
            />
            <div className="sidebar-group-label">{t("Settings")}</div>
            <div className="sidebar-group">
              {SETTINGS_SECTIONS.map((item) => (
                <NavItem
                  key={item.id}
                  icon={item.icon}
                  label={t(item.label)}
                  isCurrent={section === item.id}
                  isDirty={sectionState[item.id]}
                  onClick={() => navigate("settings", item.id)}
                />
              ))}
            </div>
          </>
        ) : (
          <>
            <div className="sidebar-group">
              <NavItem
                icon={ChartNoAxesCombinedIcon}
                label={t("Usage")}
                title={`${t("Usage")} (Ctrl+1)`}
                isCurrent={route === "statistics"}
                onClick={() => navigate("statistics")}
              />
              <NavItem
                icon={GaugeIcon}
                label={t("Quota")}
                title={`${t("Quota")} (Ctrl+2)`}
                isCurrent={route === "quota"}
                onClick={() => navigate("quota")}
              />
              <NavItem
                icon={FlameIcon}
                label={t("Loss")}
                title={`${t("Loss")} (Ctrl+3)`}
                isCurrent={route === "loss"}
                onClick={() => navigate("loss")}
              />
              <NavItem
                icon={TimerIcon}
                label={t("Speed")}
                title={`${t("Speed")} (Ctrl+4)`}
                isCurrent={route === "speed"}
                onClick={() => navigate("speed")}
              />
            </div>
            <div className="sidebar-footer">
              <NavItem
                icon={SettingsIcon}
                label={t("Settings")}
                title={`${t("Settings")} (Ctrl+,)`}
                onClick={() => navigate("settings")}
              />
            </div>
          </>
        )}
      </nav>
      <main
        ref={content}
        inert={isClosing}
        data-file-drop-target
        tabIndex={-1}
        aria-label={t("Page content")}
      >
        <div hidden={route !== "statistics" && route !== "quota"}>
          <UsagePage
            isActive={route === "statistics" || route === "quota"}
            view={route === "statistics" ? "statistics" : "limits"}
            onSettings={(next, model) => {
              if (model) setPriceModel(model);
              navigate("settings", next);
            }}
          />
        </div>
        <Suspense fallback={null}>
          {visitedPages.has("loss") && (
            <div hidden={route !== "loss"}>
              <LossPage isActive={route === "loss"} />
            </div>
          )}
          {visitedPages.has("speed") && (
            <div hidden={route !== "speed"}>
              <SpeedPage isActive={route === "speed"} />
            </div>
          )}
        </Suspense>
        <Suspense
          fallback={
            <p className="p-6 text-sm text-muted-foreground" role="status">
              {t("Loading settings…")}
            </p>
          }
        >
          {visitedSections.has("pricing") && (
            <div hidden={route !== "settings" || section !== "pricing"}>
              <PricingPanel
                model={priceModel}
                onDirtyChange={changePricingState}
              />
            </div>
          )}
          {visitedSections.has("appearance") && (
            <div hidden={route !== "settings" || section !== "appearance"}>
              <ThemePanel
                onDirtyChange={setIsThemeDirty}
                isActive={route === "settings" && section === "appearance"}
                droppedFiles={droppedThemes}
              />
            </div>
          )}
          {visitedSections.has("display") && (
            <div hidden={route !== "settings" || section !== "display"}>
              <DisplayPanel />
            </div>
          )}
          {visitedSections.has("sources") && (
            <div hidden={route !== "settings" || section !== "sources"}>
              <SourcesPanel onDraftChange={changeSourcesState} />
            </div>
          )}
          {visitedSections.has("diagnostics") && (
            <div hidden={route !== "settings" || section !== "diagnostics"}>
              <DiagnosticsPanel nativeStatus={status} />
            </div>
          )}
        </Suspense>
      </main>
      {appearance.editor && (
        <Suspense fallback={null}>
          <RootThemeEditor />
        </Suspense>
      )}
      <AlertDialog
        open={isClosing}
        onOpenChange={(isOpen) => {
          if (!isOpen) setIsClosing(false);
        }}
      >
        <AlertDialogPopup>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("Close Yousage?")}</AlertDialogTitle>
            <AlertDialogDescription>
              {closeState.current.isPending
                ? t(
                    "An account or settings change is running. Wait for the result before closing.",
                  )
                : t("Unsaved source, price or theme drafts will be discarded.")}
            </AlertDialogDescription>
            {closeError && (
              <p role="status" className="text-sm text-destructive-foreground">
                {closeError}
              </p>
            )}
          </AlertDialogHeader>
          <AlertDialogFooter>
            <Button variant="outline" onClick={() => setIsClosing(false)}>
              {t("Keep editing")}
            </Button>
            <Button
              variant="destructive"
              disabled={closeState.current.isPending}
              onClick={() =>
                void nativeApi
                  .ConfirmClose()
                  .catch((cause) => setCloseError(String(cause)))
              }
            >
              {t("Discard drafts and exit")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogPopup>
      </AlertDialog>
    </div>
  );
}

/** Top-level pages outside Settings. */
type PageRoute = "statistics" | "quota" | "loss" | "speed";

/** Settings destinations in sidebar order. */
type SettingsSection =
  | "appearance"
  | "display"
  | "diagnostics"
  | "pricing"
  | "sources";

const SETTINGS_SECTIONS: readonly {
  id: SettingsSection;
  label: string;
  icon: LucideIcon;
}[] = [
  { id: "appearance", label: msg("Appearance"), icon: PaletteIcon },
  {
    id: "display",
    label: msg("Display & behavior"),
    icon: SlidersHorizontalIcon,
  },
  { id: "sources", label: msg("Sources & connections"), icon: PlugIcon },
  { id: "pricing", label: msg("Prices & mappings"), icon: TagsIcon },
  { id: "diagnostics", label: msg("About & diagnostics"), icon: InfoIcon },
];

/**
 * Sidebar navigation row that collapses to its icon in narrow windows.
 *
 * @param props - Icon, label, current/dirty state and click handler.
 * @returns The navigation button.
 */
function NavItem({
  icon: Icon,
  label,
  title,
  isCurrent,
  isDirty,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  title?: string;
  isCurrent?: boolean;
  isDirty?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="sidebar-item"
      aria-current={isCurrent ? "page" : undefined}
      aria-label={isDirty ? t("{label} (unsaved)", { label }) : label}
      title={title ?? label}
      onClick={onClick}
    >
      <Icon aria-hidden />
      <span className="sidebar-item-label">{label}</span>
      {isDirty && <span className="sidebar-dirty" aria-hidden />}
    </button>
  );
}

/**
 * Apply the saved language and match reset timestamps to it.
 *
 * Following the system keeps the regional locale for timestamps; an explicit
 * language uses that language's own conventions.
 *
 * @param preference - Saved language preference.
 * @param systemLocale - Operating system locale.
 */
function applyLanguage(preference: string, systemLocale: string) {
  const language = resolveLanguage(preference, systemLocale);
  setLanguage(language);
  configureTimestampLocale(
    language === resolveLanguage("system", systemLocale)
      ? systemLocale
      : formattingLocale(),
  );
}

/** Load authoritative Go state before rendering the first visible frame. */
async function start() {
  const [bootstrap, appearance, display] = await Promise.all([
    nativeApi.GetBootstrap(),
    nativeApi.GetAppearance(),
    nativeApi.GetDisplay(),
  ]);
  applyLanguage(display.value.language, bootstrap.systemLocale);
  applySavedAppearance(appearance, bootstrap.isSystemDark);
  createRoot(document.getElementById("root")!).render(
    <AppearanceProvider
      initial={appearance}
      isSystemDark={bootstrap.isSystemDark}
    >
      <DataProvider initial={display}>
        <Desktop systemLocale={bootstrap.systemLocale} />
      </DataProvider>
    </AppearanceProvider>,
  );
}
void start().catch(async (cause) => {
  document.getElementById("root")!.textContent = String(cause);
  await nativeApi.FrontendReady();
});
