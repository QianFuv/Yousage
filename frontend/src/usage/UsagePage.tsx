/** Usage navigation presents observations; pricing, source and display configuration live in Settings. */
import { useEffect, useRef, useState } from "react";
import {
  enumerateDays,
  enumerateHourStarts,
  formatUsageContractMismatch,
} from "@t3tools/shared/usageFormat";
import {
  CHATGPT_USAGE_URL,
  collectLimitAccounts,
  usesChatGptSharing,
  type LimitPresentations,
} from "@t3tools/shared/usageLimits";
import { UsageLimitsPooled } from "../../vendor/UsageLimitsPooled";
import { UsageStatsPanel } from "./UsageStatsPanel";
import { mergeAnsweredUsage } from "./data";
import { useData } from "./controller";
import { Request } from "../../bindings/yousage/internal/refresh/models";
import * as nativeApi from "../../bindings/yousage/app";

/** Preserve null All and explicit empty selection while exposing coverage inside the environment filter. */
export function UsagePage({
  isActive,
  onSettings,
}: {
  isActive: boolean;
  onSettings: (section: "pricing" | "sources", model?: string) => void;
}) {
  const data = useData();
  useEffect(
    () => data.setUsageActive(isActive),
    [data.setUsageActive, isActive],
  );
  const refreshRef = useRef(data.refresh);
  refreshRef.current = data.refresh;
  const [limitsNow, setLimitsNow] = useState(Date.now);
  const [cursorError, setCursorError] = useState("");
  const [copyStatus, setCopyStatus] = useState("");
  const environmentKey =
    data.snapshot?.environments
      .filter(
        (value) =>
          data.selected === null || data.selected.has(value.environmentId),
      )
      .map((value) => `${value.environmentId}:${value.connected}`)
      .join(";") ?? "";
  const selectionKey =
    data.selected === null ? "all" : [...data.selected].sort().join(";");
  const metric = data.display.value.metric;
  useEffect(() => {
    if (!isActive || !environmentKey) return;
    setLimitsNow(Date.now());
    void refreshRef.current(false, true);
    /** Refresh on actual foreground activity; hidden/minimized settings never start automatic scans. */
    const activity = () => {
      if (document.visibilityState === "visible")
        void refreshRef.current(false, true);
    };
    window.addEventListener("focus", activity);
    document.addEventListener("visibilitychange", activity);
    return () => {
      window.removeEventListener("focus", activity);
      document.removeEventListener("visibilitychange", activity);
    };
  }, [
    isActive,
    metric,
    data.display.value.windowDays,
    selectionKey,
    environmentKey,
    data.display.value.shouldAutoRefresh,
  ]);
  const selected =
    data.snapshot?.environments.filter(
      (value) =>
        data.selected === null || data.selected.has(value.environmentId),
    ) ?? [];
  const merged = mergeAnsweredUsage(selected);
  const usageWindow = data.snapshot?.window;
  const answered = selected.filter((value) => value.summary !== null).length;
  const stillReporting = selected.filter(
    (value) => value.summary === null && !value.error,
  ).length;
  const presentations: LimitPresentations = new Map(
    selected.map((value) => [
      value.environmentId,
      {
        entry: { target: { label: value.label } },
        serverConfig: value.serverConfig,
      },
    ]),
  );
  const cursorTargets = selected.filter(
    (environment) =>
      environment.canOperate &&
      environment.summary?.sources.some(
        (source) => source.action === "enableCursorKeychain",
      ),
  );
  const hasCursorAccount = selected.some((environment) =>
    environment.summary?.sources.some(
      (source) =>
        source.fingerprint.provider === "cursor" &&
        source.fingerprint.hostId === "cursor.com",
    ),
  );
  const cursorPrompt =
    !hasCursorAccount && cursorTargets.length > 0 ? (
      <p className="usage-notice">
        Cursor usage requires explicit macOS Keychain access.{" "}
        <button
          disabled={data.isBusy}
          onClick={async () => {
            try {
              const outcomes = await nativeApi.EnableCursorKeychain(
                cursorTargets.map((environment) => environment.environmentId),
              );
              const failure = outcomes.find((outcome) => !outcome.saved);
              if (failure) throw new Error(failure.error);
              const identities = cursorTargets.map(
                (environment) => environment.environmentId,
              );
              const request = Request.createFrom({
                IsManual: true,
                IsVisible: document.visibilityState === "visible",
                IsUsagePage: isActive,
                IsAutoEnabled: false,
                AfterPending: true,
              });
              await Promise.all([
                nativeApi.RefreshUsage(
                  data.display.value.windowDays,
                  Intl.DateTimeFormat().resolvedOptions().timeZone ?? "UTC",
                  identities,
                  request,
                ),
                nativeApi.RefreshLimits(identities, request),
              ]);
              await data.reload();
            } catch (cause) {
              setCursorError(String(cause));
            }
          }}
        >
          Enable Cursor Keychain access
        </button>
        {cursorError && <span role="status">{cursorError}</span>}
      </p>
    ) : null;
  const sourceMessages = [
    ...new Set(
      selected.flatMap(
        (value) =>
          value.summary?.sources.flatMap((source) =>
            source.message &&
            !source.action &&
            (source.status === "partial" ||
              source.status === "failed" ||
              source.fingerprint.provider === "cursor")
              ? [source.message]
              : [],
          ) ?? [],
      ),
    ),
  ];
  return (
    <section className="usage-page">
      <header className="usage-page-header">
        <div>
          <h1>Usage</h1>
          <p className="text-xs text-muted-foreground">
            {data.isBusy
              ? answered === 0
                ? "Scanning…"
                : "Refreshing…"
              : data.snapshot
                ? selected.length === 0
                  ? "No environments selected"
                  : answered === 0 && metric !== "limits"
                    ? "Unavailable"
                    : "Ready"
                : "Loading…"}
          </p>
        </div>
        <div className="usage-controls">
          <div className="view-selector" role="group" aria-label="Usage metric">
            {(["cost", "tokens", "limits"] as const).map((value) => (
              <button
                key={value}
                aria-pressed={metric === value}
                onClick={() =>
                  void data.patchDisplay({ metric: value }).catch(() => {})
                }
              >
                {value[0].toUpperCase() + value.slice(1)}
              </button>
            ))}
          </div>
          <select
            aria-label="Usage period"
            value={data.display.value.windowDays}
            disabled={metric === "limits"}
            onChange={(event) =>
              void data
                .patchDisplay({ windowDays: Number(event.target.value) })
                .catch(() => {})
            }
          >
            {[1, 7, 30, 90].map((days) => (
              <option key={days} value={days}>
                {days === 1 ? "Past 24 hours" : `${days} days`}
              </option>
            ))}
          </select>
          <details className="environment-filter">
            <summary>
              {data.selected === null
                ? "All environments"
                : `${data.selected.size} environments`}
            </summary>
            <div className="environment-popup">
              <button onClick={() => data.setSelected(null)}>
                All environments
              </button>
              <button onClick={() => data.setSelected(new Set())}>None</button>
              {data.snapshot?.environments.map((value) => (
                <label key={value.environmentId}>
                  <input
                    type="checkbox"
                    checked={
                      data.selected === null ||
                      data.selected.has(value.environmentId)
                    }
                    onChange={(event) => {
                      const identities =
                        data.selected === null
                          ? new Set(
                              data.snapshot!.environments.map(
                                (item) => item.environmentId,
                              ),
                            )
                          : new Set(data.selected);
                      if (event.target.checked)
                        identities.add(value.environmentId);
                      else identities.delete(value.environmentId);
                      data.setSelected(identities);
                    }}
                  />
                  {value.label}
                  <small>
                    {value.connected
                      ? value.canOperate
                        ? "Connected"
                        : "Read-only"
                      : "Offline"}
                  </small>
                </label>
              ))}
              <div className="coverage-notices">
                {selected
                  .filter((value) => value.error)
                  .map((value) => (
                    <p key={value.environmentId}>
                      {value.label}: {value.error}
                    </p>
                  ))}
                {merged.contractMismatches.map((value) => (
                  <p key={value.environmentId}>
                    {formatUsageContractMismatch(
                      selected.find(
                        (item) => item.environmentId === value.environmentId,
                      )?.label ?? value.environmentId,
                      value,
                    )}
                  </p>
                ))}
                {merged.duplicateSources.length > 0 && (
                  <p>
                    Counted once across environments sharing a transcript
                    directory: {merged.duplicateSources.join(", ")}
                  </p>
                )}
              </div>
            </div>
          </details>
          <button
            disabled={data.isBusy}
            onClick={() => {
              setLimitsNow(Date.now());
              void data.refresh(true, true);
            }}
          >
            {data.isBusy ? "Refreshing…" : "Refresh"}
          </button>
          <button onClick={() => onSettings("pricing")}>
            Edit prices & mappings
          </button>
        </div>
      </header>
      {data.error && (
        <p role="status" className="usage-notice">
          {data.error}
        </p>
      )}
      {selected
        .filter((environment) => environment.error)
        .map((environment) => (
          <p
            className="usage-notice"
            role="status"
            key={environment.environmentId}
          >
            {environment.label}: {environment.error}
          </p>
        ))}
      {metric === "limits" ? (
        <>
          <div className="usage-controls">
            <button
              onClick={async () => {
                try {
                  const observations = collectLimitAccounts(presentations).map(
                    (account, index) => ({
                      account: index + 1,
                      driver: account.driver,
                      checkedAt: account.limits.checkedAt,
                      windows: account.limits.windows,
                      availableResetCredits:
                        account.limits.resetCredits?.availableCount,
                    }),
                  );
                  await nativeApi.CopyText(
                    JSON.stringify(observations, null, 2),
                  );
                  setCopyStatus("Exact quota observations copied.");
                } catch (cause) {
                  setCopyStatus(String(cause));
                }
              }}
            >
              Copy exact quota observations
            </button>
            {copyStatus && <output role="status">{copyStatus}</output>}
          </div>
          <UsageLimitsPooled
            presentations={presentations}
            now={limitsNow}
            cursorPrompt={cursorPrompt}
          />
        </>
      ) : (
        usageWindow && (
          <>
            {cursorPrompt}
            {answered > 0 && stillReporting > 0 && (
              <p className="usage-notice">
                Some environments are still reporting. Totals include {answered}{" "}
                available environments.
              </p>
            )}
            {sourceMessages.map((message) => (
              <p className="usage-notice" key={message}>
                {message}
              </p>
            ))}
            {selected.some((value) =>
              value.serverConfig?.providers.some(usesChatGptSharing),
            ) && (
              <p className="text-xs text-muted-foreground">
                ChatGPT shared usage{" "}
                <button
                  onClick={() => void nativeApi.OpenExternal(CHATGPT_USAGE_URL)}
                >
                  Manage usage
                </button>
              </p>
            )}
            {selected.length > 0 &&
              selected.every(
                (value) => !value.summary || value.summary.sources.length === 0,
              ) && (
                <p className="usage-notice">
                  No saved history has been scanned yet.{" "}
                  <button onClick={() => onSettings("sources")}>
                    Review data sources
                  </button>
                </p>
              )}
            {answered === 0 ? (
              <p className="usage-notice" role="status">
                {selected.length === 0
                  ? "Select an environment to view usage."
                  : stillReporting > 0
                    ? "Waiting for usage reports…"
                    : "No usage report is available. Refresh or review the selected environments."}
              </p>
            ) : (
              <UsageStatsPanel
                merged={merged}
                environments={selected}
                metric={metric === "tokens" ? "tokens" : "cost"}
                chartWindow={{
                  days: enumerateDays(
                    usageWindow.sinceDay,
                    usageWindow.untilDay,
                  ),
                  hours:
                    usageWindow.sinceTimeMs !== undefined &&
                    usageWindow.untilTimeMs !== undefined
                      ? enumerateHourStarts(
                          new Date(usageWindow.sinceTimeMs).toISOString(),
                          new Date(usageWindow.untilTimeMs).toISOString(),
                        )
                      : [],
                  resolution: usageWindow.resolution,
                  timeZone: usageWindow.timeZone,
                  referenceTime:
                    usageWindow.untilTimeMs !== undefined
                      ? new Date(usageWindow.untilTimeMs).toISOString()
                      : undefined,
                }}
                onSetPrice={(model) => onSettings("pricing", model)}
              />
            )}
          </>
        )
      )}
    </section>
  );
}
