/** About describes Yousage itself, and the diagnostics report is shown field by field. */
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";

vi.mock("../../bindings/yousage/internal/desktop/app", () => ({
  GetDiagnostics: vi.fn(async () => ""),
  GetAppVersion: vi.fn(async () => "0.2.1"),
  OpenExternal: vi.fn(async () => {}),
  ExportDiagnostics: vi.fn(async () => true),
  CopyText: vi.fn(async () => {}),
}));

import { DiagnosticsPanel, parseDiagnosticReport } from "./DiagnosticsPanel";

describe("diagnostics report", () => {
  it("reads the backend report fields", () => {
    const report = JSON.stringify({
      application: "Yousage",
      version: "0.2.1",
      platform: "windows/amd64",
      historySources: 2,
      metadataAvailable: true,
      logs: "first\nsecond\n",
    });
    expect(parseDiagnosticReport(report)).toMatchObject({
      version: "0.2.1",
      platform: "windows/amd64",
      historySources: 2,
      logs: "first\nsecond\n",
    });
  });

  it("keeps text that is not a JSON object as the logs", () => {
    expect(parseDiagnosticReport("not json")).toEqual({ logs: "not json" });
    expect(parseDiagnosticReport("42")).toEqual({ logs: "42" });
  });
});

describe("about", () => {
  it("introduces Yousage and its project page instead of the toolkit", () => {
    const markup = renderToStaticMarkup(<DiagnosticsPanel nativeStatus="" />);
    expect(markup).toContain("Yousage");
    expect(markup).toContain("github.com/QianFuv/Yousage");
    expect(markup).toContain("Report a problem");
    expect(markup).not.toMatch(/Wails/i);
  });
});
