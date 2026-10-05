/** Controlled archives exercise integrity, licensing, include resolution and expansion boundaries. */
import { afterEach, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import JSZip from "jszip";
import {
  importOpenVsxThemeExtension,
  type OpenVsxThemeExtension,
} from "../../vendor/openVsxThemes";

const EXTENSION: OpenVsxThemeExtension = {
  id: "fixture.palette",
  collectionId: "open-vsx:fixture.palette",
  name: "Fixture palette",
  publisher: "fixture",
  version: "1.0.0",
  license: "MIT",
  description: "Controlled fixture",
  downloadCount: 0,
  iconUrl: null,
  sourceUrl: null,
  manifestUrl: "https://open-vsx.org/fixture/manifest",
  sha256Url: "https://open-vsx.org/fixture/checksum",
  vsixUrl: "https://open-vsx.org/fixture/package",
};

/** Install an isolated response graph, including an independently computed archive checksum. */
async function archiveFixture(
  options: {
    license?: string;
    shouldCycle?: boolean;
    shouldTamper?: boolean;
    shouldExpand?: boolean;
  } = {},
) {
  const manifest = {
    publisher: "fixture",
    name: "palette",
    version: "1.0.0",
    license: options.license ?? "MIT",
    contributes: {
      themes: [
        { label: "Fixture Light", uiTheme: "vs", path: "themes/light.json" },
        { label: "Fixture Dark", uiTheme: "vs-dark", path: "themes/dark.json" },
      ],
    },
  };
  const zip = new JSZip();
  zip.file("extension/package.json", JSON.stringify(manifest));
  zip.file(
    "extension/themes/base.json",
    JSON.stringify({
      colors: {
        "editor.background": "#fcfcfc",
        "editor.foreground": "#101010",
      },
    }),
  );
  zip.file(
    "extension/themes/light.json",
    JSON.stringify({
      name: "Fixture Light",
      include: options.shouldCycle ? "light.json" : "base.json",
      colors: { "button.background": "#123456" },
    }),
  );
  zip.file(
    "extension/themes/dark.json",
    JSON.stringify({
      name: "Fixture Dark",
      colors: {
        "editor.background": "#111111",
        "editor.foreground": "#eeeeee",
        "button.background": "#abcdef",
      },
    }),
  );
  const bytes = await zip.generateAsync({
    type: "uint8array",
    compression: "STORE",
  });
  if (options.shouldExpand) {
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    for (let offset = 0; offset < bytes.length - 46; offset++) {
      if (view.getUint32(offset, true) === 0x02014b50) {
        view.setUint32(offset + 24, 101 * 1024 * 1024, true);
        break;
      }
    }
  }
  const checksum = options.shouldTamper
    ? "0".repeat(64)
    : createHash("sha256").update(bytes).digest("hex");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (request: RequestInfo | URL) => {
      const url = String(request);
      if (url === EXTENSION.manifestUrl)
        return new Response(JSON.stringify(manifest));
      if (url === EXTENSION.vsixUrl)
        return new Response(Uint8Array.from(bytes).buffer);
      if (url === EXTENSION.sha256Url) return new Response(checksum);
      throw new Error("Unexpected fixture URL");
    }),
  );
}
afterEach(() => vi.unstubAllGlobals());
it("imports inherited colors and pairs both appearances with stable collection identities", async () => {
  await archiveFixture();
  const themes = await importOpenVsxThemeExtension(EXTENSION);
  expect(themes).toHaveLength(1);
  expect(themes[0].collection?.id).toBe(EXTENSION.collectionId);
  expect(themes[0].variants?.dark).toBeDefined();
  expect(Object.keys(themes[0].colors)).toHaveLength(57);
  const repeated = await importOpenVsxThemeExtension(EXTENSION);
  expect(repeated[0].id).toBe(themes[0].id);
});
it("rejects changed package bytes before import", async () => {
  await archiveFixture({ shouldTamper: true });
  await expect(importOpenVsxThemeExtension(EXTENSION)).rejects.toThrow(
    "integrity",
  );
});
it("rejects mismatched licensing and cyclic includes", async () => {
  await archiveFixture({ license: "Apache-2.0" });
  await expect(importOpenVsxThemeExtension(EXTENSION)).rejects.toThrow(
    "license",
  );
  await archiveFixture({ shouldCycle: true });
  await expect(importOpenVsxThemeExtension(EXTENSION)).rejects.toThrow(
    "imported safely",
  );
});
it("rejects advertised expansion even when the checksum is valid", async () => {
  await archiveFixture({ shouldExpand: true });
  await expect(importOpenVsxThemeExtension(EXTENSION)).rejects.toThrow(
    "expands",
  );
});
