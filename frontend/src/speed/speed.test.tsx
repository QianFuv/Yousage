/** Providers without request timing show their requests, never a fabricated zero speed. */
import { describe, expect, it } from "vitest";
import { SpeedProvider } from "../../bindings/yousage/internal/service/models";
import { providerSpeedFacts } from "./SpeedPage";

describe("provider speed summary", () => {
  it("marks Pi history without timing as not measured", () => {
    const summary = providerSpeedFacts(
      SpeedProvider.createFrom({
        provider: "pi",
        unmeasuredRequests: 18,
        totalTokens: 1000,
      }),
    );
    expect(summary).toEqual(["18 without timing", "Not measured"]);
    expect(JSON.stringify(summary)).not.toMatch(/tok\/s|ms|TTFT/);
  });

  it("keeps measured providers' request count, first-token time and generation speed", () => {
    const summary = providerSpeedFacts(
      SpeedProvider.createFrom({
        provider: "codex",
        requests: 1,
        unmeasuredRequests: 2,
        medianTtftMs: 850,
        decodeTokensPerSecond: 71.3,
      }),
    );
    expect(summary).toEqual([
      "1 request",
      "TTFT 850 ms",
      "Generation 71.3 tok/s",
    ]);
  });
});
