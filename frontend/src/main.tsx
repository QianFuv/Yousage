/** Desktop routes share saved appearance and a root-mounted editor across navigation. */
import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Reference } from "@reference";
import * as nativeApi from "../bindings/yousage/app";
import { canvasBackground, type Bootstrap } from "./native";
import {
  AppearanceProvider,
  applySavedAppearance,
  useAppearance,
} from "./theme/appearance";
import { RootThemeEditor, ThemePanel } from "./theme/ThemePanel";
import "./style.css";

/** Keep Usage and Settings independent while synchronizing actual CSS with the native window. */
function Desktop({ bootstrap }: { bootstrap: Bootstrap }) {
  const context = useAppearance();
  const [route, setRoute] = useState<"usage" | "settings">("usage");
  const [section, setSection] = useState<"appearance" | "diagnostics">(
    "appearance",
  );
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
    <div className="desktop-layout" data-density="comfortable">
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
              aria-pressed={section === "diagnostics"}
              onClick={() => setSection("diagnostics")}
            >
              About & diagnostics
            </button>
          </div>
        </section>
      )}
      <main data-file-drop-target>
        {route === "usage" ? (
          <Reference
            appearance={context.appearance}
            view="usage"
            shouldManageTheme={false}
          />
        ) : section === "appearance" ? (
          <ThemePanel />
        ) : (
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
        )}
      </main>
      <RootThemeEditor />
    </div>
  );
}

/** Load authoritative Go state before rendering the first visible frame. */
async function start() {
  const bootstrap = await nativeApi.GetBootstrap();
  const appearance = await nativeApi.GetAppearance();
  applySavedAppearance(appearance, bootstrap.isSystemDark);
  createRoot(document.getElementById("root")!).render(
    <AppearanceProvider
      initial={appearance}
      isSystemDark={bootstrap.isSystemDark}
    >
      <Desktop bootstrap={bootstrap} />
    </AppearanceProvider>,
  );
}
void start().catch(async (cause) => {
  document.getElementById("root")!.textContent = String(cause);
  await nativeApi.FrontendReady();
});
