/** Document identities must be captured once and never replaced by a later page's identity. */
import { describe, expect, it, vi } from "vitest";
import { startupContentReporter } from "./startup";

describe("startup document reporter", () => {
  it("retains its original ticket and forwards invalidation as well as readiness", () => {
    const ready = vi.fn();
    const ticket = vi.fn(() => "first-document");
    const reporter = startupContentReporter({
      wails: { startupDocument: ticket, setStartupContentReady: ready },
    });
    ticket.mockReturnValue("replacement-document");
    reporter(false);
    reporter(true);
    expect(ticket).toHaveBeenCalledOnce();
    expect(ready.mock.calls).toEqual([
      ["first-document", false],
      ["first-document", true],
    ]);
  });

  it("contains absent, unreadable and detached native hosts", () => {
    const scope = Object.defineProperty({}, "wails", {
      get() {
        throw new Error("unavailable");
      },
    });
    expect(() => startupContentReporter(scope)(true)).not.toThrow();
    expect(() => startupContentReporter({})(false)).not.toThrow();
    const reporter = startupContentReporter({
      wails: {
        startupDocument: () => "document",
        setStartupContentReady() {
          throw new Error("detached");
        },
      },
    });
    expect(() => reporter(true)).not.toThrow();
  });
});
