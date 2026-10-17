/** Android entry: pair with the desktop's relay once, then show its saved and latest snapshots read-only. */
import { createRoot, type Root } from "react-dom/client";
import { getTransport } from "@wailsio/runtime";
import { RefreshCwIcon, Unlink2Icon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import * as mobileApi from "../../bindings/yousage/internal/mobile/service";
import { resolveLanguage, setLanguage, t } from "../i18n";
import { configureTimestampLocale } from "../../vendor/timestampFormat";
import { PROBLEMS } from "../web/load";
import { mountViewer } from "../web/mountViewer";
import { RelayProblem } from "../web/WebViewer";
import { interpretSnapshot } from "./snapshot";
import { PairScreen } from "./PairScreen";
import "../style.css";
import "../web/web.css";
import "./android.css";

const root: Root = createRoot(document.getElementById("root")!);
const locale = navigator.language || "en";
const nativeTransport = getTransport();
/** Undo the mounted viewer's transport and listeners, if one is shown. */
let unmountViewer: (() => void) | null = null;

/** Leave the viewer, if one is mounted, so native calls reach Go directly again. */
function leaveViewer() {
  unmountViewer?.();
  unmountViewer = null;
}

/** Show the standalone screens in the device's language and color scheme. */
function applyDeviceLook() {
  setLanguage(resolveLanguage("system", locale));
  document.documentElement.classList.toggle(
    "dark",
    matchMedia("(prefers-color-scheme: dark)").matches,
  );
}

/** Whether the change-link screen added a history entry that the back button closes. */
let isChangingLink = false;

/** Ask for another link while keeping the saved one until a new link connects. */
function changeLink() {
  if (!isChangingLink) {
    history.pushState(null, "");
    isChangingLink = true;
  }
  showPairing(true);
}

/** Leave the change-link screen, or open the newly saved link on first pairing. */
function finishPairing() {
  if (isChangingLink) history.back();
  else void openSnapshot().catch(() => showProblem("network"));
}

/**
 * Ask for the desktop's phone link.
 *
 * @param canKeepLink - A link is saved already, so the screen offers a way back to it.
 */
function showPairing(canKeepLink = false) {
  leaveViewer();
  applyDeviceLook();
  root.render(
    <PairScreen
      onPaired={finishPairing}
      onCancel={canKeepLink ? () => history.back() : undefined}
    />,
  );
}

addEventListener("popstate", () => {
  if (!isChangingLink) return;
  isChangingLink = false;
  void openSnapshot().catch(() => showProblem("network"));
});

/**
 * Explain why the snapshot cannot be shown, with ways forward.
 *
 * @param problem - Which failure occurred.
 */
function showProblem(problem: keyof typeof PROBLEMS) {
  leaveViewer();
  applyDeviceLook();
  root.render(
    <RelayProblem
      title={t(PROBLEMS[problem].title)}
      detail={t(PROBLEMS[problem].detail)}
    >
      <div className="android-problem-actions">
        <Button onClick={() => void openSnapshot()}>
          <RefreshCwIcon aria-hidden />
          {t("Try again")}
        </Button>
        <Button variant="outline" onClick={changeLink}>
          <Unlink2Icon aria-hidden />
          {t("Use another link")}
        </Button>
      </div>
    </RelayProblem>,
  );
}

/** Keep the startup screen from `android.html` while the first snapshot downloads. */
function showLoading() {
  leaveViewer();
  applyDeviceLook();
  root.render(
    <div className="android-loading" role="status" aria-label={t("Loading…")}>
      <strong className="sidebar-wordmark android-pair-wordmark" aria-hidden>
        <span>Y</span>
        <span className="sidebar-wordmark-tail">ou</span>
        <span className="sidebar-wordmark-tail">sage</span>
      </strong>
      <RefreshCwIcon aria-hidden className="animate-spin" />
    </div>,
  );
}

/** Show the snapshot saved on this device at once, or download the first one, and mount the viewer. */
async function openSnapshot() {
  leaveViewer();
  let first = interpretSnapshot(await mobileApi.CachedSnapshot());
  const isSaved = first.status === "ok";
  if (first.status === "empty") {
    showLoading();
    first = interpretSnapshot(await mobileApi.LoadSnapshot(""));
  }
  if (first.status === "unpaired") return showPairing();
  if (first.status !== "ok")
    return showProblem(first.status === "problem" ? first.problem : "network");
  let etag = first.etag;
  const pairing = await mobileApi.GetPairing().catch(() => null);
  unmountViewer = await mountViewer({
    root,
    initial: first.payload,
    isSaved,
    locale,
    fallback: nativeTransport,
    async fetchNewer() {
      const next = interpretSnapshot(await mobileApi.LoadSnapshot(etag));
      if (next.status === "problem")
        throw new Error(t(PROBLEMS[next.problem].title));
      if (next.status !== "ok") return null;
      etag = next.etag;
      return next.payload;
    },
    open: (url) => void mobileApi.OpenURL(url).catch(() => {}),
    connection: {
      relay: pairing?.relayOrigin.replace(/^https?:\/\//, "") ?? "",
      onChangeLink: changeLink,
    },
  });
}

applyDeviceLook();
configureTimestampLocale(locale);
void openSnapshot().catch(() => showProblem("network"));
