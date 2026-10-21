/** Frontend diagnostics send only fixed, rate-bounded categories and never read or alter the failure. */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { runInNewContext } from "node:vm";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactElement } from "react";
import type { ViewerHost } from "./web/mountViewer";
import {
  androidDiagnosticSink,
  androidDiagnosticsOpener,
  androidViewerShownReporter,
  createDiagnosticReporter,
  DIAGNOSTIC_LIMIT,
  DIAGNOSTIC_WINDOW_MS,
  installDiagnosticListeners,
  uncaughtRenderHandler,
  type DiagnosticCategory,
} from "./diagnostics";

/** A value that fails on any read, as a thrown object with throwing getters and `toJSON` would. */
const UNREADABLE = new Proxy(
  {},
  {
    get() {
      throw new Error("inspected");
    },
    has() {
      throw new Error("inspected");
    },
    getPrototypeOf() {
      throw new Error("inspected");
    },
    ownKeys() {
      throw new Error("inspected");
    },
  },
);

/** An event target that records listeners and calls them with any value, like an unreadable event. */
class FakeEventTarget {
  listeners = new Map<string, ((event: unknown) => void)[]>();

  /**
   * Record a listener.
   *
   * @param type - Event type.
   * @param listener - The listener.
   */
  addEventListener(type: string, listener: (event: unknown) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  /**
   * Call every listener for a type with an unreadable event.
   *
   * @param type - Event type.
   */
  fire(type: string) {
    for (const listener of this.listeners.get(type) ?? []) listener(UNREADABLE);
  }

  /**
   * Count listeners for a type.
   *
   * @param type - Event type.
   * @returns How many listeners are registered.
   */
  count(type: string): number {
    return this.listeners.get(type)?.length ?? 0;
  }
}

/**
 * Install listeners on a fake target.
 *
 * @param report - The reporter.
 * @returns The target.
 */
function listeningTarget(report: (category: DiagnosticCategory) => void) {
  const target = new FakeEventTarget();
  installDiagnosticListeners(target as unknown as EventTarget, report);
  return target;
}

describe("createDiagnosticReporter", () => {
  it("records nothing without a host intake", () => {
    expect(() => createDiagnosticReporter(null)("render")).not.toThrow();
  });

  it("passes only the two fixed categories", () => {
    const sink = vi.fn();
    const report = createDiagnosticReporter(sink);
    report("render");
    report("internal");
    report("network" as DiagnosticCategory);
    report(UNREADABLE as DiagnosticCategory);
    expect(sink.mock.calls).toEqual([["render"], ["internal"]]);
  });

  it("drops reports beyond the limit in a rolling minute instead of queueing them", () => {
    let now = 0;
    const sink = vi.fn();
    const report = createDiagnosticReporter(sink, () => now);
    for (let index = 0; index < DIAGNOSTIC_LIMIT + 5; index++)
      report("internal");
    expect(sink).toHaveBeenCalledTimes(DIAGNOSTIC_LIMIT);
    now = DIAGNOSTIC_WINDOW_MS - 1;
    report("render");
    expect(sink).toHaveBeenCalledTimes(DIAGNOSTIC_LIMIT);
    now = DIAGNOSTIC_WINDOW_MS;
    report("render");
    expect(sink).toHaveBeenCalledTimes(DIAGNOSTIC_LIMIT + 1);
  });

  it("contains a sink that throws or rejects", async () => {
    const throwing = createDiagnosticReporter(() => {
      throw UNREADABLE;
    });
    expect(() => throwing("render")).not.toThrow();
    const rejecting = vi.fn(() => Promise.reject(UNREADABLE));
    const report = createDiagnosticReporter(rejecting);
    report("internal");
    await new Promise((resolve) => setTimeout(resolve));
    expect(rejecting).toHaveBeenCalledOnce();
  });

  it("does not recurse when the sink raises another failure event", () => {
    const target = new FakeEventTarget();
    const sink = vi.fn(() => {
      target.fire("error");
      throw new Error("sink failed");
    });
    installDiagnosticListeners(
      target as unknown as EventTarget,
      createDiagnosticReporter(sink),
    );
    target.fire("error");
    expect(sink).toHaveBeenCalledOnce();
  });
});

describe("installDiagnosticListeners", () => {
  it("reports uncaught errors and rejections as internal without reading the event", () => {
    const sink = vi.fn();
    const target = listeningTarget(createDiagnosticReporter(sink));
    expect(() => target.fire("error")).not.toThrow();
    expect(() => target.fire("unhandledrejection")).not.toThrow();
    expect(sink.mock.calls).toEqual([["internal"], ["internal"]]);
  });

  it("installs once per target", () => {
    const sink = vi.fn();
    const report = createDiagnosticReporter(sink);
    const target = listeningTarget(report);
    expect(
      installDiagnosticListeners(target as unknown as EventTarget, report),
    ).toBe(false);
    expect(target.count("error")).toBe(1);
    expect(target.count("unhandledrejection")).toBe(1);
    target.fire("error");
    expect(sink).toHaveBeenCalledOnce();
  });

  it("leaves a real event uncanceled", () => {
    const target = new EventTarget();
    installDiagnosticListeners(target, createDiagnosticReporter(vi.fn()));
    const event = new Event("error", { cancelable: true });
    target.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
  });
});

describe("uncaughtRenderHandler", () => {
  it("records render, forwards the error unread and does not count its global event again", () => {
    const sink = vi.fn();
    const report = createDiagnosticReporter(sink);
    const target = listeningTarget(report);
    const forward = vi.fn<(error: unknown) => void>(() => target.fire("error"));
    uncaughtRenderHandler(report, forward)(UNREADABLE);
    expect(forward.mock.calls[0][0] === UNREADABLE).toBe(true);
    expect(sink.mock.calls).toEqual([["render"]]);
    target.fire("error");
    expect(sink.mock.calls).toEqual([["render"], ["internal"]]);
  });
});

describe("Android native interface", () => {
  it("reports only boolean visibility independently of exhausted logging limits", () => {
    const wails = {
      shown: [] as boolean[],
      diagnostic: vi.fn(),
      /** Record native visibility while verifying the bridge receiver is preserved. */
      setViewerShown(isShown: boolean) {
        this.shown.push(isShown);
      },
    };
    const report = createDiagnosticReporter(androidDiagnosticSink({ wails }));
    for (let index = 0; index <= DIAGNOSTIC_LIMIT; index++) report("render");
    const reportShown = androidViewerShownReporter({ wails });
    reportShown!(true);
    reportShown!(false);
    reportShown!(true);
    expect(wails.shown).toEqual([true, false, true]);
    expect(wails.diagnostic).toHaveBeenCalledTimes(DIAGNOSTIC_LIMIT);
  });

  it("guards missing, non-callable, unreadable and detached visibility methods", () => {
    for (const scope of [
      {},
      { wails: { setViewerShown: true } },
      { wails: UNREADABLE },
      UNREADABLE,
    ])
      expect(androidViewerShownReporter(scope)).toBeNull();
    const report = androidViewerShownReporter({
      wails: {
        setViewerShown() {
          throw UNREADABLE;
        },
      },
    });
    expect(() => report!(false)).not.toThrow();
  });

  it("is absent outside the Android app, including the relay web page and desktop", () => {
    expect(androidDiagnosticSink({})).toBeNull();
    expect(androidDiagnosticsOpener({})).toBeNull();
    const runtimeOnly = { wails: { invoke() {} } };
    expect(androidDiagnosticSink(runtimeOnly)).toBeNull();
    expect(androidDiagnosticsOpener(runtimeOnly)).toBeNull();
    const throwing = {
      get wails() {
        throw new Error("unavailable");
      },
    };
    expect(androidDiagnosticSink(throwing)).toBeNull();
    expect(androidDiagnosticsOpener(throwing)).toBeNull();
  });

  it("treats an interface whose methods cannot be read as absent, so startup continues", () => {
    const unreadableMethods = {
      wails: Object.defineProperties(
        {},
        {
          diagnostic: {
            get() {
              throw new Error("unavailable");
            },
          },
          showDiagnostics: {
            get() {
              throw new Error("unavailable");
            },
          },
        },
      ),
    };
    const unreadableBridge = { wails: UNREADABLE };
    for (const scope of [unreadableMethods, unreadableBridge]) {
      expect(androidDiagnosticSink(scope)).toBeNull();
      expect(androidDiagnosticsOpener(scope)).toBeNull();
    }
  });

  it("sends a fixed category and opens the native dialog directly", () => {
    const bridge = {
      calls: [] as string[],
      diagnostic(category: string) {
        this.calls.push(category);
      },
      showDiagnostics() {
        this.calls.push("show");
      },
    };
    createDiagnosticReporter(androidDiagnosticSink({ wails: bridge }))(
      "render",
    );
    androidDiagnosticsOpener({ wails: bridge })!();
    expect(bridge.calls).toEqual(["render", "show"]);
  });

  it("contains native calls that throw", () => {
    const wails = {
      diagnostic() {
        throw new Error("detached");
      },
      showDiagnostics() {
        throw new Error("detached");
      },
    };
    const report = createDiagnosticReporter(androidDiagnosticSink({ wails }));
    expect(() => report("render")).not.toThrow();
    const open = androidDiagnosticsOpener({ wails });
    expect(() => open!()).not.toThrow();
  });
});

describe("Android viewer visibility lifecycle", () => {
  afterEach(() => {
    vi.doUnmock("react-dom/client");
    vi.doUnmock("@wailsio/runtime");
    vi.doUnmock("../bindings/yousage/internal/mobile/service");
    vi.doUnmock("./web/mountViewer");
    vi.doUnmock("./web/WebViewer");
    vi.doUnmock("./android/snapshot");
    vi.doUnmock("./usage/dialogHistory");
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  /**
   * Run the real Android entry with controllable native reads and viewer callbacks.
   *
   * @param cached - Initial native cache response, passed through the mock decoder.
   * @returns Native visibility calls, rendered standalone screens and viewer controls.
   */
  async function androidEntry(
    cached: object = {
      status: "ok",
      payload: {},
      etag: "cached",
      text: "cached",
    },
    downloaded?: object,
  ) {
    vi.resetModules();
    vi.useFakeTimers();
    const runtime = await import("@wailsio/runtime");
    vi.doMock("@wailsio/runtime", () => runtime);
    const shown = vi.fn();
    const contentReady = vi.fn();
    const diagnostic = vi.fn();
    const render =
      vi.fn<(element: ReactElement<Record<string, unknown>>) => void>();
    let failRoot = () => {};
    const hosts: ViewerHost[] = [];
    const unmount = vi.fn();
    const mount = vi.fn(async (host: ViewerHost) => {
      hosts.push(host);
      return unmount;
    });
    const load = vi.fn(() =>
      downloaded ? Promise.resolve(downloaded) : new Promise(() => {}),
    );
    vi.stubGlobal("wails", {
      setViewerShown: shown,
      diagnostic,
      startupDocument: () => "test-document",
      setStartupContentReady: contentReady,
    });
    vi.stubGlobal("window", new EventTarget());
    vi.stubGlobal(
      "document",
      Object.assign(new EventTarget(), {
        getElementById: () => ({}),
        documentElement: { lang: "", classList: { toggle() {} } },
      }),
    );
    vi.stubGlobal("navigator", { language: "en", userAgent: "" });
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("history", { pushState: vi.fn(), back: vi.fn() });
    vi.doMock("react-dom/client", () => ({
      createRoot: (_: unknown, options: { onUncaughtError: () => void }) => {
        failRoot = options.onUncaughtError;
        return { render };
      },
    }));
    vi.doMock("../bindings/yousage/internal/mobile/service", () => ({
      CachedSnapshot: async () => cached,
      LoadSnapshot: load,
      GetPairing: async () => null,
    }));
    vi.doMock("./web/mountViewer", () => ({ mountViewer: mount }));
    vi.doMock("./web/WebViewer", () => ({ RelayProblem: () => null }));
    vi.doMock("./android/snapshot", () => ({
      interpretSnapshot: (value: unknown) => value,
    }));
    vi.doMock("./usage/dialogHistory", () => ({
      viewerHistory: () => ({
        whenSettled: (callback: () => void) => callback(),
        listen() {},
      }),
    }));
    await import("./android/android");
    await vi.advanceTimersByTimeAsync(0);
    return {
      shown,
      contentReady,
      diagnostic,
      render,
      failRoot: () => failRoot(),
      hosts,
      unmount,
      load,
      mount,
    };
  }

  it.each(["unpaired", "empty"])(
    "keeps native access on the %s standalone screen",
    async (status) => {
      const { shown, hosts, render } = await androidEntry({ status });
      expect(shown).toHaveBeenLastCalledWith(false);
      expect(shown).not.toHaveBeenCalledWith(true);
      expect(hosts).toHaveLength(0);
      expect(render).toHaveBeenCalled();
    },
  );

  it.each(["unpaired", "problem"])(
    "hands off %s only after its own content commit and rejects the replaced marker",
    async (status) => {
      const response = {
        status,
        problem: "network",
      };
      const { contentReady, render, failRoot } = await androidEntry(
        response,
        response,
      );
      expect(contentReady).not.toHaveBeenCalledWith("test-document", true);
      const ready = render.mock.calls.at(-1)![0].props.onReady as (
        ready: boolean,
      ) => void;
      ready(true);
      expect(contentReady).toHaveBeenLastCalledWith("test-document", true);
      failRoot();
      const count = contentReady.mock.calls.length;
      ready(true);
      expect(contentReady).toHaveBeenCalledTimes(count);
      await vi.advanceTimersByTimeAsync(0);
      const errorReady = render.mock.calls.at(-1)![0].props.onReady as (
        ready: boolean,
      ) => void;
      errorReady(true);
      expect(contentReady).toHaveBeenLastCalledWith("test-document", true);
    },
  );

  it("keeps loading covered and opens a committed problem screen on initial viewer failure", async () => {
    const loading = await androidEntry({ status: "empty" });
    expect(loading.contentReady).not.toHaveBeenCalledWith(
      "test-document",
      true,
    );
    const viewer = await androidEntry();
    viewer.hosts[0].onInitialFailure!();
    expect(viewer.contentReady).toHaveBeenLastCalledWith(
      "test-document",
      false,
    );
    const count = viewer.contentReady.mock.calls.length;
    viewer.hosts[0].reportViewerShown!(true);
    expect(viewer.contentReady).toHaveBeenCalledTimes(count);
    const ready = viewer.render.mock.calls.at(-1)![0].props.onReady as (
      ready: boolean,
    ) => void;
    ready(true);
    expect(viewer.contentReady).toHaveBeenLastCalledWith("test-document", true);
  });

  it("does not equate mount completion with readiness and ignores a viewer left for pairing", async () => {
    const { shown, hosts, unmount } = await androidEntry();
    expect(hosts).toHaveLength(1);
    expect(shown).not.toHaveBeenCalledWith(true);
    hosts[0].reportViewerShown!(true);
    expect(shown).toHaveBeenLastCalledWith(true);
    hosts[0].connection!.onChangeLink!();
    expect(unmount).toHaveBeenCalledOnce();
    expect(shown).toHaveBeenLastCalledWith(false);
    const count = shown.mock.calls.length;
    hosts[0].reportViewerShown!(true);
    hosts[0].reportViewerShown!(false);
    expect(shown).toHaveBeenCalledTimes(count);
  });

  it("reveals native access on outer failure even after diagnostic logging is exhausted", async () => {
    const { shown, hosts, failRoot, diagnostic, render } = await androidEntry();
    hosts[0].reportViewerShown!(true);
    for (let index = 0; index < DIAGNOSTIC_LIMIT; index++)
      window.dispatchEvent(new Event("error"));
    expect(diagnostic).toHaveBeenCalledTimes(DIAGNOSTIC_LIMIT);
    failRoot();
    expect(shown).toHaveBeenLastCalledWith(false);
    const count = shown.mock.calls.length;
    hosts[0].reportViewerShown!(true);
    expect(shown).toHaveBeenCalledTimes(count);
    await vi.advanceTimersByTimeAsync(0);
    expect(render).toHaveBeenCalled();
    expect(diagnostic).toHaveBeenCalledTimes(DIAGNOSTIC_LIMIT);
  });
});

describe("early Android listener", () => {
  const html = readFileSync(join(__dirname, "..", "android.html"), "utf8");
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)];
  const early = scripts.find((match) => match[1].includes("diagnostic"))![1];

  /**
   * Run the inline listener in a page without Go, React or the Wails runtime.
   *
   * @param diagnostic - Native intake.
   * @returns The page.
   */
  function page(diagnostic: (category: string) => void) {
    const target = new FakeEventTarget();
    const context = {
      wails: { diagnostic },
      addEventListener: target.addEventListener.bind(target),
    } as Record<string, unknown>;
    context.window = context;
    runInNewContext(early, context);
    return { target, context };
  }

  it("runs before the module entry", () => {
    expect(html.indexOf(early)).toBeLessThan(
      html.indexOf('<script type="module"'),
    );
  });

  it("sends only the fixed internal category, rate bounded, without reading the event", () => {
    const diagnostic = vi.fn();
    const { target } = page(diagnostic);
    for (let index = 0; index < DIAGNOSTIC_LIMIT + 3; index++) {
      target.fire("error");
      target.fire("unhandledrejection");
    }
    expect(diagnostic).toHaveBeenCalledTimes(DIAGNOSTIC_LIMIT);
    expect(new Set(diagnostic.mock.calls.flat())).toEqual(
      new Set(["internal"]),
    );
  });

  it("contains a native intake that throws or re-raises a failure", () => {
    const shown: { target?: FakeEventTarget } = {};
    const diagnostic = vi.fn(() => {
      shown.target!.fire("error");
      throw new Error("detached");
    });
    shown.target = page(diagnostic).target;
    expect(() => shown.target!.fire("error")).not.toThrow();
    expect(diagnostic).toHaveBeenCalledOnce();
  });

  it("keeps the module entry from installing duplicate listeners", () => {
    const { target, context } = page(vi.fn());
    runInNewContext(early, context);
    expect(
      installDiagnosticListeners(
        context as unknown as EventTarget,
        createDiagnosticReporter(vi.fn()),
      ),
    ).toBe(false);
    expect(target.count("error")).toBe(1);
    expect(target.count("unhandledrejection")).toBe(1);
  });
});
