/** Desktop routes share saved appearance across navigation. */
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
import { Toaster } from "./notify";
import {
  createDiagnosticReporter,
  installDiagnosticListeners,
  uncaughtRenderHandler,
} from "./diagnostics";
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
  SmartphoneIcon,
  TimerIcon,
  type LucideIcon,
} from "lucide-react";
import { Button } from "../../tools/parity/upstream/apps/web/src/components/ui/button";
import { NavItem } from "./NavItem";
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
const SyncPanel = lazy(() =>
  import("./settings/SyncPanel").then((module) => ({
    default: module.SyncPanel,
  })),
);
const ThemePanel = lazy(() =>
  import("./theme/ThemePanel").then((module) => ({
    default: module.ThemePanel,
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
  useEffect(() => {
    /** Open a destination requested by a component outside the navigation, such as the share dialog. */
    const requested = (event: Event) => {
      const detail = (event as CustomEvent<{ section?: SettingsSection }>)
        .detail;
      if (detail?.section) navigate("settings", detail.section);
    };
    window.addEventListener("yousage-navigate", requested);
    return () => window.removeEventListener("yousage-navigate", requested);
  }, [navigate]);
  const [status, setStatus] = useState("");
  const closeState = useRef({ isDirty: false, isPending: false });
  closeState.current = {
    isDirty: sourcesState.isDirty || isThemeDirty,
    isPending:
      data.isSavingDisplay || sourcesState.isPending || appearance.isBusy,
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
    sources: sourcesState.isDirty,
    appearance: isThemeDirty,
  };
  return (
    <div className="desktop-layout">
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
                  shortLabel={item.shortLabel && t(item.shortLabel)}
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
                isCurrent={route === "statistics"}
                onClick={() => navigate("statistics")}
              />
              <NavItem
                icon={GaugeIcon}
                label={t("Quota")}
                isCurrent={route === "quota"}
                onClick={() => navigate("quota")}
              />
              <NavItem
                icon={FlameIcon}
                label={t("Loss")}
                isCurrent={route === "loss"}
                onClick={() => navigate("loss")}
              />
              <NavItem
                icon={TimerIcon}
                label={t("Speed")}
                isCurrent={route === "speed"}
                onClick={() => navigate("speed")}
              />
            </div>
            <div className="sidebar-footer">
              <NavItem
                icon={SettingsIcon}
                label={t("Settings")}
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
            onSettings={(next) => navigate("settings", next)}
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
          {visitedSections.has("appearance") && (
            <div hidden={route !== "settings" || section !== "appearance"}>
              <ThemePanel
                onDirtyChange={setIsThemeDirty}
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
          {visitedSections.has("sync") && (
            <div hidden={route !== "settings" || section !== "sync"}>
              <SyncPanel />
            </div>
          )}
          {visitedSections.has("diagnostics") && (
            <div hidden={route !== "settings" || section !== "diagnostics"}>
              <DiagnosticsPanel nativeStatus={status} />
            </div>
          )}
        </Suspense>
      </main>
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
  | "sources"
  | "sync";

const SETTINGS_SECTIONS: readonly {
  id: SettingsSection;
  label: string;
  shortLabel?: string;
  icon: LucideIcon;
}[] = [
  { id: "appearance", label: msg("Appearance"), icon: PaletteIcon },
  {
    id: "display",
    label: msg("Display & behavior"),
    shortLabel: msg("Display"),
    icon: SlidersHorizontalIcon,
  },
  {
    id: "sources",
    label: msg("Sources & connections"),
    shortLabel: msg("Sources"),
    icon: PlugIcon,
  },
  {
    id: "sync",
    label: msg("Sync"),
    shortLabel: msg("Sync"),
    icon: SmartphoneIcon,
  },
  {
    id: "diagnostics",
    label: msg("About & diagnostics"),
    shortLabel: msg("About"),
    icon: InfoIcon,
  },
];

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

/** Fixed render/internal categories through the generated binding; nothing else is sent. */
const reportDiagnostic = createDiagnosticReporter((category) =>
  nativeApi.RecordFrontendDiagnostic(category),
);
installDiagnosticListeners(window, reportDiagnostic);

/** Load authoritative Go state before rendering the first visible frame. */
async function start() {
  const [bootstrap, appearance, display] = await Promise.all([
    nativeApi.GetBootstrap(),
    nativeApi.GetAppearance(),
    nativeApi.GetDisplay(),
  ]);
  applyLanguage(display.value.language, bootstrap.systemLocale);
  applySavedAppearance(appearance, bootstrap.isSystemDark);
  createRoot(document.getElementById("root")!, {
    onUncaughtError: uncaughtRenderHandler(reportDiagnostic),
  }).render(
    <AppearanceProvider
      initial={appearance}
      isSystemDark={bootstrap.isSystemDark}
    >
      <DataProvider initial={display}>
        <Desktop systemLocale={bootstrap.systemLocale} />
      </DataProvider>
      <Toaster />
    </AppearanceProvider>,
  );
}
void start().catch(async (cause) => {
  reportDiagnostic("internal");
  document.getElementById("root")!.textContent = String(cause);
  await nativeApi.FrontendReady();
});
