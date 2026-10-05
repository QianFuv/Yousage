/** Desktop routes share saved appearance and a root-mounted editor across navigation. */
import { useCallback, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { DataProvider, useData } from "./usage/controller";
import { UsagePage } from "./usage/UsagePage";
import { PricingPanel } from "./usage/PricingPanel";
import * as nativeApi from "../bindings/yousage/app";
import { canvasBackground, type Bootstrap } from "./native";
import { AppearanceProvider, applySavedAppearance } from "./theme/appearance";
import { RootThemeEditor, ThemePanel } from "./theme/ThemePanel";
import "./style.css";

/** Keep Usage and Settings independent while synchronizing actual CSS with the native window. */
function Desktop({ bootstrap }: { bootstrap: Bootstrap }) {
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
    "appearance" | "diagnostics" | "pricing" | "sources"
  >("appearance");
  const [status, setStatus] = useState("");
  useEffect(() => {
    /** Offer the same settings shortcut in every application route. */
    const changed = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key === ",") {
        event.preventDefault();
        setRoute("settings");
      }
    };
    window.addEventListener("keydown", changed);
    return () => window.removeEventListener("keydown", changed);
  }, []);
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
      <nav className="app-navigation" aria-label="Application">
        <strong>Yousage</strong>
        <button
          aria-current={route === "usage" ? "page" : undefined}
          onClick={() => setRoute("usage")}
        >
          Usage
        </button>
        <button
          aria-current={route === "settings" ? "page" : undefined}
          onClick={() => setRoute("settings")}
        >
          Settings
        </button>
      </nav>
      {route === "settings" && (
        <section className="settings-controls" aria-label="Settings">
          <div className="settings-sections">
            <button
              aria-pressed={section === "appearance"}
              onClick={() => setSection("appearance")}
            >
              Appearance & display
            </button>
            <button
              aria-pressed={section === "pricing"}
              onClick={() => setSection("pricing")}
            >
              Prices &amp; mappings{pricingState.isDirty ? " · Unsaved" : ""}
            </button>
            <button
              aria-pressed={section === "sources"}
              onClick={() => setSection("sources")}
            >
              Sources &amp; refresh
            </button>
            <button
              aria-pressed={section === "diagnostics"}
              onClick={() => setSection("diagnostics")}
            >
              About & diagnostics
            </button>
          </div>
        </section>
      )}
      <main data-file-drop-target>
        <div hidden={route !== "usage"}>
          <UsagePage
            isActive={route === "usage"}
            onSettings={(next, model) => {
              if (model) setPriceModel(model);
              setSection(next);
              setRoute("settings");
            }}
          />
        </div>
        <div hidden={route !== "settings" || section !== "pricing"}>
          <PricingPanel model={priceModel} onDirtyChange={changePricingState} />
        </div>
        {route === "settings" &&
          (section === "appearance" ? (
            <ThemePanel />
          ) : section === "sources" ? (
            <p className="usage-notice">
              Data source controls are being integrated.
            </p>
          ) : section === "diagnostics" ? (
            <aside className="native-toolbar">
              <strong>Yousage · Wails {bootstrap.wailsVersion}</strong>
              <span>
                {bootstrap.providers.length} history providers ·{" "}
                {bootstrap.roleCount} theme roles
              </span>
              <output role="status">
                {status || "Themes are saved in the application database."}
              </output>
              <small>Lifecycle log: {bootstrap.lifecycleLog}</small>
              <button onClick={() => void nativeApi.OpenDocumentation()}>
                Wails documentation
              </button>
            </aside>
          ) : null)}
      </main>
      <RootThemeEditor />
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
        <Desktop bootstrap={bootstrap} />
      </DataProvider>
    </AppearanceProvider>,
  );
}
void start().catch(async (cause) => {
  document.getElementById("root")!.textContent = String(cause);
  await nativeApi.FrontendReady();
});
