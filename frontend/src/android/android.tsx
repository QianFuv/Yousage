/** Android entry: pair with the desktop's relay once, then show its saved and latest snapshots read-only. */
import { createRoot, type Root } from "react-dom/client";
import { getTransport } from "@wailsio/runtime";
import { RefreshCwIcon, Unlink2Icon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import * as mobileApi from "../../bindings/yousage/internal/mobile/service";
import { resolveLanguage, setLanguage, t } from "../i18n";
import { configureTimestampLocale } from "../../vendor/timestampFormat";
import { viewerHistory } from "../usage/dialogHistory";
import {
  androidDiagnosticSink,
  androidDiagnosticsOpener,
  createDiagnosticReporter,
  installDiagnosticListeners,
} from "../diagnostics";
import { PROBLEMS, refreshError } from "../web/load";
import { mountViewer } from "../web/mountViewer";
import type { PrivateSnapshot } from "../web/payloads";
import type { SnapshotCandidate } from "../web/snapshotSession";
import { RelayProblem } from "../web/WebViewer";
import { interpretSnapshot, type DeviceRead } from "./snapshot";
import { PairScreen } from "./PairScreen";
import "../style.css";
import "../web/web.css";
import "./android.css";

/** Fixed render/internal categories for the native diagnostics, which work without Go. */
const reportDiagnostic = createDiagnosticReporter(androidDiagnosticSink());
/** The early listener in `android.html` normally owns these, so this only fills in when it is missing. */
installDiagnosticListeners(window, reportDiagnostic);

/** Whether the last-resort problem screen is up, so a failure while showing it is not handled again. */
let isShowingFatal = false;

const root: Root = createRoot(document.getElementById("root")!, {
  /** Replace a blank page after a failure no boundary caught, without looping on the problem screen. */
  onUncaughtError() {
    reportDiagnostic("render");
    if (isShowingFatal) return;
    isShowingFatal = true;
    setTimeout(() => showProblem("display", true));
  },
});
const locale = navigator.language || "en";
const nativeTransport = getTransport();
/** Undo the mounted viewer's transport and listeners, if one is shown. */
let unmountViewer: (() => void) | null = null;
/** Counts requests to open the snapshot, so an earlier one that finishes late changes nothing. */
let openGeneration = 0;
/** Opens run one after another, so two viewers never swap transports concurrently. */
let openQueue = Promise.resolve();

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
/** Shared with viewer dialogs, so a closing dialog's traversal never leaves the change-link screen. */
const dialogHistory = viewerHistory();

/**
 * Ask for another link while keeping the saved one until a new link connects.
 *
 * Waits for a just-closed dialog's history traversal to land first.
 */
function changeLink() {
  dialogHistory.whenSettled(() => {
    openGeneration++;
    if (!isChangingLink) {
      history.pushState(null, "");
      isChangingLink = true;
    }
    showPairing(true);
  });
}

/** Leave the change-link screen, or open the newly saved link on first pairing. */
function finishPairing() {
  if (isChangingLink) history.back();
  else void openSnapshot();
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

dialogHistory.listen(() => {
  if (!isChangingLink) return;
  isChangingLink = false;
  void openSnapshot();
});

/**
 * Explain why the snapshot cannot be shown, with ways forward.
 *
 * @param problem - Which failure occurred.
 * @param shouldDownload - Trying again downloads the snapshot instead of opening the saved one.
 */
function showProblem(problem: keyof typeof PROBLEMS, shouldDownload = false) {
  leaveViewer();
  applyDeviceLook();
  root.render(
    <RelayProblem
      title={t(PROBLEMS[problem].title)}
      detail={t(PROBLEMS[problem].detail)}
    >
      <div className="android-problem-actions">
        <Button onClick={() => void openSnapshot(shouldDownload)}>
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

/**
 * A snapshot opened on the device, kept by the viewer only once it shows.
 *
 * @param read - The opened snapshot.
 * @returns The candidate; downloads carry the ID that saves them on the device.
 */
function toCandidate(
  read: Extract<DeviceRead, { status: "ok" }>,
): SnapshotCandidate<PrivateSnapshot> {
  return {
    payload: read.payload,
    etag: read.etag,
    identity: read.text,
    commit: read.candidateId
      ? () => mobileApi.CommitSnapshot(read.candidateId)
      : undefined,
  };
}

/**
 * Show the snapshot saved on this device at once, or download one, and mount the viewer.
 *
 * Saved data this version cannot show is downloaded again without deleting it or the link.
 *
 * @param generation - This request's place among requests to open the snapshot.
 * @param shouldDownload - Download the snapshot instead of opening the saved one.
 */
async function showSnapshot(generation: number, shouldDownload: boolean) {
  const isCurrent = () => generation === openGeneration;
  leaveViewer();
  let first: DeviceRead = shouldDownload
    ? { status: "empty" }
    : interpretSnapshot(await mobileApi.CachedSnapshot());
  if (!isCurrent()) return;
  const isSaved = first.status === "ok";
  if (first.status === "empty" || first.status === "problem") {
    showLoading();
    first = interpretSnapshot(await mobileApi.LoadSnapshot(""));
    if (!isCurrent()) return;
  }
  if (first.status === "unpaired") return showPairing();
  if (first.status !== "ok")
    return showProblem(first.status === "problem" ? first.problem : "network");
  const pairing = await mobileApi.GetPairing().catch(() => null);
  if (!isCurrent()) return;
  let unmount: () => void;
  try {
    unmount = await mountViewer({
      root,
      initial: toCandidate(first),
      isSaved,
      locale,
      fallback: nativeTransport,
      async download(etag) {
        const next = interpretSnapshot(await mobileApi.LoadSnapshot(etag));
        if (next.status === "problem") throw refreshError(next.problem);
        return next.status === "ok" ? toCandidate(next) : null;
      },
      recover: () => void openSnapshot(true),
      reportDiagnostic,
      showDiagnostics: androidDiagnosticsOpener() ?? undefined,
      open: (url) => void mobileApi.OpenURL(url).catch(() => {}),
      connection: {
        relay: pairing?.relayOrigin.replace(/^https?:\/\//, "") ?? "",
        onChangeLink: changeLink,
      },
    });
  } catch {
    if (isCurrent()) showProblem("display", true);
    return;
  }
  if (!isCurrent()) return unmount();
  unmountViewer = unmount;
}

/**
 * Open the snapshot after any earlier request finishes; a later request supersedes this one.
 *
 * @param shouldDownload - Download the snapshot instead of opening the saved one.
 * @returns Settles once this request is done; never rejects.
 */
function openSnapshot(shouldDownload = false): Promise<void> {
  const generation = ++openGeneration;
  isShowingFatal = false;
  openQueue = openQueue
    .then(() => {
      if (generation === openGeneration)
        return showSnapshot(generation, shouldDownload);
    })
    .catch(() => {
      if (generation === openGeneration) showProblem("network");
    });
  return openQueue;
}

applyDeviceLook();
configureTimestampLocale(locale);
void openSnapshot();
