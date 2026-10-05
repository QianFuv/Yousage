/** Diagnostics export is an explicit native action over a backend-owned whitelist. */
import { useEffect, useState } from "react";
import * as nativeApi from "../../bindings/yousage/app";

/** Preview precisely what is copied or exported; no account labels, URLs, paths or credentials. */
export function DiagnosticsPanel({ nativeStatus }: { nativeStatus: string }) {
  const [report, setReport] = useState("");
  const [status, setStatus] = useState("");
  useEffect(() => {
    void nativeApi
      .GetDiagnostics()
      .then(setReport)
      .catch((cause) => setStatus(String(cause)));
  }, []);
  /** Show only the actual outcome of the explicit native operation. */
  async function act(operation: () => Promise<unknown>, message: string) {
    try {
      const result = await operation();
      setStatus(result === false ? "Canceled." : message);
    } catch (cause) {
      setStatus(String(cause));
    }
  }
  return (
    <section className="settings-panel" aria-label="About and diagnostics">
      <h1>About &amp; diagnostics</h1>
      <p>Yousage · standalone Wails v3 application</p>
      <p>
        This report includes versions and configured source counts. It excludes
        credentials, account identities, server URLs, private paths and raw
        error logs. Nothing is uploaded.
      </p>
      <pre className="diagnostic-preview">{report || "Loading report…"}</pre>
      <div className="settings-actions">
        <button
          onClick={() =>
            void nativeApi
              .GetDiagnostics()
              .then(setReport)
              .catch((cause) => setStatus(String(cause)))
          }
        >
          Refresh report
        </button>
        <button
          disabled={!report}
          onClick={() =>
            void act(
              () => nativeApi.CopyText(report),
              "Redacted report copied.",
            )
          }
        >
          Copy redacted report
        </button>
        <button
          disabled={!report}
          onClick={() =>
            void act(
              () => nativeApi.ExportDiagnostics(),
              "Redacted report exported.",
            )
          }
        >
          Export redacted report
        </button>
        <button
          onClick={() =>
            void act(
              () => nativeApi.OpenDocumentation(),
              "Documentation opened.",
            )
          }
        >
          Wails documentation
        </button>
      </div>
      {nativeStatus && <p role="status">{nativeStatus}</p>}
      <output role="status">{status}</output>
    </section>
  );
}
