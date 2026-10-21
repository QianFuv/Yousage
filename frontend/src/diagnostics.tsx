/**
 * Fixed-category frontend diagnostics.
 *
 * Failures are reported to the local host only as `render` or `internal`, rate bounded,
 * without queueing. Errors, events, messages, stacks and URLs are never read or sent.
 */

/** The only categories the native and Go intakes accept. */
export type DiagnosticCategory = "render" | "internal";

/** Host intake for one fixed category; may return a promise, whose rejection is ignored. */
export type DiagnosticSink = (category: DiagnosticCategory) => unknown;

/** Record one fixed category; never throws and never rejects. */
export type DiagnosticReporter = (category: DiagnosticCategory) => void;

/** At most this many reports reach the host in any rolling window. */
export const DIAGNOSTIC_LIMIT = 10;

/** Rolling rate-limit window. */
export const DIAGNOSTIC_WINDOW_MS = 60_000;

/** Marks a target whose failure listeners are installed, shared with the early `android.html` listener. */
const LISTENERS_KEY = Symbol.for("yousage.diagnostics.listeners");

/** Whether an uncaught render failure is being forwarded, so its global error event is not counted again. */
let isForwardingRenderFailure = false;

/** The Android `window.wails` interface methods used here; other hosts lack them. */
type AndroidMethod = "diagnostic" | "showDiagnostics" | "setViewerShown";

/**
 * Create a reporter that forwards fixed categories to a host intake.
 *
 * Reports beyond the rate limit are dropped rather than queued. A report raised while
 * the sink runs is dropped, so a failing sink cannot recurse through failure listeners.
 *
 * @param sink - Host intake, or `null` to record nothing.
 * @param now - Clock in milliseconds.
 * @returns The reporter.
 */
export function createDiagnosticReporter(
  sink: DiagnosticSink | null,
  now: () => number = Date.now,
): DiagnosticReporter {
  if (!sink) return () => {};
  const recent: number[] = [];
  let isReporting = false;
  return (category) => {
    if (isReporting || (category !== "render" && category !== "internal"))
      return;
    isReporting = true;
    try {
      const instant = now();
      while (recent.length && instant - recent[0] >= DIAGNOSTIC_WINDOW_MS)
        recent.shift();
      if (recent.length >= DIAGNOSTIC_LIMIT) return;
      recent.push(instant);
      const result = sink(category);
      if (result instanceof Promise) result.catch(() => {});
    } catch {
      /* Diagnostics never change the outcome of the failure being reported. */
    } finally {
      isReporting = false;
    }
  };
}

/**
 * Read one Android native interface method without trusting its shape.
 *
 * @param scope - Global object that may carry `wails`.
 * @param name - The method.
 * @returns The method called on its interface, or `null` outside the Android app or when it cannot be read.
 */
function androidMethod(
  scope: object,
  name: AndroidMethod,
): ((...args: unknown[]) => unknown) | null {
  try {
    const bridge: unknown = Reflect.get(scope, "wails");
    if (typeof bridge !== "object" || bridge === null) return null;
    const method: unknown = Reflect.get(bridge, name);
    return typeof method === "function"
      ? (...args) => Reflect.apply(method, bridge, args)
      : null;
  } catch {
    return null;
  }
}

/**
 * The Android native intake, which works without Go or the Wails runtime.
 *
 * @param scope - Global object that may carry `wails`.
 * @returns The sink, or `null` when the native interface is absent or unreadable.
 */
export function androidDiagnosticSink(
  scope: object = globalThis,
): DiagnosticSink | null {
  return androidMethod(scope, "diagnostic");
}

/**
 * Open the Android native diagnostics dialog, which works without Go or React state.
 *
 * @param scope - Global object that may carry `wails`.
 * @returns The opener, or `null` when the native dialog is unavailable or unreadable.
 */
export function androidDiagnosticsOpener(
  scope: object = globalThis,
): (() => void) | null {
  const showDiagnostics = androidMethod(scope, "showDiagnostics");
  return showDiagnostics
    ? () => {
        try {
          showDiagnostics();
        } catch {
          /* The native screen offers the same dialog when this call fails. */
        }
      }
    : null;
}

/**
 * Report accepted viewer visibility independently of diagnostic logging limits.
 *
 * @param scope - Global object that may carry `wails`.
 * @returns The boolean-only reporter, or `null` when the native method is unavailable.
 */
export function androidViewerShownReporter(
  scope: object = globalThis,
): ((isShown: boolean) => void) | null {
  const setViewerShown = androidMethod(scope, "setViewerShown");
  return setViewerShown
    ? (isShown) => {
        try {
          setViewerShown(isShown);
        } catch {
          /* A detached native bridge must not interrupt viewer recovery. */
        }
      }
    : null;
}

/**
 * Report uncaught script errors and unhandled rejections as `internal`, once per target.
 *
 * The listeners never read the event, so they neither inspect nor suppress the failure.
 *
 * @param target - Usually `window`.
 * @param report - Reporter for the host intake.
 * @returns Whether listeners were installed; `false` when the target already has them.
 */
export function installDiagnosticListeners(
  target: EventTarget,
  report: DiagnosticReporter,
): boolean {
  if (Reflect.get(target, LISTENERS_KEY)) return false;
  Object.defineProperty(target, LISTENERS_KEY, { value: true });
  const onFailure = () => {
    if (!isForwardingRenderFailure) report("internal");
  };
  target.addEventListener("error", onFailure);
  target.addEventListener("unhandledrejection", onFailure);
  return true;
}

/**
 * Pass an error to the global error reporting React uses by default.
 *
 * @param error - The uncaught error, forwarded unread.
 */
function reportGlobally(error: unknown) {
  if (typeof reportError === "function") reportError(error);
  else console.error(error);
}

/**
 * Handle a React root's uncaught error: record `render`, then keep React's default reporting.
 *
 * @param report - Reporter for the host intake.
 * @param forward - Default reporting of the error, which is passed on unread.
 * @returns The `onUncaughtError` root option.
 */
export function uncaughtRenderHandler(
  report: DiagnosticReporter,
  forward: (error: unknown) => void = reportGlobally,
): (error: unknown) => void {
  return (error) => {
    report("render");
    isForwardingRenderFailure = true;
    try {
      forward(error);
    } finally {
      isForwardingRenderFailure = false;
    }
  };
}
