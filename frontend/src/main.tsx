/** Desktop routes share saved appearance and a root-mounted editor across navigation. */
import { useCallback, useEffect, useLayoutEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import { useRef } from "react";
import { DisplayPanel } from "./settings/DisplayPanel";
import { SourcesPanel } from "./settings/SourcesPanel";
import { DiagnosticsPanel } from "./settings/DiagnosticsPanel";
import { createRoot } from "react-dom/client";
import { DataProvider, useData } from "./usage/controller";
import { UsagePage } from "./usage/UsagePage";
import { PricingPanel } from "./usage/PricingPanel";
import * as nativeApi from "../bindings/yousage/app";
import { canvasBackground } from "./native";
import {
  AppearanceProvider,
  applySavedAppearance,
  useAppearance,
} from "./theme/appearance";
import { RootThemeEditor, ThemePanel } from "./theme/ThemePanel";
import "./style.css";

/** Keep Usage and Settings independent while synchronizing actual CSS with the native window. */
function Desktop() {
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
  const [route, setRoute] = useState<"usage" | "settings">("usage");
  const [section, setSection] = useState<
    "appearance" | "display" | "diagnostics" | "pricing" | "sources"
  >("appearance");
  const positions = useRef<Record<string, number>>({});
  const content = useRef<HTMLElement>(null);
  /** Capture the current viewport before hiding its page, then select the destination. */
  const navigate = useCallback(
    (nextRoute: typeof route, nextSection = section) => {
      const identity = route === "usage" ? "usage" : `settings:${section}`;
      positions.current[identity] = content.current?.scrollTop ?? 0;
      setSection(nextSection);
      setRoute(nextRoute);
    },
    [route, section],
  );
  useLayoutEffect(() => {
    const identity = route === "usage" ? "usage" : `settings:${section}`;
    if (content.current)
      content.current.scrollTop = positions.current[identity] ?? 0;
  }, [route, section]);
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
    const dispose = Events.On("close-requested", () => {
      if (closeState.current.isDirty || closeState.current.isPending) {
        setIsClosing(true);
        setCloseError("");
      } else
        void nativeApi.ConfirmClose().catch((cause) => {
          setCloseError(String(cause));
          setIsClosing(true);
        });
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
    if (!isClosing) return;
    const previous = document.activeElement as HTMLElement | null;
    const dialog = document.querySelector<HTMLElement>(".close-dialog");
    dialog?.querySelector<HTMLButtonElement>("button")?.focus();
    /** Keep keyboard focus inside the blocking native close decision. */
    const trap = (event: KeyboardEvent) => {
      if (event.key !== "Tab") return;
      const buttons = [
        ...(dialog?.querySelectorAll<HTMLButtonElement>(
          "button:not(:disabled)",
        ) ?? []),
      ];
      const first = buttons[0],
        last = buttons.at(-1);
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    window.addEventListener("keydown", trap);
    return () => {
      window.removeEventListener("keydown", trap);
      previous?.focus();
    };
  }, [isClosing]);
  useEffect(() => {
    /** Offer the same settings shortcut in every application route. */
    const changed = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key === ",") {
        event.preventDefault();
        navigate("settings");
      } else if ((event.ctrlKey || event.metaKey) && event.key === "1") {
        event.preventDefault();
        navigate("usage");
      } else if (
        (event.ctrlKey || event.metaKey) &&
        event.key.toLowerCase() === "r"
      ) {
        event.preventDefault();
        if (route === "usage") void data.refresh(true, true);
      } else if (event.key === "Escape") setIsClosing(false);
    };
    window.addEventListener("keydown", changed);
    return () => window.removeEventListener("keydown", changed);
  }, [route, data.refresh, navigate]);
  useEffect(() => {
    /** Synchronize preview colors as well as saved themes without waiting on hidden WebView frames. */
    const synchronize = () => {
      const [red, green, blue] = canvasBackground();
      void nativeApi
        .SyncAppearance(
          document.documentElement.classList.contains("dark"),
          red,
          green,
          blue,
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
        const [red, green, blue] = canvasBackground();
        await nativeApi.SyncAppearance(
          document.documentElement.classList.contains("dark"),
          red,
          green,
          blue,
        );
      } catch (cause) {
        setStatus(String(cause));
        setRoute("settings");
        setSection("diagnostics");
      }
      if (!isDisposed) await nativeApi.FrontendReady();
    }
    void reveal();
    return () => {
      isDisposed = true;
    };
  }, []);
  return (
    <div className="desktop-layout" data-density={data.display.value.density}>
      <nav
        inert={isClosing}
        className="app-navigation"
        aria-label="Application"
      >
        <strong>Yousage</strong>
        <button
          aria-current={route === "usage" ? "page" : undefined}
          onClick={() => navigate("usage")}
        >
          Usage
        </button>
        <button
          aria-current={route === "settings" ? "page" : undefined}
          onClick={() => navigate("settings")}
        >
          Settings
        </button>
      </nav>
      {route === "settings" && (
        <section
          inert={isClosing}
          className="settings-controls"
          aria-label="Settings"
        >
          <div className="settings-sections" aria-label="Settings sections">
            <button
              aria-pressed={section === "display"}
              onClick={() => navigate("settings", "display")}
            >
              Display &amp; behavior
            </button>
            <button
              aria-pressed={section === "appearance"}
              onClick={() => navigate("settings", "appearance")}
            >
              Appearance
            </button>
            <button
              aria-pressed={section === "pricing"}
              onClick={() => navigate("settings", "pricing")}
            >
              Prices &amp; mappings{pricingState.isDirty ? " · Unsaved" : ""}
            </button>
            <button
              aria-pressed={section === "sources"}
              onClick={() => navigate("settings", "sources")}
            >
              Sources &amp; connections
            </button>
            <button
              aria-pressed={section === "diagnostics"}
              onClick={() => navigate("settings", "diagnostics")}
            >
              About & diagnostics
            </button>
          </div>
        </section>
      )}
      <main ref={content} inert={isClosing} data-file-drop-target>
        <div hidden={route !== "usage"}>
          <UsagePage
            isActive={route === "usage"}
            onSettings={(next, model) => {
              if (model) setPriceModel(model);
              navigate("settings", next);
            }}
          />
        </div>
        <div hidden={route !== "settings" || section !== "pricing"}>
          <PricingPanel model={priceModel} onDirtyChange={changePricingState} />
        </div>
        <div hidden={route !== "settings" || section !== "appearance"}>
          <ThemePanel onDirtyChange={setIsThemeDirty} />
        </div>
        <div hidden={route !== "settings" || section !== "display"}>
          <DisplayPanel />
        </div>
        <div hidden={route !== "settings" || section !== "sources"}>
          <SourcesPanel onDraftChange={changeSourcesState} />
        </div>
        <div hidden={route !== "settings" || section !== "diagnostics"}>
          <DiagnosticsPanel nativeStatus={status} />
        </div>
      </main>
      <RootThemeEditor />
      {isClosing && (
        <div className="close-backdrop">
          <section
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="close-title"
            className="close-dialog"
          >
            <h2 id="close-title">Close Yousage?</h2>
            <p>
              {closeState.current.isPending
                ? "An account or settings change is running. Wait for the result before closing."
                : "Unsaved source, price or theme drafts will be discarded."}
            </p>
            {closeError && <p role="status">{closeError}</p>}
            <div className="settings-actions">
              <button onClick={() => setIsClosing(false)}>Keep editing</button>
              <button
                disabled={closeState.current.isPending}
                onClick={() =>
                  void nativeApi
                    .ConfirmClose()
                    .catch((cause) => setCloseError(String(cause)))
                }
              >
                Discard drafts and exit
              </button>
            </div>
          </section>
        </div>
      )}
    </div>
  );
}

/** Load authoritative Go state before rendering the first visible frame. */
async function start() {
  const bootstrap = await nativeApi.GetBootstrap();
  const appearance = await nativeApi.GetAppearance();
  const display = await nativeApi.GetDisplay();
  applySavedAppearance(appearance, bootstrap.isSystemDark);
  createRoot(document.getElementById("root")!).render(
    <AppearanceProvider
      initial={appearance}
      isSystemDark={bootstrap.isSystemDark}
    >
      <DataProvider initial={display}>
        <Desktop />
      </DataProvider>
    </AppearanceProvider>,
  );
}
void start().catch(async (cause) => {
  document.getElementById("root")!.textContent = String(cause);
  await nativeApi.FrontendReady();
});
