/** Relay viewer entry: open the private snapshot and render the read-only pages. */
import { createRoot, type Root } from "react-dom/client";
import { setTransport } from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { DataProvider } from "../usage/controller";
import { AppearanceProvider, applySavedAppearance } from "../theme/appearance";
import { resolveLanguage, setLanguage, t } from "../i18n";
import { configureTimestampLocale } from "../../vendor/timestampFormat";
import { isPrivateSnapshot, type PrivateSnapshot } from "./payloads";
import { parseRelayAddress } from "./relay";
import { loadRelayPayload, PROBLEMS } from "./load";
import { captureMethodIds, createSnapshotTransport } from "./snapshotTransport";
import { ReadOnlyContext } from "./readOnly";
import { RelayProblem, WebViewer } from "./WebViewer";
import "../style.css";
import "./web.css";

const DARK_QUERY = "(prefers-color-scheme: dark)";

const root: Root = createRoot(document.getElementById("root")!);

/**
 * Replace the page with an explanation of why it cannot be shown.
 *
 * @param problem - Which failure occurred.
 */
function showProblem(problem: keyof typeof PROBLEMS) {
  root.render(
    <RelayProblem
      title={t(PROBLEMS[problem].title)}
      detail={t(PROBLEMS[problem].detail)}
    />,
  );
}

/** Load the snapshot, then mount the usage pages over a snapshot-backed transport. */
async function start() {
  const locale = navigator.language || "en";
  setLanguage(resolveLanguage("system", locale));
  const address = parseRelayAddress(location.pathname, location.hash);
  if (!address || address.kind !== "space") return showProblem("link");
  const first = await loadRelayPayload(address, isPrivateSnapshot);
  if (first.status !== "ok")
    return showProblem(first.status === "problem" ? first.problem : "network");
  let snapshot: PrivateSnapshot = first.payload;
  let etag = first.etag;
  const names = await captureMethodIds(setTransport);
  const darkQuery = matchMedia(DARK_QUERY);
  /** Render the shell again so the banner shows the latest snapshot time. */
  let renderTime: (generatedAt: string) => void = () => {};
  setTransport(
    createSnapshotTransport(names, {
      current: () => snapshot,
      async refetch() {
        const next = await loadRelayPayload(address, isPrivateSnapshot, etag);
        if (next.status === "ok") {
          snapshot = next.payload;
          etag = next.etag;
          renderTime(snapshot.generatedAt);
        } else if (next.status === "problem") {
          throw new Error(t(PROBLEMS[next.problem].title));
        }
      },
      device: { isDark: () => darkQuery.matches, locale },
      open: (url) => {
        if (/^https:\/\//.test(url))
          window.open(url, "_blank", "noopener,noreferrer");
      },
    }),
  );
  darkQuery.addEventListener("change", (event) =>
    (
      window as unknown as {
        _wails?: {
          dispatchWailsEvent: (event: { name: string; data: boolean }) => void;
        };
      }
    )._wails?.dispatchWailsEvent({
      name: "system-appearance",
      data: event.matches,
    }),
  );
  const [bootstrap, appearance, display] = await Promise.all([
    nativeApi.GetBootstrap(),
    nativeApi.GetAppearance(),
    nativeApi.GetDisplay(),
  ]);
  const language = resolveLanguage(display.value.language, locale);
  setLanguage(language);
  configureTimestampLocale(locale);
  applySavedAppearance(appearance, bootstrap.isSystemDark);
  /** Mount or update the viewer for a snapshot time. */
  renderTime = (generatedAt) =>
    root.render(
      <ReadOnlyContext.Provider value>
        <AppearanceProvider
          initial={appearance}
          isSystemDark={bootstrap.isSystemDark}
        >
          <DataProvider initial={display}>
            <WebViewer generatedAt={generatedAt} locale={locale} />
          </DataProvider>
        </AppearanceProvider>
      </ReadOnlyContext.Provider>,
    );
  renderTime(snapshot.generatedAt);
}

void start().catch(() => showProblem("network"));
