/** Immediate display preferences acknowledge durable native state before updating controls. */
import { useState } from "react";
import { useData } from "../usage/controller";
import * as nativeApi from "../../bindings/yousage/app";

/** Keep scale, spacing, timestamps and activity policy independent of theme selection. */
export function DisplayPanel() {
  const data = useData();
  const value = data.display.value;
  const [status, setStatus] = useState("");
  /** Report failed ordinary preference writes while retaining the last acknowledged choice. */
  function change(patch: Partial<typeof value>) {
    void data
      .patchDisplay(patch)
      .then(() => setStatus("Saved."))
      .catch((cause) => setStatus(String(cause)));
  }
  return (
    <section className="settings-panel" aria-label="Display and behavior">
      <h1>Display &amp; behavior</h1>
      <p>Spacing changes density. Scale changes text and controls together.</p>
      <div className="settings-grid">
        <label>
          Information density
          <select
            value={value.density}
            onChange={(event) => change({ density: event.target.value })}
          >
            <option value="comfortable">Comfortable</option>
            <option value="compact">Compact</option>
          </select>
          <small>
            Both modes show all statistics; compact reduces spacing.
          </small>
        </label>
        <label>
          Interface scale
          <select
            value={value.zoom}
            onChange={(event) => change({ zoom: Number(event.target.value) })}
          >
            {[1, 1.1, 1.25, 1.5, 1.75, 2].map((zoom) => (
              <option key={zoom} value={zoom}>
                {Math.round(zoom * 100)}%
              </option>
            ))}
          </select>
          <small>Independent of Windows display scaling.</small>
        </label>
        <label>
          Reset time format
          <select
            value={value.timestampFormat}
            onChange={(event) =>
              change({ timestampFormat: event.target.value })
            }
          >
            <option value="locale">System default</option>
            <option value="12h">12-hour</option>
            <option value="24h">24-hour</option>
          </select>
        </label>
      </div>
      <label className="check-row">
        <input
          type="checkbox"
          checked={value.shouldAutoRefresh}
          onChange={(event) =>
            change({ shouldAutoRefresh: event.target.checked })
          }
        />
        Refresh when Usage is active and visible
      </label>
      <p>
        History refreshes when stale. Limits wait five minutes after completion.
        Refresh always remains available; Settings and minimized windows do not
        start automatic queries.
      </p>
      <label className="check-row">
        <input
          type="checkbox"
          checked={value.shouldRestoreWindow}
          onChange={(event) =>
            change({ shouldRestoreWindow: event.target.checked })
          }
        />
        Restore window size, position and maximized state
      </label>
      <button
        onClick={() =>
          void nativeApi
            .RecenterWindow()
            .then(() => setStatus("Window recentered."))
            .catch((cause) => setStatus(String(cause)))
        }
      >
        Recenter window
      </button>
      <div className="settings-card">
        <h2>Keyboard</h2>
        <p>
          <kbd>Ctrl / ⌘ ,</kbd> opens Settings. <kbd>Ctrl / ⌘ 1</kbd> returns to
          Usage. <kbd>Ctrl / ⌘ R</kbd> refreshes Usage. <kbd>Tab</kbd> moves
          focus; <kbd>Escape</kbd> dismisses a close confirmation.{" "}
          <kbd>Ctrl / ⌘ Alt A</kbd> opens Appearance;{" "}
          <kbd>Ctrl / ⌘ Alt Shift A</kbd> cycles System, Light and Dark.
        </p>
        <p>
          Returning to Usage retains your environments, period, metric and
          reading position. Closing the window exits the app.
        </p>
      </div>
      {status && <output role="status">{status}</output>}
    </section>
  );
}
