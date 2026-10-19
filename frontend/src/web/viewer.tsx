/** Relay viewer entry: open the private snapshot and render the read-only pages. */
import { createRoot, type Root } from "react-dom/client";
import { resolveLanguage, setLanguage, t } from "../i18n";
import { isPrivateSnapshot, type PrivateSnapshot } from "./payloads";
import { parseRelayAddress, type RelayAddress } from "./relay";
import {
  loadRelayPayload,
  PROBLEMS,
  refreshError,
  type LoadResult,
} from "./load";
import { mountViewer } from "./mountViewer";
import type { SnapshotCandidate } from "./snapshotSession";
import { RelayProblem } from "./WebViewer";
import "../style.css";
import "./web.css";

/** Whether the last-resort problem page is up, so a failure while showing it is not handled again. */
let isShowingFatal = false;

const root: Root = createRoot(document.getElementById("root")!, {
  /** Replace a blank page after a failure no boundary caught, without looping on the problem page. */
  onUncaughtError() {
    if (isShowingFatal) return;
    isShowingFatal = true;
    setTimeout(() => showProblem("display"));
  },
});

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

/**
 * Download the snapshot newer than an entity tag as a candidate the viewer keeps once shown.
 *
 * @param address - Object and key from the page URL.
 * @param etag - Entity tag of the snapshot kept so far.
 * @returns The candidate, or the unchanged marker or problem.
 */
async function loadCandidate(
  address: RelayAddress,
  etag: string,
): Promise<
  | { status: "ok"; candidate: SnapshotCandidate<PrivateSnapshot> }
  | Exclude<LoadResult<PrivateSnapshot>, { status: "ok" }>
> {
  const read = await loadRelayPayload(address, isPrivateSnapshot, etag);
  if (read.status !== "ok") return read;
  return {
    status: "ok",
    candidate: {
      payload: read.payload,
      etag: read.etag,
      identity: JSON.stringify(read.payload),
    },
  };
}

/** Load the snapshot, then mount the usage pages over a snapshot-backed transport. */
async function start() {
  const locale = navigator.language || "en";
  setLanguage(resolveLanguage("system", locale));
  const address = parseRelayAddress(location.pathname, location.hash);
  if (!address || address.kind !== "space") return showProblem("link");
  const first = await loadCandidate(address, "");
  if (first.status !== "ok")
    return showProblem(first.status === "problem" ? first.problem : "network");
  try {
    await mountViewer({
      root,
      initial: first.candidate,
      locale,
      async download(etag) {
        const next = await loadCandidate(address, etag);
        if (next.status === "problem") throw refreshError(next.problem);
        return next.status === "ok" ? next.candidate : null;
      },
      recover: () => location.reload(),
      open: (url) => {
        if (/^https:\/\//.test(url))
          window.open(url, "_blank", "noopener,noreferrer");
      },
      connection: { relay: location.host },
    });
  } catch {
    showProblem("display");
  }
}

void start().catch(() => showProblem("network"));
