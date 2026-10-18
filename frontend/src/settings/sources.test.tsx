/** Pi keeps an automatic source plus any number of custom session folders; other providers keep their single row. */
import { describe, expect, it } from "vitest";
import { ProviderDetection } from "../../bindings/yousage/internal/service/models";
import {
  addSessionFolder,
  canAddSessionFolder,
  isMissingFolder,
  isRemovableSessionFolder,
  providerLabel,
  providersWithoutQuota,
  removeSessionFolder,
  sessionFolderNumber,
  sessionFolderProviders,
  sourceStatus,
  type SourceRow,
} from "./sources";

/**
 * Build a saved history source row.
 *
 * @param id - Row identity.
 * @param provider - History provider.
 * @param root - Custom folder; automatic rows leave it empty.
 * @returns The row.
 */
function row(id: string, provider: string, root = ""): SourceRow {
  return { id, provider, root, isEnabled: true, isAutomatic: root === "" };
}

const SAVED: SourceRow[] = [
  row("codex-auto", "codex"),
  row("pi-auto", "pi"),
  row("pi-custom-1", "pi", "D:/Sessions/first"),
  row("claude-auto", "claude"),
];

describe("custom Pi session folders", () => {
  it("adds an empty enabled folder directly after the existing Pi rows", () => {
    const next = addSessionFolder(SAVED, "pi", () => "pi-custom-2");
    expect(next.id).toBe("pi-custom-2");
    expect(next.sources.map((source) => source.id)).toEqual([
      "codex-auto",
      "pi-auto",
      "pi-custom-1",
      "pi-custom-2",
      "claude-auto",
    ]);
    expect(next.sources[3]).toEqual({
      id: "pi-custom-2",
      provider: "pi",
      root: "",
      isEnabled: true,
      isAutomatic: false,
    });
    expect(SAVED).toHaveLength(4);
  });

  it("never reuses an identity that already exists in the draft", () => {
    const identities = ["pi-custom-1", "pi-custom-1", "pi-custom-3"];
    const next = addSessionFolder(SAVED, "pi", () => identities.shift()!);
    expect(next.id).toBe("pi-custom-3");
    expect(new Set(next.sources.map((source) => source.id)).size).toBe(5);
  });

  it("removes only custom Pi folders and keeps automatic or other providers' rows", () => {
    const legacyCustom = row("codex-custom", "codex", "D:/Codex");
    const rows = [...SAVED, legacyCustom];
    expect(removeSessionFolder(rows, "pi-custom-1").map((r) => r.id)).toEqual([
      "codex-auto",
      "pi-auto",
      "claude-auto",
      "codex-custom",
    ]);
    expect(removeSessionFolder(rows, "pi-auto")).toEqual(rows);
    expect(removeSessionFolder(rows, "codex-custom")).toEqual(rows);
    expect(isRemovableSessionFolder(legacyCustom)).toBe(false);
    expect(canAddSessionFolder("codex")).toBe(false);
    expect(canAddSessionFolder("claude")).toBe(false);
    expect(canAddSessionFolder("pi")).toBe(true);
  });

  it("numbers custom folders per provider so every row has a distinct name", () => {
    const rows = addSessionFolder(SAVED, "pi", () => "pi-custom-2").sources;
    expect(sessionFolderNumber(rows, 2)).toBe(1);
    expect(sessionFolderNumber(rows, 3)).toBe(2);
  });

  it("blocks applying a custom folder without a path", () => {
    expect(isMissingFolder(row("pi-custom-x", "pi", "   "))).toBe(true);
    expect(isMissingFolder(row("pi-custom-y", "pi", "D:/Sessions"))).toBe(
      false,
    );
    expect(isMissingFolder(row("pi-auto", "pi"))).toBe(false);
  });
});

describe("source status and capabilities", () => {
  const detections = [
    ProviderDetection.createFrom({
      driver: "codex",
      provider: "codex",
      isInstalled: true,
      hasHistory: true,
      supportsHistory: true,
      supportsQuota: true,
    }),
    ProviderDetection.createFrom({
      driver: "pi",
      provider: "pi",
      isInstalled: true,
      hasHistory: false,
      supportsHistory: true,
      supportsQuota: false,
    }),
  ];

  it("describes automatic, custom Pi and legacy custom rows", () => {
    expect(sourceStatus(row("codex-auto", "codex"), detections)).toBe(
      "History location detected",
    );
    expect(sourceStatus(row("pi-auto", "pi"), detections)).toBe(
      "No history location detected yet",
    );
    expect(sourceStatus(row("pi-custom", "pi", "D:/S"), detections)).toBe(
      "Custom session folder",
    );
    expect(sourceStatus(row("codex-custom", "codex", "D:/C"), detections)).toBe(
      "Custom history location",
    );
    expect(providerLabel("pi")).toBe("Pi");
    expect(providerLabel("claudeAgent")).toBe("Claude Code");
  });

  it("lists Pi as having no quota while quota providers keep their accounts", () => {
    expect(providersWithoutQuota(detections).map((d) => d.provider)).toEqual([
      "pi",
    ]);
  });

  it("keeps Pi addable after removing the last custom folder of a custom-only configuration", () => {
    const customOnly = [
      row("codex-auto", "codex"),
      row("pi-custom-1", "pi", "D:/Sessions/only"),
      row("claude-auto", "claude"),
    ];
    expect(sessionFolderProviders(customOnly, [])).toEqual(["pi"]);
    const removed = removeSessionFolder(customOnly, "pi-custom-1");
    expect(removed.some((source) => source.provider === "pi")).toBe(false);
    expect(sessionFolderProviders(removed, [])).toEqual([]);
    expect(sessionFolderProviders(removed, detections)).toEqual(["pi"]);
    const readded = addSessionFolder(removed, "pi", () => "pi-custom-2");
    expect(sessionFolderProviders(readded.sources, detections)).toEqual(["pi"]);
  });

  it("offers folders only for multi-folder providers that support history", () => {
    const unsupported = ProviderDetection.createFrom({
      driver: "pi",
      provider: "pi",
      isInstalled: false,
      hasHistory: false,
      supportsHistory: false,
      supportsQuota: false,
    });
    const codexOnly = [
      row("codex-auto", "codex"),
      row("claude-auto", "claude"),
    ];
    expect(sessionFolderProviders(codexOnly, [unsupported])).toEqual([]);
    expect(
      sessionFolderProviders(codexOnly, [detections[0]!, unsupported]),
    ).toEqual([]);
  });

  it("does not invent unsupported quota rows for detections from older hosts", () => {
    const legacy = ProviderDetection.createFrom({
      driver: "codex",
      provider: "codex",
      isInstalled: true,
      hasHistory: true,
    });
    expect(providersWithoutQuota([legacy])).toEqual([]);
  });
});
