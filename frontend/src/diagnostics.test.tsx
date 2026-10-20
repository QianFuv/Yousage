/** Frontend diagnostics send only fixed, rate-bounded categories and never read or alter the failure. */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { runInNewContext } from "node:vm";
import { describe, expect, it, vi } from "vitest";
import {
  androidDiagnosticSink,
  androidDiagnosticsOpener,
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
