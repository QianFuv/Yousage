/** Android entry: pair with the desktop's relay once, then show its latest snapshot read-only. */
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

/** Forget the saved link and ask for a new one. */
async function changeLink() {
  leaveViewer();
  await mobileApi.Unpair().catch(() => {});
  showPairing();
}

/** Ask for the desktop's phone link. */
function showPairing() {
  leaveViewer();
  applyDeviceLook();
  root.render(<PairScreen onPaired={() => void openSnapshot()} />);
}

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
        <Button variant="outline" onClick={() => void changeLink()}>
          <Unlink2Icon aria-hidden />
          {t("Use another link")}
        </Button>
      </div>
    </RelayProblem>,
  );
}

/** Read the saved relay's snapshot and mount the viewer over it. */
async function openSnapshot() {
  leaveViewer();
  const first = interpretSnapshot(await mobileApi.LoadSnapshot(""));
  if (first.status === "unpaired") return showPairing();
  if (first.status !== "ok")
    return showProblem(first.status === "problem" ? first.problem : "network");
  let etag = first.etag;
  const pairing = await mobileApi.GetPairing().catch(() => null);
  unmountViewer = await mountViewer({
    root,
    initial: first.payload,
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
      onChangeLink: () => void changeLink(),
    },
  });
}

applyDeviceLook();
configureTimestampLocale(locale);
void openSnapshot().catch(() => showProblem("network"));
