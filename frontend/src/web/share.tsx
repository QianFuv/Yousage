/** Relay share entry: open a public receipt share and render it in the sharer's theme. */
import { createRoot } from "react-dom/client";
import { resolveLanguage, setLanguage, t } from "../i18n";
import { configureTimestampLocale } from "../../vendor/timestampFormat";
import { isSharePayload } from "./payloads";
import { parseRelayAddress } from "./relay";
import { loadRelayPayload, PROBLEMS } from "./load";
import { applySharePalette } from "./palette";
import { SharePage } from "./SharePage";
import { RelayProblem } from "./WebViewer";
import "../style.css";
import "./web.css";

/** Load the share named by the URL and render it, or explain why it cannot be shown. */
async function start() {
  const locale = navigator.language || "en";
  setLanguage(resolveLanguage("system", locale));
  configureTimestampLocale(locale);
  const root = createRoot(document.getElementById("root")!);
  const address = parseRelayAddress(location.pathname, location.hash);
  const result =
    address && address.kind === "share"
      ? await loadRelayPayload(address, isSharePayload)
      : ({ status: "problem", problem: "link" } as const);
  if (result.status !== "ok") {
    const problem =
      PROBLEMS[result.status === "problem" ? result.problem : "network"];
    root.render(
      <RelayProblem title={t(problem.title)} detail={t(problem.detail)} />,
    );
    return;
  }
  applySharePalette(result.payload.palette);
  document.title = `Yousage · ${result.payload.receipt.number}`;
  root.render(
    <main className="share-shell">
      <SharePage payload={result.payload} />
    </main>,
  );
}

void start();
