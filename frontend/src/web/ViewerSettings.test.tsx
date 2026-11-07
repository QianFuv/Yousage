/** The viewer settings offer local diagnostics only when the host provides them, and the same About as the desktop. */
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ViewerSettings } from "./ViewerSettings";

/**
 * Render the settings page.
 *
 * @param onShowDiagnostics - The host's diagnostics opener, if any.
 * @returns The rendered markup.
 */
function render(onShowDiagnostics?: () => void): string {
  return renderToStaticMarkup(
    <ViewerSettings
      generatedAt="2026-10-20T00:00:00Z"
      appVersion="test"
      locale="en"
      onShowDiagnostics={onShowDiagnostics}
    />,
  );
}

describe("ViewerSettings", () => {
  it("shows a diagnostics entry when the host can open its diagnostics", () => {
    const html = render(vi.fn());
    expect(html).toContain("Diagnostics report");
    expect(html).toContain("View");
  });

  it("has no diagnostics entry without a host, as on the relay web page", () => {
    expect(render()).not.toContain("Diagnostics");
  });

  it("keeps freshness without a manual refresh button, since the viewer follows the relay", () => {
    const html = render();
    expect(html).toContain("Last updated");
    expect(html).not.toContain("Check for a newer snapshot");
    expect(html).not.toMatch(/<button[^>]*>[^<]*Refresh/);
  });

  it("introduces Yousage with its project page like the desktop About", () => {
    const html = render();
    expect(html).toContain("Yousage");
    expect(html).toContain("github.com/QianFuv/Yousage");
    expect(html).toContain("Report a problem");
    expect(html).toContain("Desktop test");
  });
});
