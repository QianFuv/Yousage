/** Relay viewer entry: open the private snapshot and render the read-only pages. */
import { createRoot, type Root } from "react-dom/client";
import { resolveLanguage, setLanguage, t } from "../i18n";
import { isPrivateSnapshot } from "./payloads";
import { parseRelayAddress } from "./relay";
import { loadRelayPayload, PROBLEMS } from "./load";
import { mountViewer } from "./mountViewer";
import { RelayProblem } from "./WebViewer";
import "../style.css";
import "./web.css";

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
  let etag = first.etag;
  await mountViewer({
    root,
    initial: first.payload,
    locale,
    async fetchNewer() {
      const next = await loadRelayPayload(address, isPrivateSnapshot, etag);
      if (next.status === "problem")
        throw new Error(t(PROBLEMS[next.problem].title));
      if (next.status !== "ok") return null;
      etag = next.etag;
      return next.payload;
    },
    open: (url) => {
      if (/^https:\/\//.test(url))
        window.open(url, "_blank", "noopener,noreferrer");
    },
  });
}

void start().catch(() => showProblem("network"));
