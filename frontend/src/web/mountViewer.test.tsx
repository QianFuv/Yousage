/** The mounted viewer keeps a downloaded snapshot only after it shows, and a failed mount leaves nothing running. */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactElement } from "react";
import type { Root } from "react-dom/client";
import {
  getTransport,
  setTransport,
  type RuntimeTransport,
} from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { Request } from "../../bindings/yousage/internal/refresh/models";
import { FOLLOW_INTERVAL_MS } from "./follow";
import { mountViewer, type ViewerHost } from "./mountViewer";
import { ACKNOWLEDGE_TIMEOUT_MS } from "./renderAcknowledgment";
import type { PrivateSnapshot } from "./payloads";
import type { SnapshotCandidate } from "./snapshotSession";

vi.mock("../theme/appearance", () => ({
  AppearanceProvider: () => null,
  applySavedAppearance: () => "dark",
}));

/** A stand-in page whose visibility listeners the test can count. */
class FakeDocument extends EventTarget {
  visibilityState = "visible";
  documentElement = { lang: "", classList: { toggle() {} } };
}

/** Listeners registered on the device color scheme query. */
let schemeListeners: Set<unknown>;

beforeEach(() => {
  vi.useFakeTimers();
  schemeListeners = new Set();
  vi.stubGlobal("document", new FakeDocument());
  vi.stubGlobal("window", {});
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: (_: string, listener: unknown) =>
      schemeListeners.add(listener),
    removeEventListener: (_: string, listener: unknown) =>
      schemeListeners.delete(listener),
  }));
});

