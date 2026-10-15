/** Relay pages: the snapshot viewer and the share page, embedded into the relay binary. */
import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";
import desktopConfig from "./vite.config";

export default defineConfig({
  ...desktopConfig,
  build: {
    outDir: fileURLToPath(
      new URL("../internal/relay/web/dist", import.meta.url),
    ),
    emptyOutDir: true,
    rollupOptions: {
      input: {
        viewer: fileURLToPath(new URL("./viewer.html", import.meta.url)),
        share: fileURLToPath(new URL("./share.html", import.meta.url)),
      },
    },
  },
});
