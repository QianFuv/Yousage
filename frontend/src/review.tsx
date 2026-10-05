/** Development-only browser transport fixture; production index never imports this module. */
import { setTransport } from "@wailsio/runtime";
import type {
  LibrarySnapshot,
  LibraryValue,
} from "../bindings/yousage/internal/theme/models";

if (!import.meta.env.DEV)
  throw new Error("Browser review fixtures are development-only.");
let snapshot: LibrarySnapshot = {
  revision: 0,
  value: {
    preferences: {
      mode: "system",
      baseThemeId: "system",
      lightOwner: "",
      darkOwner: "",
      typography: {
        sans: "",
        code: "",
        interfaceSize: 16,
        codeSize: 13,
        smoothing: false,
      },
      contrast: 100,
      glassOpacity: 80,
      motionMs: 200,
      appliedGenerations: {},
    },
    customThemes: [],
    attributions: [],
  },
  published: { themes: [] },
};
setTransport({
  /** Keep fixture mutations in memory while exercising the actual generated call/model conversions. */
  async call(object, ...parameters) {
    const args = parameters[2];
    if (object !== 0) return null;
    const request = args as { methodID: number; args: unknown[] };
    switch (request.methodID) {
      case 2483192963:
        return {
          sourceCommit: "51f7f77f87318a5329d3bab15d513fda5654200b",
          wailsVersion: "3.0.0-beta.28",
          referenceTime: "2026-10-06T00:00:00Z",
          timeZone: "Asia/Shanghai",
          appearanceMode: "system",
          isSystemDark: false,
          providers: [
            "codex",
            "claude",
            "grok",
            "cursor",
            "opencode",
            "antigravity",
          ],
          roleCount: 57,
          pid: 0,
          lifecycleLog: "Browser transport fixture",
        };
      case 3469612837:
        return JSON.parse(JSON.stringify(snapshot));
      case 2592400194: {
        const [revision, value] = request.args;
        if (revision !== snapshot.revision)
          throw new Error(
            "Appearance changed. Reload before saving this draft.",
          );
        await new Promise((resolve) => setTimeout(resolve, 100));
        snapshot = {
          ...snapshot,
          revision: snapshot.revision + 1,
          value: JSON.parse(JSON.stringify(value)) as LibraryValue,
        };
        return JSON.parse(JSON.stringify(snapshot));
      }
      case 1388571440:
        return [
          {
            name: "Fixture.json",
            text: '{"version":1,"id":"browser-fixture","name":"Browser fixture","appearance":"dark","colors":{"canvas":"rebeccapurple","accent":"gold"}}',
            size: 160,
            error: "",
          },
        ];
      case 3294236452:
        return false;
      case 645226692:
      case 757831160:
      case 3754150517:
        return null;
      default:
        throw new Error(
          `Unsupported browser fixture method ${request.methodID}`,
        );
    }
  },
});
await import("./main");