afterEach(() => {
  setTransport(null);
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

/**
 * Build a snapshot with empty reports.
 *
 * @param generatedAt - Snapshot time, which also tells snapshots apart.
 * @returns The snapshot.
 */
function snapshot(generatedAt: string): PrivateSnapshot {
  const period = { usage: {}, loss: {}, speed: {}, receipt: {} };
  return {
    formatVersion: 1,
    generatedAt,
    appVersion: "test",
    timeZone: "UTC",
    periods: { "1": period, "7": period, "30": period, "90": period },
    display: { revision: 1, value: { language: "en" } },
    appearance: { revision: 1 },
    bootstrap: { isSystemDark: false, systemLocale: "en" },
  };
}

/**
 * Wrap a snapshot as a candidate.
 *
 * @param payload - The snapshot.
 * @param commit - Saving it on the device.
 * @returns The candidate identified by its time.
 */
function candidate(
  payload: PrivateSnapshot,
  commit?: () => Promise<void>,
): SnapshotCandidate<PrivateSnapshot> {
  return {
    payload,
    etag: `e-${payload.generatedAt}`,
    identity: payload.generatedAt,
    commit,
  };
}

/**
 * Mount over a fake root that records what it would render.
 *
 * @param host - Host fields to override.
 * @returns The rendered elements, the host, and the unmount function.
 */
async function mount(host: Partial<ViewerHost>) {
  const rendered: ReactElement<{
    onError: (error: unknown) => void;
    children: ReactElement;
  }>[] = [];
  const root = {
    render: (element: (typeof rendered)[number]) => rendered.push(element),
  } as unknown as Root;
  const full: ViewerHost = {
    root,
    initial: candidate(snapshot("2026-10-20T00:00:00Z")),
    download: vi.fn(async () => null),
    recover: vi.fn(),
    open: vi.fn(),
    locale: "en",
    ...host,
  };
  const unmount = await mountViewer(full);
  return { rendered, host: full, unmount };
}

/**
 * Find the props of the viewer pages within a rendered element tree.
 *
 * @param element - A rendered element tree.
 * @returns The props of the element that receives the snapshot time.
 */
function viewerProps(element: unknown): Record<string, unknown> | undefined {
  const props = (element as ReactElement<Record<string, unknown>> | null)
    ?.props;
  if (!props) return undefined;
  if (typeof props.generatedAt === "string") return props;
  for (const child of [props.children].flat()) {
    const found = viewerProps(child);
    if (found) return found;
  }
  return undefined;
}

/**
 * Find the snapshot time the rendered viewer shows.
 *
 * @param element - A rendered element tree.
 * @returns The `generatedAt` prop of the viewer within it.
 */
function shownTime(element: unknown): string | undefined {
  return viewerProps(element)?.generatedAt as string | undefined;
}

/**
 * Ask the viewer to refresh the way the Usage page's refresh button does.
 *
 * @returns The refresh call.
 */
function pressRefresh() {
  return nativeApi.RefreshUsage(1, "UTC", [], Request.createFrom({}));
}

/**
 * Report a commit of the active page's content, as its commit marker does.
 *
 * @param rendered - Elements the viewer rendered.
 * @param index - Which render committed; the latest by default.
 */
function commitPage(rendered: ReactElement[], index = -1) {
  const find = (element: unknown): unknown => {
    const props = (element as ReactElement<Record<string, unknown>> | null)
      ?.props;
    if (!props) return undefined;
    const value = props.value as { onCommit?: unknown } | undefined;
    if (typeof value?.onCommit === "function") return value;
    for (const child of [props.children].flat()) {
      const found = find(child);
      if (found) return found;
    }
    return undefined;
  };
  const commit = find(rendered.at(index)) as
    | { token: number; onCommit: (token: number) => void }
    | undefined;
  commit?.onCommit(commit.token);
}

describe("mountViewer", () => {
  it("does not report a cached commit whose render fails before acknowledgement settles", async () => {
    const reportViewerShown = vi.fn();
    const { rendered, unmount } = await mount({
      isSaved: true,
      reportViewerShown,
    });
    await vi.advanceTimersByTimeAsync(0);
    commitPage(rendered);
    rendered.at(-1)!.props.onError(new Error("commit effect failed"));
    await vi.advanceTimersByTimeAsync(0);
    expect(reportViewerShown).not.toHaveBeenCalledWith(true);
    expect(reportViewerShown).toHaveBeenLastCalledWith(false);
    unmount();
  });

  it("reports a cached offline viewer only after its fresh page acknowledgement", async () => {
    const reportViewerShown = vi.fn();
    const { rendered, unmount } = await mount({
      isSaved: true,
      download: vi.fn(async () => {
        throw new Error("offline");
      }),
      reportViewerShown,
    });
    expect(reportViewerShown).not.toHaveBeenCalledWith(true);
    await vi.advanceTimersByTimeAsync(1_500);
    commitPage(rendered, 0);
    expect(reportViewerShown).not.toHaveBeenCalledWith(true);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(reportViewerShown).toHaveBeenLastCalledWith(true);
    unmount();
    expect(reportViewerShown).toHaveBeenLastCalledWith(false);
  });

  it("never reports cached data as shown merely because its acknowledgement timed out", async () => {
    const reportViewerShown = vi.fn();
    const { rendered, unmount } = await mount({
      isSaved: true,
      reportViewerShown,
    });
    await vi.advanceTimersByTimeAsync(ACKNOWLEDGE_TIMEOUT_MS);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(reportViewerShown).not.toHaveBeenCalledWith(true);
    unmount();
  });

  it("waits for saving after render acknowledgement before reporting a download shown", async () => {
    let finishSave = () => {};
    const commit = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finishSave = resolve;
        }),
    );
    const reportViewerShown = vi.fn();
    const { rendered, unmount } = await mount({
      initial: candidate(snapshot("2026-10-20T00:00:00Z"), commit),
      reportViewerShown,
    });
    await vi.advanceTimersByTimeAsync(0);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).toHaveBeenCalledOnce();
    expect(reportViewerShown).not.toHaveBeenCalledWith(true);
    finishSave();
    await vi.advanceTimersByTimeAsync(0);
    expect(reportViewerShown).toHaveBeenLastCalledWith(true);
    unmount();
  });

  it.each(["unmount", "render failure"])(
    "ignores save completion after %s",
    async (ending) => {
      let finishSave = () => {};
      const reportViewerShown = vi.fn();
      const { rendered, unmount } = await mount({
        initial: candidate(
          snapshot("2026-10-20T00:00:00Z"),
          () =>
            new Promise<void>((resolve) => {
              finishSave = resolve;
            }),
        ),
        reportViewerShown,
      });
      await vi.advanceTimersByTimeAsync(0);
      commitPage(rendered);
      await vi.advanceTimersByTimeAsync(0);
      if (ending === "unmount") unmount();
      else rendered.at(-1)!.props.onError(new Error("late failure"));
      finishSave();
      await vi.advanceTimersByTimeAsync(0);
      expect(reportViewerShown).not.toHaveBeenCalledWith(true);
      expect(reportViewerShown).toHaveBeenLastCalledWith(false);
      unmount();
    },
  );

  it.each(["render", "save"])(
    "requires a fresh rollback acknowledgement after a candidate's %s failure",
    async (failure) => {
      const kept = candidate(snapshot("2026-10-20T00:00:00Z"));
      const commit = vi.fn(async () => {
        if (failure === "save") throw new Error("storage failed");
      });
      const newer = candidate(snapshot("2026-10-20T01:00:00Z"), commit);
      const reportViewerShown = vi.fn();
      const { rendered, unmount } = await mount({
        initial: kept,
        isSaved: true,
        download: vi
          .fn<ViewerHost["download"]>()
          .mockResolvedValueOnce(null)
          .mockResolvedValueOnce(newer),
        reportViewerShown,
      });
      await vi.advanceTimersByTimeAsync(0);
      const cachedIndex = rendered.length - 1;
      const refresh = pressRefresh();
      await vi.advanceTimersByTimeAsync(0);
      const rejectedIndex = rendered.length - 1;
      if (failure === "render")
        rendered.at(-1)!.props.onError(new Error("bad page"));
      else commitPage(rendered);
      await refresh;
      expect(shownTime(rendered.at(-1))).toBe(kept.payload.generatedAt);
      expect(reportViewerShown).not.toHaveBeenCalledWith(true);
      commitPage(rendered, cachedIndex);
      commitPage(rendered, rejectedIndex);
      await vi.advanceTimersByTimeAsync(0);
      expect(reportViewerShown).not.toHaveBeenCalledWith(true);
      commitPage(rendered);
      await vi.advanceTimersByTimeAsync(0);
      expect(reportViewerShown).toHaveBeenLastCalledWith(true);
      const calls = reportViewerShown.mock.calls.length;
      rendered[rejectedIndex].props.onError(new Error("obsolete failure"));
      expect(reportViewerShown).toHaveBeenCalledTimes(calls);
      unmount();
      const closedCalls = reportViewerShown.mock.calls.length;
      rendered.at(-1)!.props.onError(new Error("closed viewer"));
      commitPage(rendered);
      await vi.advanceTimersByTimeAsync(0);
      expect(reportViewerShown).toHaveBeenCalledTimes(closedCalls);
    },
  );

  it("reports mount failure without allowing a throwing visibility callback to change recovery", async () => {
    const reportViewerShown = vi.fn(() => {
      throw new Error("bridge detached");
    });
    const broken = snapshot("2026-10-20T00:00:00Z");
    broken.display = null as unknown as PrivateSnapshot["display"];
    await expect(
      mount({ initial: candidate(broken), reportViewerShown }),
    ).rejects.toThrow();
    expect(reportViewerShown).toHaveBeenLastCalledWith(false);
    const { rendered, unmount } = await mount({
      isSaved: true,
      reportViewerShown,
    });
    await vi.advanceTimersByTimeAsync(0);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(reportViewerShown).toHaveBeenLastCalledWith(true);
    expect(() => unmount()).not.toThrow();
  });

  it("tells the host's diagnostics about each render failure and offers its diagnostics in settings", async () => {
    const reportDiagnostic = vi.fn();
    const showDiagnostics = vi.fn();
    const { rendered } = await mount({ reportDiagnostic, showDiagnostics });
    rendered.at(-1)!.props.onError(new Error("render failed"));
    expect(reportDiagnostic.mock.calls).toEqual([["render"]]);
    expect(viewerProps(rendered.at(-1))?.onShowDiagnostics).toBe(
      showDiagnostics,
    );
  });

  it("sends nothing to a native interface the host did not pass, as on the relay web page", async () => {
    const diagnostic = vi.fn();
    vi.stubGlobal("window", {
      wails: { diagnostic, showDiagnostics: diagnostic },
    });
    const { rendered } = await mount({});
    rendered.at(-1)!.props.onError(new Error("render failed"));
    expect(diagnostic).not.toHaveBeenCalled();
    expect(viewerProps(rendered.at(-1))?.onShowDiagnostics).toBeUndefined();
  });

  it("restores the fallback transport and stops listening when mounting fails", async () => {
    const fallback: RuntimeTransport = { call: vi.fn(async () => null) };
    const broken = snapshot("2026-10-20T00:00:00Z");
    broken.display = null as unknown as PrivateSnapshot["display"];
    const download = vi.fn(async () => null);
    await expect(
      mount({ initial: candidate(broken), fallback, download }),
    ).rejects.toThrow();
    expect(getTransport()).toBe(fallback);
    expect(schemeListeners.size).toBe(0);
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS * 2);
    expect(download).not.toHaveBeenCalled();
  });

  it("saves the first download only once its page commits, however long that takes", async () => {
    const commit = vi.fn(async () => {});
    const download = vi.fn(async () => null);
    const first = candidate(snapshot("2026-10-20T00:00:00Z"), commit);
    const { rendered } = await mount({ initial: first, download });
    const refresh = pressRefresh();
    await vi.advanceTimersByTimeAsync(ACKNOWLEDGE_TIMEOUT_MS / 2);
    expect(commit).not.toHaveBeenCalled();
    commitPage(rendered);
    await refresh;
    expect(commit).toHaveBeenCalledOnce();
    expect(download).not.toHaveBeenCalled();
    await pressRefresh();
    expect(download).toHaveBeenCalledWith(first.etag);
  });

  it("ignores commits rendered before the reports were answered, such as loading fallbacks", async () => {
    const commit = vi.fn(async () => {});
    const { rendered } = await mount({
      initial: candidate(snapshot("2026-10-20T00:00:00Z"), commit),
    });
    await vi.advanceTimersByTimeAsync(0);
    commitPage(rendered, 0);
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).not.toHaveBeenCalled();
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).toHaveBeenCalledOnce();
  });

  it("waits for a report read that starts after the confirmation before saving", async () => {
    const commit = vi.fn(async () => {});
    const { rendered } = await mount({
      initial: candidate(snapshot("2026-10-20T00:00:00Z"), commit),
    });
    await vi.advanceTimersByTimeAsync(0);
    const confirmations = rendered.length;
    const read = nativeApi.GetLossReport(30, "UTC");
    commitPage(rendered);
    await read;
    expect(commit).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(0);
    expect(rendered.length).toBeGreaterThan(confirmations);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).toHaveBeenCalledOnce();
  });

  it("does not save a snapshot whose page fails after rendering slowly", async () => {
    const commit = vi.fn(async () => {});
    const { rendered } = await mount({
      initial: candidate(snapshot("2026-10-20T00:00:00Z"), commit),
    });
    await vi.advanceTimersByTimeAsync(1_500);
    rendered.at(-1)!.props.onError(new Error("late render failed"));
    await vi.advanceTimersByTimeAsync(0);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).not.toHaveBeenCalled();
  });

  it("never saves a first download that failed to render", async () => {
    const commit = vi.fn(async () => {});
    const download = vi.fn(async () => null);
    const { rendered } = await mount({
      initial: candidate(snapshot("2026-10-20T00:00:00Z"), commit),
      download,
    });
    rendered.at(-1)!.props.onError(new Error("render failed"));
    await vi.advanceTimersByTimeAsync(0);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).not.toHaveBeenCalled();
    await pressRefresh().catch(() => {});
    expect(download).toHaveBeenCalledWith("");
  });

  it("gives up without saving when the page never commits", async () => {
    const commit = vi.fn(async () => {});
    const download = vi.fn(async () => null);
    const { rendered } = await mount({
      initial: candidate(snapshot("2026-10-20T00:00:00Z"), commit),
      download,
    });
    const refresh = pressRefresh();
    await vi.advanceTimersByTimeAsync(ACKNOWLEDGE_TIMEOUT_MS - 1);
    expect(viewerProps(rendered.at(-1))?.refresh).toMatchObject({
      status: { state: "current" },
    });
    await vi.advanceTimersByTimeAsync(1);
    await refresh;
    expect(commit).not.toHaveBeenCalled();
    expect(viewerProps(rendered.at(-1))?.refresh).toMatchObject({
      status: { state: "failed" },
    });
  });

  it("saves nothing and stops waiting once unmounted", async () => {
    const commit = vi.fn(async () => {});
    const { rendered, unmount } = await mount({
      initial: candidate(snapshot("2026-10-20T00:00:00Z"), commit),
    });
    await vi.advanceTimersByTimeAsync(0);
    unmount();
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("shows the kept snapshot again and keeps its tag when a newer one fails to render", async () => {
    const kept = candidate(snapshot("2026-10-20T00:00:00Z"));
    const commitNewer = vi.fn(async () => {});
    const newer = candidate(snapshot("2026-10-20T01:00:00Z"), commitNewer);
    const download = vi
      .fn<ViewerHost["download"]>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(newer)
      .mockResolvedValue(null);
    const { rendered } = await mount({
      initial: kept,
      isSaved: true,
      download,
    });
    await vi.advanceTimersByTimeAsync(0);
    const refresh = pressRefresh();
    await vi.advanceTimersByTimeAsync(0);
    rendered.at(-1)!.props.onError(new Error("render failed"));
    commitPage(rendered);
    await refresh;
    expect(commitNewer).not.toHaveBeenCalled();
    expect(shownTime(rendered.at(-1)!)).toBe(kept.payload.generatedAt);
    await pressRefresh();
    expect(download).toHaveBeenLastCalledWith(kept.etag);
  });

  it("rolls back a failed render the same way when the host's diagnostics throw", async () => {
    const kept = candidate(snapshot("2026-10-20T00:00:00Z"));
    const commitNewer = vi.fn(async () => {});
    const newer = candidate(snapshot("2026-10-20T01:00:00Z"), commitNewer);
    const download = vi
      .fn<ViewerHost["download"]>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(newer)
      .mockResolvedValue(null);
    const reportDiagnostic = vi.fn(() => {
      throw new Error("diagnostics unavailable");
    });
    const { rendered } = await mount({
      initial: kept,
      isSaved: true,
      download,
      reportDiagnostic,
    });
    await vi.advanceTimersByTimeAsync(0);
    const refresh = pressRefresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(() =>
      rendered.at(-1)!.props.onError(new Error("render failed")),
    ).not.toThrow();
    commitPage(rendered);
    await refresh;
    expect(reportDiagnostic).toHaveBeenCalledOnce();
    expect(commitNewer).not.toHaveBeenCalled();
    expect(shownTime(rendered.at(-1)!)).toBe(kept.payload.generatedAt);
    expect(viewerProps(rendered.at(-1))?.refresh).toMatchObject({
      status: { state: "failed" },
    });
    await pressRefresh();
    expect(download).toHaveBeenLastCalledWith(kept.etag);
    expect(viewerProps(rendered.at(-1))?.refresh).toMatchObject({
      status: { state: "current" },
    });
  });

  it("keeps a newer snapshot when a failure belongs to the snapshot shown before it", async () => {
    const kept = candidate(snapshot("2026-10-20T00:00:00Z"));
    const commitNewer = vi.fn(async () => {});
    const newer = candidate(snapshot("2026-10-20T01:00:00Z"), commitNewer);
    const download = vi
      .fn<ViewerHost["download"]>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(newer)
      .mockResolvedValue(null);
    const { rendered } = await mount({
      initial: kept,
      isSaved: true,
      download,
    });
    await vi.advanceTimersByTimeAsync(0);
    const keptError = rendered.at(-1)!.props.onError;
    const refresh = pressRefresh();
    await vi.advanceTimersByTimeAsync(0);
    keptError(new Error("the kept snapshot's page failed late"));
    await vi.advanceTimersByTimeAsync(0);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(1_000);
    await refresh;
    expect(commitNewer).toHaveBeenCalledOnce();
    expect(shownTime(rendered.at(-1)!)).toBe(newer.payload.generatedAt);
  });

  it("retries a rejected snapshot in the same tap during a quiet poll, then recovers with a valid one", async () => {
    const kept = candidate(snapshot("2026-10-20T00:00:00Z"));
    const bad = snapshot("2026-10-20T01:00:00Z");
    const commitBad = vi.fn(async () => {});
    const commitGood = vi.fn(async () => {});
    const good = candidate(snapshot("2026-10-20T02:00:00Z"), commitGood);
    let finishPoll: (
      value: SnapshotCandidate<PrivateSnapshot>,
    ) => void = () => {};
    const download = vi
      .fn<ViewerHost["download"]>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(candidate(bad, commitBad))
      .mockImplementationOnce(
        () => new Promise((resolve) => (finishPoll = resolve)),
      )
      .mockResolvedValueOnce(candidate(bad, commitBad))
      .mockResolvedValueOnce(candidate(bad, commitBad))
      .mockResolvedValueOnce(good)
      .mockResolvedValue(null);
    const { rendered } = await mount({
      initial: kept,
      isSaved: true,
      download,
    });
    /** Fail the page of whatever the viewer shows now. */
    const failPage = async () => {
      await vi.advanceTimersByTimeAsync(0);
      rendered.at(-1)!.props.onError(new Error("render failed"));
      await vi.advanceTimersByTimeAsync(0);
    };
    /** How many renders showed the bad snapshot. */
    const badRenders = () =>
      rendered.filter((element) => shownTime(element) === bad.generatedAt)
        .length;
    await vi.advanceTimersByTimeAsync(0);
    const first = pressRefresh();
    await failPage();
    await first;
    const shownBefore = badRenders();
    expect(shownBefore).toBeGreaterThan(0);

    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    const refresh = viewerProps(rendered.at(-1))?.refresh as {
      onRetry: () => void;
    };
    refresh.onRetry();
    finishPoll(candidate(bad, commitBad));
    await vi.advanceTimersByTimeAsync(0);
    expect(badRenders()).toBeGreaterThan(shownBefore);
    await failPage();
    expect(download).toHaveBeenCalledTimes(4);
    expect(shownTime(rendered.at(-1)!)).toBe(kept.payload.generatedAt);
    expect(viewerProps(rendered.at(-1))?.refresh).toMatchObject({
      status: { state: "failed" },
    });

    const shownAfterRetry = badRenders();
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    expect(download).toHaveBeenCalledTimes(5);
    expect(badRenders()).toBe(shownAfterRetry);

    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    expect(download).toHaveBeenLastCalledWith(kept.etag);
    expect(shownTime(rendered.at(-1)!)).toBe(good.payload.generatedAt);
    await vi.advanceTimersByTimeAsync(1);
    commitPage(rendered);
    await vi.advanceTimersByTimeAsync(0);
    expect(commitBad).not.toHaveBeenCalled();
    expect(commitGood).toHaveBeenCalledOnce();
    expect(shownTime(rendered.at(-1)!)).toBe(good.payload.generatedAt);
    expect(viewerProps(rendered.at(-1))?.refresh).toMatchObject({
      status: { state: "current" },
    });
    await pressRefresh();
    expect(download).toHaveBeenLastCalledWith(good.etag);
  });
});
