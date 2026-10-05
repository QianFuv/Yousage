/** Native fixture host; startup visibility is gated on theme application and the committed React frame. */
import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Reference } from "@reference";
import * as nativeApi from "../bindings/yousage/app";
import { Events } from "@wailsio/runtime";
import {
  canvasBackground,
  droppedNames,
  effectiveAppearance,
  type Bootstrap,
} from "./native";
import "./style.css";

const API = nativeApi;
const MEDIA = window.matchMedia("(prefers-color-scheme: dark)");

/** Render bounded native demos around the unmodified upstream component graph. */
function Desktop({ bootstrap }: { bootstrap: Bootstrap }) {
  const [route, setRoute] = useState<"usage" | "settings">("usage");
  const [settingsSection, setSettingsSection] = useState<
    "appearance" | "diagnostics"
  >("appearance");
  const [density, setDensity] = useState<"comfortable" | "compact">(
    "comfortable",
  );
  const [mode, setMode] = useState(bootstrap.appearanceMode);
  const [isSystemDark, setIsSystemDark] = useState(bootstrap.isSystemDark);
  const [status, setStatus] = useState(
    "Synthetic fixtures only · Ctrl+Shift+R checks shortcut",
  );
  const appearance = effectiveAppearance(mode, isSystemDark);

  /** Display a native action's settled result and expose failures in the reviewable app. */
  async function runAction(action: () => Promise<string>) {
    try {
      setStatus(await action());
    } catch (error) {
      setStatus(String(error));
    }
  }

  useEffect(() => {
    /** Track OS events without changing explicit Light/Dark mode. */
    const onChange = () => setIsSystemDark(MEDIA.matches);
    /** Demonstrate shortcut handling while the native window owns focus. */
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key === ",") {
        event.preventDefault();
        setRoute("settings");
      }
      if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "r") {
        event.preventDefault();
        setStatus("Native shortcut received");
      }
    };
    MEDIA.addEventListener("change", onChange);
    window.addEventListener("keydown", onKey);
    const disposeDrop = Events.On("fixture-files-dropped", (event) => {
      setStatus(`Dropped: ${droppedNames(event.data as string[]).join(", ")}`);
    });
    const disposeAppearance = Events.On("system-appearance", (event) => {
      setIsSystemDark(Boolean(event.data));
    });
    return () => {
      MEDIA.removeEventListener("change", onChange);
      window.removeEventListener("keydown", onKey);
      disposeDrop();
      disposeAppearance();
    };
  }, []);

  useEffect(() => {
    document.documentElement.classList.toggle("dark", appearance === "dark");
    /** Synchronize the actual CSS palette with the opaque native background. */
    const synchronize = () => {
      const [red, green, blue] = canvasBackground();
      void API.SyncAppearance(
        document.documentElement.classList.contains("dark"),
        red,
        green,
        blue,
      ).catch((error) => setStatus(String(error)));
    };
    synchronize();
    const observer = new MutationObserver(synchronize);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "style", "data-theme-id"],
    });
    return () => observer.disconnect();
  }, [appearance]);

  useEffect(() => {
    let isDisposed = false;
    /** Apply committed DOM colors before showing; hidden WebViews may suspend animation frames. */
    async function revealCommittedView() {
      try {
        const [red, green, blue] = canvasBackground();
        await API.SyncAppearance(appearance === "dark", red, green, blue);
      } catch (error) {
        setStatus(`Startup appearance failed: ${String(error)}`);
        setRoute("settings");
        setSettingsSection("diagnostics");
      }
      if (!isDisposed) await API.FrontendReady();
    }
    void revealCommittedView();
    return () => {
      isDisposed = true;
    };
  }, []);

  return (
    <div className="desktop-layout" data-density={density}>
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
              aria-pressed={settingsSection === "appearance"}
              onClick={() => setSettingsSection("appearance")}
            >
              Appearance & display
            </button>
            <button
              aria-pressed={settingsSection === "diagnostics"}
              onClick={() => setSettingsSection("diagnostics")}
            >
              About & diagnostics
            </button>
          </div>
          {settingsSection === "appearance" && (
            <div className="display-controls">
              <label>
                Appearance
                <select
                  value={mode}
                  onChange={(event) => setMode(event.target.value)}
                >
                  <option value="system">System</option>
                  <option value="light">Light</option>
                  <option value="dark">Dark</option>
                </select>
              </label>
              <label>
                Density
                <select
                  value={density}
                  onChange={(event) =>
                    setDensity(event.target.value as "comfortable" | "compact")
                  }
                >
                  <option value="comfortable">Comfortable</option>
                  <option value="compact">Compact</option>
                </select>
              </label>
              <p>
                Density changes spacing. Text size and system scaling stay
                independent.
              </p>
            </div>
          )}
        </section>
      )}
      {route === "settings" && settingsSection === "diagnostics" && (
        <aside className="native-toolbar">
          <strong>Windows native slice</strong>
          <span>
            {bootstrap.providers.length} providers · {bootstrap.roleCount} theme
            roles · Wails {bootstrap.wailsVersion}
          </span>
          <select
            aria-label="Appearance"
            value={mode}
            onChange={(event) => setMode(event.target.value)}
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
          <button
            onClick={() =>
              void runAction(async () => {
                const names = await API.PickFixtureFiles();
                return names?.length
                  ? `Selected: ${names.join(", ")}`
                  : "Picker cancelled";
              })
            }
          >
            Native picker
          </button>
          <button
            onClick={() =>
              void runAction(async () => {
                const name = await API.ExportFixture();
                return name ? `Exported: ${name}` : "Export cancelled";
              })
            }
          >
            Export fixture
          </button>
          <button
            onClick={() =>
              void runAction(async () => {
                await API.OpenDocumentation();
                return "Official documentation opened";
              })
            }
          >
            Wails docs
          </button>
          <output role="status">{status}</output>
          <small>
            PID {bootstrap.pid} · Lifecycle: {bootstrap.lifecycleLog}
          </small>
        </aside>
      )}
      <div
        className="native-drop-surface"
        data-file-drop-target
        hidden={route === "settings" && settingsSection === "diagnostics"}
      >
        <Reference
          appearance={appearance}
          onAppearanceChange={setMode}
          view={route === "usage" ? "usage" : "appearance"}
        />
      </div>
    </div>
  );
}

/** Bootstrap from Go before rendering; the native window remains hidden until React is ready. */
async function start() {
  const bootstrap = await API.GetBootstrap();
  document.documentElement.classList.toggle(
    "dark",
    effectiveAppearance(bootstrap.appearanceMode, bootstrap.isSystemDark) ===
      "dark",
  );
  createRoot(document.getElementById("root")!).render(
    <Desktop bootstrap={bootstrap} />,
  );
}
void start().catch(async (error) => {
  document.getElementById("root")!.textContent = String(error);
  await API.FrontendReady();
});
