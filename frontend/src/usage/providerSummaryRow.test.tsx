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
    expect(facts.map(([, , text]) => text.replace(/<[^>]+>/g, ""))).toEqual([
      "12,817 requests ·",
      "TTFT 1.4 s ·",
      "Generation 119 tok/s",
    ]);
    for (const [, classes] of facts) expect(classes).toBe("whitespace-nowrap");
    expect(markup).toMatch(/<ul class="flex flex-wrap/);
  });
});
