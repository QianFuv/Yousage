/** Bundle the exact T1 graph into embedded assets; no reference server is needed at runtime. */
import { defineConfig } from "vitest/config";
import referenceConfig from "../tools/parity/vite.config";
import { fileURLToPath } from "node:url";

export default defineConfig({
  ...referenceConfig,
  server: { fs: { allow: [fileURLToPath(new URL("..", import.meta.url))] } },
  plugins: [
    {
      name: "desktop-theme-mirror",
      enforce: "pre",
      /** Original UI primitives share the desktop's transient theme mirror. */
      resolveId(source, importer) {
        if (
          source.endsWith("/themePalette") &&
          importer?.includes("/upstream/")
        )
          return fileURLToPath(
            new URL("./vendor/themePalette.tsx", import.meta.url),
          );
      },
    },
    ...(referenceConfig.plugins ?? []),
  ],
  resolve: {
    ...referenceConfig.resolve,
    alias: {
      ...referenceConfig.resolve?.alias,
      "@reference": fileURLToPath(
        new URL("./reference-adapter.tsx", import.meta.url),
      ),
      react: fileURLToPath(new URL("./node_modules/react", import.meta.url)),
      "react-dom": fileURLToPath(
        new URL("./node_modules/react-dom", import.meta.url),
      ),
    },
  },
  test: { include: ["src/**/*.test.tsx"] },
});
