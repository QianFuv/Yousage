/** Android startup handoff uses committed content, independently of report acceptance diagnostics. */
import { useEffect, type ReactNode } from "react";

/**
 * Capture this document's native ticket before asynchronous reads or rendering.
 *
 * @param scope - Global object, or a test host.
 * @returns A contained boolean reporter; older native hosts need no handoff.
 */
export function startupContentReporter(scope: object = globalThis) {
  try {
    const bridge: unknown = Reflect.get(scope, "wails");
    if (typeof bridge !== "object" || bridge === null) return () => {};
    const ticketMethod: unknown = Reflect.get(bridge, "startupDocument");
    const readyMethod: unknown = Reflect.get(bridge, "setStartupContentReady");
    if (typeof ticketMethod !== "function" || typeof readyMethod !== "function")
      return () => {};
    const ticket: unknown = Reflect.apply(ticketMethod, bridge, []);
    if (typeof ticket !== "string" || !ticket) return () => {};
    return (isReady: boolean) => {
      try {
        Reflect.apply(readyMethod, bridge, [ticket, isReady]);
      } catch {
        /* Detached native hosts must not prevent the page from rendering. */
      }
    };
  } catch {
    return () => {};
  }
}

/**
 * Acknowledge a standalone screen only after its content commits.
 *
 * @param props - Content and a presentation-scoped readiness callback.
 * @returns The same content without another loading view.
 */
export function StartupContent({
  children,
  onReady,
}: {
  children: ReactNode;
  onReady: (isReady: boolean) => void;
}) {
  useEffect(() => {
    onReady(true);
    return () => onReady(false);
  }, [onReady]);
  return children;
}
