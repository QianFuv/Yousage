/** Bundle the exact T1 graph into embedded assets; no reference server is needed at runtime. */
import { defineConfig } from "vitest/config";
import referenceConfig from "../tools/parity/vite.config";
import { fileURLToPath } from "node:url";

export default defineConfig({
  ...referenceConfig,
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
