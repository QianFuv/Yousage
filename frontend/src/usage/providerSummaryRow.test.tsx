/** Provider summaries keep the whole name and value and wrap only between whole facts. */
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ProviderSummaryRow } from "./ProviderSummaryRow";

describe("provider summary row", () => {
  const markup = renderToStaticMarkup(
    <ProviderSummaryRow
      provider="claude"
      value="$12,345.67"
      facts={["12,817 requests", "TTFT 1.4 s", "Generation 119 tok/s"]}
    />,
  );

  it("never truncates the provider name or wraps its value", () => {
    expect(markup).toContain("Claude Code");
    expect(markup).not.toContain("truncate");
    expect(markup).toMatch(/whitespace-nowrap[^"]*">\$12,345\.67</);
  });

  it("puts the count with the other facts, each unbreakable and separated", () => {
    const facts = [...markup.matchAll(/<li class="([^"]*)">(.*?)<\/li>/g)];
    expect(facts.map(([, , text]) => text)).toEqual([
      "12,817 requests",
      "TTFT 1.4 s",
      "Generation 119 tok/s",
    ]);
    for (const [, classes] of facts) {
      expect(classes).toContain("whitespace-nowrap");
      expect(classes).toContain(
        "before:content-[&#x27;·&#x27;_/_&#x27;&#x27;]",
      );
      expect(classes).toContain("before:w-4");
    }
  });

  it("clips the separator that starts each line, so wrapped lines never begin or end with one", () => {
    expect(markup).toContain(
      '<div class="[clip-path:inset(0_-100vw_0_0)]"><ul class="-ms-4 flex flex-wrap',
    );
  });
});
